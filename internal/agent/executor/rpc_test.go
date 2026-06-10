package executor

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func TestRPCCodeWorkerDescribeAndBuild(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalExecutor())
	go srv.ServeConn(ctx, serverRWC)

	executor := NewRPCExecutor(clientRWC)
	defer executor.Close()

	describe, err := executor.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}
	if describe.Role != "executor" {
		t.Fatalf("expected executor role, got %q", describe.Role)
	}

	plan, err := executor.Build(ctx, "wf-1", model.IntentSpec{
		ID:        "intent-1",
		SessionID: "session-1",
		Goal:      "generate an artifact",
		CreatedAt: time.Now(),
	}, []model.Task{
		{ID: "task-1", Title: "write the artifact", Status: "planned", Created: time.Now()},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if plan.ToolCallID == "" {
		t.Fatal("expected tool call id")
	}
	if plan.Request.Kind != "write_file" {
		t.Fatalf("expected write_file kind, got %q", plan.Request.Kind)
	}
	if plan.Request.Path == "" || plan.Request.Content == "" {
		t.Fatalf("expected non-empty request: %+v", plan.Request)
	}
}

type multiCallExecutor struct{}

func (multiCallExecutor) Build(_ context.Context, workflowID string, _ model.IntentSpec, _ []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{
			{
				ToolCallID: "tool-" + workflowID + "-1",
				Request: toolruntime.Request{
					Kind: "read_file",
					Path: "README.md",
				},
			},
			{
				ToolCallID: "tool-" + workflowID + "-2",
				Request: toolruntime.Request{
					Kind:    "write_file",
					Path:    "artifacts/out.md",
					Content: "hello",
				},
			},
		},
	}, nil
}

func TestRPCCodeWorkerPreservesMultipleToolCalls(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(multiCallExecutor{})
	go srv.ServeConn(ctx, serverRWC)

	executor := NewRPCExecutor(clientRWC)
	defer executor.Close()

	plan, err := executor.Build(ctx, "wf-1", model.IntentSpec{
		ID:        "intent-1",
		SessionID: "session-1",
		Goal:      "generate multiple files",
		CreatedAt: time.Now(),
	}, []model.Task{
		{ID: "task-1", Title: "inspect workspace", Status: "planned", Created: time.Now()},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	calls := plan.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %+v", calls)
	}
	if calls[0].Request.Kind != "read_file" || calls[1].Request.Kind != "write_file" {
		t.Fatalf("unexpected tool calls: %+v", calls)
	}
}
