package kernel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// testPlanner returns a simple task plan.
type testPlanner struct{}

func (p testPlanner) Plan(_ context.Context, _ string, _ model.IntentSpec) ([]model.Task, error) {
	return []model.Task{
		{ID: "task-1", Title: "plan output", Status: "planned", Created: time.Now()},
	}, nil
}

// testInputter returns a simple intent.
type testInputter struct{}

func (i testInputter) Build(_ context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	return model.IntentSpec{
		ID:        "intent-test",
		SessionID: sessionID,
		Goal:      prompt,
		CreatedAt: time.Now(),
	}, nil
}

func (i testInputter) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{
		{Kind: "info", Role: "input", Content: "input normalized prompt"},
	}
}

// testCoder returns a simple write_file tool call.
type testCoder struct{}

func (c testCoder) Build(_ context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{{
			ToolCallID: "tool-call-" + workflowID,
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    "artifacts/" + intent.ID + ".md",
				Content: tasks[0].Title,
			},
		}},
		ToolCallID: "tool-call-" + workflowID,
		Request: toolruntime.Request{
			Kind:    "write_file",
			Path:    "artifacts/" + intent.ID + ".md",
			Content: tasks[0].Title,
		},
	}, nil
}

func (c testCoder) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{
		{Kind: "info", Role: "coder", Content: "coder produced tool call"},
	}
}

// testExecutor simulates successful tool execution.
type testExecutor struct{}

func (e testExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	return toolruntime.Result{
		ArtifactPath: req.Path,
		Summary:      "executed " + req.Kind,
	}, nil
}

// testAcceptor always passes.
type testAcceptor struct{}

func (a testAcceptor) Accept(_ context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact, _ []model.Task) (model.CheckpointResult, error) {
	return model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     intent.SessionID,
		Status:        "pass",
		ArtifactPaths: []string{artifact.Path},
		Evidence:      []string{"all requirements met"},
		CreatedAt:     time.Now(),
	}, nil
}

func (a testAcceptor) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{
		{Kind: "info", Role: "acceptor", Content: "acceptor verdict emitted"},
	}
}

// decodePayload helper decodes event payload.
func decodePayload[T any](t *testing.T, ev *model.Event) T {
	t.Helper()
	var payload T
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	return payload
}
