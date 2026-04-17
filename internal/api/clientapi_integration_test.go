package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/checkpoint"
	"github.com/mingzhi1/coden/internal/core/insight"
	"github.com/mingzhi1/coden/internal/core/intent"
	"github.com/mingzhi1/coden/internal/core/kernel"
	"github.com/mingzhi1/coden/internal/core/message"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/objectstore"
	"github.com/mingzhi1/coden/internal/core/session"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/turn"
	"github.com/mingzhi1/coden/internal/core/turnsummary"
	"github.com/mingzhi1/coden/internal/core/workspace"
)

// mockWorker is a test double for workflow workers.
type mockWorker struct {
	role    string
	execute func(ctx context.Context, input any) (any, error)
}

// TestServiceObjectStoreChain tests the full chain: API -> Kernel -> ObjectStore.
func TestServiceObjectStoreChain(t *testing.T) {
	ctx := context.Background()

	// Setup workspace
	tmpDir := t.TempDir()
	ws := workspace.New(tmpDir)

	// Setup stores
	sessionStore := session.NewStore()
	intentStore := intent.NewStore()
	messageStore := message.NewStore()
	checkpointStore := checkpoint.NewStore()
	turnStore := turn.NewStore()
	objectStore := objectstore.NewStore()

	// Setup kernel with mock workers
	tools := toolruntime.New(ws)
	k := kernel.NewWithStores(
		tmpDir, "",
		sessionStore, intentStore, messageStore, checkpointStore, turnStore, turnsummary.NewStore(), objectStore, insight.NewStore(),
		nil, nil, nil, tools,
	)
	defer k.Close()

	// Create service (API layer)
	svc := api.New(k)

	// Test: Submit workflow that produces objects
	t.Run("workflow produces objects", func(t *testing.T) {
		// Subscribe to events
		events, cancel, err := svc.Subscribe(ctx, "test-session")
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		defer cancel()

		// Submit workflow
		workflowID, err := svc.Submit(ctx, "test-session", "test prompt")
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		if workflowID == "" {
			t.Error("workflowID is empty")
		}

		// Wait for checkpoint event
		var checkpointEvent *model.CheckpointUpdatedPayload
		timeout := time.After(5 * time.Second)
	waitLoop:
		for {
			select {
			case ev := <-events:
				if ev.Topic == model.EventCheckpointUpdated {
					payload, _ := model.DecodePayload[model.CheckpointUpdatedPayload](ev)
					checkpointEvent = &payload
					break waitLoop
				}
			case <-timeout:
				t.Fatal("timeout waiting for checkpoint event")
			}
		}

		if checkpointEvent == nil {
			t.Fatal("no checkpoint event received")
		}
		if checkpointEvent.WorkflowID != workflowID {
			t.Errorf("workflowID mismatch: got %q, want %q", checkpointEvent.WorkflowID, workflowID)
		}
	})

	// Test: List workflow objects
	t.Run("list workflow objects", func(t *testing.T) {
		// Get workflows
		runs, err := svc.ListWorkflowRuns(ctx, "test-session", 10)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) == 0 {
			t.Fatal("no workflow runs found")
		}

		workflowID := runs[0].WorkflowID

		// List objects
		objects, err := svc.ListWorkflowRunObjects(ctx, "test-session", workflowID)
		if err != nil {
			t.Fatalf("list objects: %v", err)
		}
		t.Logf("Found %d objects for workflow %s", len(objects), workflowID)
	})

	// Test: Read workflow object
	t.Run("read workflow object", func(t *testing.T) {
		runs, _ := svc.ListWorkflowRuns(ctx, "test-session", 1)
		if len(runs) == 0 {
			t.Skip("no runs available")
		}

		workflowID := runs[0].WorkflowID
		objects, _ := svc.ListWorkflowRunObjects(ctx, "test-session", workflowID)
		if len(objects) == 0 {
			t.Skip("no objects available")
		}

		objectID := objects[0].ID
		payload, err := svc.ReadWorkflowRunObject(ctx, "test-session", workflowID, objectID)
		if err != nil {
			t.Fatalf("read object: %v", err)
		}
		if !json.Valid(payload) {
			t.Error("payload is not valid JSON")
		}
	})
}

// TestServiceSessionManagement tests session lifecycle.
func TestServiceSessionManagement(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	svc := api.New(k)

	t.Run("create and list sessions", func(t *testing.T) {
		// Create sessions
		_, err := svc.CreateSession(ctx, "session-1")
		if err != nil {
			t.Fatalf("create session-1: %v", err)
		}
		_, err = svc.CreateSession(ctx, "session-2")
		if err != nil {
			t.Fatalf("create session-2: %v", err)
		}

		// List sessions
		sessions, err := svc.ListSessions(ctx, 10)
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		if len(sessions) < 2 {
			t.Errorf("expected at least 2 sessions, got %d", len(sessions))
		}
	})

	t.Run("attach and detach", func(t *testing.T) {
		err := svc.Attach(ctx, "session-1", "test-client", "tui")
		if err != nil {
			t.Fatalf("attach: %v", err)
		}

		err = svc.Detach(ctx, "session-1", "test-client")
		if err != nil {
			t.Fatalf("detach: %v", err)
		}
	})
}

// TestServiceCheckpointOperations tests checkpoint queries.
func TestServiceCheckpointOperations(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	svc := api.New(k)

	// Create sessions and run one workflow per session.
	// R-04: concurrent submit to the same session fast-fails, so each workflow
	// gets its own session to avoid any ordering dependency.
	for i := 0; i < 3; i++ {
		sessID := fmt.Sprintf("cp-session-%d", i)
		svc.CreateSession(ctx, sessID)
		_, err := svc.Submit(ctx, sessID, "prompt")
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	t.Run("list checkpoints", func(t *testing.T) {
		// Wait for workflows to complete
		time.Sleep(100 * time.Millisecond)

		checkpoints, err := svc.ListCheckpoints(ctx, "cp-session", 10)
		if err != nil {
			t.Fatalf("list checkpoints: %v", err)
		}
		t.Logf("Found %d checkpoints", len(checkpoints))
	})

	t.Run("get specific checkpoint", func(t *testing.T) {
		checkpoints, _ := svc.ListCheckpoints(ctx, "cp-session", 1)
		if len(checkpoints) == 0 {
			t.Skip("no checkpoints available")
		}

		workflowID := checkpoints[0].WorkflowID
		cp, err := svc.GetCheckpoint(ctx, "cp-session", workflowID)
		if err != nil {
			t.Fatalf("get checkpoint: %v", err)
		}
		if cp.WorkflowID != workflowID {
			t.Errorf("workflowID mismatch")
		}
	})
}

// TestServiceWorkspaceOperations tests workspace change tracking.
func TestServiceWorkspaceOperations(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	svc := api.New(k)
	svc.CreateSession(ctx, "ws-session")

	// Note: Actual workspace changes require workflow execution
	// This test verifies the API layer
	t.Run("workspace changes API", func(t *testing.T) {
		changes, err := svc.WorkspaceChanges(ctx, "ws-session")
		if err != nil {
			t.Fatalf("workspace changes: %v", err)
		}
		// Initially empty
		t.Logf("Workspace changes: %d", len(changes))
	})
}

// TestServiceMessageOperations tests message listing.
func TestServiceMessageOperations(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	svc := api.New(k)
	svc.CreateSession(ctx, "msg-session")

	t.Run("list messages", func(t *testing.T) {
		messages, err := svc.ListMessages(ctx, "msg-session", 10)
		if err != nil {
			t.Fatalf("list messages: %v", err)
		}
		// Initially empty
		t.Logf("Messages: %d", len(messages))
	})
}
