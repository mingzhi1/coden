package kernel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/checkpoint"
	"github.com/mingzhi1/coden/internal/core/message"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/turnsummary"
)


// TestBuildAccumChanges verifies deduplicated file-change accumulation across turns.
func TestBuildAccumChanges(t *testing.T) {
	t.Parallel()

	turns := []model.TurnSummary{
		{
			ChangedFiles: []model.FileChange{
				{Path: "internal/foo.go", Op: model.FileChangeCreated},
				{Path: "internal/bar.go", Op: model.FileChangeCreated},
			},
		},
		{
			ChangedFiles: []model.FileChange{
				{Path: "internal/foo.go", Op: model.FileChangeModified}, // newer op wins
				{Path: "internal/baz.go", Op: model.FileChangeCreated},
			},
		},
	}

	result := buildAccumChanges(turns)

	if len(result) != 3 {
		t.Fatalf("expected 3 unique paths, got %d: %+v", len(result), result)
	}

	opFor := make(map[string]string, len(result))
	for _, fc := range result {
		opFor[fc.Path] = fc.Op
	}
	if opFor["internal/foo.go"] != model.FileChangeModified {
		t.Errorf("expected foo.go to show 'modified', got %q", opFor["internal/foo.go"])
	}
	if opFor["internal/bar.go"] != model.FileChangeCreated {
		t.Errorf("expected bar.go to show 'created', got %q", opFor["internal/bar.go"])
	}
	if opFor["internal/baz.go"] != model.FileChangeCreated {
		t.Errorf("expected baz.go to show 'created', got %q", opFor["internal/baz.go"])
	}
}

// TestBuildAccumChangesEmpty returns nil for empty input.
func TestBuildAccumChangesEmpty(t *testing.T) {
	t.Parallel()
	if got := buildAccumChanges(nil); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
	if got := buildAccumChanges([]model.TurnSummary{{}}); got != nil {
		t.Errorf("expected nil for turn with no changes, got %+v", got)
	}
}

// TestBuildWorkflowContextInjectsPreviousTurns verifies that buildWorkflowContext
// loads and reverses TurnSummaries from the store (oldest first).
func TestBuildWorkflowContextInjectsPreviousTurns(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()

	// Save 3 turn summaries (newest id = ts-3).
	for idx := 1; idx <= 3; idx++ {
		_ = k.turnSummaries.Save(model.TurnSummary{
			ID:        fmt.Sprintf("ts-%d", idx),
			TurnID:    fmt.Sprintf("wf-%d", idx),
			SessionID: "session-ctx",
			Intent: model.IntentSpec{
				Goal: fmt.Sprintf("goal %d", idx),
			},
			Checkpoint: model.CheckpointResult{Status: "pass"},
			CreatedAt:  time.Now().UTC(),
		})
	}

	wfCtx := k.buildWorkflowContext(context.Background(), "session-ctx")

	if len(wfCtx.PreviousTurns) != 3 {
		t.Fatalf("expected 3 previous turns, got %d", len(wfCtx.PreviousTurns))
	}
	// Oldest first: goal 1 → goal 2 → goal 3.
	if wfCtx.PreviousTurns[0].Intent.Goal != "goal 1" {
		t.Errorf("expected oldest first, got %q", wfCtx.PreviousTurns[0].Intent.Goal)
	}
	if wfCtx.PreviousTurns[2].Intent.Goal != "goal 3" {
		t.Errorf("expected newest last, got %q", wfCtx.PreviousTurns[2].Intent.Goal)
	}
}

// TestCommitWorkflowSagaSuccess verifies the happy path: all 4 saga steps commit.
func TestCommitWorkflowSagaSuccess(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()

	sessionID := "saga-sess"
	workflowID := "saga-wf"
	_ = k.ensureSession(sessionID)
	_ = k.turns.Save(model.Turn{
		ID: workflowID, SessionID: sessionID, WorkflowID: workflowID,
		Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	cp := model.CheckpointResult{
		WorkflowID: workflowID, SessionID: sessionID,
		Status: "pass", CreatedAt: time.Now().UTC(),
	}
	ts := model.TurnSummary{
		ID: "ts-saga", TurnID: workflowID, SessionID: sessionID,
		CreatedAt: time.Now().UTC(),
	}
	msg := model.Message{
		ID: "msg-saga", SessionID: sessionID, Role: "assistant",
		Content: "done", CreatedAt: time.Now(),
	}

	err := k.commitWorkflowSaga(sessionID, workflowID, cp, ts, msg)
	if err != nil {
		t.Fatalf("saga should succeed: %v", err)
	}

	// Verify checkpoint saved.
	cpGot, ok := k.checkpoints.Get(sessionID, workflowID)
	if !ok {
		t.Fatal("checkpoint not found after saga commit")
	}
	if cpGot.Status != "pass" {
		t.Errorf("checkpoint status = %q, want pass", cpGot.Status)
	}

	// Verify turn summary saved.
	tsGot, ok := k.turnSummaries.Get(workflowID)
	if !ok {
		t.Fatal("turn summary not found after saga commit")
	}
	if tsGot.ID != "ts-saga" {
		t.Errorf("turn summary ID = %q, want ts-saga", tsGot.ID)
	}

	// Verify message saved.
	msgs := k.messages.List(sessionID, 10)
	found := false
	for _, m := range msgs {
		if m.ID == "msg-saga" {
			found = true
		}
	}
	if !found {
		t.Error("assistant message not found after saga commit")
	}

	// Verify turn status updated.
	turn, ok := k.turns.Get(workflowID)
	if !ok {
		t.Fatal("turn not found")
	}
	if turn.Status != "pass" {
		t.Errorf("turn status = %q, want pass", turn.Status)
	}
}

// failingMessageStore always fails on Save (simulates a persistence error).
type failingMessageStore struct{ message.Store }

func (f failingMessageStore) Save(_ model.Message) error {
	return fmt.Errorf("simulated message save failure")
}

// TestCommitWorkflowSagaRollbackOnFailure verifies that when the 3rd saga step
// (save message) fails, the first 2 steps (checkpoint + turn summary) are
// compensated (rolled back).
func TestCommitWorkflowSagaRollbackOnFailure(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()

	// Inject failing message store to make step 3 fail.
	k.messages = failingMessageStore{Store: message.NewStore()}

	sessionID := "saga-fail-sess"
	workflowID := "saga-fail-wf"
	_ = k.ensureSession(sessionID)
	_ = k.turns.Save(model.Turn{
		ID: workflowID, SessionID: sessionID, WorkflowID: workflowID,
		Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	cp := model.CheckpointResult{
		WorkflowID: workflowID, SessionID: sessionID,
		Status: "pass", CreatedAt: time.Now().UTC(),
	}
	ts := model.TurnSummary{
		ID: "ts-fail", TurnID: workflowID, SessionID: sessionID,
		CreatedAt: time.Now().UTC(),
	}
	msg := model.Message{
		ID: "msg-fail", SessionID: sessionID, Role: "assistant",
		Content: "done", CreatedAt: time.Now(),
	}

	err := k.commitWorkflowSaga(sessionID, workflowID, cp, ts, msg)
	if err == nil {
		t.Fatal("saga should fail when message save fails")
	}

	// Verify checkpoint was rolled back.
	_, ok := k.checkpoints.Get(sessionID, workflowID)
	if ok {
		t.Error("checkpoint should have been rolled back after saga failure")
	}

	// Verify turn summary was rolled back.
	_, ok = k.turnSummaries.Get(workflowID)
	if ok {
		t.Error("turn summary should have been rolled back after saga failure")
	}

	// Verify turn status remains "running" (not updated since step 4 never ran).
	turn, ok := k.turns.Get(workflowID)
	if !ok {
		t.Fatal("turn not found")
	}
	if turn.Status != "running" {
		t.Errorf("turn status should still be 'running', got %q", turn.Status)
	}
}

// TestOrphanTurnRecovery verifies L4-08: Start() marks orphan turns as crashed.
func TestOrphanTurnRecovery(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()

	// Simulate an orphan turn left from a previous process crash.
	_ = k.turns.Save(model.Turn{
		ID: "orphan-wf", SessionID: "orphan-sess", WorkflowID: "orphan-wf",
		Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	k.Start()

	turn, ok := k.turns.Get("orphan-wf")
	if !ok {
		t.Fatal("orphan turn not found")
	}
	if turn.Status != "crashed" {
		t.Errorf("orphan turn status = %q, want 'crashed'", turn.Status)
	}
}

// TestGuidanceHistoryOscillation verifies L3-06: AppendGuidance detects
// oscillation when two consecutive attempts have identical evidence.
func TestGuidanceHistoryOscillation(t *testing.T) {
	t.Parallel()

	var rc model.RetryContext

	cp1 := model.CheckpointResult{
		Evidence:    []string{"undefined: Foo"},
		FixGuidance: "add import",
	}
	osc1 := rc.AppendGuidance(0, cp1)
	if osc1 {
		t.Error("first attempt should not detect oscillation")
	}

	// Same evidence repeated → oscillation.
	cp2 := model.CheckpointResult{
		Evidence:    []string{"undefined: Foo"},
		FixGuidance: "add import again",
	}
	osc2 := rc.AppendGuidance(1, cp2)
	if !osc2 {
		t.Error("second consecutive identical evidence should detect oscillation")
	}

	// Different evidence → no oscillation.
	cp3 := model.CheckpointResult{
		Evidence:    []string{"syntax error on line 42"},
		FixGuidance: "fix syntax",
	}
	osc3 := rc.AppendGuidance(2, cp3)
	if osc3 {
		t.Error("different evidence should not detect oscillation")
	}

	if len(rc.GuidanceHistory) != 3 {
		t.Errorf("expected 3 entries in GuidanceHistory, got %d", len(rc.GuidanceHistory))
	}
}

// TestBuildRetryFeedbackContainsOscillationWarning verifies the oscillation
// warning is emitted in the retry feedback string.
func TestBuildRetryFeedbackContainsOscillationWarning(t *testing.T) {
	t.Parallel()

	cp := model.CheckpointResult{
		Evidence:    []string{"compile failed"},
		FixGuidance: "fix the error",
	}
	art := model.Artifact{Path: "main.go"}
	rc := &model.RetryContext{
		GuidanceHistory: []model.FixGuidanceEntry{
			{Attempt: 0, Evidence: "compile failed", FixGuidance: "fix the error"},
			{Attempt: 1, Evidence: "compile failed", FixGuidance: "fix the error"},
		},
	}

	feedback := buildRetryFeedback(cp, art, rc, true)

	if !contains(feedback, "OSCILLATION DETECTED") {
		t.Error("expected oscillation warning in feedback")
	}
	if !contains(feedback, "GUIDANCE HISTORY") {
		t.Error("expected guidance history section in feedback")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestCheckpointStoreDelete verifies L4-07 Delete compensating action.
func TestCheckpointStoreDelete(t *testing.T) {
	t.Parallel()
	store := checkpoint.NewStore()
	cp := model.CheckpointResult{
		WorkflowID: "wf-del", SessionID: "sess-del",
		Status: "pass", CreatedAt: time.Now().UTC(),
	}
	_ = store.Save(cp)
	if _, ok := store.Get("sess-del", "wf-del"); !ok {
		t.Fatal("checkpoint not found after save")
	}
	store.Delete("wf-del", "sess-del")
	if _, ok := store.Get("sess-del", "wf-del"); ok {
		t.Error("checkpoint should be deleted")
	}
}

// TestTurnSummaryStoreDelete verifies L4-07 Delete compensating action.
func TestTurnSummaryStoreDelete(t *testing.T) {
	t.Parallel()
	store := turnsummary.NewStore()
	ts := model.TurnSummary{
		ID: "ts-del", TurnID: "wf-del", SessionID: "sess-del",
		CreatedAt: time.Now().UTC(),
	}
	_ = store.Save(ts)
	if _, ok := store.Get("wf-del"); !ok {
		t.Fatal("turn summary not found after save")
	}
	store.Delete("ts-del")
	if _, ok := store.Get("wf-del"); ok {
		t.Error("turn summary should be deleted")
	}
}

// TestMessageStoreDelete verifies L4-07 Delete compensating action.
func TestMessageStoreDelete(t *testing.T) {
	t.Parallel()
	store := message.NewStore()
	msg := model.Message{
		ID: "msg-del", SessionID: "sess-del", Role: "assistant",
		Content: "test", CreatedAt: time.Now(),
	}
	_ = store.Save(msg)
	msgs := store.List("sess-del", 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	store.Delete("msg-del")
	msgs = store.List("sess-del", 10)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after delete, got %d", len(msgs))
	}
}
