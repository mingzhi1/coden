package kernel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// recursingPlanner always returns a single task carrying a SubGoal, so every
// workflow level decomposes into a child — until maxWorkflowDepth forces the
// task to execute directly. callCount records how many times Plan ran: once per
// workflow (top-level + each child), the signal that proves recursion happened
// and that the depth bound stopped it.
type recursingPlanner struct {
	mu        sync.Mutex
	callCount int
}

func (p *recursingPlanner) Plan(_ context.Context, _ string, _ model.IntentSpec) ([]model.Task, error) {
	p.mu.Lock()
	p.callCount++
	p.mu.Unlock()
	return []model.Task{
		{ID: "task-1", Title: "decompose me", SubGoal: "dig one level deeper", Status: "planned", Created: time.Now()},
	}, nil
}

func (p *recursingPlanner) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// TestSubWorkflowRecursionBounded verifies the core recursion mechanism: a task
// carrying a SubGoal spawns a child workflow, and maxWorkflowDepth (1 = 父+子,
// two layers) bounds it. With an always-SubGoal planner the path is top-level
// (depth 0) → child (depth 1: SubGoal ignored, executes directly — no
// grandchild). The planner runs once per workflow, so exactly 2 calls: more
// would mean the depth bound leaked, fewer would mean it never recursed. The
// result bubbles back up to a passing top-level checkpoint.
func TestSubWorkflowRecursionBounded(t *testing.T) {
	t.Parallel()

	planner := &recursingPlanner{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, planner, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	if _, err := k.Submit(context.Background(), "session-1", "decompose recursively"); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Only the top-level workflow commits a saga, so exactly one checkpoint.updated
	// fires — after the whole recursive tree has completed.
	var status string
	timeout := time.After(10 * time.Second)
	for status == "" {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Topic == "checkpoint.updated" {
				status = decodePayload[model.CheckpointUpdatedPayload](t, &ev).Status
			}
		case <-timeout:
			t.Fatalf("timed out; planner calls so far = %d (unbounded recursion?)", planner.calls())
		}
	}

	if got := planner.calls(); got != 2 {
		t.Errorf("planner calls = %d, want 2 (top depth0 + child depth1); recursion or depth-bound is wrong", got)
	}
	if status != "pass" {
		t.Errorf("top-level checkpoint = %q, want pass", status)
	}
}

// alwaysFailAcceptor rejects every artifact, to drive failure-policy paths.
type alwaysFailAcceptor struct{}

func (alwaysFailAcceptor) Accept(_ context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact, _ []model.Task) (model.CheckpointResult, error) {
	return model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     intent.SessionID,
		Status:        "fail",
		ArtifactPaths: []string{artifact.Path},
		Evidence:      []string{"rejected by test"},
		FixGuidance:   "rejected",
		CreatedAt:     time.Now(),
	}, nil
}

func (alwaysFailAcceptor) TakeMessages() []model.WorkerMessage { return nil }

// TestSubWorkflowChildFailureHonorsSkipPolicy locks F4: a failing child workflow
// is routed through the kernel failure policy like any task, not fail-loud. Under
// "skip" the parent must still reach a terminal checkpoint (saga commit fires
// checkpoint.updated) instead of aborting — an abort would emit
// EventWorkflowCanceled and never checkpoint.updated, so this would time out.
// Recursion still happened (planner ran twice) before the child failed.
func TestSubWorkflowChildFailureHonorsSkipPolicy(t *testing.T) {
	t.Parallel()

	planner := &recursingPlanner{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, planner, testExecutor{}, testToolExecutor{}, alwaysFailAcceptor{})
	k.SetFailurePolicy("skip")
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	if _, err := k.Submit(context.Background(), "session-1", "decompose then fail"); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before checkpoint")
			}
			if ev.Topic == "checkpoint.updated" {
				goto done
			}
		case <-timeout:
			t.Fatal("timed out waiting for checkpoint.updated — child failure fail-loud aborted the parent instead of honoring skip")
		}
	}
done:
	if got := planner.calls(); got != 2 {
		t.Errorf("planner calls = %d, want 2 (recursion ran before the child failed)", got)
	}
}

// TestSubWorkflowChangesRollUpToParent locks F1-audit + F5: because the child
// runs under the PARENT's workflow ID, the file written by the depth-1 child is
// recorded against the parent workflow rather than orphaned under a separate
// child ID, so the parent's audit/turn-summary sees it. Under the old childWfID
// scheme fileChangesForWorkflow(parentID) would have filtered it out.
func TestSubWorkflowChangesRollUpToParent(t *testing.T) {
	t.Parallel()

	planner := &recursingPlanner{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, planner, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "session-1", "decompose and write")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Topic == "checkpoint.updated" {
				goto done
			}
		case <-timeout:
			t.Fatal("timed out waiting for checkpoint.updated")
		}
	}
done:
	// The only file write happened in the depth-1 child; it must appear under the
	// parent workflow ID, proving the child shares the parent's identity.
	if changes := k.fileChangesForWorkflow("session-1", wfID); len(changes) == 0 {
		t.Errorf("expected the child's write to roll up under parent workflow %q, got none", wfID)
	}
}
