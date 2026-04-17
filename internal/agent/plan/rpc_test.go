package plan

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func TestRPCPlannerDescribeAndPlan(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalPlanner())
	go srv.ServeConn(ctx, serverRWC)

	planner := NewRPCPlanner(clientRWC)
	defer planner.Close()

	describe, err := planner.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}
	if describe.Role != "planner" {
		t.Fatalf("expected planner role, got %q", describe.Role)
	}

	tasks, err := planner.Plan(ctx, "wf-1", model.IntentSpec{
		ID:        "intent-1",
		SessionID: "session-1",
		Goal:      "generate an artifact",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].Status != "planned" {
		t.Fatalf("expected planned status, got %q", tasks[0].Status)
	}
	if tasks[0].Title == "" || tasks[1].Title == "" {
		t.Fatalf("expected non-empty task titles: %+v", tasks)
	}
}

func TestRPCPlannerCancelReportsNotSupported(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalPlanner())
	go srv.ServeConn(ctx, serverRWC)

	codec := transport.NewCodec(clientRWC)
	defer codec.Close()

	req, err := protocol.NewRequest(1, protocol.MethodWorkerCancel, protocol.CancelParams{
		SessionID:  "session-1",
		WorkflowID: "wf-1",
		TaskID:     "task-1",
	})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if err := codec.WriteMessage(req); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	raw, err := codec.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}

	var ack protocol.AckResult
	if err := json.Unmarshal(resp.Result, &ack); err != nil {
		t.Fatalf("unmarshal ack failed: %v", err)
	}
	if ack.Status != "not_supported" {
		t.Fatalf("unexpected ack: %+v", ack)
	}
}
