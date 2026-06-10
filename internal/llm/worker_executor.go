package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
	"github.com/mingzhi1/coden/internal/llm/provider"
	"github.com/mingzhi1/coden/internal/llm/tokenbudget"
	"github.com/mingzhi1/coden/internal/outputcompressor"
)

// LLMExecutor uses an LLM to generate a structured tool plan.
// When an Executor is provided, it runs an agentic loop: read-only tool
// calls (read_file, search, list_dir) are executed locally and their
// results fed back into the LLM conversation for up to maxRounds.
// Mutation calls (write_file, edit_file, run_shell) are collected and
// returned in the final CodePlan for the kernel to execute.
type LLMExecutor struct {
	chatter          Chatter
	executor         toolruntime.Executor // optional; enables agentic read loop
	deps             *ExecutorDeps           // optional; nil = use production defaults
	outputCompressor *outputcompressor.Compressor
	msgBuffer
}

const maxExecutorRounds = 5

// maxReadResultChars caps a single read-class tool result fed back into the
// agentic history: ~16K tokens ≈ a 2000-line source file, i.e. whole files in
// practice. The cap exists only so one pathological file cannot evict the
// entire round history; everything under it passes through verbatim — the
// output compressor's strategies match run_shell output only, so read_file
// content is never rewritten, at most truncated at this bound.
const maxReadResultChars = 64000

const (
	maxTruncationRetries  = 3
	truncationRecoveryMsg = "Output token limit hit. Resume directly — no apology, no recap of what you were doing. Pick up mid-thought if that is where the cut happened. Break remaining work into smaller pieces."
)

func NewLLMExecutor(chatter Chatter) *LLMExecutor {
	return &LLMExecutor{chatter: chatter, outputCompressor: outputcompressor.New()}
}

// NewAgenticExecutor creates an agentic executor with tool-use loop capability.
func NewAgenticExecutor(chatter Chatter, executor toolruntime.Executor) *LLMExecutor {
	return &LLMExecutor{chatter: chatter, executor: executor, outputCompressor: outputcompressor.New()}
}

// SetDeps overrides the I/O dependencies used by the agentic loop.
// This is intended for testing; production code leaves deps nil so that
// ProductionExecutorDeps is used automatically.
func (c *LLMExecutor) SetDeps(deps ExecutorDeps) {
	c.deps = &deps
}

// pass — skeleton filled by replace below

func (c *LLMExecutor) Build(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (workflow.CodePlan, error) {
	taskList := make([]string, 0, len(tasks))
	for _, t := range tasks {
		entry := fmt.Sprintf("- %s", t.Title)
		if t.SuccessCmd != "" {
			entry += fmt.Sprintf(" [verify: %s]", t.SuccessCmd)
		}
		if len(t.Steps) > 0 {
			for _, s := range t.Steps {
				entry += fmt.Sprintf("\n  - %s", s)
			}
		}
		taskList = append(taskList, entry)
	}

	wc := model.WorkflowContextFrom(ctx)
	systemPrompt := prompts.Executor(c.executor != nil, wc.ToolsPrompt)
	ctxInfo := contextSummary(ctx)
	userMsg := fmt.Sprintf("Goal: %s\n\nTasks:\n%s\n\nGenerate the implementation artifact plan.",
		intent.Goal, strings.Join(taskList, "\n"))
	// Workflow-assigned objective: what to implement and the success condition.
	if obj := strings.TrimSpace(wc.RoleObjectives[string(workflow.RoleExecutor)]); obj != "" {
		userMsg += "\n\nObjective — what to implement and when it's done:\n" + obj
	}

	// Inject critic feedback so the executor addresses flagged issues.
	if len(wc.CritiqueIssues) > 0 {
		var issues strings.Builder
		issues.WriteString("\n\n## Critic Feedback (address in implementation)\n")
		for _, issue := range wc.CritiqueIssues {
			issues.WriteString("- " + issue + "\n")
		}
		userMsg += issues.String()
	}

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

	// Questions / chat must not modify the repo (IntentKindQuestion is defined as
	// "no code changes"). Force single-shot text mode so the agentic loop never
	// pre-executes file writes for a greeting or an explanation.
	if intent.IsQuestion() {
		return c.singleShotBuild(ctx, workflowID, intent, messages)
	}
	if c.executor == nil {
		return c.singleShotBuild(ctx, workflowID, intent, messages)
	}
	return c.agenticBuild(ctx, workflowID, intent, messages, collectVerifyCmds(tasks))
}

func (c *LLMExecutor) singleShotBuild(ctx context.Context, workflowID string, intent model.IntentSpec, messages []Message) (workflow.CodePlan, error) {
	reply, err := RecoverableChat(ctx, c.chatter, RoleExecutor, messages, defaultRecoveryConfig())
	if err != nil {
		if recovered, ok := c.recoverTruncation(ctx, messages, err); ok {
			reply = recovered
		} else {
			return workflow.CodePlan{}, fmt.Errorf("executor llm: %w", err)
		}
	}

	plan := parseCodePlanReply(workflowID, intent.ID, intent.Goal, reply)
	slog.Info("[llm:executor] parsed code plan", "workflow_id", workflowID, "tool_calls", len(plan.Calls()))
	plan = refineCodePlanWithContext(ctx, workflowID, plan)
	c.push("info", "executor", fmt.Sprintf("generated %d tool call(s)", len(plan.Calls())))
	return plan, nil
}

func (c *LLMExecutor) agenticBuild(ctx context.Context, workflowID string, intent model.IntentSpec, messages []Message, verifyCmds []string) (workflow.CodePlan, error) {
	var allMutations []workflow.ToolCall

	// Verify-in-loop state. Before honoring the executor's "done" signal, the
	// task's verify command(s) are run inside the loop so the model fixes a
	// failure here — with the real output in hand — instead of finishing, being
	// rejected by the kernel's out-of-loop success_cmd, and having the whole
	// task re-run from scratch. verified latches once the commands pass (or
	// can't be run); the attempt cap bounds in-loop fix cycles before falling
	// back to the kernel's authoritative gate.
	verified := false
	verifyAttempts := 0
	const maxVerifyAttempts = 2

	// Read-only mode (e.g. analyze): the Executor may execute read tools to gather
	// context but must NEVER modify the repo. Mutation tool calls are reported
	// back to the model as "not executed" and dropped — see the loop below.
	readOnly := model.WorkflowContextFrom(ctx).ExecutorMode == model.ExecutorModeReadOnly

	// M8-07: Token budget for the agentic message history.
	// Allocation: tool-history 40% of available tokens (after system+context).
	// 120K matches the analyzer loop and assumes a big-context primary provider
	// (claude / gpt-4o class). Smaller-context providers overflow into the
	// recovery layer (emergencyCompress) — the same trade the analyzer makes.
	const (
		availableTokens  = 120000 // prompt budget available to the executor
		toolHistoryRatio = 40     // % of availableTokens reserved for round history
	)
	toolHistoryBudget := availableTokens * toolHistoryRatio / 100 // 48 000 tokens

	// Resolve dependency injection: use explicit deps if set, otherwise
	// lazily wire up production implementations.
	deps := c.deps
	if deps == nil {
		d := ProductionExecutorDeps(c.chatter, c.executor, toolHistoryBudget)
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
	// 30K output tokens — deliberately independent of the prompt-side
	// availableTokens above; raising it would make the continue-nudge fire
	// on nearly every reply.
	contTracker := tokenbudget.NewContinuationTracker(30000)
	cumulativeOutputTokens := 0 // cumulative output tokens across all rounds
	// read_file results: whole files. The executor edits code it must first see —
	// truncated reads produce blind patches. maxReadResultChars only guards
	// against a single pathological file evicting the whole round history;
	// older rounds degrade through compressAgenticHistory, which keeps the
	// last two rounds verbatim.
	readBudgetChars := maxReadResultChars

	degenerateCount := 0           // consecutive degenerate replies
	const maxDegenerateRetries = 2 // max degenerate retries before giving up on round

	for round := 0; round < maxExecutorRounds; round++ {
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
				return workflow.CodePlan{}, fmt.Errorf("executor llm round %d: %w", round+1, err)
			}
		}

		calls := parsePlanToolCalls(workflowID, reply)
		slog.Info("[llm:executor] parsed tool calls from reply", "round", round+1, "total", len(calls), "reads", len(func() []workflow.ToolCall { r, _ := splitToolCalls(calls); return r }()), "mutations", len(func() []workflow.ToolCall { _, m := splitToolCalls(calls); return m }()))
		if len(calls) == 0 {
			// Check for degenerate response (rate-limit degradation).
			// Degenerate replies are very short and contain no tool calls — they
			// indicate the API is returning stub responses after rate limiting.
			// Back off instead of nudging, to avoid wasting API quota.
			if IsDegenerateReply(reply) {
				degenerateCount++
				slog.Warn("[llm:executor] degenerate response detected",
					"round", round+1, "reply_len", len(reply),
					"degenerate_count", degenerateCount)
				c.push("warn", "executor", fmt.Sprintf("round %d: degenerate response (%d chars), backing off (%d/%d)",
					round+1, len(reply), degenerateCount, maxDegenerateRetries))
				if degenerateCount > maxDegenerateRetries {
					slog.Warn("[llm:executor] too many degenerate responses, aborting",
						"round", round+1, "degenerate_count", degenerateCount)
					return workflow.CodePlan{}, fmt.Errorf(
						"executor: %d consecutive degenerate responses (<%d chars) — API may be rate-limited or degraded",
						degenerateCount, DegenerateReplyThreshold)
				}
				// Exponential backoff: 2s, 4s before retry
				backoff := time.Duration(1<<uint(degenerateCount)) * time.Second
				select {
				case <-ctx.Done():
					return workflow.CodePlan{}, ctx.Err()
				case <-time.After(backoff):
				}
				round-- // degenerate retry does not consume a round
				continue
			}

			// Reset degenerate counter on non-degenerate reply
			degenerateCount = 0

			// T1-02: Check if the model stopped prematurely — nudge to continue
			// if the output token budget has not been sufficiently consumed.
			//
			// EXCEPTION: an explicit empty tool_calls array is the protocol's
			// "I'm done" signal (the prompt instructs the model to reply with
			// {"tool_calls": []} when finished). Nudging it to continue would loop
			// forever on trivial turns (e.g. a chat greeting that needs no tools),
			// since a short final reply always looks "budget under-consumed".
			cumulativeOutputTokens += tokenbudget.EstimateTokens(reply)
			decision := contTracker.Check(cumulativeOutputTokens)
			if decision.ShouldContinue && !replyDeclaresDone(reply) {
				slog.Info("[llm:executor] token budget continuation",
					"round", round+1, "pct", decision.Pct, "tokens", decision.TurnTokens)
				c.push("info", "executor", fmt.Sprintf("round %d: budget %d%% used, nudging to continue", round+1, decision.Pct))
				messages = append(messages,
					Message{Role: "assistant", Content: reply},
					Message{Role: "user", Content: decision.NudgeMessage},
				)
				round-- // continuation does not consume a round
				continue
			}

			// ── Verify-in-loop gate ──────────────────────────────────────────
			// Run the task's verify command(s) before honoring "done". On failure
			// the model gets the real output and fixes it in-loop instead of
			// finishing and being bounced by the kernel's out-of-loop gate. The
			// kernel still re-runs the command afterward as the authoritative
			// decision (LLM proposes, code decides); this front-loads the
			// red/green cycle into the executor's hands.
			if !readOnly && len(verifyCmds) > 0 && !verified && verifyAttempts < maxVerifyAttempts {
				verifyAttempts++
				passed, runnable, report := runVerifyCmds(ctx, deps, verifyCmds)
				switch {
				case !runnable:
					verified = true // can't self-verify (e.g. shell disabled) — kernel gate covers it
				case passed:
					verified = true
					c.push("info", "executor", "verify passed in-loop: "+strings.Join(verifyCmds, "; "))
				default:
					c.push("warn", "executor", fmt.Sprintf("verify failed, fixing in-loop (%d/%d)", verifyAttempts, maxVerifyAttempts))
					messages = append(messages,
						Message{Role: "assistant", Content: reply},
						Message{Role: "user", Content: "Before finishing, the task's verify command ran and FAILED:\n\n" + report + "\n\nDiagnose and fix the cause, then reply with tool_calls. You will be re-verified."},
					)
					round-- // the verify run itself is not a model round
					continue
				}
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
				c.push("info", "executor", fmt.Sprintf("round %d: final plan with %d mutation(s)", round+1, len(plan.Calls())))
				return plan, nil
			}
			// No mutations accumulated — fall back to parsing reply for inline code.
			plan := parseCodePlanReply(workflowID, intent.ID, intent.Goal, reply)
			plan = refineCodePlanWithContext(ctx, workflowID, plan)
			c.push("info", "executor", fmt.Sprintf("round %d: final plan with %d call(s)", round+1, len(plan.Calls())))
			return plan, nil
		}

		reads, mutations := splitToolCalls(calls)
		cumulativeOutputTokens += tokenbudget.EstimateTokens(reply)
		degenerateCount = 0 // successful tool calls reset degenerate counter

		prof.Checkpoint(fmt.Sprintf("round_%d_tool_exec_start", round+1))
		// Execute reads and mutations immediately; feed all results back to LLM.
		var resultSummary strings.Builder

		resultSummary.WriteString(executeReadsParallel(ctx, "executor", c.executor, reads, readBudgetChars, round+1, c.push, c.outputCompressor))

		// Read-only mode: drop mutations without executing them, and tell the
		// model they were not run so it stops re-issuing writes and concludes.
		if readOnly && len(mutations) > 0 {
			for _, call := range mutations {
				slog.Info("[llm:executor] read-only mode: skipping mutation", "round", round+1, "kind", call.Request.Kind, "target", toolCallTarget(call))
				c.push("info", "executor", fmt.Sprintf("round %d: read-only — skipped %s %s", round+1, call.Request.Kind, toolCallTarget(call)))
				fmt.Fprintf(&resultSummary, "\n### %s %s\n(read-only analysis mode: NOT executed — do not modify files; provide your analysis as text)\n", call.Request.Kind, toolCallTarget(call))
			}
			mutations = nil
		}

		for _, call := range mutations {
			slog.Info("[llm:executor] executing mutation tool call", "round", round+1, "kind", call.Request.Kind, "target", toolCallTarget(call))
			result, execErr := deps.Execute(ctx, call.Request)
			if execErr != nil {
				slog.Warn("[llm:executor] mutation tool call failed", "round", round+1, "kind", call.Request.Kind, "target", toolCallTarget(call), "error", execErr)
				// Record failed mutation — LLM may retry with corrected args.
				resultSummary.WriteString(fmt.Sprintf("\n### %s %s\nerror: %s\n",
					call.Request.Kind, toolCallTarget(call), execErr.Error()))
				c.push("warn", "executor", fmt.Sprintf("round %d: %s %s → error: %s",
					round+1, call.Request.Kind, toolCallTarget(call), execErr.Error()))
				continue
			}
			// Mutation succeeded — mark as pre-executed so the kernel skips
			// re-execution but still performs bookkeeping.
			call.Executed = true
			call.ExecResult = result
			allMutations = append(allMutations, call)
			resultSummary.WriteString(mutationResultLine(call, result, c.outputCompressor))
			slog.Info("[llm:executor] mutation tool call completed", "round", round+1, "kind", call.Request.Kind, "target", toolCallTarget(call), "summary", result.Summary)
			c.push("info", "executor", fmt.Sprintf("round %d: %s %s → %s",
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
		if readOnly {
			// Read-only (analyze): producing no mutations is the expected outcome —
			// the investigation is done by reading. Return an empty plan so the
			// workflow completes and the Responder delivers the analysis.
			return workflow.CodePlan{}, nil
		}
		return workflow.CodePlan{}, fmt.Errorf("executor agentic loop produced no mutations after %d rounds", maxExecutorRounds)
	}

	first := allMutations[0]
	c.push("info", "executor", fmt.Sprintf("agentic loop: %d mutation(s) total", len(allMutations)))
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
func executeReadsParallel(ctx context.Context, role string, executor toolruntime.Executor, reads []workflow.ToolCall, readBudgetChars int, round int, pushFn func(string, string, string), oc *outputcompressor.Compressor) string {
	if len(reads) == 0 {
		return ""
	}
	logPrefix := "[llm:" + role + "]"
	slog.Info(logPrefix+" executing reads in parallel", "round", round, "count", len(reads))

	results := make([]string, len(reads))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // concurrency limiter

	for i, call := range reads {
		wg.Add(1)
		go func(i int, call workflow.ToolCall) {
			defer wg.Done()
			sem <- struct{}{}        // acquire slot
			defer func() { <-sem }() // release slot

			slog.Info(logPrefix+" executing read tool call", "round", round, "kind", call.Request.Kind, "target", toolCallTarget(call))
			result, execErr := executor.Execute(ctx, call.Request)
			if execErr != nil {
				slog.Warn(logPrefix+" read tool call failed", "round", round, "kind", call.Request.Kind, "target", toolCallTarget(call), "error", execErr)
				results[i] = fmt.Sprintf("\n### %s %s\nError: %s\n",
					call.Request.Kind, toolCallTarget(call), execErr.Error())
				return
			}
			// M12-01: Spill is archival bookkeeping, not a visibility limit — the
			// model gets full content whenever it fits the per-read budget. Only
			// genuinely over-budget results degrade to a preview + hint. (The old
			// behavior keyed on SpilledPath alone, so any file over the 8K spill
			// threshold reached the model as a 20-line preview telling it to
			// read_file again — which would just spill again.)
			if result.SpilledPath != "" && len(result.Output) > readBudgetChars {
				results[i] = fmt.Sprintf(
					"\n### %s %s\nResult too large to inline (%d bytes). Preview (first lines):\n%s\nNarrow it down: use grep_context/search for the specific symbols or sections you need.\n",
					call.Request.Kind, toolCallTarget(call), len(result.Output), result.Preview)
			} else {
				// M8-07: Pass-through under budget; truncate at the bound.
				results[i] = fmt.Sprintf("\n### %s %s\n%s\n",
					call.Request.Kind, toolCallTarget(call), oc.Compress(call.Request.Kind, "", result.Output, readBudgetChars, ""))
			}
			slog.Info(logPrefix+" read tool call completed", "round", round, "kind", call.Request.Kind, "target", toolCallTarget(call), "output_len", len(result.Output), "spilled", result.SpilledPath != "")
			pushFn("info", role, fmt.Sprintf("round %d: %s %s → %s",
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
// combined reply.  Recovery retries do NOT consume rounds from maxExecutorRounds.
// It operates on a copy of messages so the caller's conversation state is unchanged.
func (c *LLMExecutor) recoverTruncation(ctx context.Context, messages []Message, firstErr error) (string, bool) {
	var te *provider.TruncatedError
	if !errors.As(firstErr, &te) {
		return "", false
	}

	combined := te.Content
	// Work on a copy so the caller's messages slice is not mutated.
	recoveryMsgs := make([]Message, len(messages))
	copy(recoveryMsgs, messages)

	for attempt := 0; attempt < maxTruncationRetries; attempt++ {
		slog.Warn("[llm:executor] output truncated, recovery attempt",
			"attempt", attempt+1, "max", maxTruncationRetries,
			"partial_len", len(te.Content), "combined_len", len(combined))
		c.push("warn", "executor", fmt.Sprintf("output truncated, recovery attempt %d/%d", attempt+1, maxTruncationRetries))

		recoveryMsgs = append(recoveryMsgs,
			Message{Role: "assistant", Content: te.Content},
			Message{Role: "user", Content: truncationRecoveryMsg},
		)

		reply, err := RecoverableChat(ctx, c.chatter, RoleExecutor, recoveryMsgs, defaultRecoveryConfig())
		if err == nil {
			combined += reply
			slog.Info("[llm:executor] truncation recovery succeeded",
				"attempt", attempt+1, "combined_len", len(combined))
			return combined, true
		}

		if !errors.As(err, &te) {
			// Non-truncation error — return what we accumulated so far, if any.
			slog.Warn("[llm:executor] truncation recovery hit non-truncation error", "error", err)
			if combined != "" {
				return combined, true
			}
			return "", false
		}
		combined += te.Content
	}

	// All retries exhausted — return accumulated content if we have any.
	if combined != "" {
		slog.Warn("[llm:executor] truncation recovery exhausted retries, using accumulated content",
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

// collectVerifyCmds returns the de-duplicated, non-empty success commands of the
// given tasks — the deterministic checks the executor must pass before finishing.
func collectVerifyCmds(tasks []model.Task) []string {
	seen := make(map[string]bool)
	var cmds []string
	for _, t := range tasks {
		cmd := strings.TrimSpace(t.SuccessCmd)
		if cmd != "" && !seen[cmd] {
			seen[cmd] = true
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// runVerifyCmds runs each verify command through the tool executor in order.
// passed is true only when every command exits 0. runnable is false when the
// tool layer refuses the command (e.g. shell disabled), signaling that in-loop
// verification isn't possible and the kernel's gate should be relied on instead.
func runVerifyCmds(ctx context.Context, deps *ExecutorDeps, cmds []string) (passed, runnable bool, report string) {
	for _, cmd := range cmds {
		res, err := deps.Execute(ctx, toolruntime.Request{Kind: "run_shell", Command: cmd})
		if err != nil {
			return false, false, ""
		}
		if res.ExitCode != 0 {
			out := strings.TrimSpace(res.Output)
			if s := strings.TrimSpace(res.Stderr); s != "" {
				out += "\n" + s
			}
			return false, true, fmt.Sprintf("$ %s\n(exit %d)\n%s", cmd, res.ExitCode, truncateToLines(out, 60))
		}
	}
	return true, true, ""
}

// splitToolCalls partitions calls by the catalog's ReadOnly flag. Reads run
// in parallel with the full read budget and stay available in read-only modes
// (analyzer, plan_only executor); everything else — true mutations, MCP tools
// (side effects unknown), unrecognized kinds — takes the serial mutation path.
// The previous hardcoded list misclassified tool_search/web_fetch/artifact
// reads as mutations, which both serialized them and made read-only mode
// refuse them outright.
func splitToolCalls(calls []workflow.ToolCall) (reads, mutations []workflow.ToolCall) {
	for _, call := range calls {
		if toolruntime.ReadOnlyKind(call.Request.Kind) {
			reads = append(reads, call)
		} else {
			mutations = append(mutations, call)
		}
	}
	return
}

// mutationResultLine formats a successful mutation result for LLM feedback.
func mutationResultLine(call workflow.ToolCall, result toolruntime.Result, oc *outputcompressor.Compressor) string {
	target := toolCallTarget(call)
	switch call.Request.Kind {
	case "write_file":
		nBytes := len(result.After)
		return fmt.Sprintf("\n### write_file %s\nwritten (%d bytes)\n", target, nBytes)
	case "edit_file":
		return fmt.Sprintf("\n### edit_file %s\n%s\n", target, result.Summary)
	case "run_shell":
		ec := string(result.ErrorClass)
		output := oc.Compress("run_shell", call.Request.Command, result.Output, 2000, ec)
		if result.Stderr != "" {
			output += "\nstderr: " + oc.Compress("run_shell", call.Request.Command, result.Stderr, 1000, ec)
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

var _ workflow.Executor = (*LLMExecutor)(nil)

func (c *LLMExecutor) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "llm-executor", Role: workflow.RoleExecutor}
}

// replyDeclaresDone reports whether the reply carries an explicit (present but
// empty) tool_calls array — the agentic protocol's "I'm finished, no more
// actions" signal. A pointer slice distinguishes "tool_calls": [] (done) from a
// reply with no tool_calls key at all (ambiguous — may still warrant a nudge).
func replyDeclaresDone(reply string) bool {
	var v struct {
		ToolCalls *[]json.RawMessage `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &v); err != nil {
		return false
	}
	return v.ToolCalls != nil && len(*v.ToolCalls) == 0
}
