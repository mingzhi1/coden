package kernel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	clog "github.com/mingzhi1/coden/internal/log"
	"github.com/mingzhi1/coden/internal/tool/inventory"
)

// bucketScheduler is the heart of the blackboard workflow engine: it owns a
// bucket of tasks (the goal 总桶) and is the SINGLE WRITER of their state and the
// completion record. Tasks ready by readiness+priority are dispatched to a
// worker; workers are PURE (read a task, do work, return a taskOutcome) and the
// scheduler applies every outcome serially. No locks on bucket/completed — the
// single-writer goroutine owns them. See docs/design/blackboard_bucket_workflow.md
// §10 (统一原则 + 持久化 / 完成记录 / 并发 / 改桶边界).
//
// This is the new engine, built toward the end state — not a patch on the old
// runWorkflow/runTasksConcurrent, which it replaces.
type bucketScheduler struct {
	bucket     []model.Task              // the goal bucket (engine-owned)
	completed  map[string]string         // completion record (§10.2): taskID → outcome (passed|abandoned|skipped)
	artifacts  map[string]model.Artifact // per-task artifacts (§10.2 evidence; §11 DepArtifacts source)
	wip        int                       // max concurrent workers
	maxRetries int                       // per-task retry budget before abandon (§5 单任务卡死)
	guardian   guardian                  // deterministic termination supervisor (§5)

	feedback map[string]string // taskID → last failure feedback, projected on retry (#3)

	seq     int                                                             // monotonic checkpoint sequence (§6); bumped per apply
	persist checkpointer                                                    // incremental snapshot sink; nil = pure in-memory run
	onRetry func(taskID string, attempt, maxRetries int, evidence []string) // retry observability hook; nil = silent

	// onResearch observes a research block: a task was broken and rebuilt behind a
	// research task. deduped is true when the research task already existed (a shared
	// need across tasks reused one research task). nil = silent.
	onResearch func(origID, researchID, rebuiltID string, deduped bool)
}

// bucketSnapshot is an incremental checkpoint of the engine's authoritative state
// (§6): the remaining bucket + completion record + a monotonic seq. The
// single-writer scheduler emits one after every apply(), so a snapshot is always
// internally consistent (§10.3) and doubles as the crash-resume / undo / steer
// anchor and the kanban source of truth. Slices/maps are deep copies — immutable
// once emitted, so an async sink never races the scheduler's ongoing mutation.
type bucketSnapshot struct {
	seq       int
	bucket    []model.Task
	completed map[string]string
	artifacts map[string]model.Artifact
	metrics   snapshotMetrics
}

// snapshotMetrics carries the loop counters worth persisting alongside the bucket
// (§6: 轮数/剩余预算) — enough to resume budget enforcement and render progress.
type snapshotMetrics struct {
	dispatches int
	pending    int
}

// checkpointer persists one incremental snapshot. Injected so the scheduler core
// is testable without a real store; nil means no persistence.
type checkpointer func(snap bucketSnapshot) error

// guardian is the deterministic termination supervisor (§5): it owns NO bucket
// state, it only watches counters and tells the scheduler when to stop. Pure code,
// never an LLM — stopping is a `代码裁决`, zero-cost, never "let me try once more".
// The scheduler consults it; deadlock detection lives in run() itself.
type guardian struct {
	maxDispatches int // global work-unit budget; 0 = unlimited
	maxGrowth     int // consecutive bucket-growth applies before declaring non-convergence; 0 = off

	dispatches   int // work units dispatched so far
	prevPending  int // pending count at the previous apply (for growth tracking)
	growthStreak int // consecutive applies where the bucket grew instead of draining
	seeded       bool
}

// guardianStop is the typed signal that the Guardian halted the loop. It is NOT a
// crash: the bucket holds partial results and the reason explains the stop, so a
// caller can hand both to the Responder for graceful收口.
type guardianStop struct {
	code   string // budget | oscillation
	detail string
}

func (e *guardianStop) Error() string { return fmt.Sprintf("guardian stop (%s): %s", e.code, e.detail) }

// checkBudget reports a stop when the global dispatch budget is exhausted —
// consulted before each dispatch so a runaway producer (Planner re-emitting work)
// or retry storm can't spin forever.
func (g *guardian) checkBudget() *guardianStop {
	if g.maxDispatches > 0 && g.dispatches >= g.maxDispatches {
		return &guardianStop{code: "budget", detail: fmt.Sprintf("dispatch budget %d reached", g.maxDispatches)}
	}
	return nil
}

// observe tracks bucket convergence after each apply: if pending work grows for
// maxGrowth consecutive applies the bucket isn't draining (Planner outpacing
// execution / non-convergence) → stop. A single plan expansion grows the bucket
// once and is fine; only sustained growth trips it.
func (g *guardian) observe(pending int) *guardianStop {
	if g.seeded && pending > g.prevPending {
		g.growthStreak++
	} else {
		g.growthStreak = 0
	}
	g.prevPending = pending
	g.seeded = true
	if g.maxGrowth > 0 && g.growthStreak >= g.maxGrowth {
		return &guardianStop{code: "oscillation", detail: fmt.Sprintf("bucket grew %d consecutive applies without draining", g.growthStreak)}
	}
	return nil
}

// taskOutcome is the signal a worker returns to the scheduler. The scheduler is
// the only writer of bucket/completed, so workers never mutate shared state —
// they hand back what should change and the scheduler applies it (§10.3).
type taskOutcome struct {
	taskID   string
	status   string // model.TaskStatusPassed | model.TaskStatusFailed
	artifact model.Artifact
	// newTasks enter the bucket here — the sole-producer rule (§10.4): tasks only
	// originate from a Planner outcome, applied by the scheduler, never added by
	// a worker mutating the bucket directly.
	newTasks []model.Task
	// researchNeed, when non-empty, reports the Executor was BLOCKED on external
	// knowledge (the string is WHAT to research). The scheduler (single writer)
	// turns it into a research task + a rebuilt dependent task in apply(); two
	// tasks blocked on the same need share one research task (bucket id dedup).
	// status is set to failed alongside it, so if the rebuild budget is exhausted
	// it degrades to an ordinary failure.
	researchNeed string
	err          error
}

// taskInput is what the scheduler projects to a worker at dispatch (§11): the task
// plus the single-writer-computed context it needs — the artifacts of its
// completed dependencies (so a downstream task sees upstream findings) and the
// previous attempt's failure feedback (so a retry isn't blind).
type taskInput struct {
	task          model.Task
	depArtifacts  map[string]model.Artifact // artifacts of this task's passed dependencies
	retryFeedback string                    // why the previous attempt failed (empty on first try)
}

// worker runs one task to a terminal outcome (execute → accept). Injected so the
// scheduler core is testable without real agents wired in.
type worker func(ctx context.Context, in taskInput) taskOutcome

func newBucketScheduler(tasks []model.Task, wip int) *bucketScheduler {
	if wip < 1 {
		wip = 1
	}
	return &bucketScheduler{
		bucket:     tasks,
		completed:  make(map[string]string),
		artifacts:  make(map[string]model.Artifact),
		feedback:   make(map[string]string),
		wip:        wip,
		maxRetries: 1, // 2 attempts total, matching the old per-task retry budget
		// Generous defaults: a hard backstop against runaway loops, well above any
		// real workflow. Callers (and tests) tighten these for specific budgets.
		guardian: guardian{maxDispatches: 256, maxGrowth: 32},
	}
}

// newBucketSchedulerFromSnapshot resumes a scheduler from a persisted snapshot
// (§6 crash-resume / undo / steer): the bucket and completion record are restored,
// and any task caught in-flight at snapshot time (coding/accepting) is reset to
// planned so it re-dispatches (§10.1 — the in-flight work was lost; the Executor
// reads current workspace state and redoes it, near-idempotent). seq continues
// from the snapshot so checkpoints stay monotonic. dispatches is restored so the
// Guardian budget carries across the resume.
func newBucketSchedulerFromSnapshot(snap bucketSnapshot, wip int) *bucketScheduler {
	if wip < 1 {
		wip = 1
	}
	s := &bucketScheduler{
		bucket:     resetInFlight(cloneTasks(snap.bucket)),
		completed:  cloneStringMap(snap.completed),
		artifacts:  cloneArtifactMap(snap.artifacts),
		feedback:   make(map[string]string),
		wip:        wip,
		maxRetries: 1,
		guardian:   guardian{maxDispatches: 256, maxGrowth: 32, dispatches: snap.metrics.dispatches},
		seq:        snap.seq,
	}
	return s
}

// resetInFlight flips any task left mid-flight (coding/accepting/retrying) back to
// planned, so resume re-dispatches it. Terminal and planned tasks are untouched.
func resetInFlight(tasks []model.Task) []model.Task {
	for i := range tasks {
		switch tasks[i].Status {
		case model.TaskStatusCoding, model.TaskStatusAccepting, model.TaskStatusRetrying:
			tasks[i].Status = model.TaskStatusPlanned
		}
	}
	return tasks
}

// run drives the single-writer loop until the bucket is drained (success), the
// Guardian halts it (budget / oscillation — graceful stop with partial results),
// or a deadlock is detected (pending tasks but none ready and none running — a
// failed or missing dependency blocks everything). On a Guardian stop, in-flight
// workers are allowed to drain before returning so no goroutine is orphaned.
func (s *bucketScheduler) run(ctx context.Context, w worker) error {
	running := 0
	results := make(chan taskOutcome)
	var stop *guardianStop
	for {
		// Dispatch ready tasks up to the WIP limit. Each dispatch marks the task
		// running so the next readiness pass won't pick it again. Once the Guardian
		// has signalled a stop we dispatch nothing new and just drain in-flight work.
		for stop == nil && running < s.wip {
			if r := s.guardian.checkBudget(); r != nil {
				stop = r
				break
			}
			ready := s.readyOrdered()
			if len(ready) == 0 {
				break
			}
			t := ready[0]
			s.setStatus(t.ID, model.TaskStatusCoding)
			s.bumpAttempt(t.ID) // Attempts counts execute→accept cycles (first dispatch = 1)
			s.guardian.dispatches++
			running++
			// Single-writer projection (§11): hand the worker its deps' artifacts and
			// any prior-failure feedback, computed here where the maps are owned.
			in := taskInput{task: t, depArtifacts: s.depArtifactsFor(t), retryFeedback: s.feedback[t.ID]}
			go func(in taskInput) {
				select {
				case results <- w(ctx, in):
				case <-ctx.Done():
				}
			}(in)
		}

		if running == 0 {
			if stop != nil {
				return stop // Guardian halt: bucket holds partial results
			}
			if s.pending() {
				return fmt.Errorf("scheduler deadlock: %d pending task(s) but none ready (failed/missing dependency)", s.pendingCount())
			}
			return nil // bucket drained
		}

		select {
		case out := <-results:
			running--
			s.apply(out)
			if stop == nil {
				stop = s.guardian.observe(s.pendingCount())
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// apply is the single-writer mutation point: retain the artifact, settle the task
// (retry / terminal), admit any Planner-produced tasks, then emit an incremental
// checkpoint. Only the run loop calls this, so bucket/completed/artifacts need no
// locking, and the snapshot it emits is consistent by construction (§10.3).
func (s *bucketScheduler) apply(out taskOutcome) {
	status := out.status
	if status == "" {
		status = model.TaskStatusFailed
	}

	// Retain the artifact — the evidence source for Responder收口, §11 DepArtifacts,
	// and §10.2 evidence_ref. Kept even on a failed attempt (last evidence wins).
	if !artifactEmpty(out.artifact) {
		s.artifacts[out.taskID] = out.artifact
	}

	// Research block (§6.1): the Executor was blocked on external knowledge. BREAK
	// the task and REBUILD it behind a research task that supplies the findings — a
	// shared need across concurrent tasks dedups to one research task (bucket id
	// uniqueness, single-writer-safe). Bounded by maxResearchRebuilds; once the
	// budget is spent the outcome falls through as an ordinary failure below
	// (status is already failed), so a persistently-blocked task is abandoned.
	if out.researchNeed != "" && researchGen(out.taskID) < maxResearchRebuilds {
		s.handleResearchBlock(out)
		s.checkpoint()
		return
	}

	// Failed task → retry by re-dispatch until cycles exceed maxRetries, then abandon
	// (§4 fail 边 / §5 单任务卡死). Attempts (= cycles) is bumped at dispatch, so here
	// it already reflects the cycle just finished: retry while Attempts ≤ maxRetries
	// (maxRetries=1 → 2 cycles total). A retry stays in the bucket as planned and is
	// NOT recorded in the completion record; only a terminal outcome is. Each
	// re-dispatch also counts against the Guardian dispatch budget.
	if status == model.TaskStatusFailed {
		// Record WHY it failed so the retry isn't blind (#3): the next dispatch
		// projects this as retryFeedback → the Executor sees it via contextSummary.
		if fb := failureFeedback(out); fb != "" {
			s.feedback[out.taskID] = fb
		}
		if s.attemptOf(out.taskID) <= s.maxRetries {
			s.setStatus(out.taskID, model.TaskStatusPlanned)
			if s.onRetry != nil {
				s.onRetry(out.taskID, s.attemptOf(out.taskID), s.maxRetries, out.artifact.Evidence)
			}
			s.checkpoint()
			return
		}
		status = model.TaskStatusAbandoned // retries exhausted → terminal, blocks dependents
	}
	delete(s.feedback, out.taskID) // passed/abandoned → clear stale feedback

	s.setStatus(out.taskID, status)
	s.completed[out.taskID] = status
	for _, nt := range out.newTasks {
		if nt.Status == "" {
			nt.Status = model.TaskStatusPlanned
		}
		s.bucket = append(s.bucket, nt)
	}
	s.checkpoint()
}

// attemptOf / bumpAttempt track per-task retry count on the task itself (§5).
func (s *bucketScheduler) attemptOf(id string) int {
	for i := range s.bucket {
		if s.bucket[i].ID == id {
			return s.bucket[i].Attempts
		}
	}
	return 0
}

func (s *bucketScheduler) bumpAttempt(id string) {
	for i := range s.bucket {
		if s.bucket[i].ID == id {
			s.bucket[i].Attempts++
			return
		}
	}
}

func artifactEmpty(a model.Artifact) bool {
	return a.Path == "" && a.Summary == "" && len(a.Evidence) == 0
}

// checkpoint bumps the sequence and persists a deep-copied snapshot of the current
// authoritative state. Deep copy means the sink can hold/serialize the snapshot
// while the scheduler keeps mutating its own bucket — no shared-slice race.
func (s *bucketScheduler) checkpoint() {
	s.seq++
	if s.persist == nil {
		return
	}
	snap := bucketSnapshot{
		seq:       s.seq,
		bucket:    cloneTasks(s.bucket),
		completed: cloneStringMap(s.completed),
		artifacts: cloneArtifactMap(s.artifacts),
		metrics:   snapshotMetrics{dispatches: s.guardian.dispatches, pending: s.pendingCount()},
	}
	// Best-effort: a persistence failure must not crash the single-writer loop.
	// The next apply re-snapshots the full state, so a dropped checkpoint only
	// costs resume granularity, not correctness.
	_ = s.persist(snap)
}

// cloneTasks deep-copies a task slice, including the per-task slice fields, so a
// snapshot is fully independent of the live bucket.
func cloneTasks(in []model.Task) []model.Task {
	if in == nil {
		return nil
	}
	out := make([]model.Task, len(in))
	for i, t := range in {
		t.Files = cloneStrings(t.Files)
		t.DependsOn = cloneStrings(t.DependsOn)
		t.Steps = cloneStrings(t.Steps)
		out[i] = t
	}
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneArtifactMap(in map[string]model.Artifact) map[string]model.Artifact {
	out := make(map[string]model.Artifact, len(in))
	for k, v := range in {
		v.Evidence = cloneStrings(v.Evidence)
		out[k] = v
	}
	return out
}

// readyOrdered returns planned tasks whose dependencies are all satisfied,
// ordered by descending priority then ascending ID (deterministic).
func (s *bucketScheduler) readyOrdered() []model.Task {
	var ready []model.Task
	for _, t := range s.bucket {
		if t.Status != model.TaskStatusPlanned {
			continue
		}
		if !s.depsDone(t) {
			continue
		}
		ready = append(ready, t)
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].ID < ready[j].ID
	})
	return ready
}

// depsDone consults the completion record (§10.2), not the bucket: a passed dep
// satisfies even after it leaves the bucket. A dep still present in the bucket
// but not yet passed blocks; a dangling dep (neither completed nor in the bucket)
// is treated as satisfied so a stray ID can't deadlock a task forever.
func (s *bucketScheduler) depsDone(t model.Task) bool {
	for _, dep := range t.DependsOn {
		if s.completed[dep] == model.TaskStatusPassed {
			continue
		}
		if s.inBucket(dep) {
			return false
		}
	}
	return true
}

func (s *bucketScheduler) inBucket(id string) bool {
	for _, t := range s.bucket {
		if t.ID == id {
			return true
		}
	}
	return false
}

func (s *bucketScheduler) setStatus(id, status string) {
	for i := range s.bucket {
		if s.bucket[i].ID == id {
			s.bucket[i].Status = status
			return
		}
	}
}

// executeTask is the bucketScheduler's worker for a single execute-task: run the
// Executor (agentic, self-verifies), execute its tool plan, gate on the
// deterministic success_cmd, then the Acceptor. Returns a terminal taskOutcome.
//
// Unlike the old runOneTask it carries NO retry loop, sharedTasks coupling, or
// SubGoal recursion: the engine loop owns retry (re-dispatch / re-plan), and
// decomposition is the Planner's job (sole-producer rule). Workspace writes
// happen here (concurrent across workers, guarded by Files-scope disjointness,
// §10.3); the scheduler applies the returned status to the bucket as sole writer.
func (k *Kernel) executeTask(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, in taskInput, wfCtx model.WorkflowContext) taskOutcome {
	task := in.task
	// Project the scheduler-computed context into wfCtx so contextSummary surfaces
	// it to the Executor: prior-failure feedback (#3 — retry isn't blind) and the
	// findings of completed dependencies (#4 — §11 DepArtifacts).
	wfCtx.RetryFeedback = in.retryFeedback
	wfCtx.DepFindings = formatDepFindings(in.depArtifacts)
	// Per-task Environment projection: scope the toolchain to THIS task's subproject
	// (by its file paths) instead of the whole repo. A task touching only web/ gets
	// the JS toolchain, not a monorepo's five languages. Falls back to the repo-wide
	// prompt when the task declares no files or none map to a known subproject.
	if k.inventory != nil {
		if langs := inventory.SubprojectLanguagesForFiles(k.inventory.Subprojects(), task.Files); len(langs) > 0 {
			wfCtx.EnvironmentPrompt = inventory.FormatEnvironmentPromptForLanguages(k.inventory, langs)
		}
	}
	taskCtx := model.WithWorkflowContext(ctx, wfCtx)
	fail := func(format string, args ...any) taskOutcome {
		return taskOutcome{taskID: task.ID, status: model.TaskStatusFailed, err: fmt.Errorf(format, args...)}
	}

	// 1. Execute — the Executor agent produces a tool plan (and self-verifies).
	codeResult, err := k.executeWorker(taskCtx, sessionID, workflowID, "code", workflow.RoleExecutor, k.workflow.ExecutorWorker(), workflow.WorkerInput{
		SessionID: sessionID, WorkflowID: workflowID, TaskID: task.ID, Intent: intent, Tasks: []model.Task{task},
	})
	if err != nil {
		return fail("executor: %w", err)
	}
	if codeResult.CodePlan == nil {
		return fail("executor returned nil code plan for %q", task.Title)
	}

	// 1b. Mid-execution research block (§6.1): the Executor reports it is BLOCKED on
	// external knowledge it couldn't look up inline (CodePlan.ResearchNeed — a
	// dedicated control field, never the tool stream). Hand the need straight to the
	// scheduler as a failed-with-researchNeed outcome; apply() (single writer) turns
	// it into a research task + a rebuilt dependent task, deduping a shared need
	// across concurrent tasks. status=failed so an exhausted rebuild budget degrades
	// to an ordinary failure.
	if q := strings.TrimSpace(codeResult.CodePlan.ResearchNeed); q != "" {
		return taskOutcome{taskID: task.ID, status: model.TaskStatusFailed, researchNeed: q,
			err: fmt.Errorf("blocked on external knowledge: %s", q)}
	}

	toolCalls := codeResult.CodePlan.Calls()
	if len(toolCalls) == 0 {
		return fail("executor returned empty tool plan for %q", task.Title)
	}

	// 2. Execute the tool plan (pre-executed agentic mutations are bookkept, not re-run).
	workerID := workerIDFor(roleOrDefault(codeResult.Metadata, workflow.RoleExecutor))
	artifact, _, execErr := k.executeToolPlan(taskCtx, sessionID, workflowID, workerID, task.Files, toolCalls)
	if execErr != nil {
		return fail("tool execution: %w", execErr)
	}

	// 3. Deterministic success_cmd gate (Kernel-side, after the in-loop self-verify).
	if task.SuccessCmd != "" {
		if out, ok := runSuccessCmd(taskCtx, k.workspace.Root(), task.SuccessCmd); !ok {
			return taskOutcome{taskID: task.ID, status: model.TaskStatusFailed, artifact: artifact,
				err: fmt.Errorf("success_cmd failed (%s): %s", task.SuccessCmd, truncateCmdOutput(out, 512))}
		}
	}

	// 4. Accept — the Acceptor agent judges the artifact.
	acceptResult, aErr := k.executeWorker(taskCtx, sessionID, workflowID, "accept", workflow.RoleAcceptor, k.workflow.AcceptorWorker(), workflow.WorkerInput{
		SessionID: sessionID, WorkflowID: workflowID, TaskID: task.ID, Intent: intent, Tasks: []model.Task{task}, Artifact: artifact,
	})
	if aErr != nil {
		return fail("acceptor: %w", aErr)
	}
	if acceptResult.Checkpoint == nil {
		return fail("acceptor returned nil checkpoint for %q", task.Title)
	}

	status := model.TaskStatusFailed
	if acceptResult.Checkpoint.Status == "pass" {
		status = model.TaskStatusPassed
	}
	return taskOutcome{taskID: task.ID, status: status, artifact: artifact}
}

// defaultBucketWIP is the engine's default max concurrent workers.
const defaultBucketWIP = 4

// runBucketWorkflow is the blackboard engine entry: seed the bucket with a single
// plan-root task (the goal carried as a plan-request in SubGoal), then run the
// single-writer scheduler. The Planner expands plan-root into execute-tasks (sole
// producer, §10.4); execute-tasks run through execute→accept; the loop drains the
// bucket. This replaces the linear runWorkflow plan→code→accept pipeline.
func (k *Kernel) runBucketWorkflow(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, wfCtx model.WorkflowContext) (*bucketScheduler, error) {
	root := model.Task{
		ID:      "plan-root",
		Title:   "plan: " + intent.Goal,
		Status:  model.TaskStatusPlanned,
		SubGoal: intent.Goal, // marks this as a plan-request → routed to Planner
	}
	s := newBucketScheduler([]model.Task{root}, defaultBucketWIP)
	worker := func(c context.Context, in taskInput) taskOutcome {
		return k.dispatchTask(c, sessionID, workflowID, intent, in, wfCtx)
	}
	if err := s.run(ctx, worker); err != nil {
		return s, err
	}
	return s, nil
}

// runTasksBucket runs an already-planned task set through the blackboard bucket
// engine — the drop-in replacement for runTasksConcurrent. The engine owns
// readiness+priority scheduling, per-task retry/abandon (§5, so the old
// replan-on-failure loop is gone), Guardian termination, and incremental
// checkpoints. Tasks carry no SubGoal here, so dispatchTask routes them all to
// execute→accept. Returns the terminal checkpoint, a representative artifact, the
// final task list, and a non-nil error only on a Guardian stop / deadlock.
func (k *Kernel) runTasksBucket(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, tasks []model.Task, wfCtx model.WorkflowContext) (model.CheckpointResult, model.Artifact, []model.Task, error) {
	s := newBucketScheduler(tasks, defaultBucketWIP)
	s.maxRetries = k.maxTaskRetries // honor the Kernel's per-task retry budget
	// Persist each incremental snapshot to the durable store (§6/§10.1 crash-resume
	// anchor). Best-effort: the scheduler ignores persistence errors so a flaky
	// write never stalls the single-writer loop.
	if k.checkpoints != nil {
		s.persist = func(snap bucketSnapshot) error {
			return k.checkpoints.SaveBucket(model.BucketSnapshot{
				SessionID:  sessionID,
				WorkflowID: workflowID,
				Seq:        snap.seq,
				Bucket:     snap.bucket,
				Completed:  snap.completed,
				Artifacts:  snap.artifacts,
				Dispatches: snap.metrics.dispatches,
				CreatedAt:  time.Now(),
			})
		}
	}
	s.onRetry = func(taskID string, attempt, maxRetries int, evidence []string) {
		k.emitTasksUpdated(sessionID, workflowID, s.finalTasks())
		k.events.Emit(sessionID, model.EventWorkflowRetry, model.WorkflowRetryPayload{
			WorkflowID: workflowID,
			Attempt:    attempt,
			MaxRetries: maxRetries,
			Reason:     "task failed verification/acceptance",
			Evidence:   evidence,
		})
	}
	s.onResearch = func(origID, researchID, rebuiltID string, deduped bool) {
		k.emitTasksUpdated(sessionID, workflowID, s.finalTasks())
		clog.Session(sessionID).Info("research block: task rebuilt behind research task",
			"workflow_id", workflowID, "blocked_task", origID, "research_task", researchID,
			"rebuilt_task", rebuiltID, "deduped", deduped)
	}
	worker := func(c context.Context, in taskInput) taskOutcome {
		return k.dispatchTask(c, sessionID, workflowID, intent, in, wfCtx)
	}
	runErr := s.run(ctx, worker)

	status, paths, evidence, artifact := s.result()
	cp := model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     sessionID,
		Status:        status,
		ArtifactPaths: paths,
		Evidence:      evidence,
		CreatedAt:     time.Now(),
	}
	// A non-pass terminal (tasks abandoned, or a Guardian stop / deadlock) is a
	// workflow failure: surface it as an error so runWorkflow's failure branch marks
	// the run "failed" and persists a partial turn summary (preserving the old
	// task-failure → failed-workflow contract).
	if runErr == nil && status != "pass" {
		runErr = fmt.Errorf("workflow did not complete: %d task(s) did not pass", s.notPassedCount())
	}
	return cp, artifact, s.finalTasks(), runErr
}

// resumeTasksBucket rebuilds a scheduler from the latest persisted snapshot of a
// workflow (§10.1): in-flight tasks reset to planned, seq/dispatch budget carried
// over. Returns (nil, false) when no snapshot exists to resume from.
func (k *Kernel) resumeTasksBucket(sessionID, workflowID string) (*bucketScheduler, bool) {
	if k.checkpoints == nil {
		return nil, false
	}
	snap, ok := k.checkpoints.LatestBucket(sessionID, workflowID)
	if !ok {
		return nil, false
	}
	s := newBucketSchedulerFromSnapshot(bucketSnapshot{
		seq:       snap.Seq,
		bucket:    snap.Bucket,
		completed: snap.Completed,
		artifacts: snap.Artifacts,
		metrics:   snapshotMetrics{dispatches: snap.Dispatches},
	}, defaultBucketWIP)
	return s, true
}

// notPassedCount returns how many recorded tasks reached a genuine non-passed
// terminal. A skipped task was superseded (e.g. broken & rebuilt behind a research
// task), not a failure, so it does not count.
func (s *bucketScheduler) notPassedCount() int {
	n := 0
	for _, st := range s.completed {
		if st != model.TaskStatusPassed && st != model.TaskStatusSkipped {
			n++
		}
	}
	return n
}

// finishBucketWorkflow is the outer-loop收口 (§3): assemble the drained bucket's
// result, run a heterogeneous goal-critique over the final tasks (best-effort,
// anti-自恋), then produce the final user-facing message via the Responder. It
// returns the message and the terminal CheckpointResult the caller commits.
func (k *Kernel) finishBucketWorkflow(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, s *bucketScheduler, runErr error) (string, model.CheckpointResult) {
	status, paths, evidence, artifact := s.result()
	if runErr != nil {
		status = "fail" // Guardian stop / deadlock → partial result, not a clean pass
	}
	tasks := s.finalTasks()

	// Outer goal-critique: a heterogeneous reviewer judges the assembled result, not
	// the plan. Best-effort — it surfaces unmet-goal issues into FixGuidance and can
	// downgrade a self-reported pass, but never blocks收口.
	var fixGuidance string
	if critic := k.workflow.Critic(); critic != nil {
		if crit, err := critic.Critique(ctx, workflowID, intent, tasks); err == nil && !crit.Approved {
			fixGuidance = strings.Join(append(append([]string{}, crit.Issues...), crit.Suggestions...), "; ")
			if status == "pass" {
				status = "fail" // Critic overrides a self-reported pass when the goal isn't met
			}
		}
	}

	cp := model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     sessionID,
		Status:        status,
		ArtifactPaths: paths,
		Evidence:      evidence,
		FixGuidance:   fixGuidance,
		CreatedAt:     time.Now(),
	}
	msg := k.buildResponderMessage(ctx, sessionID, intent, tasks, cp, artifact)
	return msg, cp
}

// applyGoalCritique runs the heterogeneous outer Critic over the final tasks and
// the execution evidence (§3 goal-critique), folding its verdict into the
// checkpoint: a rejection records FixGuidance and downgrades a self-reported pass.
// Best-effort — no Critic configured, or a Critic error, leaves the checkpoint
// untouched; it never upgrades a fail to a pass. Unlike the plan-Critic (which
// judges the plan before execution), this judges the assembled result, so the
// goal is annotated with the run's evidence to give the Critic result context.
func (k *Kernel) applyGoalCritique(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task, cp model.CheckpointResult) model.CheckpointResult {
	critic := k.workflow.Critic()
	if critic == nil {
		return cp
	}
	goalIntent := intent
	if len(cp.Evidence) > 0 {
		goalIntent.Goal = fmt.Sprintf("%s\n\n[EXECUTION EVIDENCE] %s", intent.Goal, strings.Join(cp.Evidence, "; "))
	}
	crit, err := critic.Critique(ctx, workflowID, goalIntent, tasks)
	if err != nil || crit.Approved {
		return cp
	}
	cp.FixGuidance = strings.Join(append(append([]string{}, crit.Issues...), crit.Suggestions...), "; ")
	if cp.Status == "pass" {
		cp.Status = "fail" // Critic overrides a self-reported pass when the goal isn't met
	}
	return cp
}

// result aggregates the drained bucket into a terminal summary (§3收口 input):
// overall status (pass only if every recorded task passed; fail if any was
// abandoned/failed), the deterministic union of artifact paths + evidence, and a
// representative artifact. Completion-record keys are sorted for reproducibility.
func (s *bucketScheduler) result() (status string, artifactPaths, evidence []string, last model.Artifact) {
	status = "pass"
	ids := make([]string, 0, len(s.completed))
	for id := range s.completed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		// A skipped task was superseded (e.g. broken & rebuilt behind a research
		// task), not a failure — only a genuine non-pass terminal fails the run.
		if st := s.completed[id]; st != model.TaskStatusPassed && st != model.TaskStatusSkipped {
			status = "fail"
		}
		if a, ok := s.artifacts[id]; ok {
			if a.Path != "" {
				artifactPaths = append(artifactPaths, a.Path)
			}
			evidence = append(evidence, a.Evidence...)
			last = a
		}
	}
	return status, artifactPaths, evidence, last
}

// finalTasks returns a deep copy of the bucket's terminal task list for收口.
func (s *bucketScheduler) finalTasks() []model.Task { return cloneTasks(s.bucket) }

// depArtifactsFor returns the artifacts of t's PASSED dependencies — the §11
// projection a downstream task needs to see what upstream tasks produced. Read by
// the single-writer run loop at dispatch, so no locking.
func (s *bucketScheduler) depArtifactsFor(t model.Task) map[string]model.Artifact {
	var deps map[string]model.Artifact
	for _, dep := range t.DependsOn {
		if s.completed[dep] != model.TaskStatusPassed {
			continue
		}
		a, ok := s.artifacts[dep]
		if !ok || artifactEmpty(a) {
			continue
		}
		if deps == nil {
			deps = make(map[string]model.Artifact)
		}
		deps[dep] = a
	}
	return deps
}

// maxResearchRebuilds bounds how many times a single task lineage may be broken
// and rebuilt behind a research task. 1 means: a blocked task is rebuilt once
// (behind its research findings); if the rebuilt task blocks AGAIN it falls through
// to an ordinary failure rather than looping. Combined with the Guardian budget,
// this makes research-driven re-planning strictly terminating.
const maxResearchRebuilds = 1

// handleResearchBlock breaks a research-blocked task and rebuilds it behind a
// research task (single-writer mutation, called only from apply): (1) the blocked
// task is superseded (skipped, not failed); (2) a research task for the need is
// admitted, deduped by a query-derived id so a SHARED need across concurrent tasks
// maps to ONE research task; (3) the implementation work is rebuilt at the next
// research-generation id, depending on the research task; (4) anything that
// depended on the broken task is rewired onto the rebuild so the graph follows the
// work. The research task's findings reach the rebuild via DepArtifacts (§11).
func (s *bucketScheduler) handleResearchBlock(out taskOutcome) {
	origID := out.taskID
	q := strings.TrimSpace(out.researchNeed)

	// (1) Break the blocked task — superseded by the rebuild, not a failure.
	s.setStatus(origID, model.TaskStatusSkipped)
	s.completed[origID] = model.TaskStatusSkipped
	delete(s.feedback, origID)

	// (2) Admit the research task, deduped by query identity. If one already exists
	// (in the bucket or completed) the rebuild simply depends on it — no duplicate.
	researchID := researchTaskIDPrefix + slugForResearch(q)
	deduped := s.inBucket(researchID) || s.completedHas(researchID)
	if !deduped {
		s.bucket = append(s.bucket, model.Task{
			ID:      researchID,
			Title:   "research: " + q,
			Status:  model.TaskStatusPlanned,
			SubGoal: q, // the research query; dispatchTask routes by id to runResearchTask
		})
	}

	// (3) Rebuild the implementation work behind the research task.
	rebuilt := s.cloneTaskByID(origID)
	rebuilt.ID = nextResearchGenID(origID)
	rebuilt.Status = model.TaskStatusPlanned
	rebuilt.Attempts = 0
	rebuilt.DependsOn = appendUnique(rebuilt.DependsOn, researchID)

	// (4) Rewire downstream dependents from the broken task onto the rebuild.
	s.rewireDeps(origID, rebuilt.ID)

	s.bucket = append(s.bucket, rebuilt)

	if s.onResearch != nil {
		s.onResearch(origID, researchID, rebuilt.ID, deduped)
	}
}

// completedHas reports whether id has a terminal outcome in the completion record.
func (s *bucketScheduler) completedHas(id string) bool {
	_, ok := s.completed[id]
	return ok
}

// cloneTaskByID returns a deep copy of the bucket task with the given id (zero
// value if absent), so the caller can derive a rebuilt task without aliasing.
func (s *bucketScheduler) cloneTaskByID(id string) model.Task {
	for _, t := range s.bucket {
		if t.ID == id {
			return cloneTasks([]model.Task{t})[0]
		}
	}
	return model.Task{}
}

// rewireDeps repoints every bucket task's DependsOn entry from → to, so a rebuilt
// task inherits the dependents of the task it supersedes.
func (s *bucketScheduler) rewireDeps(from, to string) {
	for i := range s.bucket {
		for j, dep := range s.bucket[i].DependsOn {
			if dep == from {
				s.bucket[i].DependsOn[j] = to
			}
		}
	}
}

// researchTaskIDPrefix / researchGen / nextResearchGenID encode the research
// rebuild generation in a task id as a "#r<N>" suffix (gen 0 = no suffix). The
// generation bounds rebuilds (maxResearchRebuilds) and keeps each rebuild's id
// distinct from the superseded task's (already in the completion record).
const researchGenSep = "#r"

func researchGen(id string) int {
	i := strings.LastIndex(id, researchGenSep)
	if i < 0 {
		return 0
	}
	n := 0
	for _, r := range id[i+len(researchGenSep):] {
		if r < '0' || r > '9' {
			return 0 // not a generation suffix — treat as base id
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func nextResearchGenID(id string) string {
	gen := researchGen(id)
	base := id
	if gen > 0 {
		base = id[:strings.LastIndex(id, researchGenSep)]
	}
	return fmt.Sprintf("%s%s%d", base, researchGenSep, gen+1)
}

// slugForResearch derives a stable, id-safe slug from a research query so the same
// need maps to the same research-task id (the basis of dedup). Lowercased, runs of
// non-alphanumerics collapsed to a single '-', trimmed, and capped.
func slugForResearch(q string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(q) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 48 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "need"
	}
	return s
}

// appendUnique appends v to xs only if absent, preserving order.
func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// failureFeedback distills a failed outcome into a short retry hint (#3): the error
// plus any evidence, so the next attempt knows what went wrong.
func failureFeedback(out taskOutcome) string {
	var b strings.Builder
	if out.err != nil {
		b.WriteString(out.err.Error())
	}
	for _, e := range out.artifact.Evidence {
		if e == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(e)
	}
	return b.String()
}

// formatDepFindings renders dependency artifacts as a compact context block for the
// downstream task's prompt (§11 — surfaced via wfCtx.DepFindings → contextSummary).
// Keys sorted for reproducibility.
func formatDepFindings(deps map[string]model.Artifact) string {
	if len(deps) == 0 {
		return ""
	}
	ids := make([]string, 0, len(deps))
	for id := range deps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		a := deps[id]
		fmt.Fprintf(&b, "- [%s] %s", id, a.Summary)
		if a.Path != "" {
			fmt.Fprintf(&b, " (%s)", a.Path)
		}
		b.WriteString("\n")
		for _, e := range a.Evidence {
			if e != "" {
				fmt.Fprintf(&b, "    • %s\n", e)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// dispatchTask is the Dispatcher's code-level routing (§5) as a single worker
// function: a task in the reserved research-task namespace goes to the read-only
// research producer; a task carrying a plan-request (SubGoal set) goes to the
// Planner (the sole producer); any other task is an execute-task and runs
// execute→accept. The research check is first because a research task also carries
// its query in SubGoal — it must not be mistaken for a plan-request.
func (k *Kernel) dispatchTask(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, in taskInput, wfCtx model.WorkflowContext) taskOutcome {
	if isResearchTaskID(in.task.ID) {
		return k.runResearchTask(ctx, sessionID, workflowID, intent, in, wfCtx)
	}
	if strings.TrimSpace(in.task.SubGoal) != "" {
		return k.runPlanTask(ctx, sessionID, workflowID, intent, in, wfCtx)
	}
	return k.executeTask(ctx, sessionID, workflowID, intent, in, wfCtx)
}

// runPlanTask is the bucketScheduler's worker for a plan-task: run the Planner on
// the task's plan-request (carried in SubGoal) and hand back the produced tasks as
// the outcome's newTasks. This is the SOLE-PRODUCER entry (§10.4) — execute-tasks
// only ever originate here, and the scheduler (sole writer) admits them. A
// plan-task does NO workspace mutation; it only expands the bucket.
func (k *Kernel) runPlanTask(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, in taskInput, wfCtx model.WorkflowContext) taskOutcome {
	task := in.task
	wfCtx.DepFindings = formatDepFindings(in.depArtifacts) // a re-plan sees prior findings too
	taskCtx := model.WithWorkflowContext(ctx, wfCtx)
	fail := func(format string, args ...any) taskOutcome {
		return taskOutcome{taskID: task.ID, status: model.TaskStatusFailed, err: fmt.Errorf(format, args...)}
	}

	// The plan request lives in SubGoal; scope a derived intent to it so the
	// Planner plans toward this goal (for plan-root, SubGoal == intent.Goal).
	planIntent := intent
	if g := strings.TrimSpace(task.SubGoal); g != "" {
		planIntent.Goal = g
	}

	planResult, err := k.executeWorker(taskCtx, sessionID, workflowID, "plan", workflow.RolePlanner, k.workflow.PlannerWorker(), workflow.WorkerInput{
		SessionID:  sessionID,
		WorkflowID: workflowID,
		Intent:     planIntent,
	})
	if err != nil {
		return fail("planner: %w", err)
	}
	produced := planResult.Tasks
	for i := range produced {
		if produced[i].Status == "" {
			produced[i].Status = model.TaskStatusPlanned
		}
	}
	if len(produced) == 0 {
		return fail("planner returned no tasks for %q", planIntent.Goal)
	}
	if err := validateTaskDAG(produced); err != nil {
		return fail("invalid task graph: %w", err)
	}
	return taskOutcome{taskID: task.ID, status: model.TaskStatusPassed, newTasks: produced}
}

func (s *bucketScheduler) pending() bool { return s.pendingCount() > 0 }

func (s *bucketScheduler) pendingCount() int {
	n := 0
	for _, t := range s.bucket {
		switch t.Status {
		case model.TaskStatusPlanned, model.TaskStatusCoding,
			model.TaskStatusAccepting, model.TaskStatusRetrying:
			n++
		}
	}
	return n
}
