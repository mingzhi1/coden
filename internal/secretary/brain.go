package secretary

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// --- AfterTurn: post-workflow LLM processing ---

// AfterTurnResult holds the results of Secretary's post-turn analysis.
type AfterTurnResult struct {
	Insights        []ExtractedInsight // structured insights from worker output
	CompressedTurns []CompressedTurn   // compressed old turn summaries
	// ProfileStale signals that this turn's code changes materially altered the
	// project such that the cached ProjectProfile (overview/style) is likely
	// inaccurate and should be rebuilt. The kernel acts on this (invalidation);
	// the Secretary only decides.
	ProfileStale bool
}

// ExtractedInsight is a structured insight extracted by the Secretary.
type ExtractedInsight struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Category   string   `json:"category"` // "decision", "finding", "comparison", "design"
	Confidence float64  `json:"confidence"`
	Tags       []string `json:"tags,omitempty"`
}

// CompressedTurn is a compressed summary of old turns.
type CompressedTurn struct {
	TurnIDs []string `json:"turn_ids"`
	Summary string   `json:"summary"`
}

// AfterTurn runs Secretary's post-workflow LLM analysis.
// Called asynchronously after commitWorkflowSaga completes.
//
// When LLM is available:
//   - Extracts structured insights from worker output (replaces regex extraction)
//   - Identifies decisions, findings, comparisons worth remembering
//
// When LLM is not available:
//   - Returns empty result (pure-code insight extraction continues in kernel)
func (s *Secretary) AfterTurn(ctx context.Context, sessionID string, input AfterTurnInput) AfterTurnResult {
	if s.llm == nil {
		return AfterTurnResult{}
	}

	var result AfterTurnResult

	// 1. Extract insights from worker output
	if input.WorkerOutput != "" {
		insights := s.extractInsights(ctx, sessionID, input)
		result.Insights = insights
	}

	// 2. Re-check the cached ProjectProfile: did this turn's code changes
	// materially alter the project so the cached overview/style is now stale?
	// Only meaningful when the turn actually wrote files (its own writes are a
	// reliable signal here, unlike a reconstructed cross-turn diff).
	if len(input.ChangedFiles) > 0 {
		result.ProfileStale = s.judgeProfileStale(ctx, sessionID, input)
	}

	return result
}

// judgeProfileStale decides whether this turn's changes materially altered the
// project — i.e. enough that a one-paragraph project overview would now be
// inaccurate. Conservative: returns false on any uncertainty/error so the
// profile is rebuilt only when a change is clearly structural, not on trivial
// edits. The kernel acts on the result (cache invalidation).
func (s *Secretary) judgeProfileStale(ctx context.Context, sessionID string, input AfterTurnInput) bool {
	if s.llm == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	files := input.ChangedFiles
	if len(files) > 30 {
		files = files[:30]
	}
	prompt := fmt.Sprintf(`A cached one-paragraph OVERVIEW describes this project (its purpose,
architecture, main components, storage/build tech). The latest change just
modified these files:

%s

Goal of the change: %s

Question: did this change MATERIALLY alter the project's architecture or identity
— e.g. a new subsystem/package, a new storage or runtime tech, a restructured
layout, a new major capability — such that the cached overview is now inaccurate?
A localized bug fix, a single new function, or tweaks to existing files do NOT count.

Reply with ONLY JSON: {"stale": true|false}`,
		strings.Join(files, "\n"),
		input.Goal,
	)

	reply, err := s.llm.Chat(ctx, "secretary", []LLMMessage{
		{Role: "system", Content: "You decide if a project's cached overview is stale after a code change. Reply with JSON only."},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		slog.Warn("[secretary] profile-stale judgment failed", "session", sessionID, "error", err)
		return false
	}
	reply = strings.TrimSpace(reply)
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	var parsed struct {
		Stale bool `json:"stale"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(reply)), &parsed); err != nil {
		return false
	}
	if parsed.Stale {
		slog.Info("[secretary] project profile judged stale after change", "session", sessionID, "files", len(input.ChangedFiles))
	}
	return parsed.Stale
}

// AfterTurnInput provides the Secretary with context for post-turn processing.
type AfterTurnInput struct {
	WorkflowID   string
	Goal         string   // intent goal
	TaskTitles   []string // what was planned
	WorkerOutput string   // raw LLM output from workers
	Status       string   // "pass" / "fail"
	ChangedFiles []string // files this turn wrote (reliable: coden's own writes)
}

// extractInsights uses the Light model to find structured insights in worker output.
func (s *Secretary) extractInsights(ctx context.Context, sessionID string, input AfterTurnInput) []ExtractedInsight {
	// Generous timeout: subprocess providers (claude-cli) spawn a process per
	// call, so 15s was too tight and silently dropped all insights.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`You are a project analyst. Analyze this workflow output and extract structured insights.

Workflow goal: %s
Tasks: %s
Status: %s

Worker output:
%s

Extract 0-5 insights as JSON array. Each insight has:
- "title": one-line summary (max 80 chars)
- "content": detailed explanation (max 200 chars)
- "category": one of "decision", "finding", "comparison", "design"
- "confidence": 0.0-1.0 how confident you are this is worth remembering
- "tags": relevant keywords

Only extract genuinely important insights. If nothing is noteworthy, return [].
Reply with ONLY a JSON array, no markdown fences.`,
		input.Goal,
		strings.Join(input.TaskTitles, ", "),
		input.Status,
		truncateForLLM(input.WorkerOutput, 3000),
	)

	reply, err := s.llm.Chat(ctx, "secretary", []LLMMessage{
		{Role: "system", Content: "You are a Secretary agent that extracts structured insights from workflow output. Reply with JSON only."},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		slog.Warn("[secretary] insight extraction LLM call failed", "session", sessionID, "error", err)
		return nil
	}

	// Parse JSON response
	reply = strings.TrimSpace(reply)
	// Strip markdown fences if present
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)

	var insights []ExtractedInsight
	if err := json.Unmarshal([]byte(reply), &insights); err != nil {
		slog.Warn("[secretary] failed to parse insight extraction response",
			"session", sessionID,
			"error", err,
			"reply_len", len(reply),
		)
		return nil
	}

	// Filter by minimum confidence
	var filtered []ExtractedInsight
	for _, ins := range insights {
		if ins.Confidence >= 0.5 && ins.Title != "" {
			filtered = append(filtered, ins)
		}
	}

	if len(filtered) > 0 {
		slog.Info("[secretary] extracted insights",
			"session", sessionID,
			"count", len(filtered),
		)
		s.audit(sessionID, AuditEntry{
			Type:    "insight_extraction",
			Allowed: true,
			Details: map[string]any{
				"count":       len(filtered),
				"workflow_id": input.WorkflowID,
				"method":      "llm",
			},
		})
	}

	return filtered
}

// --- Smart Context Ranking (future enhancement) ---

// RankSkills uses the Light model to assess which skills are most relevant
// to the current task. This is a future enhancement — currently returns
// skills in their original order.
//
// When implemented, this will replace the simple priority-based ordering
// in ContextGate with semantic relevance scoring.
func (s *Secretary) RankSkills(ctx context.Context, goal string, skills []ContextBlock) []ContextBlock {
	// Future: use LLM to rank skills by relevance to goal
	// For now, return as-is (priority ordering from ContextGate is sufficient)
	return skills
}

// --- Helpers ---

// truncateForLLM truncates text to approximately maxChars characters.
func truncateForLLM(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "\n... (truncated)"
}
