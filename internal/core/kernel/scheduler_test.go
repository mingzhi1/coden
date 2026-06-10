package kernel

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
)

// recordingWorker passes every task and records the order tasks were dispatched.
type recordingWorker struct {
	mu    sync.Mutex
	order []string
	// onTask, if set for a task ID, returns extra newTasks the first time that
	// task runs — modeling the sole-producer "Planner adds tasks" path.
	onTask map[string][]model.Task
}

func (r *recordingWorker) run(_ context.Context, in taskInput) taskOutcome {
	t := in.task
	r.mu.Lock()
	r.order = append(r.order, t.ID)
	extra := r.onTask[t.ID]
	delete(r.onTask, t.ID)
	r.mu.Unlock()
	return taskOutcome{taskID: t.ID, status: model.TaskStatusPassed, newTasks: extra}
}

func planned(id string, prio int, deps ...string) model.Task {
	return model.Task{ID: id, Status: model.TaskStatusPlanned, Priority: prio, DependsOn: deps}
}

// TestScheduler_PriorityAndDeps verifies the core contract: WIP=1 so dispatch is
// serial and observable — ready tasks run in priority order, and a dependent
// runs only after its dependency completes (via the completion record).
func TestScheduler_PriorityAndDeps(t *testing.T) {
	t.Parallel()
	w := &recordingWorker{onTask: map[string][]model.Task{}}
	s := newBucketScheduler([]model.Task{
		planned("low", 1),
		planned("high", 5),
		planned("dep", 0, "high"), // depends on high
	}, 1)

	if err := s.run(context.Background(), w.run); err != nil {
		t.Fatalf("run: %v", err)
	}
	// high (prio 5) first; then low (prio 1); dep waits for high → runs last
	// (it only becomes ready after high passes, and low outranks it once ready
	// is recomputed — but low already ran, so order is high, low, dep).
	want := []string{"high", "low", "dep"}
	if len(w.order) != 3 || w.order[0] != "high" || w.order[2] != "dep" {
		t.Errorf("dispatch order = %v, want prefix high… suffix …dep (%v)", w.order, want)
	}
	if s.completed["high"] != model.TaskStatusPassed || s.completed["dep"] != model.TaskStatusPassed {
		t.Errorf("completion record incomplete: %v", s.completed)
	}
}

// TestScheduler_SoleProducerAddsTasks verifies that tasks returned in an outcome
// (the Planner path) enter the bucket and get scheduled — append is no longer a
// special post-DAG loop, just admission via apply.
func TestScheduler_SoleProducerAddsTasks(t *testing.T) {
	t.Parallel()
	w := &recordingWorker{onTask: map[string][]model.Task{
		"seed": {planned("child-a", 0), planned("child-b", 0, "child-a")},
	}}
	s := newBucketScheduler([]model.Task{planned("seed", 0)}, 4)

	if err := s.run(context.Background(), w.run); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, id := range []string{"seed", "child-a", "child-b"} {
		if s.completed[id] != model.TaskStatusPassed {
			t.Errorf("expected %q passed, completion record = %v", id, s.completed)
		}
	}
	// child-b depends on child-a → must run after it.
	idx := map[string]int{}
	for i, id := range w.order {
		idx[id] = i
	}
	if idx["child-b"] < idx["child-a"] {
		t.Errorf("child-b ran before its dependency child-a: %v", w.order)
	}
}

// TestScheduler_Deadlock verifies that an unsatisfiable real dependency (a dep
// present in the bucket that fails) stalls the loop into a detected deadlock
// rather than spinning or hanging.
func TestScheduler_Deadlock(t *testing.T) {
	t.Parallel()
	// failer fails; blocked depends on failer → never ready after failer fails.
	failing := func(_ context.Context, in taskInput) taskOutcome {
		tk := in.task
		st := model.TaskStatusPassed
		if tk.ID == "failer" {
			st = model.TaskStatusFailed
		}
		return taskOutcome{taskID: tk.ID, status: st}
	}
	s := newBucketScheduler([]model.Task{
		planned("failer", 0),
		planned("blocked", 0, "failer"),
	}, 4)

	err := s.run(context.Background(), failing)
	if err == nil {
		t.Fatal("expected deadlock error when a dependency fails, got nil")
	}
}

// TestBucketScheduler_RealAgents drives the engine end-to-end with the real
// execute→accept worker and the test agents: two dependent tasks run through
// Executor → tool exec → Acceptor and both pass, in dependency order.
func TestBucketScheduler_RealAgents(t *testing.T) {
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()

	intent := model.IntentSpec{ID: "i1", SessionID: "session-1", Goal: "build", Kind: model.IntentKindCodeGen}
	wfCtx := model.WorkflowContext{WorkspaceRoot: k.workspace.Root()}

	s := newBucketScheduler([]model.Task{
		{ID: "t1", Title: "first task", Status: model.TaskStatusPlanned},
		{ID: "t2", Title: "second task", Status: model.TaskStatusPlanned, DependsOn: []string{"t1"}},
	}, 4)

	worker := func(ctx context.Context, in taskInput) taskOutcome {
		return k.executeTask(ctx, "session-1", "wf-test", intent, in, wfCtx)
	}
	if err := s.run(context.Background(), worker); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, id := range []string{"t1", "t2"} {
		if s.completed[id] != model.TaskStatusPassed {
			t.Errorf("task %q not passed: completion record = %v", id, s.completed)
		}
	}
}

// TestProjection_RetryFeedbackAndDepArtifacts verifies the scheduler projects
// per-task context to workers: a retried task sees WHY it failed (#3), and a
// dependent task sees its passed dependency's artifact (#4 §11 DepArtifacts).
func TestProjection_RetryFeedbackAndDepArtifacts(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var bRetryFeedback string
	var bDeps map[string]model.Artifact
	flakyRuns := 0
	w := func(_ context.Context, in taskInput) taskOutcome {
		mu.Lock()
		defer mu.Unlock()
		switch in.task.ID {
		case "a":
			return taskOutcome{taskID: "a", status: model.TaskStatusPassed,
				artifact: model.Artifact{Summary: "found X", Evidence: []string{"src: docs"}}}
		case "b": // depends on a → must receive a's artifact
			bDeps = in.depArtifacts
			return taskOutcome{taskID: "b", status: model.TaskStatusPassed}
		case "flaky":
			flakyRuns++
			if flakyRuns == 1 {
				return taskOutcome{taskID: "flaky", status: model.TaskStatusFailed,
					artifact: model.Artifact{Evidence: []string{"build broke: missing import"}}}
			}
			bRetryFeedback = in.retryFeedback // 2nd attempt must carry the failure
			return taskOutcome{taskID: "flaky", status: model.TaskStatusPassed}
		}
		return taskOutcome{taskID: in.task.ID, status: model.TaskStatusPassed}
	}
	s := newBucketScheduler([]model.Task{
		planned("a", 0), planned("b", 0, "a"), planned("flaky", 0),
	}, 1)
	s.maxRetries = 1
	if err := s.run(context.Background(), w); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(bRetryFeedback, "missing import") {
		t.Errorf("#3 retry did not receive failure feedback, got %q", bRetryFeedback)
	}
	if bDeps["a"].Summary != "found X" {
		t.Errorf("#4 dependent task did not receive dep artifact, got %+v", bDeps)
	}
}

// TestRetry_AbandonsAfterBudget verifies a task that keeps failing is re-dispatched
// up to maxRetries, then abandoned (terminal) — and that retries bump Attempts.
func TestRetry_AbandonsAfterBudget(t *testing.T) {
	t.Parallel()
	var runs int
	var mu sync.Mutex
	alwaysFail := func(_ context.Context, in taskInput) taskOutcome {
		tk := in.task
		mu.Lock()
		runs++
		mu.Unlock()
		return taskOutcome{taskID: tk.ID, status: model.TaskStatusFailed}
	}
	s := newBucketScheduler([]model.Task{planned("flaky", 0)}, 1)
	s.maxRetries = 2 // 3 attempts total

	err := s.run(context.Background(), alwaysFail)
	if err != nil {
		t.Fatalf("run: %v", err) // single task, no dependents → drains to abandoned, no deadlock
	}
	if runs != 3 {
		t.Errorf("expected 3 attempts (1 + 2 retries), got %d", runs)
	}
	if s.completed["flaky"] != model.TaskStatusAbandoned {
		t.Errorf("expected abandoned after retries, got %q", s.completed["flaky"])
	}
}

// TestArtifact_RetainedOnApply verifies the engine retains each task's artifact
// (the evidence source for收口 / DepArtifacts), keyed by task ID, and that it
// survives into the snapshot.
func TestArtifact_RetainedOnApply(t *testing.T) {
	t.Parallel()
	var last bucketSnapshot
	var mu sync.Mutex
	withArtifact := func(_ context.Context, in taskInput) taskOutcome {
		tk := in.task
		return taskOutcome{
			taskID:   tk.ID,
			status:   model.TaskStatusPassed,
			artifact: model.Artifact{Path: tk.ID + ".go", Summary: "built " + tk.ID, Evidence: []string{"go build ok"}},
		}
	}
	s := newBucketScheduler([]model.Task{planned("t1", 0)}, 1)
	s.persist = func(snap bucketSnapshot) error {
		mu.Lock()
		last = snap
		mu.Unlock()
		return nil
	}
	if err := s.run(context.Background(), withArtifact); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := s.artifacts["t1"]; got.Path != "t1.go" || len(got.Evidence) != 1 {
		t.Errorf("artifact not retained on scheduler: %+v", got)
	}
	if got := last.artifacts["t1"]; got.Summary != "built t1" {
		t.Errorf("artifact not in snapshot: %+v", last.artifacts)
	}
}

// TestCheckpoint_EmitsPerApplyMonotonic verifies the engine emits one incremental
// snapshot per apply with a monotonic seq, capturing the evolving completion record.
func TestCheckpoint_EmitsPerApplyMonotonic(t *testing.T) {
	t.Parallel()
	var snaps []bucketSnapshot
	var mu sync.Mutex
	w := &recordingWorker{onTask: map[string][]model.Task{}}
	s := newBucketScheduler([]model.Task{planned("a", 0), planned("b", 0, "a")}, 1)
	s.persist = func(snap bucketSnapshot) error {
		mu.Lock()
		snaps = append(snaps, snap)
		mu.Unlock()
		return nil
	}
	if err := s.run(context.Background(), w.run); err != nil {
		t.Fatalf("run: %v", err)
	}
	// One snapshot per apply (2 tasks → 2 applies), seq strictly increasing 1,2.
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].seq != 1 || snaps[1].seq != 2 {
		t.Errorf("seq not monotonic: %d, %d", snaps[0].seq, snaps[1].seq)
	}
	// Final snapshot's completion record holds both tasks passed.
	last := snaps[len(snaps)-1]
	if last.completed["a"] != model.TaskStatusPassed || last.completed["b"] != model.TaskStatusPassed {
		t.Errorf("final snapshot completion incomplete: %v", last.completed)
	}
}

// TestCheckpoint_SnapshotIsDeepCopy verifies a snapshot is isolated from later
// scheduler mutation: after seq-1 captures "b" as planned, the seq-2 apply flips
// "b" to passed on the LIVE bucket via setStatus — an aliased snapshot would show
// the change, a deep copy stays planned.
func TestCheckpoint_SnapshotIsDeepCopy(t *testing.T) {
	t.Parallel()
	var first bucketSnapshot
	var got bool
	var mu sync.Mutex
	w := &recordingWorker{onTask: map[string][]model.Task{}}
	// b depends on a → WIP 1 forces apply(a) at seq 1, then apply(b) at seq 2.
	s := newBucketScheduler([]model.Task{planned("a", 0), planned("b", 0, "a")}, 1)
	s.persist = func(snap bucketSnapshot) error {
		mu.Lock()
		if !got {
			first, got = snap, true // capture the seq-1 snapshot (a passed, b planned)
		}
		mu.Unlock()
		return nil
	}
	if err := s.run(context.Background(), w.run); err != nil {
		t.Fatalf("run: %v", err)
	}
	// In the seq-1 snapshot, "b" must still be planned — the later apply that
	// passed "b" on the live bucket must not have reached the deep-copied snapshot.
	for _, tk := range first.bucket {
		if tk.ID == "b" && tk.Status != model.TaskStatusPlanned {
			t.Fatalf("snapshot aliased the live bucket: b = %q, want planned", tk.Status)
		}
	}
}

// TestCheckpoint_ResumeFromSnapshot verifies a scheduler reconstructed from a
// snapshot continues to completion, with in-flight tasks reset to planned.
func TestCheckpoint_ResumeFromSnapshot(t *testing.T) {
	t.Parallel()
	// A snapshot mid-run: "a" passed, "b" was caught coding (in-flight), "c" planned.
	snap := bucketSnapshot{
		seq: 3,
		bucket: []model.Task{
			{ID: "b", Status: model.TaskStatusCoding},
			{ID: "c", Status: model.TaskStatusPlanned, DependsOn: []string{"b"}},
		},
		completed: map[string]string{"a": model.TaskStatusPassed},
		metrics:   snapshotMetrics{dispatches: 2},
	}
	s := newBucketSchedulerFromSnapshot(snap, 1)
	// In-flight "b" must have been reset to planned so it re-dispatches.
	if s.bucket[0].Status != model.TaskStatusPlanned {
		t.Errorf("in-flight task not reset: %q", s.bucket[0].Status)
	}
	// seq and dispatch budget carry across resume.
	if s.seq != 3 || s.guardian.dispatches != 2 {
		t.Errorf("resume lost seq/dispatches: seq=%d dispatches=%d", s.seq, s.guardian.dispatches)
	}
	w := &recordingWorker{onTask: map[string][]model.Task{}}
	if err := s.run(context.Background(), w.run); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if s.completed["b"] != model.TaskStatusPassed || s.completed["c"] != model.TaskStatusPassed {
		t.Errorf("resume did not finish bucket: %v", s.completed)
	}
}

// TestGuardian_DispatchBudget verifies the global work-unit budget halts a runaway
// producer: each task spawns one more (unbounded), but the Guardian stops the loop
// once the dispatch budget is reached, returning a typed budget stop (not a crash).
func TestGuardian_DispatchBudget(t *testing.T) {
	t.Parallel()
	// Each task passes and emits one fresh child → the bucket never drains.
	runaway := func(_ context.Context, in taskInput) taskOutcome {
		tk := in.task
		return taskOutcome{
			taskID:   tk.ID,
			status:   model.TaskStatusPassed,
			newTasks: []model.Task{planned(tk.ID+"-c", 0)},
		}
	}
	s := newBucketScheduler([]model.Task{planned("root", 0)}, 1)
	s.guardian.maxDispatches = 4
	s.guardian.maxGrowth = 0 // isolate the budget guard

	err := s.run(context.Background(), runaway)
	var gs *guardianStop
	if !errors.As(err, &gs) || gs.code != "budget" {
		t.Fatalf("expected budget guardianStop, got %v", err)
	}
	if s.guardian.dispatches != 4 {
		t.Errorf("expected exactly 4 dispatches at budget, got %d", s.guardian.dispatches)
	}
}

// TestGuardian_Oscillation verifies the convergence guard: a producer that adds
// more work than it completes each apply makes the bucket grow consecutively, and
// the Guardian halts it as non-convergent.
func TestGuardian_Oscillation(t *testing.T) {
	t.Parallel()
	var n int
	var mu sync.Mutex
	// Each task spawns two children → net bucket growth on every apply.
	grower := func(_ context.Context, in taskInput) taskOutcome {
		tk := in.task
		mu.Lock()
		n++
		a, b := n*2, n*2+1
		mu.Unlock()
		return taskOutcome{
			taskID:   tk.ID,
			status:   model.TaskStatusPassed,
			newTasks: []model.Task{planned(tk.ID+"-a"+strconv.Itoa(a), 0), planned(tk.ID+"-b"+strconv.Itoa(b), 0)},
		}
	}
	s := newBucketScheduler([]model.Task{planned("root", 0)}, 1)
	s.guardian.maxGrowth = 3
	s.guardian.maxDispatches = 0 // isolate the oscillation guard

	err := s.run(context.Background(), grower)
	var gs *guardianStop
	if !errors.As(err, &gs) || gs.code != "oscillation" {
		t.Fatalf("expected oscillation guardianStop, got %v", err)
	}
}

// rejectCritic always rejects — used to verify the outer goal-critique downgrade.
type rejectCritic struct{}

func (rejectCritic) Critique(_ context.Context, _ string, _ model.IntentSpec, _ []model.Task) (model.CritiqueResult, error) {
	return model.CritiqueResult{Approved: false, Issues: []string{"goal not met"}, Suggestions: []string{"handle errors"}}, nil
}

// TestApplyGoalCritique_DowngradesOnReject verifies the outer goal-critique turns a
// self-reported pass into a fail with guidance when the heterogeneous Critic
// rejects, and is a no-op when no Critic is configured.
func TestApplyGoalCritique_DowngradesOnReject(t *testing.T) {
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()
	intent := model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "build"}
	tasks := []model.Task{{ID: "t1", Status: model.TaskStatusPassed}}
	pass := model.CheckpointResult{Status: "pass", Evidence: []string{"go build ok"}}

	// No Critic configured → unchanged.
	if got := k.applyGoalCritique(context.Background(), "wf", intent, tasks, pass); got.Status != "pass" {
		t.Errorf("no-critic should be a no-op, got %q", got.Status)
	}

	// Rejecting Critic → downgrade to fail with guidance.
	k.SetCritic(rejectCritic{})
	got := k.applyGoalCritique(context.Background(), "wf", intent, tasks, pass)
	if got.Status != "fail" {
		t.Errorf("expected downgrade to fail, got %q", got.Status)
	}
	if got.FixGuidance == "" {
		t.Error("expected FixGuidance from the rejecting Critic")
	}
}

// TestRunTasksBucket_PersistsAndResumes verifies the production execution path
// persists incremental snapshots to the checkpoint store, and that
// resumeTasksBucket can rebuild a scheduler from the latest one.
func TestRunTasksBucket_PersistsAndResumes(t *testing.T) {
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()

	intent := model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "build", Kind: model.IntentKindCodeGen}
	wfCtx := model.WorkflowContext{WorkspaceRoot: k.workspace.Root()}
	tasks := []model.Task{{ID: "t1", Title: "one", Status: model.TaskStatusPlanned}}

	cp, _, _, err := k.runTasksBucket(context.Background(), "s1", "wf-persist", intent, tasks, wfCtx)
	if err != nil {
		t.Fatalf("runTasksBucket: %v", err)
	}
	if cp.Status != "pass" {
		t.Fatalf("expected pass, got %q", cp.Status)
	}
	// The latest snapshot must be durable and reflect the completed task.
	snap, ok := k.checkpoints.LatestBucket("s1", "wf-persist")
	if !ok {
		t.Fatal("no bucket snapshot persisted")
	}
	if snap.Completed["t1"] != model.TaskStatusPassed {
		t.Errorf("persisted snapshot missing completion: %+v", snap.Completed)
	}
	// resumeTasksBucket rebuilds from it — nothing pending → drains immediately.
	rs, ok := k.resumeTasksBucket("s1", "wf-persist")
	if !ok {
		t.Fatal("resumeTasksBucket found no snapshot")
	}
	if rs.completed["t1"] != model.TaskStatusPassed {
		t.Errorf("resumed scheduler lost completion record: %+v", rs.completed)
	}
}

// TestFinishBucketWorkflow_Responder drives the engine to drain, then收口: assemble
// result + (no Critic set → skipped) + Responder produces a non-empty user message
// and a pass checkpoint carrying the task's artifact evidence.
func TestFinishBucketWorkflow_Responder(t *testing.T) {
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()

	intent := model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "build a thing", Kind: model.IntentKindCodeGen}
	wfCtx := model.WorkflowContext{WorkspaceRoot: k.workspace.Root()}

	s, runErr := k.runBucketWorkflow(context.Background(), "s1", "wf-fin", intent, wfCtx)
	if runErr != nil {
		t.Fatalf("runBucketWorkflow: %v", runErr)
	}
	msg, cp := k.finishBucketWorkflow(context.Background(), "s1", "wf-fin", intent, s, runErr)
	if msg == "" {
		t.Error("收口 produced an empty user message")
	}
	if cp.Status != "pass" {
		t.Errorf("expected pass checkpoint, got %q (guidance=%q)", cp.Status, cp.FixGuidance)
	}
}

// TestBucketWorkflow_PlannerSeeds drives the engine from its real entry: a single
// plan-root task is seeded, the Planner (sole producer) expands it into an
// execute-task, and the execute→accept worker carries it to passed. Verifies the
// SubGoal-based dispatch routing (plan vs execute) and sole-producer admission.
func TestBucketWorkflow_PlannerSeeds(t *testing.T) {
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()

	intent := model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "build a thing", Kind: model.IntentKindCodeGen}
	wfCtx := model.WorkflowContext{WorkspaceRoot: k.workspace.Root()}

	s, err := k.runBucketWorkflow(context.Background(), "s1", "wf-seed", intent, wfCtx)
	if err != nil {
		t.Fatalf("runBucketWorkflow: %v", err)
	}
	// plan-root is routed to the Planner and passes; testPlanner produces task-1,
	// which is routed to execute→accept and passes.
	if s.completed["plan-root"] != model.TaskStatusPassed {
		t.Errorf("plan-root not passed: completion record = %v", s.completed)
	}
	if s.completed["task-1"] != model.TaskStatusPassed {
		t.Errorf("planner-produced task-1 not passed: completion record = %v", s.completed)
	}
}
