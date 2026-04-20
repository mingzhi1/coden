package kernel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// --- Stubs for kernel e2e tests ---

// criticStub rejects the first call and approves the second.
type criticStub struct {
	calls atomic.Int32
}

func (c *criticStub) Critique(_ context.Context, _ string, _ model.IntentSpec, tasks []model.Task) (model.CritiqueResult, error) {
	n := c.calls.Add(1)
	if n == 1 {
		return model.CritiqueResult{
			Score:       0.3,
			Approved:    false,
			Issues:      []string{"incomplete error handling"},
			Suggestions: []string{"add validation for edge cases"},
			Summary:     "plan needs work",
		}, nil
	}
	return model.CritiqueResult{Score: 0.95, Approved: true, Summary: "plan looks good"}, nil
}

// replannerStub refines tasks by marking them refined.
type replannerStub struct {
	called atomic.Int32
}

func (r *replannerStub) RePlan(_ context.Context, _ model.IntentSpec, tasks []model.Task, _ []model.FileSnippet) ([]model.Task, error) {
	r.called.Add(1)
	out := make([]model.Task, len(tasks))
	for i, t := range tasks {
		out[i] = t
		out[i].Title = t.Title + " [refined]"
	}
	return out, nil
}

// retryAcceptor rejects the first N calls then approves.
type retryAcceptor struct {
	rejectCount int32
	calls       atomic.Int32
}

func (a *retryAcceptor) Accept(_ context.Context, wfID string, intent model.IntentSpec, art model.Artifact, _ []model.Task) (model.CheckpointResult, error) {
	n := a.calls.Add(1)
	status := "fail"
	var evidence []string
	if n > a.rejectCount {
		status = "pass"
		evidence = []string{"all checks passed"}
	} else {
		evidence = []string{"test suite failed: 2 failures"}
	}
	return model.CheckpointResult{
		WorkflowID:    wfID,
		SessionID:     intent.SessionID,
		Status:        status,
		ArtifactPaths: []string{art.Path},
		Evidence:      evidence,
		CreatedAt:     time.Now(),
	}, nil
}

func (a *retryAcceptor) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "info", Role: "acceptor", Content: "acceptor verdict"}}
}

// TestCriticReplannerIntegration verifies that the kernel wires Critic and
// Replanner correctly: critique feedback flows to the replanner and refined
// tasks are used for coding.
func TestCriticReplannerIntegration(t *testing.T) {
	t.Parallel()

	critic := &criticStub{}
	replanner := &replannerStub{}

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	k.SetCritic(critic)
	k.SetReplanner(replanner)
	defer k.Close()

	events, cancel := k.Subscribe("session-cr")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-cr", "test critic replanner")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for workflow completion.
	timeout := time.After(5 * time.Second)
	var gotCritique, gotReplan, gotCheckpoint bool
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed unexpectedly")
			}
			if ev.Topic == "workflow.step_updated" {
				var payload model.WorkflowStepUpdatedPayload
				if decErr := ev.DecodePayload(&payload); decErr == nil {
					if payload.Step == "critique" && payload.Status == "done" {
						gotCritique = true
					}
					if payload.Step == "replan" && payload.Status == "done" {
						gotReplan = true
					}
				}
			}
			if ev.Topic == "checkpoint.updated" {
				gotCheckpoint = true
				goto done
			}
		case <-timeout:
			t.Fatalf("timed out: gotCritique=%v gotReplan=%v gotCheckpoint=%v", gotCritique, gotReplan, gotCheckpoint)
		}
	}
done:
	if !gotCritique {
		t.Error("expected critique step event")
	}
	if !gotReplan {
		t.Error("expected replan step event")
	}
	if !gotCheckpoint {
		t.Error("expected checkpoint event")
	}

	// Verify critic and replanner were actually called.
	if critic.calls.Load() < 1 {
		t.Error("critic was not called")
	}
	if replanner.called.Load() < 1 {
		t.Error("replanner was not called")
	}
}

// TestAcceptorRetryThenPass verifies the accept→retry→pass loop: the acceptor
// rejects the first attempt, the kernel retries, and the second attempt passes.
func TestAcceptorRetryThenPass(t *testing.T) {
	t.Parallel()

	acceptor := &retryAcceptor{rejectCount: 1} // reject first, pass second
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, acceptor)
	k.maxTaskRetries = 3 // allow retries
	defer k.Close()

	events, cancel := k.Subscribe("session-retry")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "session-retry", "test retry flow")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for workflow completion.
	timeout := time.After(5 * time.Second)
	var gotRetry, gotCheckpoint bool
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed unexpectedly")
			}
			if ev.Topic == "workflow.retry" {
				gotRetry = true
			}
			if ev.Topic == "checkpoint.updated" {
				gotCheckpoint = true
				goto done
			}
		case <-timeout:
			t.Fatalf("timed out: gotRetry=%v gotCheckpoint=%v", gotRetry, gotCheckpoint)
		}
	}
done:
	if !gotRetry {
		t.Error("expected at least one retry event")
	}
	if !gotCheckpoint {
		t.Error("expected checkpoint event")
	}

	// Verify the workflow completed successfully (acceptor approved on 2nd call).
	if acceptor.calls.Load() < 2 {
		t.Errorf("expected acceptor called at least 2 times, got %d", acceptor.calls.Load())
	}

	// Verify the turn summary shows the task passed.
	summary, ok := k.turnSummaries.Get(wfID)
	if !ok {
		t.Fatal("expected TurnSummary after workflow")
	}
	if len(summary.TaskResults) == 0 {
		t.Fatal("expected at least one task result")
	}
	if summary.TaskResults[0].Status != model.TaskStatusPassed {
		t.Errorf("expected task status 'passed', got %q", summary.TaskResults[0].Status)
	}
	if summary.TaskResults[0].Attempts < 2 {
		t.Errorf("expected at least 2 attempts, got %d", summary.TaskResults[0].Attempts)
	}
}

// TestAcceptorRejectExhaustsRetries verifies that when the acceptor always
// rejects, the workflow fails gracefully after exhausting retries.
func TestAcceptorRejectExhaustsRetries(t *testing.T) {
	t.Parallel()

	acceptor := &retryAcceptor{rejectCount: 100} // always reject
	executor := &countingExecutor{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, executor, acceptor)
	k.maxTaskRetries = 1 // allow 1 retry (2 total attempts)
	defer k.Close()

	events, cancel := k.Subscribe("session-exhaust")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-exhaust", "test retry exhaustion")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for workflow completion (it should fail).
	timeout := time.After(5 * time.Second)
	var retryCount int
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed unexpectedly")
			}
			if ev.Topic == "workflow.retry" {
				retryCount++
			}
			if ev.Topic == "checkpoint.updated" || ev.Topic == "workflow.failed" {
				goto done
			}
		case <-timeout:
			t.Fatalf("timed out: retryCount=%d", retryCount)
		}
	}
done:
	// Should have exactly 1 retry (attempt 0 fails → retry → attempt 1 fails → give up).
	if retryCount != 1 {
		t.Errorf("expected 1 retry event, got %d", retryCount)
	}
	// Acceptor should have been called exactly 2 times (initial + 1 retry).
	if acceptor.calls.Load() != 2 {
		t.Errorf("expected acceptor called 2 times, got %d", acceptor.calls.Load())
	}
}

// skipAcceptCoder returns a preExecuted CodePlan that includes a success_cmd.
// The kernel should skip accept for pre-executed mutations with success_cmd.
type skipAcceptCoder struct {
	receivedRetryFeedback string
}

func (c *skipAcceptCoder) Build(_ context.Context, wfID string, intent model.IntentSpec, tasks []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{{
			ToolCallID: "tool-" + wfID,
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    "out/" + intent.ID + ".go",
				Content: tasks[0].Title,
			},
		}},
	}, nil
}

func (c *skipAcceptCoder) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "info", Role: "coder", Content: "code produced"}}
}
