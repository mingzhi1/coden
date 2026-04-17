package accept

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func TestRPCAcceptorDescribeAndAccept(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalAcceptor())
	go srv.ServeConn(ctx, serverRWC)

	acceptor := NewRPCAcceptor(clientRWC)
	defer acceptor.Close()

	describe, err := acceptor.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}
	if describe.Role != "acceptor" {
		t.Fatalf("expected acceptor role, got %q", describe.Role)
	}

	result, err := acceptor.Accept(ctx, "wf-1", model.IntentSpec{
		ID:        "intent-1",
		SessionID: "session-1",
		Goal:      "verify the artifact",
		CreatedAt: time.Now(),
	}, model.Artifact{
		Path:    "artifacts/intent-1.md",
		Summary: "artifact created",
	})
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}

	if result.Status != "pass" {
		t.Fatalf("expected pass, got %q", result.Status)
	}
	if len(result.ArtifactPaths) != 1 || result.ArtifactPaths[0] != "artifacts/intent-1.md" {
		t.Fatalf("unexpected artifact paths: %+v", result.ArtifactPaths)
	}
	if len(result.Evidence) == 0 {
		t.Fatal("expected acceptance evidence")
	}
}
