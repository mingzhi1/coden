package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMReplanner refines high-level tasks into concrete implementation steps
// after the Discovery phase has found relevant code. This bridges:
//
//	Plan  (WHAT direction) →
//	Discovery (WHERE in code) →
//	RePlan (HOW specifically) →
//	Code (DO — low-level execution)
type LLMReplanner struct {
	chatter Chatter
	msgBuffer
}

func NewLLMReplanner(chatter Chatter) *LLMReplanner {
	return &LLMReplanner{chatter: chatter}
}

// SA-08: macroscopic and microscopic discovery budgets.
const (
	// replanSnippetMaxLines: RePlanner needs structure/signatures, not full content.
	replanSnippetMaxLines = 50
	// replanCodeBudgetChars: total code context budget for RePlanner (~1500 tokens).
	replanCodeBudgetChars = 6000
)

// RePlan takes original tasks + discovered code snippets and produces
// refined tasks with concrete implementation instructions.
func (r *LLMReplanner) RePlan(ctx context.Context, intent model.IntentSpec, tasks []model.Task, snippets []model.FileSnippet) ([]model.Task, error) {
	// SA-08: Build meso-level code context — per-snippet line cap + total budget.
	// RePlanner needs file structure/signatures to produce concrete steps, not
	// full implementations. Full snippets go to Coder.
	var codeCtx strings.Builder
	for _, s := range snippets {
		if !s.Exists {
			codeCtx.WriteString(fmt.Sprintf("### %s — NOT FOUND\n", s.Path))
			continue
		}
		content := truncateToLines(s.Content, replanSnippetMaxLines)
		truncNote := ""
		if s.Lines > replanSnippetMaxLines {
			truncNote = fmt.Sprintf(" (showing first %d of %d lines)", replanSnippetMaxLines, s.Lines)
		}
		snippet := fmt.Sprintf("### %s (%d lines%s)\n```\n%s\n```\n\n", s.Path, s.Lines, truncNote, content)
		if codeCtx.Len()+len(snippet) > replanCodeBudgetChars {
			break // total budget exhausted
		}
		codeCtx.WriteString(snippet)
	}

	// Build original task list.
	var taskList strings.Builder
	for _, t := range tasks {
		taskList.WriteString(fmt.Sprintf("- %s: %s", t.ID, t.Title))
		if len(t.Files) > 0 {
			taskList.WriteString(fmt.Sprintf(" (files: %s)", strings.Join(t.Files, ", ")))
		}
		taskList.WriteString("\n")
	}

	systemPrompt := prompts.Replanner()

	// Inject critic feedback so the replanner acts on issues flagged before execution.
	var criticSection string
	if wc := model.WorkflowContextFrom(ctx); len(wc.CritiqueIssues) > 0 {
		var sb strings.Builder
		sb.WriteString("## Critic Feedback (must address)\n")
		for _, issue := range wc.CritiqueIssues {
			sb.WriteString("- ")
			sb.WriteString(issue)
			sb.WriteString("\n")
		}
		criticSection = sb.String() + "\n"
	}

	userMsg := fmt.Sprintf("%s## Goal\n%s\n\n## Original Plan\n%s\n## Discovered Code\n%s",
		criticSection, intent.Goal, taskList.String(), codeCtx.String())

	reply, err := RecoverableChat(ctx, r.chatter, RoleReplanner, []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}, defaultRecoveryConfig())
	if err != nil {
		return nil, fmt.Errorf("replanner llm: %w", err)
	}

	var raw []struct {
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		Steps      []string `json:"steps"`
		Files      []string `json:"files"`
		DependsOn  []string `json:"depends_on"`
		SuccessCmd string   `json:"success_cmd"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &raw); err != nil || len(raw) == 0 {
		// Fallback: return original tasks enriched with step hints.
		r.push("warn", "replanner", "JSON parse failed, using original tasks")
		return tasks, nil
	}

	const maxTasks = 5
	if len(raw) > maxTasks {
		raw = raw[:maxTasks]
	}

	// Build refined tasks, preserving original task metadata where possible.
	origMap := make(map[string]model.Task, len(tasks))
	for _, t := range tasks {
		origMap[t.ID] = t
	}

	// Build the set of refined IDs so we can strip dangling depends_on refs.
	// Pre-populate with all valid LLM-returned IDs to avoid collisions in fallback.
	refinedIDs := make(map[string]bool, len(raw))
	for _, rt := range raw {
		if rt.ID != "" && strings.HasPrefix(rt.ID, "task-") {
			refinedIDs[rt.ID] = true
		}
	}
	autoCounter := 0
	for i, rt := range raw {
		id := rt.ID
		if id == "" || !strings.HasPrefix(id, "task-") {
			// Fall back to the original task ID at the same position, if available
			// and not already taken; otherwise generate a collision-free ID.
			if i < len(tasks) && !refinedIDs[tasks[i].ID] {
				id = tasks[i].ID
			} else {
				autoCounter++
				candidate := fmt.Sprintf("task-%d", autoCounter)
				for refinedIDs[candidate] {
					autoCounter++
					candidate = fmt.Sprintf("task-%d", autoCounter)
				}
				id = candidate
			}
			raw[i].ID = id
		}
		refinedIDs[id] = true
	}

	refined := make([]model.Task, 0, len(raw))
	for _, rt := range raw {
		// Enforce title length limit (100 chars for title part)
		title := rt.Title
		if len(title) > 100 {
			title = title[:100]
		}
		// Enforce steps count and length limits (1-3 items, 120 chars each)
		steps := rt.Steps
		if len(steps) > 3 {
			steps = steps[:3]
		}
		// R-01 fix: ensure at least one step so downstream Coder has instructions.
		if len(steps) == 0 {
			steps = []string{title}
		}
		for i := range steps {
			if len(steps[i]) > 120 {
				steps[i] = steps[i][:120]
			}
		}
		// Strip dangling depends_on references (LLM may hallucinate IDs).
		validDeps := rt.DependsOn[:0]
		for _, dep := range rt.DependsOn {
			if refinedIDs[dep] {
				validDeps = append(validDeps, dep)
			}
		}
		t := model.Task{
			ID:         rt.ID,
			Title:      title,
			Status:     model.TaskStatusPlanned,
			Files:      rt.Files,
			DependsOn:  validDeps,
			SuccessCmd: rt.SuccessCmd,
			Steps:      steps,
		}
		// RP-02 fix: if Replanner omitted success_cmd, preserve the Planner's original value.
		// The Planner's success_cmd (e.g. "go build ./...") is the deterministic verification
		// step; losing it degrades acceptance to LLM-only review.
		if t.SuccessCmd == "" {
			if orig, ok := origMap[rt.ID]; ok && orig.SuccessCmd != "" {
				t.SuccessCmd = orig.SuccessCmd
			}
		}
		// Preserve Created time from original if it existed.
		if orig, ok := origMap[rt.ID]; ok {
			t.Created = orig.Created
		}
		refined = append(refined, t)
	}

	r.push("info", "replanner", fmt.Sprintf("refined %d tasks with concrete steps", len(refined)))
	return refined, nil
}

var _ workflow.Replanner = (*LLMReplanner)(nil)

func (r *LLMReplanner) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "llm-replanner", Role: workflow.RolePlanner}
}
