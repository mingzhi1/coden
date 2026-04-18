package kernel

// kernel_task.go implements N-08: DAG-aware parallel task execution.
//
// runTasksConcurrent uses level-based scheduling:
//   1. computeTaskLevels assigns each task a level based on its DependsOn depth.
//   2. Tasks at the same level are launched concurrently (no shared dependencies).
//   3. Each level's results are awaited before the next level begins.
//
// runOneTask encapsulates the per-task Code→Accept retry loop and is safe to
// call concurrently for different tasks. L3-05: it runs task.SuccessCmd
// deterministically before calling the LLM acceptor.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/taskqueue"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/hook"
	clog "github.com/mingzhi1/coden/internal/log"
	"github.com/mingzhi1/coden/internal/secretary"
)

// taskExecResult carries the outcome of executing a single task through the
// Code→Accept retry loop.
type taskExecResult struct {
	taskIdx        int
	task           model.Task // final state (Status = passed | failed)
	checkpoint     model.CheckpointResult
	artifact       model.Artifact
	llmOutput      string       // accumulated assistant messages for insight extraction
	err            error        // non-nil → abort the whole workflow
	appendedTasks  []model.Task // M11-03: tasks the coder requested to append
	replanEvidence []string     // non-nil → replan remaining tasks with this failure evidence
}

// sharedTasks guards concurrent read/write access to the tasks slice.
// Multiple goroutines can update their own slot; callers share the same
// backing array so the main goroutine's slice is automatically updated.
type sharedTasks struct {
	mu    sync.Mutex
	tasks []model.Task
}

// wrapTasks creates a sharedTasks around the supplied slice WITHOUT copying it.
// When goroutines write to their own slot the caller's slice is also updated.
func wrapTasks(tasks []model.Task) *sharedTasks {
	return &sharedTasks{tasks: tasks}
}

// setStatus atomically updates tasks[idx].Status and returns a snapshot.
func (st *sharedTasks) setStatus(idx int, status string) []model.Task {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.tasks[idx].Status = status
	return st.copyLocked()
}

// setTask atomically replaces tasks[idx] and returns a snapshot.
func (st *sharedTasks) setTask(idx int, task model.Task) []model.Task {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.tasks[idx] = task
	return st.copyLocked()
}

// all returns a snapshot of the full slice.
func (st *sharedTasks) all() []model.Task {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.copyLocked()
}

// get returns a copy of tasks[idx].
func (st *sharedTasks) get(idx int) model.Task {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.tasks[idx]
}

// copyLocked copies the tasks slice; caller must hold mu.
func (st *sharedTasks) copyLocked() []model.Task {
	out := make([]model.Task, len(st.tasks))
	copy(out, st.tasks)
	return out
}

// computeTaskLevels assigns a DAG level to each task.
// Level 0 = no dependencies; level N = max(dep levels) + 1.
// Tasks at the same level can run concurrently.
// Returns levels as slices of task indices, sorted from 0 upward.
func computeTaskLevels(tasks []model.Task) [][]int {
	n := len(tasks)
	if n == 0 {
		return nil
	}
	idx := make(map[string]int, n)
	for i, t := range tasks {
		if t.ID != "" {
			idx[t.ID] = i
		}
	}

	level := make([]int, n) // level[i] = assigned level for task i

	// Iterative fix-point convergence (safe even for already-validated DAGs).
	changed := true
	for changed {
		changed = false
		for i, t := range tasks {
			maxDep := -1
			for _, dep := range t.DependsOn {
				if j, ok := idx[dep]; ok && level[j] > maxDep {
					maxDep = level[j]
				}
			}
			want := maxDep + 1
			if want != level[i] {
				level[i] = want
				changed = true
			}
		}
	}

	maxLevel := 0
	for _, l := range level {
		if l > maxLevel {
			maxLevel = l
		}
	}
	levels := make([][]int, maxLevel+1)
	for i, l := range level {
		levels[l] = append(levels[l], i)
	}
	return levels
}

// M11-03: maxAppendPerCoder caps how many tasks a single coder execution can
// append to prevent runaway LLM loops.
const maxAppendPerCoder = 3

// runOneTask executes the Code→Accept retry loop for a single task.
// It is self-contained and safe to run concurrently for different tasks.
// wfCtx is passed by value so each goroutine mutates its own copy.
func (k *Kernel) runOneTask(
	ctx context.Context,
	sessionID, workflowID string,
	intentSpec model.IntentSpec,
	taskIdx int,
	shared *sharedTasks,
	queue *taskqueue.Queue,
	wfCtx model.WorkflowContext,
) taskExecResult {
	task := shared.get(taskIdx)

	var taskRetryFeedback string
	var retryCtx model.RetryContext
	var llmOut strings.Builder
	var finalCheckpoint model.CheckpointResult
	var finalArtifact model.Artifact
	// cumulativeBeforeState accumulates the original file content from the FIRST
	// time each path is written across all attempts.  On rollback we restore to
	// the true pre-workflow state rather than the post-attempt-N state.
	cumulativeBeforeState := make(map[string][]byte)

	maxRetries := k.maxTaskRetries
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// ── Hook: pre_code ──────────────────────────────────────────────
		if blocked := k.runHookPoint(ctx, hook.PreCode, sessionID, workflowID, task.ID, task.Title, attempt); blocked {
			finalCheckpoint = model.CheckpointResult{
				WorkflowID: workflowID, SessionID: intentSpec.SessionID,
				Status: "fail", Evidence: []string{"pre_code hook blocked"},
				FixGuidance: "A pre_code hook prevented code execution.", CreatedAt: time.Now(),
			}
			break
		}
		// ── Code phase ──────────────────────────────────────────────────────
		snap := shared.setStatus(taskIdx, model.TaskStatusCoding)
		k.emitTasksUpdated(sessionID, workflowID, snap)
		k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
			WorkflowID: workflowID,
			Step:       "code",
			Status:     "running",
		})

		// CTX-02: Refresh FileTree on retry so the Coder sees files created
		// by previous attempts (e.g. new calc.go from attempt 1).
		if attempt > 0 {
			if freshFiles, listErr := k.workspace.ListFiles("", 200); listErr == nil {
				wfCtx.FileTree = freshFiles
			}
		}
		wfCtx.RetryFeedback = taskRetryFeedback
		taskCtx := model.WithWorkflowContext(ctx, wfCtx)

		coderInput := workflow.WorkerInput{
			SessionID:     sessionID,
			WorkflowID:    workflowID,
			TaskID:        task.ID,
			Intent:        intentSpec,
			Tasks:         []model.Task{task},
			RetryFeedback: taskRetryFeedback,
		}

		codeResult, err := k.executeWorker(taskCtx, sessionID, workflowID, "code", workflow.RoleCoder, k.workflow.CoderWorker(), coderInput)
		if err != nil {
			return taskExecResult{taskIdx: taskIdx, task: shared.get(taskIdx), err: err}
		}
		for _, msg := range codeResult.Messages {
			if msg.Role == "assistant" && msg.Content != "" {
				llmOut.WriteString(msg.Content)
				llmOut.WriteString("\n")
			}
		}

		// KA-06: Guard against nil CodePlan — coder may return messages without
		// a structured tool call plan (e.g. when the LLM responds conversationally).
		if codeResult.CodePlan == nil {
			return taskExecResult{
				taskIdx: taskIdx, task: shared.get(taskIdx),
				err: fmt.Errorf("coder returned nil code plan for task %q", task.Title),
			}
		}
		codePlan := *codeResult.CodePlan
		toolCalls := codePlan.Calls()
		if len(toolCalls) == 0 {
			return taskExecResult{
				taskIdx: taskIdx, task: shared.get(taskIdx),
				err: fmt.Errorf("coder returned empty tool plan for task %q", task.Title),
			}
		}

		codeWorkerID := workerIDFor(roleOrDefault(codeResult.Metadata, workflow.RoleCoder))
		attemptBeforeState, execErr := func() (map[string][]byte, error) {
			a, bs, e := k.executeToolPlan(taskCtx, sessionID, workflowID, codeWorkerID, task.Files, toolCalls)
			finalArtifact = a
			return bs, e
		}()
		if execErr != nil {
			return taskExecResult{taskIdx: taskIdx, task: shared.get(taskIdx), err: execErr}
		}
		// Merge into cumulativeBeforeState: first-write-wins preserves original content.
		for path, before := range attemptBeforeState {
			if _, already := cumulativeBeforeState[path]; !already {
				cumulativeBeforeState[path] = before
			}
		}
		if len(attemptBeforeState) > 0 {
			_, _ = k.objects.SaveSnapshot(workflowID, k.mainDBPath, attemptBeforeState)
		}
		k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
			WorkflowID: workflowID,
			Step:       "code",
			Status:     "done",
		})

		// ── Post-code hooks (zero-LLM-cost quality gates) ───────────────────
		skipAccept := false
		if hookBlocked := k.runHookPoint(taskCtx, hook.PostCode, sessionID, workflowID, task.ID, task.Title, attempt); hookBlocked {
			hookErrMsg := "post-code hooks blocked execution"
			finalCheckpoint = model.CheckpointResult{
				WorkflowID:    workflowID,
				SessionID:     intentSpec.SessionID,
				Status:        "fail",
				ArtifactPaths: []string{finalArtifact.Path},
				Evidence: []string{
					"post-code hooks detected issues",
					truncateCmdOutput(hookErrMsg, 4096),
				},
				FixGuidance: "Fix the issues reported by the post-code hooks before proceeding.",
				CreatedAt:   time.Now(),
			}
			skipAccept = true
			clog.Session(sessionID).Warn("post-code hooks failed, skipping accept",
				"workflow_id", workflowID,
				"task_id", task.ID,
				"attempt", attempt)
		}

		// ── L3-05: SuccessCmd deterministic verification ─────────────────────
		if !skipAccept && task.SuccessCmd != "" {
			if cmdOut, ok := runSuccessCmd(taskCtx, k.workspace.Root(), task.SuccessCmd); !ok {
				finalCheckpoint = model.CheckpointResult{
					WorkflowID:    workflowID,
					SessionID:     intentSpec.SessionID,
					Status:        "fail",
					ArtifactPaths: []string{finalArtifact.Path},
					Evidence: []string{
						fmt.Sprintf("success_cmd failed: %s", task.SuccessCmd),
						truncateCmdOutput(cmdOut, 2048),
					},
					FixGuidance: "Fix the issue shown by the task's success_cmd before proceeding.",
					CreatedAt:   time.Now(),
				}
				skipAccept = true
			}
		}

		// ── Accept phase ─────────────────────────────────────────────────────
		if skipAccept {
			// SM-03: Emit skipped event so the UI knows accept was bypassed.
			k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
				WorkflowID: workflowID,
				Step:       "accept",
				Status:     "skipped",
			})
		}
		if !skipAccept {
			snap = shared.setStatus(taskIdx, model.TaskStatusAccepting)
			k.emitTasksUpdated(sessionID, workflowID, snap)
			k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
				WorkflowID: workflowID,
				Step:       "accept",
				Status:     "running",
			})

			acceptResult, aErr := k.executeWorker(taskCtx, sessionID, workflowID, "accept", workflow.RoleAcceptor, k.workflow.AcceptorWorker(), workflow.WorkerInput{
				SessionID:  sessionID,
				WorkflowID: workflowID,
				TaskID:     task.ID,
				Intent:     intentSpec,
				Tasks:      []model.Task{task},
				Artifact:   finalArtifact,
			})
			if aErr != nil {
				return taskExecResult{taskIdx: taskIdx, task: shared.get(taskIdx), err: aErr}
			}
			for _, msg := range acceptResult.Messages {
				if msg.Role == "assistant" && msg.Content != "" {
					llmOut.WriteString(msg.Content)
					llmOut.WriteString("\n")
				}
			}
			if acceptResult.Checkpoint == nil {
				return taskExecResult{
					taskIdx: taskIdx,
					task:    shared.get(taskIdx),
					err:     fmt.Errorf("acceptor returned nil checkpoint for task %q", task.Title),
				}
			}
			finalCheckpoint = *acceptResult.Checkpoint
			// Guard against acceptor returning an empty status (bug in acceptor
			// implementation). Treat it as a fail so evidence is propagated.
			if finalCheckpoint.Status != "pass" && finalCheckpoint.Status != "fail" {
				finalCheckpoint.Status = "fail"
				finalCheckpoint.Evidence = append(finalCheckpoint.Evidence,
					fmt.Sprintf("acceptor returned invalid checkpoint status %q; treated as fail", acceptResult.Checkpoint.Status))
			}
			k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
				WorkflowID: workflowID,
				Step:       "accept",
				Status:     "done",
			})

			// ── Hook: post_accept ───────────────────────────────────────────
			k.runHookPoint(ctx, hook.PostAccept, sessionID, workflowID, task.ID, task.Title, attempt)
		}

		// ── Pass / Fail / Retry ─────────────────────────────────────────────
		if finalCheckpoint.Status == "pass" {
			task.Status = model.TaskStatusPassed
			task.Attempts = attempt + 1
			snap = shared.setTask(taskIdx, task)
			k.emitTasksUpdated(sessionID, workflowID, snap)
			clog.Session(sessionID).Info("task passed",
				"workflow_id", workflowID,
				"task_id", task.ID,
				"task", task.Title,
				"attempts", task.Attempts)

			// M11-03: Handle Coder-requested task appends.
			var appended []model.Task
			if len(codePlan.AppendTasks) > 0 {
				appendCount := len(codePlan.AppendTasks)
				if appendCount > maxAppendPerCoder {
					appendCount = maxAppendPerCoder
					clog.Session(sessionID).Warn("coder requested too many appends, capping",
						"requested", len(codePlan.AppendTasks), "cap", maxAppendPerCoder)
				}
				for i := 0; i < appendCount; i++ {
					at := codePlan.AppendTasks[i]
					if at.ID == "" {
						at.ID = nextKernelID("task-appended")
					}
					queue.Append(at, "coder")
					appended = append(appended, at)
					k.events.Emit(sessionID, model.EventWorkflowTaskAppended, model.TaskAppendedPayload{
						WorkflowID: workflowID,
						TaskID:     at.ID,
						TaskTitle:  at.Title,
						Source:     "coder",
					})
					clog.Session(sessionID).Info("coder appended task",
						"workflow_id", workflowID,
						"appended_task_id", at.ID,
						"appended_task_title", at.Title)
				}
				// Emit updated task list including appended tasks.
				k.emitTasksUpdated(sessionID, workflowID, queue.Snapshot())
			}

			return taskExecResult{
				taskIdx:       taskIdx,
				task:          task,
				checkpoint:    finalCheckpoint,
				artifact:      finalArtifact,
				llmOutput:     llmOut.String(),
				appendedTasks: appended,
			}
		}

		if attempt >= maxRetries {
			task.Status = model.TaskStatusFailed
			task.Attempts = attempt + 1
			snap = shared.setTask(taskIdx, task)
			k.emitTasksUpdated(sessionID, workflowID, snap)

			// M11-04: Kernel-level failure policy check.
			k.mu.Lock()
			fp := k.failurePolicy
			k.mu.Unlock()
			if fp == "skip" {
				clog.Session(sessionID).Info("task failed but skipping per failure policy",
					"workflow_id", workflowID,
					"task_id", task.ID,
					"policy", fp)
				return taskExecResult{
					taskIdx:    taskIdx,
					task:       task,
					checkpoint: finalCheckpoint,
					artifact:   finalArtifact,
					llmOutput:  llmOut.String(),
					// err is nil — task failed but workflow continues
				}
			}
			if fp == "replan" {
				clog.Session(sessionID).Info("task failed, requesting replan",
					"workflow_id", workflowID,
					"task_id", task.ID,
					"policy", fp)
				return taskExecResult{
					taskIdx:        taskIdx,
					task:           task,
					checkpoint:     finalCheckpoint,
					artifact:       finalArtifact,
					llmOutput:      llmOut.String(),
					replanEvidence: finalCheckpoint.Evidence,
					// err is nil — workflow re-plans rather than aborting
				}
			}

			// Secretary failure policy decision (legacy path, supplements kernel policy).
			if k.secretary != nil {
				action := k.secretary.DecideFailureAction(sessionID, task.ID)
				if action == secretary.ActionSkip {
					clog.Session(sessionID).Info("task failed but skipping per secretary policy",
						"workflow_id", workflowID,
						"task_id", task.ID)
					return taskExecResult{
						taskIdx:    taskIdx,
						task:       task,
						checkpoint: finalCheckpoint,
						artifact:   finalArtifact,
						llmOutput:  llmOut.String(),
						// err is nil — task failed but workflow continues
					}
				}
				// ActionStop (default) and ActionReplan (future) fall through to existing behavior.
			}

			// Existing rollback + abandon logic continues here...
			k.mu.Lock()
			policy := k.rollbackPolicy
			k.mu.Unlock()
			if policy != "manual" && policy != "off" {
				k.rollbackFiles(sessionID, workflowID, cumulativeBeforeState)
			}
			clog.Session(sessionID).Warn("task failed",
				"workflow_id", workflowID,
				"task_id", task.ID,
				"task", task.Title,
				"attempts", task.Attempts)
			return taskExecResult{
				taskIdx:    taskIdx,
				task:       task,
				checkpoint: finalCheckpoint,
				artifact:   finalArtifact,
				llmOutput:  llmOut.String(),
				err: fmt.Errorf("task %q failed after %d attempt(s): %s",
					task.Title, maxRetries+1, strings.Join(finalCheckpoint.Evidence, "; ")),
			}
		}

		// Prepare retry.
		oscillating := retryCtx.AppendGuidance(attempt, finalCheckpoint)
		task.Status = model.TaskStatusRetrying
		snap = shared.setTask(taskIdx, task)
		k.emitTasksUpdated(sessionID, workflowID, snap)
		taskRetryFeedback = buildRetryFeedback(finalCheckpoint, finalArtifact, &retryCtx, oscillating)
		k.events.Emit(sessionID, model.EventWorkflowRetry, model.WorkflowRetryPayload{
			WorkflowID: workflowID,
			Attempt:    attempt + 1,
			MaxRetries: maxRetries,
			Reason:     "acceptor rejected artifact",
			Evidence:   finalCheckpoint.Evidence,
		})
	}

	// Unreachable when maxRetries >= 0, but keep the compiler happy.
	return taskExecResult{
		taskIdx:    taskIdx,
		task:       shared.get(taskIdx),
		checkpoint: finalCheckpoint,
		artifact:   finalArtifact,
		llmOutput:  llmOut.String(),
		err:        fmt.Errorf("task loop exited unexpectedly for task %q", task.Title),
	}
}

// runTasksConcurrent runs all tasks using level-based DAG scheduling (N-08).
// Tasks at the same DAG level execute in parallel; the next level only starts
// after all tasks in the current level complete successfully.
//
// On return, the tasks slice elements are updated with final statuses.
// Returns the final checkpoint (last passing task or the first failing task),
// the final artifact, accumulated LLM output, and any workflow-fatal error.
func (k *Kernel) runTasksConcurrent(
	ctx context.Context,
	sessionID, workflowID string,
	intentSpec model.IntentSpec,
	queue *taskqueue.Queue,
	discoverySnippets []model.FileSnippet,
) (model.CheckpointResult, model.Artifact, string, error) {
	// M11-02: Get current task list from the queue snapshot.
	tasks := queue.Snapshot()
	shared := wrapTasks(tasks)

	levels := computeTaskLevels(tasks)
	if len(levels) == 0 {
		return model.CheckpointResult{}, model.Artifact{}, "", fmt.Errorf("no tasks to execute")
	}

	var finalCheckpoint model.CheckpointResult
	var finalArtifact model.Artifact
	var llmOut strings.Builder

	for levelIdx, levelTaskIdxs := range levels {
		// Build a fresh WorkflowContext for each level so subsequent levels see
		// files written by the previous level.
		wfCtx := k.buildWorkflowContext(ctx, sessionID)
		wfCtx.Discovery = model.DiscoveryContext{
			Query:      intentSpec.Goal,
			QueryID:    workflowID + ":level-discovery",
			Snippets:   discoverySnippets,
			Confidence: discoveryConfidence(discoverySnippets),
		}
		wfCtx.DiscoveryContext = discoverySnippets

		results := make([]taskExecResult, len(levelTaskIdxs))
		var wg sync.WaitGroup
		for i, taskIdx := range levelTaskIdxs {
			wg.Add(1)
			go func(i, taskIdx int) {
				defer wg.Done()
				results[i] = k.runOneTask(ctx, sessionID, workflowID, intentSpec, taskIdx, shared, queue, wfCtx)
			}(i, taskIdx)
		}
		wg.Wait()

		// Collect results from this level.
		var levelErr error
		var replanEvidence []string
		for _, res := range results {
			llmOut.WriteString(res.llmOutput)
			if res.err != nil && levelErr == nil {
				levelErr = res.err
			}
			if len(res.replanEvidence) > 0 && replanEvidence == nil {
				replanEvidence = res.replanEvidence
			}
			if res.checkpoint.Status != "" {
				finalCheckpoint = res.checkpoint
			}
			if res.artifact.Path != "" || res.artifact.Summary != "" {
				finalArtifact = res.artifact
			}
		}

		if levelErr != nil {
			// Mark all remaining planned tasks as abandoned.
			k.abandonRemainingTasks(shared, queue, sessionID, workflowID, levelIdx+1, levels)
			return finalCheckpoint, finalArtifact, llmOut.String(), levelErr
		}

		// Replan policy: a task failed and requested re-planning of remaining work.
		if replanEvidence != nil {
			k.abandonRemainingTasks(shared, queue, sessionID, workflowID, levelIdx+1, levels)
			return finalCheckpoint, finalArtifact, llmOut.String(),
				&replanRequestedError{evidence: replanEvidence}
		}

		// M11-02: Sync final task states back to the queue after each level.
		for _, taskIdx := range levelTaskIdxs {
			t := shared.get(taskIdx)
			queue.SetTask(t)
		}
	}

	// M11-03: Execute any tasks dynamically appended by coders during execution.
	// These run sequentially after all planned DAG levels complete.
	// Safety cap: at most maxAppendRounds to prevent infinite append chains.
	const maxAppendRounds = 3
	for appendRound := 0; appendRound < maxAppendRounds; appendRound++ {
		remaining := queue.Remaining()
		if len(remaining) == 0 {
			break
		}

		// Build fresh shared slice from the full queue snapshot.
		allTasks := queue.Snapshot()
		shared = wrapTasks(allTasks)

		// Find indices of remaining tasks in the full list.
		remainingIdxMap := make(map[string]int, len(remaining))
		for _, rt := range remaining {
			for idx, at := range allTasks {
				if at.ID == rt.ID {
					remainingIdxMap[rt.ID] = idx
					break
				}
			}
		}

		// Execute remaining tasks sequentially.
		wfCtx := k.buildWorkflowContext(ctx, sessionID)
		wfCtx.Discovery = model.DiscoveryContext{
			Query:      intentSpec.Goal,
			QueryID:    workflowID + ":appended",
			Snippets:   discoverySnippets,
			Confidence: discoveryConfidence(discoverySnippets),
		}
		wfCtx.DiscoveryContext = discoverySnippets

		for _, rt := range remaining {
			taskIdx, ok := remainingIdxMap[rt.ID]
			if !ok {
				continue
			}
			res := k.runOneTask(ctx, sessionID, workflowID, intentSpec, taskIdx, shared, queue, wfCtx)
			llmOut.WriteString(res.llmOutput)
			if res.checkpoint.Status != "" {
				finalCheckpoint = res.checkpoint
			}
			if res.artifact.Path != "" || res.artifact.Summary != "" {
				finalArtifact = res.artifact
			}
			queue.SetTask(shared.get(taskIdx))
			if res.err != nil {
				return finalCheckpoint, finalArtifact, llmOut.String(), res.err
			}
		}
	}

	return finalCheckpoint, finalArtifact, llmOut.String(), nil
}

// abandonRemainingTasks marks tasks in levels after failedLevel as abandoned.
func (k *Kernel) abandonRemainingTasks(shared *sharedTasks, queue *taskqueue.Queue, sessionID, workflowID string, fromLevel int, levels [][]int) {
	for l := fromLevel; l < len(levels); l++ {
		for _, taskIdx := range levels[l] {
			snap := shared.setStatus(taskIdx, model.TaskStatusAbandoned)
			// M11-02: Sync abandoned status to queue.
			t := shared.get(taskIdx)
			queue.SetTask(t)
			k.emitTasksUpdated(sessionID, workflowID, snap)
		}
	}
}

// replanRequestedError signals that a task failure with "replan" policy requests
// the workflow to re-plan remaining tasks using the Replanner with failure evidence.
type replanRequestedError struct {
	evidence []string
}

func (e *replanRequestedError) Error() string {
	return fmt.Sprintf("replan requested: %v", e.evidence)
}
