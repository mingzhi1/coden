package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

type stubInputter struct{}

func (stubInputter) Build(_ context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	return model.IntentSpec{
		ID:        "intent-1",
		SessionID: sessionID,
		Goal:      prompt,
		CreatedAt: time.Now(),
	}, nil
}

func (stubInputter) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "info", Role: "input", Content: "normalized"}}
}

func (stubInputter) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "stub-input", Role: RoleInput}
}

type stubPlanner struct{}

func (stubPlanner) Plan(_ context.Context, _ string, _ model.IntentSpec) ([]model.Task, error) {
	return []model.Task{{ID: "task-1", Title: "plan", Status: "planned", Created: time.Now()}}, nil
}

func (stubPlanner) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "info", Role: "planner", Content: "planned"}}
}

func (stubPlanner) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "stub-planner", Role: RolePlanner}
}

type stubCoder struct{}

func (stubCoder) Build(_ context.Context, workflowID string, _ model.IntentSpec, _ []model.Task) (CodePlan, error) {
	return CodePlan{
		ToolCalls:  []ToolCall{{ToolCallID: "tool-" + workflowID}},
		ToolCallID: "tool-" + workflowID,
	}, nil
}

func (stubCoder) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "info", Role: "coder", Content: "coded"}}
}

func (stubCoder) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "stub-coder", Role: RoleCoder}
}

type stubAcceptor struct{}

func (stubAcceptor) Accept(_ context.Context, workflowID string, intent model.IntentSpec, _ model.Artifact, _ []model.Task) (model.CheckpointResult, error) {
	return model.CheckpointResult{WorkflowID: workflowID, SessionID: intent.SessionID, Status: "pass", CreatedAt: time.Now()}, nil
}

func (stubAcceptor) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "info", Role: "acceptor", Content: "accepted"}}
}

func (stubAcceptor) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "stub-acceptor", Role: RoleAcceptor}
}

func TestWorkersAdaptLegacyInterfacesToUnifiedContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	inputResult, err := NewInputterWorker(stubInputter{}).Execute(ctx, WorkerInput{
		SessionID: "session-1",
		Prompt:    "build feature",
	})
	if err != nil {
		t.Fatalf("input worker failed: %v", err)
	}
	if inputResult.Intent == nil || inputResult.Intent.Goal != "build feature" {
		t.Fatalf("unexpected input result: %+v", inputResult)
	}
	if inputResult.Metadata.Role != RoleInput || inputResult.Metadata.Worker != "stub-input" {
		t.Fatalf("unexpected input metadata: %+v", inputResult.Metadata)
	}
	if len(inputResult.Messages) != 1 || inputResult.Messages[0].Content != "normalized" {
		t.Fatalf("unexpected input messages: %+v", inputResult.Messages)
	}

	intent := *inputResult.Intent
	planResult, err := NewPlannerWorker(stubPlanner{}).Execute(ctx, WorkerInput{
		SessionID:  intent.SessionID,
		WorkflowID: "wf-1",
		Intent:     intent,
	})
	if err != nil {
		t.Fatalf("planner worker failed: %v", err)
	}
	if len(planResult.Tasks) != 1 || planResult.Tasks[0].ID != "task-1" {
		t.Fatalf("unexpected planner result: %+v", planResult)
	}
	if planResult.Metadata.Role != RolePlanner || planResult.Metadata.Worker != "stub-planner" {
		t.Fatalf("unexpected planner metadata: %+v", planResult.Metadata)
	}

	codeResult, err := NewCoderWorker(stubCoder{}).Execute(ctx, WorkerInput{
		SessionID:  intent.SessionID,
		WorkflowID: "wf-1",
		Intent:     intent,
		Tasks:      planResult.Tasks,
	})
	if err != nil {
		t.Fatalf("coder worker failed: %v", err)
	}
	if codeResult.CodePlan == nil || codeResult.CodePlan.ToolCallID != "tool-wf-1" {
		t.Fatalf("unexpected coder result: %+v", codeResult)
	}
	if calls := codeResult.CodePlan.Calls(); len(calls) != 1 || calls[0].ToolCallID != "tool-wf-1" {
		t.Fatalf("unexpected coder calls: %+v", calls)
	}
	if codeResult.Metadata.Role != RoleCoder || codeResult.Metadata.Worker != "stub-coder" {
		t.Fatalf("unexpected coder metadata: %+v", codeResult.Metadata)
	}

	acceptResult, err := NewAcceptorWorker(stubAcceptor{}).Execute(ctx, WorkerInput{
		SessionID:  intent.SessionID,
		WorkflowID: "wf-1",
		Intent:     intent,
		Artifact:   model.Artifact{Path: "artifact.md"},
	})
	if err != nil {
		t.Fatalf("acceptor worker failed: %v", err)
	}
	if acceptResult.Checkpoint == nil || acceptResult.Checkpoint.Status != "pass" {
		t.Fatalf("unexpected acceptor result: %+v", acceptResult)
	}
	if acceptResult.Metadata.Role != RoleAcceptor || acceptResult.Metadata.Worker != "stub-acceptor" {
		t.Fatalf("unexpected acceptor metadata: %+v", acceptResult.Metadata)
	}
}
