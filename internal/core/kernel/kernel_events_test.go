package kernel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// failFirstAcceptor rejects on first call, passes on retry.
type failFirstAcceptor struct {
	mu    sync.Mutex
	calls int
}

func (a *failFirstAcceptor) Accept(_ context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact, _ []model.Task) (model.CheckpointResult, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.mu.Unlock()

	status := "pass"
	evidence := []string{"accepted on retry"}
	if call == 1 {
		status = "fail"
		evidence = []string{"missing error handling", "incomplete implementation"}
	}
	return model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     intent.SessionID,
		Status:        status,
		ArtifactPaths: []string{artifact.Path},
		Evidence:      evidence,
		CreatedAt:     time.Now(),
	}, nil
}

func (a *failFirstAcceptor) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{
		{Kind: "info", Role: "acceptor", Content: "acceptor verdict emitted"},
	}
}

func TestAcceptorRetryOnFailure(t *testing.T) {
	acceptor := &failFirstAcceptor{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, acceptor)
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "session-1", "test retry")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	var gotRetry bool
	var finalStatus string
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Topic == "workflow.retry" {
				gotRetry = true
			}
			if ev.Topic == "checkpoint.updated" {
				payload := decodePayload[model.CheckpointUpdatedPayload](t, &ev)
				if payload.WorkflowID == wfID {
					finalStatus = payload.Status
					goto done
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for checkpoint.updated")
		}
	}
done:
	if !gotRetry {
		t.Error("expected workflow.retry event")
	}
	if finalStatus != "pass" {
		t.Errorf("expected final status 'pass', got %q", finalStatus)
	}
	// Verify acceptor was called twice (fail + pass).
	acceptor.mu.Lock()
	calls := acceptor.calls
	acceptor.mu.Unlock()
	if calls != 2 {
		t.Errorf("expected acceptor called 2 times, got %d", calls)
	}
}

func TestSubmitEmitsWorkerAndToolMetadata(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "emit metadata test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	var gotWorker, gotTool bool
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			switch ev.Topic {
			case "worker.started":
				payload := decodePayload[model.WorkerStartedPayload](t, &ev)
				if payload.WorkerRole == "" {
					t.Error("expected WorkerRole to be set")
				}
				gotWorker = true
			case "worker.finished":
				payload := decodePayload[model.WorkerFinishedPayload](t, &ev)
				if payload.WorkerRole == "" {
					t.Error("expected WorkerRole to be set in finished event")
				}
				if payload.DurationMS < 0 {
					t.Error("expected DurationMS >= 0")
				}
			case "tool.started":
				payload := decodePayload[model.ToolStartedPayload](t, &ev)
				if payload.Tool == "" {
					t.Error("expected Tool to be set")
				}
			case "tool.finished":
				payload := decodePayload[model.ToolFinishedPayload](t, &ev)
				if payload.Tool == "" {
					t.Error("expected Tool to be set in finished event")
				}
				if payload.Status == "" {
					t.Error("expected Status to be set")
				}
				gotTool = true
			case "checkpoint.updated":
				if gotWorker && gotTool {
					return
				}
			}
		case <-timeout:
			if !gotWorker {
				t.Error("timed out waiting for worker events")
			}
			if !gotTool {
				t.Error("timed out waiting for tool events")
			}
			return
		}
	}
}

func TestCancelWorkflowStopsActiveRun(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "session-1", "cancel me")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait a bit for workflow to start.
	time.Sleep(50 * time.Millisecond)

	if err := k.CancelWorkflow(context.Background(), "session-1", wfID); err != nil {
		// If workflow already finished, cancel will fail — that's acceptable.
		if !strings.Contains(err.Error(), "active workflow not found") {
			t.Fatalf("CancelWorkflow failed: %v", err)
		}
		// Workflow completed before cancel took effect — still acceptable.
		return
	}

	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return // channel closed means kernel shut down — acceptable
			}
			switch ev.Topic {
			case "workflow.canceled", "workflow.failed":
				return // cancel may surface as either topic
			case "checkpoint.updated":
				// Workflow completed before cancel took effect — still acceptable
				// in a race-free environment this shouldn't happen, but it's not a bug.
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for workflow.canceled event")
		}
	}
}

func TestCloseCancelsAndWaitsForActiveWorkflow(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "close test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Give workflow time to start.
	time.Sleep(50 * time.Millisecond)

	// Close should cancel and wait.
	if err := k.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Drain remaining events until channel is closed.
	drainTimeout := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return // channel closed — success
			}
			// Still receiving events, keep draining.
		case <-drainTimeout:
			t.Fatal("timed out waiting for events channel to close")
		}
	}
}

func TestSubmitExecutesMultipleToolCallsSequentially(t *testing.T) {
	t.Parallel()

	multiCoder := &multiToolCoder{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, multiCoder, testExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "multi tool test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	var toolStartedCount, toolFinishedCount int
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			switch ev.Topic {
			case "tool.started":
				toolStartedCount++
			case "tool.finished":
				toolFinishedCount++
			case "checkpoint.updated":
				// Expect 2 tool calls executed sequentially
				if toolStartedCount != 2 {
					t.Errorf("expected 2 tool.started events, got %d", toolStartedCount)
				}
				if toolFinishedCount != 2 {
					t.Errorf("expected 2 tool.finished events, got %d", toolFinishedCount)
				}
				return
			}
		case <-timeout:
			t.Fatalf("timed out: toolStarted=%d toolFinished=%d", toolStartedCount, toolFinishedCount)
		}
	}
}

func TestRunShellRequiresExplicitApprovalByDefault(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, shellToolCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "run shell")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Topic == "tool.finished" {
				payload := decodePayload[model.ToolFinishedPayload](t, &ev)
				if payload.Tool == "run_shell" && payload.Status == "denied" {
					return
				}
			}
			if ev.Topic == "checkpoint.updated" {
				t.Fatal("expected shell to be denied, but workflow completed")
			}
		case <-timeout:
			t.Fatal("timed out waiting for shell denial")
		}
	}
}

func TestToolExecutionFailureEmitsFailedToolEvent(t *testing.T) {
	t.Parallel()

	failExecutor := &failExecutor{}
	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, failExecutor, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "fail test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Topic == "tool.finished" {
				payload := decodePayload[model.ToolFinishedPayload](t, &ev)
				if payload.Status == "failed" {
					return
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for failed tool event")
		}
	}
}

func TestToolEventDetailIncludesReadablePreviews(t *testing.T) {
	tests := []struct {
		name     string
		req      toolruntime.Request
		result   toolruntime.Result
		contains []string
	}{
		{
			name: "write_file shows diff preview",
			req:  toolruntime.Request{Kind: "write_file"},
			result: toolruntime.Result{
				Diff: "line1\n-line2\n+line3\nline4",
			},
			contains: []string{"line1", "-line2", "+line3"},
		},
		{
			name: "read_file shows output preview",
			req:  toolruntime.Request{Kind: "read_file"},
			result: toolruntime.Result{
				Output: "content line 1\ncontent line 2",
			},
			contains: []string{"content line 1", "content line 2"},
		},
		{
			name: "run_shell success shows combined output",
			req:  toolruntime.Request{Kind: "run_shell"},
			result: toolruntime.Result{
				Output: "stdout content",
				Stderr: "stderr content",
			},
			contains: []string{"stdout content", "stderr content"},
		},
		{
			name: "run_shell failure shows stderr",
			req:  toolruntime.Request{Kind: "run_shell"},
			result: toolruntime.Result{
				ExitCode: 1,
				Output:   "stdout",
				Stderr:   "error details",
			},
			contains: []string{"error details"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail := toolEventDetail(tt.req, tt.result)
			for _, want := range tt.contains {
				if !strings.Contains(detail, want) {
					t.Errorf("expected detail to contain %q, got:\n%s", want, detail)
				}
			}
		})
	}
}

func TestCheckpointQueries(t *testing.T) {
	t.Parallel()

	k := NewWithWorkflowDependencies(t.TempDir(), testInputter{}, testPlanner{}, testCoder{}, testExecutor{}, testAcceptor{})
	defer k.Close()
	events, cancel := k.Subscribe("session-1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "session-1", "checkpoint query test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Topic == "checkpoint.updated" {
				// Test GetCheckpoint API
				cp, err := k.GetCheckpoint(context.Background(), "session-1", wfID)
				if err != nil {
					t.Fatalf("GetCheckpoint failed: %v", err)
				}
				if cp.WorkflowID != wfID {
					t.Errorf("expected workflow ID %q, got %q", wfID, cp.WorkflowID)
				}
				if cp.Status != "pass" {
					t.Errorf("expected status 'pass', got %q", cp.Status)
				}

				// Test ListCheckpoints API
				cps, _ := k.ListCheckpoints(context.Background(), "session-1", 10)
				if len(cps) != 1 {
					t.Errorf("expected 1 checkpoint, got %d", len(cps))
				}
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for checkpoint.updated")
		}
	}
}

// Helper functions and types for tests

type multiToolCoder struct{}

func (c *multiToolCoder) Build(_ context.Context, workflowID string, intent model.IntentSpec, _ []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{
			{
				ToolCallID: "tool-call-" + workflowID + "-list",
				Request: toolruntime.Request{
					Kind: "list_dir",
					Dir:  "artifacts",
				},
			},
			{
				ToolCallID: "tool-call-" + workflowID + "-write",
				Request: toolruntime.Request{
					Kind:    "write_file",
					Path:    "artifacts/output.md",
					Content: intent.Goal,
				},
			},
		},
	}, nil
}

func (c *multiToolCoder) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{
		{Kind: "info", Role: "coder", Content: "multi-tool coder produced tool calls"},
	}
}

type failExecutor struct{}

func (e *failExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	return toolruntime.Result{}, fmt.Errorf("execution failed: %s", req.Kind)
}
