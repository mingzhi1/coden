package kernel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// preExecutedCoder returns a write_file call that is already marked Executed,
// simulating the agentic coder having already run it in its loop.
type preExecutedCoder struct{}

func (c preExecutedCoder) Build(_ context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{{
			ToolCallID: "tool-call-" + workflowID,
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    "artifacts/" + intent.ID + ".md",
				Content: tasks[0].Title,
			},
			Executed: true,
			ExecResult: toolruntime.Result{
				ArtifactPath: "artifacts/" + intent.ID + ".md",
				Summary:      "pre-executed write_file",
			},
		}},
	}, nil
}

func (c preExecutedCoder) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{
		{Kind: "info", Role: "coder", Content: "coder produced pre-executed call"},
	}
}

// countingExecutor counts how many times Execute is called.
type countingExecutor struct {
	mu            sync.Mutex
	calls         int
	mutationCalls int
}

func (e *countingExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	e.mu.Lock()
	e.calls++
	if req.Kind == "write_file" || req.Kind == "edit_file" {
		e.mutationCalls++
	}
	e.mu.Unlock()
	return toolruntime.Result{
		ArtifactPath: req.Path,
		Summary:      "executed " + req.Kind,
		Output:       req.Path,
	}, nil
}

func TestPreExecutedMutationsNotReExecuted(t *testing.T) {
	t.Parallel()

	executor := &countingExecutor{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, preExecutedCoder{}, executor, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "test pre-executed")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for workflow to complete.
	timeout := time.After(5 * time.Second)
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

	// The executor should NOT have been called for the pre-executed write_file.
	executor.mu.Lock()
	mCalls := executor.mutationCalls
	executor.mu.Unlock()
	if mCalls != 0 {
		t.Errorf("expected 0 mutation executor calls (mutation was pre-executed), got %d", mCalls)
	}
}

func TestTurnSummaryGeneratedAfterWorkflow(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "session-1", "generate summary test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for workflow to complete.
	timeout := time.After(5 * time.Second)
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

	// Verify TurnSummary was persisted.
	summary, ok := k.turnSummaries.Get(wfID)
	if !ok {
		t.Fatal("expected TurnSummary to be persisted after workflow")
	}
	if summary.SessionID != "session-1" {
		t.Errorf("expected session-1, got %q", summary.SessionID)
	}
	if summary.TurnID != wfID {
		t.Errorf("expected turn ID %q, got %q", wfID, summary.TurnID)
	}
	if summary.Intent.Goal != "generate summary test" {
		t.Errorf("expected intent goal 'generate summary test', got %q", summary.Intent.Goal)
	}
	if len(summary.TaskResults) == 0 {
		t.Fatal("expected at least one task result")
	}
	if summary.TaskResults[0].Status != model.TaskStatusPassed {
		t.Errorf("expected task status 'passed', got %q", summary.TaskResults[0].Status)
	}
	if summary.Checkpoint.Status != "pass" {
		t.Errorf("expected checkpoint status 'pass', got %q", summary.Checkpoint.Status)
	}

	// ListBySession should also return it.
	list := k.turnSummaries.ListBySession("session-1", 10)
	if len(list) != 1 {
		t.Fatalf("expected 1 summary in session, got %d", len(list))
	}
}

func TestReplanRequestedError(t *testing.T) {
	t.Parallel()

	evidence := []string{"build failed: missing import", "test timeout"}
	err := &replanRequestedError{evidence: evidence}

	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
	if !contains(err.Error(), "replan requested") {
		t.Fatalf("expected error to contain 'replan requested', got %q", err.Error())
	}

	// Verify type assertion works (used in kernel_workflow.go).
	var taskErr error = err
	re, ok := taskErr.(*replanRequestedError)
	if !ok {
		t.Fatal("expected type assertion to succeed")
	}
	if len(re.evidence) != 2 {
		t.Fatalf("expected 2 evidence items, got %d", len(re.evidence))
	}
}
