package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
	"github.com/mingzhi1/coden/internal/outputcompressor"
)

// LLMAnalyzer implements workflow.Analyzer using the Strong-tier LLM. For
// analyze intents it runs a read-only agentic loop — executing read tools
// (read_file/search/list_dir/…) to investigate the code — and returns its
// findings as prose. It NEVER mutates the repository: any mutation tool call the
// model emits is refused and reported back as "not executed".
//
// It is deliberately separate from LLMCoder: the Coder is pure-write, the
// Analyzer is pure-read. They share the low-level helpers (tool-call parsing,
// parallel reads, output compression) but not the prompt, role, or tier.
type LLMAnalyzer struct {
	chatter          Chatter
	executor         toolruntime.Executor // optional; enables the agentic read loop
	outputCompressor *outputcompressor.Compressor
	msgBuffer
}

func NewLLMAnalyzer(chatter Chatter, executor toolruntime.Executor) *LLMAnalyzer {
	return &LLMAnalyzer{chatter: chatter, executor: executor, outputCompressor: outputcompressor.New()}
}

// Analyze is read-only and low-risk, so it gets a deliberately generous loop:
// whole-project questions ("analyze this project") need many reads to build a
// real picture. These are intentionally larger than the Coder's budgets.
const maxAnalyzerRounds = 20

// Analyze investigates the code read-only and returns the analysis prose.
func (a *LLMAnalyzer) Analyze(ctx context.Context, intent model.IntentSpec) (string, error) {
	wc := model.WorkflowContextFrom(ctx)
	systemPrompt := prompts.Analyzer(wc.ToolsPrompt)

	// The workflow Dispatcher may hand the Analyzer a concrete, bounded objective
	// (what to determine + when it's done). When present it replaces the vague raw
	// goal as the driving purpose — this is what makes the read loop converge
	// instead of exploring open-endedly. Falls back to intent.Goal when absent.
	goal := strings.TrimSpace(intent.Goal)
	objective := strings.TrimSpace(wc.RoleObjectives[string(workflow.RoleAnalyzer)])
	var userMsg string
	if objective != "" {
		userMsg = fmt.Sprintf("Question: %s\n\nObjective — exactly what to determine, and when you are DONE:\n%s\n\n"+
			"Investigate the code to satisfy this objective. Read only what the objective requires; "+
			"the moment every point is answered with file evidence, STOP reading and write your final analysis as prose.",
			goal, objective)
	} else {
		userMsg = fmt.Sprintf("Analysis goal: %s\n\nInvestigate the code and answer.", goal)
	}
	if wc.WorkspaceRoot != "" {
		userMsg = fmt.Sprintf("Workspace root: %s\n\n%s", wc.WorkspaceRoot, userMsg)
	}
	if ctxInfo := contextSummary(ctx); ctxInfo != "" {
		userMsg = ctxInfo + "\n" + userMsg
	}

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}

	// No executor (e.g. loopback) — single-shot reasoning over discovery context.
	if a.executor == nil {
		reply, err := RecoverableChat(ctx, a.chatter, RoleAnalyzer, messages, defaultRecoveryConfig())
		if err != nil {
			return "", fmt.Errorf("analyzer llm: %w", err)
		}
		return strings.TrimSpace(reply), nil
	}
	return a.investigate(ctx, messages)
}

// investigate runs the read-only agentic loop. Each round the model may request
// read tools (executed and fed back) or, when it has enough information, reply
// with plain prose — that prose is the final analysis.
func (a *LLMAnalyzer) investigate(ctx context.Context, messages []Message) (string, error) {
	const (
		availableTokens   = 120000
		toolHistoryBudget = availableTokens * 40 / 100
	)
	readBudgetChars := 12000

	var lastProse string
	degenerateCount := 0
	const maxDegenerateRetries = 2

	for round := 0; round < maxAnalyzerRounds; round++ {
		messages = SnipHistory(messages, snipMaxMessages)
		messages = compressAgenticHistory(messages, toolHistoryBudget)

		reply, err := RecoverableChat(ctx, a.chatter, RoleAnalyzer, messages, defaultRecoveryConfig())
		if err != nil {
			return "", fmt.Errorf("analyzer llm round %d: %w", round+1, err)
		}

		calls := parsePlanToolCalls("analyzer", reply)
		if len(calls) == 0 {
			// No tool calls — the reply is the final prose analysis, unless it is a
			// degenerate stub (rate-limit degradation), in which case back off.
			if IsDegenerateReply(reply) {
				degenerateCount++
				if degenerateCount > maxDegenerateRetries {
					if strings.TrimSpace(lastProse) != "" {
						return lastProse, nil
					}
					return "", fmt.Errorf("analyzer: %d consecutive degenerate responses — API may be rate-limited", degenerateCount)
				}
				backoff := time.Duration(1<<uint(degenerateCount)) * time.Second
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(backoff):
				}
				round--
				continue
			}
			a.push("info", "analyzer", fmt.Sprintf("analysis complete after %d round(s)", round+1))
			return strings.TrimSpace(reply), nil
		}
		degenerateCount = 0

		reads, mutations := splitToolCalls(calls)
		if len(mutations) > 0 {
			// Read-only contract: refuse mutations, tell the model, keep going.
			for _, m := range mutations {
				slog.Info("[llm:analyzer] refusing mutation in read-only analysis", "round", round+1, "kind", m.Request.Kind, "target", toolCallTarget(m))
				a.push("info", "analyzer", fmt.Sprintf("round %d: read-only — refused %s %s", round+1, m.Request.Kind, toolCallTarget(m)))
			}
		}

		// Keep any prose the model included alongside its tool calls as a fallback
		// final answer in case the loop exhausts its rounds.
		if prose := stripJSON(reply); strings.TrimSpace(prose) != "" {
			lastProse = prose
		}

		var resultSummary strings.Builder
		resultSummary.WriteString(executeReadsParallel(ctx, "analyzer", a.executor, reads, readBudgetChars, round+1, a.push, a.outputCompressor))
		if len(mutations) > 0 {
			for _, m := range mutations {
				fmt.Fprintf(&resultSummary, "\n### %s %s\n(read-only analysis: NOT executed — do not modify files; report your findings as prose)\n", m.Request.Kind, toolCallTarget(m))
			}
		}

		// Convergence-biased nudge: anchor on the objective, surface the round
		// budget so the model self-paces, and bias toward concluding. The old
		// "keep reading / build a complete picture" wording had no stop condition
		// and made weaker models loop until the deadline killed them.
		nudge := fmt.Sprintf("Tool results:\n%s\n\n"+
			"You are on round %d of %d. If the results so far already let you answer the objective, "+
			"STOP now and reply with your final analysis as plain prose (no JSON, no tool_calls). "+
			"Only request more reads for a SPECIFIC point the objective still needs — do not explore beyond it.",
			resultSummary.String(), round+1, maxAnalyzerRounds)
		messages = append(messages,
			Message{Role: "assistant", Content: reply},
			Message{Role: "user", Content: nudge},
		)
	}

	// Rounds exhausted — make one final call asking for the conclusion.
	messages = append(messages, Message{Role: "user", Content: "Round budget reached. Provide your final analysis now as plain prose (no tool_calls)."})
	reply, err := RecoverableChat(ctx, a.chatter, RoleAnalyzer, messages, defaultRecoveryConfig())
	if err != nil {
		if strings.TrimSpace(lastProse) != "" {
			return lastProse, nil
		}
		return "", fmt.Errorf("analyzer final round: %w", err)
	}
	if final := stripJSON(reply); strings.TrimSpace(final) != "" {
		return final, nil
	}
	if strings.TrimSpace(lastProse) != "" {
		return lastProse, nil
	}
	return strings.TrimSpace(reply), nil
}

var _ workflow.Analyzer = (*LLMAnalyzer)(nil)

// stripJSON removes a leading/trailing fenced or bare JSON tool-call object from
// a reply, returning any surrounding prose. It is a best-effort helper for the
// case where the model mixes a tool_calls object with explanatory text.
func stripJSON(reply string) string {
	trimmed := strings.TrimSpace(reply)
	js := extractJSON(trimmed)
	if js == "" || js == trimmed {
		// Whole reply is JSON (or no JSON found) — no separable prose.
		if js == trimmed {
			return ""
		}
		return trimmed
	}
	return strings.TrimSpace(strings.Replace(trimmed, js, "", 1))
}
