package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
	"github.com/mingzhi1/coden/internal/llm/provider"
	"github.com/mingzhi1/coden/internal/llm/tokenbudget"
)

// LLMCoder uses an LLM to generate a structured tool plan.
// When an Executor is provided, it runs an agentic loop: read-only tool
// calls (read_file, search, list_dir) are executed locally and their
// results fed back into the LLM conversation for up to maxRounds.
// Mutation calls (write_file, edit_file, run_shell) are collected and
// returned in the final CodePlan for the kernel to execute.
type LLMCoder struct {
	chatter  Chatter
	executor toolruntime.Executor // optional; enables agentic read loop
	deps     *CoderDeps           // optional; nil = use production defaults
	msgBuffer
}

const maxCoderRounds = 5

const (
	maxTruncationRetries  = 3
	truncationRecoveryMsg = "Output token limit hit. Resume directly — no apology, no recap of what you were doing. Pick up mid-thought if that is where the cut happened. Break remaining work into smaller pieces."
)

func NewLLMCoder(chatter Chatter) *LLMCoder {
	return &LLMCoder{chatter: chatter}
}

// NewAgenticCoder creates a coder with tool-use loop capability.
func NewAgenticCoder(chatter Chatter, executor toolruntime.Executor) *LLMCoder {
	return &LLMCoder{chatter: chatter, executor: executor}
}

// SetDeps overrides the I/O dependencies used by the agentic loop.
// This is intended for testing; production code leaves deps nil so that
// ProductionCoderDeps is used automatically.
func (c *LLMCoder) SetDeps(deps CoderDeps) {
	c.deps = &deps
}

// pass — skeleton filled by replace below

func (c *LLMCoder) Build(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (workflow.CodePlan, error) {
	taskList := make([]string, 0, len(tasks))
	for _, t := range tasks {
		entry := fmt.Sprintf("- %s", t.Title)
		if len(t.Steps) > 0 {
			for _, s := range t.Steps {
				entry += fmt.Sprintf("\n  - %s", s)
			}
		}
		taskList = append(taskList, entry)
	}

	wc := model.WorkflowContextFrom(ctx)
	systemPrompt := prompts.Coder(c.executor != nil, wc.ToolsPrompt)
	ctxInfo := contextSummary(ctx)
	userMsg := fmt.Sprintf("Goal: %s\n\nTasks:\n%s\n\nGenerate the implementation artifact plan.",
		intent.Goal, strings.Join(taskList, "\n"))
	if wc.WorkspaceRoot != "" {
		userMsg = fmt.Sprintf("Workspace root: %s\n\n%s", wc.WorkspaceRoot, userMsg)
	}
	if ctxInfo != "" {
		userMsg = ctxInfo + "\n" + userMsg
	}

	messages := []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}

	if c.executor == nil {
		return c.singleShotBuild(ctx, workflowID, intent, messages)
	}
	return c.agenticBuild(ctx, workflowID, intent, messages)
}

func (c *LLMCoder) singleShotBuild(ctx context.Context, workflowID string, intent model.IntentSpec, messages []Message) (workflow.CodePlan, error) {
	reply, err := RecoverableChat(ctx, c.chatter, RoleCoder, messages, defaultRecoveryConfig())
	if err != nil {
		if recovered, ok := c.recoverTruncation(ctx, messages, err); ok {
			reply = recovered
		} else {
			return workflow.CodePlan{}, fmt.Errorf("coder llm: %w", err)
		}
	}

	plan := parseCodePlanReply(workflowID, intent.ID, intent.Goal, reply)
	slog.Info("[llm:coder] parsed code plan", "workflow_id", workflowID, "tool_calls", len(plan.Calls()))
	plan = refineCodePlanWithContext(ctx, workflowID, plan)
	c.push("info", "coder", fmt.Sprintf("generated %d tool call(s)", len(plan.Calls())))
	return plan, nil
}

func (c *LLMCoder) agenticBuild(ctx context.Context, workflowID string, intent model.IntentSpec, messages []Message) (workflow.CodePlan, error) {
	var allMutations []workflow.ToolCall

	// M8-07: Token budget for the agentic message history.
	// Allocation: tool-history 40% of available tokens (after system+context).
	// Available ≈ 30 000 tokens is a conservative cap independent of concrete model.
	const (
		availableTokens  = 30000 // conservative prompt budget available to the coder
		toolHistoryRatio = 40    // % of availableTokens reserved for round history
	)
	toolHistoryBudget := availableTokens * toolHistoryRatio / 100 // 12 000 tokens

	// Resolve dependency injection: use explicit deps if set, otherwise
	// lazily wire up production implementations.
	deps := c.deps
	if deps == nil {
		d := ProductionCoderDeps(c.chatter, c.executor, toolHistoryBudget)
		deps = &d
	}

	// T1-03: Query profiler — zero-cost when CODEN_PROFILE_QUERY != "1".
	prof := NewProfiler()
	prof.Checkpoint("agentic_build_start")
	defer func() {
		prof.Checkpoint("agentic_build_end")
		if report := prof.Report(); report != "" {
			slog.Debug(report)
		}
	}()

	// T1-02: Output-side continuation tracker — nudges the model to keep
	// working when it stops before exhausting the output token budget.
	// Budget set to availableTokens (conservative prompt budget).
	contTracker := tokenbudget.NewContinuationTracker(30000)
	// read_file results: cap each call at 25% of tool-history budget (in chars, 4 chars/token).
	readBudgetChars := (toolHistoryBudget * 25 / 100) * 4 // = 12000 * 0.25 * 4 = 12000 chars, but cap at 3000
	if readBudgetChars > 3000 {
		readBudgetChars = 3000
	}

	for round := 0; round < maxCoderRounds; round++ {
		prof.Checkpoint(fmt.Sprintf("round_%d_compress_start", round+1))
		// M12-03: 4-layer compression chain (delegated to deps.Compress).
		messages = deps.Compress(messages, round, toolHistoryBudget)
		prof.Checkpoint(fmt.Sprintf("round_%d_compress_end", round+1))

		prof.Checkpoint("api_request_sent")
		reply, err := deps.Chat(ctx, messages)
		prof.Checkpoint("first_chunk_received")
		if err != nil {
			if recovered, ok := c.recoverTruncation(ctx, messages, err); ok {
				reply = recovered
			} else {
				return workflow.CodePlan{}, fmt.Errorf("coder llm round %d: %w", round+1, err)
			}
		}

		calls := parsePlanToolCalls(workflowID, reply)
		slog.Info("[llm:coder] parsed tool calls from reply", "round", round+1, "total", len(calls), "reads", len(func() []workflow.ToolCall { r, _ := splitToolCalls(calls); return r }()), "mutations", len(func() []workflow.ToolCall { _, m := splitToolCalls(calls); return m }()))
		if len(calls) == 0 {
			// T1-02: Check if the model stopped prematurely — nudge to continue
			// if the output token budget has not been sufficiently consumed.
			decision := contTracker.Check(tokenbudget.EstimateTokens(reply))
			if decision.ShouldContinue {
				slog.Info("[llm:coder] token budget continuation",
					"round", round+1, "pct", decision.Pct, "tokens", decision.TurnTokens)
				c.push("info", "coder", fmt.Sprintf("round %d: budget %d%% used, nudging to continue", round+1, decision.Pct))
				messages = append(messages,
					Message{Role: "assistant", Content: reply},
					Message{Role: "user", Content: decision.NudgeMessage},
				)
				round-- // continuation does not consume a round
				continue
			}

			// LLM produced no more tool calls — finalize with accumulated mutations.
			if len(allMutations) > 0 {
				// Agentic loop produced real mutations — return them directly
				// without the parseCodePlanReply fallback which would append
				// a spurious artifacts/intent-*.md write that overwrites the
				// correct artifact path.
				first := allMutations[0]
				plan := workflow.CodePlan{
					ToolCalls:  allMutations,
					ToolCallID: first.ToolCallID,
					Request:    first.Request,
				}
				plan = refineCodePlanWithContext(ctx, workflowID, plan)
				c.push("info", "coder", fmt.Sprintf("round %d: final plan with %d mutation(s)", round+1, len(plan.Calls())))
				return plan, nil
			}
			// No mutations accumulated — fall back to parsing reply for inline code.
			plan := parseCodePlanReply(workflowID, intent.ID, intent.Goal, reply)
			plan = refineCodePlanWithContext(ctx, workflowID, plan)
			c.push("info", "coder", fmt.Sprintf("round %d: final plan with %d call(s)", round+1, len(plan.Calls())))
			return plan, nil
		}

		reads, mutations := splitToolCalls(calls)

		prof.Checkpoint(fmt.Sprintf("round_%d_tool_exec_start", round+1))
		// Execute reads and mutations immediately; feed all results back to LLM.
		var resultSummary strings.Builder

		resultSummary.WriteString(executeReadsParallel(ctx, c.executor, reads, readBudgetChars, round+1, c.push))

		for _, call := range mutations {
			slog.Info("[llm:coder] executing mutation tool call", "round", round+1, "kind", call.Request.Kind, "target", toolCallTarget(call))
			result, execErr := deps.Execute(ctx, call.Request)
			if execErr != nil {
				slog.Warn("[llm:coder] mutation tool call failed", "round", round+1, "kind", call.Request.Kind, "target", toolCallTarget(call), "error", execErr)
				// Record failed mutation — LLM may retry with corrected args.
				resultSummary.WriteString(fmt.Sprintf("\n### %s %s\nerror: %s\n",
					call.Request.Kind, toolCallTarget(call), execErr.Error()))
				c.push("warn", "coder", fmt.Sprintf("round %d: %s %s → error: %s",
					round+1, call.Request.Kind, toolCallTarget(call), execErr.Error()))
				continue
			}
			// Mutation succeeded — mark as pre-executed so the kernel skips
			// re-execution but still performs bookkeeping.
			call.Executed = true
			call.ExecResult = result
			allMutations = append(allMutations, call)
			resultSummary.WriteString(mutationResultLine(call, result))
			slog.Info("[llm:coder] mutation tool call completed", "round", round+1, "kind", call.Request.Kind, "target", toolCallTarget(call), "summary", result.Summary)
			c.push("info", "coder", fmt.Sprintf("round %d: %s %s → %s",
				round+1, call.Request.Kind, toolCallTarget(call), result.Summary))
		}

		prof.Checkpoint(fmt.Sprintf("round_%d_tool_exec_end", round+1))

		// Feed all tool results (reads + mutations) back to LLM for next round.
		if resultSummary.Len() > 0 {
			messages = append(messages, Message{Role: "assistant", Content: reply})
			messages = append(messages, Message{
				Role: "user",
				Content: "Tool results:\n" + resultSummary.String() +
					"\n\nContinue. If all required mutations are done, reply with an empty tool_calls array: {\"tool_calls\": []}.",
			})
		}
	}

	if len(allMutations) == 0 {
		return workflow.CodePlan{}, fmt.Errorf("coder agentic loop produced no mutations after %d rounds", maxCoderRounds)
	}

	first := allMutations[0]
	c.push("info", "coder", fmt.Sprintf("agentic loop: %d mutation(s) total", len(allMutations)))
	plan := workflow.CodePlan{
		ToolCalls:  allMutations,
		ToolCallID: first.ToolCallID,
		Request:    first.Request,
	}
	plan = refineCodePlanWithContext(ctx, workflowID, plan)
	return plan, nil
}

// executeReadsParallel runs read-only tool calls concurrently (up to 8 at a
// time) and returns a combined result summary string. Results are returned in
// the same order as the input slice so that LLM feedback is deterministic.
// If an individual read fails, a warning is logged and an error entry is
// included in the output — other reads are not affected.
func executeReadsParallel(ctx context.Context, executor toolruntime.Executor, reads []workflow.ToolCall, readBudgetChars int, round int, pushFn func(string, string, string)) string {
	if len(reads) == 0 {
		return ""
	}
	slog.Info("[llm:coder] executing reads in parallel", "round", round, "count", len(reads))

	results := make([]string, len(reads))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // concurrency limiter

	for i, call := range reads {
		wg.Add(1)
		go func(i int, call workflow.ToolCall) {
			defer wg.Done()
			sem <- struct{}{}        // acquire slot
			defer func() { <-sem }() // release slot

			slog.Info("[llm:coder] executing read tool call", "round", round, "kind", call.Request.Kind, "target", toolCallTarget(call))
			result, execErr := executor.Execute(ctx, call.Request)
			if execErr != nil {
				slog.Warn("[llm:coder] read tool call failed", "round", round, "kind", call.Request.Kind, "target", toolCallTarget(call), "error", execErr)
				results[i] = fmt.Sprintf("\n### %s %s\nError: %s\n",
					call.Request.Kind, toolCallTarget(call), execErr.Error())
				return
			}
			// M12-01: If the result was spilled to disk, show a preview + hint
			// so the LLM knows the full content is recoverable via read_file.
			if result.SpilledPath != "" {
				results[i] = fmt.Sprintf(
					"\n### %s %s\nResult spilled to disk (%d bytes). Preview (first lines):\n%s\nUse read_file to access specific sections if needed.\n",
					call.Request.Kind, toolCallTarget(call), len(result.Output), result.Preview)
			} else {
				// M8-07: Truncate read output to fit within per-call budget.
				results[i] = fmt.Sprintf("\n### %s %s\n%s\n",
					call.Request.Kind, toolCallTarget(call), truncateOutput(result.Output, readBudgetChars))
			}
			slog.Info("[llm:coder] read tool call completed", "round", round, "kind", call.Request.Kind, "target", toolCallTarget(call), "output_len", len(result.Output), "spilled", result.SpilledPath != "")
			pushFn("info", "coder", fmt.Sprintf("round %d: %s %s → %s",
				round, call.Request.Kind, toolCallTarget(call), result.Summary))
		}(i, call)
	}

	wg.Wait()

	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(r)
	}
	return sb.String()
}

// recoverTruncation detects an output-truncated LLM response and retries up to
// maxTruncationRetries times, concatenating all partial outputs into a single
// combined reply.  Recovery retries do NOT consume rounds from maxCoderRounds.
// It operates on a copy of messages so the caller's conversation state is unchanged.
func (c *LLMCoder) recoverTruncation(ctx context.Context, messages []Message, firstErr error) (string, bool) {
	var te *provider.TruncatedError
	if !errors.As(firstErr, &te) {
		return "", false
	}

	combined := te.Content
	// Work on a copy so the caller's messages slice is not mutated.
	recoveryMsgs := make([]Message, len(messages))
	copy(recoveryMsgs, messages)

	for attempt := 0; attempt < maxTruncationRetries; attempt++ {
		slog.Warn("[llm:coder] output truncated, recovery attempt",
			"attempt", attempt+1, "max", maxTruncationRetries,
			"partial_len", len(te.Content), "combined_len", len(combined))
		c.push("warn", "coder", fmt.Sprintf("output truncated, recovery attempt %d/%d", attempt+1, maxTruncationRetries))

		recoveryMsgs = append(recoveryMsgs,
			Message{Role: "assistant", Content: te.Content},
			Message{Role: "user", Content: truncationRecoveryMsg},
		)

		reply, err := RecoverableChat(ctx, c.chatter, RoleCoder, recoveryMsgs, defaultRecoveryConfig())
		if err == nil {
			combined += reply
			slog.Info("[llm:coder] truncation recovery succeeded",
				"attempt", attempt+1, "combined_len", len(combined))
			return combined, true
		}

		if !errors.As(err, &te) {
			// Non-truncation error — return what we accumulated so far, if any.
			slog.Warn("[llm:coder] truncation recovery hit non-truncation error", "error", err)
			if combined != "" {
				return combined, true
			}
			return "", false
		}
		combined += te.Content
	}

	// All retries exhausted — return accumulated content if we have any.
	if combined != "" {
		slog.Warn("[llm:coder] truncation recovery exhausted retries, using accumulated content",
			"combined_len", len(combined))
		return combined, true
	}
	return "", false
}

// compressAgenticHistory trims the agentic message history to fit within
// toolHistoryBudget tokens. It always preserves:
//   - messages[0] (system) and messages[1] (initial user)
//   - the last 2 assistant/user round pairs (most recent context)
//
// Middle rounds are compressed to a one-line summary.
func compressAgenticHistory(messages []Message, toolHistoryBudget int) []Message {
	if msgTokens(messages) <= toolHistoryBudget {
		return messages
	}
	if len(messages) <= 6 {
		// Too few messages to compress meaningfully (need ≥7 to form a non-empty middle).
		return messages
	}

	// Identify the fixed head (system + initial user) and tail (last 2 pairs = 4 msgs).
	head := messages[:2]
	tail := messages[len(messages)-4:]
	middle := messages[2 : len(messages)-4]
	if len(middle) == 0 {
		return messages
	}

	// Compress middle rounds into brief summaries.
	compressed := make([]Message, 0, 2+len(middle)/2+4)
	compressed = append(compressed, head...)

	for i := 0; i < len(middle)-1; i += 2 {
		// Each pair is (assistant reply, user tool-result).
		assistant := middle[i]
		user := middle[i+1]
		// Count tool calls from assistant reply to summarize.
		calls := parsePlanToolCalls("_", assistant.Content)
		summary := fmt.Sprintf("[round compressed: %d tool call(s) made]", len(calls))
		_ = user // drop verbose tool result, keep 1-line summary
		compressed = append(compressed,
			Message{Role: "assistant", Content: summary},
			Message{Role: "user", Content: "(tool results omitted — see current state above)"},
		)
	}
	compressed = append(compressed, tail...)
	return compressed
}

// msgTokens estimates the total token count of a message slice.
func msgTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += tokenbudget.EstimateTokens(m.Content)
	}
	return total
}

func splitToolCalls(calls []workflow.ToolCall) (reads, mutations []workflow.ToolCall) {
	for _, call := range calls {
		switch call.Request.Kind {
		case "read_file", "search", "list_dir", "grep_context",
			"lsp_symbols", "lsp_definition", "lsp_references",
			"rag_search":
			reads = append(reads, call)
		default:
			mutations = append(mutations, call)
		}
	}
	return
}

// mutationResultLine formats a successful mutation result for LLM feedback.
func mutationResultLine(call workflow.ToolCall, result toolruntime.Result) string {
	target := toolCallTarget(call)
	switch call.Request.Kind {
	case "write_file":
		nBytes := len(result.After)
		return fmt.Sprintf("\n### write_file %s\nwritten (%d bytes)\n", target, nBytes)
	case "edit_file":
		return fmt.Sprintf("\n### edit_file %s\n%s\n", target, result.Summary)
	case "run_shell":
		output := truncateOutput(result.Output, 2000)
		if result.Stderr != "" {
			output += "\nstderr: " + truncateOutput(result.Stderr, 1000)
		}
		return fmt.Sprintf("\n### run_shell (exit %d)\n%s\n", result.ExitCode, output)
	default:
		return fmt.Sprintf("\n### %s %s\n%s\n", call.Request.Kind, target, result.Summary)
	}
}

func toolCallTarget(call workflow.ToolCall) string {
	if call.Request.Path != "" {
		return call.Request.Path
	}
	if call.Request.Dir != "" {
		return call.Request.Dir
	}
	if call.Request.Query != "" {
		return call.Request.Query
	}
	return ""
}

func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}

var _ workflow.Coder = (*LLMCoder)(nil)

func (c *LLMCoder) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "llm-coder", Role: workflow.RoleCoder}
}
