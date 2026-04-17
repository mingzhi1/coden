package input

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func TestRPCInputWorkerDescribeAndBuild(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(nil)
	go srv.ServeConn(ctx, serverRWC)

	inputter := NewRPCInputter(clientRWC)
	defer inputter.Close()

	describe, err := inputter.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}
	if describe.Role != "input" {
		t.Fatalf("expected input role, got %q", describe.Role)
	}

	intent, err := inputter.Build(ctx, "session-1", "normalize this prompt")
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if intent.SessionID != "session-1" {
		t.Fatalf("unexpected session id: %q", intent.SessionID)
	}
	if intent.Goal != "normalize this prompt" {
		t.Fatalf("unexpected goal: %q", intent.Goal)
	}
	if len(intent.SuccessCriteria) == 0 {
		t.Fatal("expected success criteria")
	}
}
