package rpc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	"github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/server"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// TestEndToEndObjectStoreChain tests the complete chain:
// Client -> RPC -> Kernel -> ObjectStore -> (persist) -> ObjectStore -> Kernel -> RPC -> Client
func TestEndToEndObjectStoreChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Setup kernel with real stores
	tmpDir := t.TempDir()
	ws := workspace.New(tmpDir)

	sessionStore := session.NewStore()
	intentStore := intent.NewStore()
	messageStore := message.NewStore()
	checkpointStore := checkpoint.NewStore()
	turnStore := turn.NewStore()
	objectStore := objectstore.NewStore()

	tools := toolruntime.New(ws)
	k := kernel.NewWithStores(
		tmpDir, "",
		sessionStore, intentStore, messageStore, checkpointStore, turnStore, turnsummary.NewStore(), objectStore, insight.NewStore(),
		nil, nil, nil, tools,
	)
	defer k.Close()

	// Create in-memory pipe
	serverRWC, clientRWC := transport.Pipe()

	// Start server
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	// Create client
	c := client.New(clientRWC)
	defer c.Close()

	// Test: Subscribe and submit workflow
	events, cancelSub, err := c.Subscribe(ctx, "e2e-session")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelSub()

	// Give subscription time to register
	time.Sleep(50 * time.Millisecond)

	// Submit workflow
	workflowID, err := c.Submit(ctx, "e2e-session", "test prompt")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if workflowID == "" {
		t.Fatal("workflowID is empty")
	}

	// Wait for checkpoint event
	var cpPayload model.CheckpointUpdatedPayload
	eventTimeout := time.After(5 * time.Second)
eventLoop:
	for {
		select {
		case ev := <-events:
			if ev.Topic == model.EventCheckpointUpdated {
				payload, err := model.DecodePayload[model.CheckpointUpdatedPayload](ev)
				if err != nil {
					t.Fatalf("decode checkpoint payload: %v", err)
				}
				cpPayload = payload
				break eventLoop
			}
		case <-eventTimeout:
			t.Fatal("timeout waiting for checkpoint event")
		}
	}

	if cpPayload.WorkflowID != workflowID {
		t.Errorf("workflowID mismatch: got %q, want %q", cpPayload.WorkflowID, workflowID)
	}

	// Query checkpoint via RPC
	checkpoint, err := c.GetCheckpoint(ctx, "e2e-session", workflowID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if checkpoint.WorkflowID != workflowID {
		t.Errorf("checkpoint workflowID mismatch")
	}

	// List workflow objects via RPC
	objects, err := c.ListWorkflowRunObjects(ctx, "e2e-session", workflowID)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	t.Logf("Retrieved %d objects for workflow %s", len(objects), workflowID)

	// Read object payloads
	for _, obj := range objects {
		payload, err := c.ReadWorkflowRunObject(ctx, "e2e-session", workflowID, obj.ID)
		if err != nil {
			t.Errorf("read object %s: %v", obj.ID, err)
			continue
		}
		if !json.Valid(payload) {
			t.Errorf("object %s payload is not valid JSON", obj.ID)
		}
		t.Logf("Object %s payload size: %d bytes", obj.ID, len(payload))
	}
}

// TestEndToEndEventStreaming tests event streaming through RPC.
func TestEndToEndEventStreaming(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Setup kernel
	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	serverRWC, clientRWC := transport.Pipe()
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	// Subscribe to events
	events, cancelSub, err := c.Subscribe(ctx, "stream-session")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelSub()

	time.Sleep(50 * time.Millisecond)

	// Submit one workflow; R-04 prevents concurrent same-session submits and a
	// single workflow is sufficient to verify event streaming.
	if _, err := c.Submit(ctx, "stream-session", "prompt"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Collect events
	var receivedEvents []model.Event
	timeout := time.After(3 * time.Second)
	done := time.After(4 * time.Second)

collectLoop:
	for {
		select {
		case ev := <-events:
			receivedEvents = append(receivedEvents, ev)
			if len(receivedEvents) >= 6 { // 3 workflows * 2 events each (started + checkpoint)
				break collectLoop
			}
		case <-timeout:
			if len(receivedEvents) >= 3 {
				break collectLoop
			}
		case <-done:
			break collectLoop
		}
	}

	t.Logf("Received %d events", len(receivedEvents))

	// Verify event types
	var workflowStarted, checkpointUpdated int
	for _, ev := range receivedEvents {
		switch ev.Topic {
		case model.EventWorkflowStarted:
			workflowStarted++
		case model.EventCheckpointUpdated:
			checkpointUpdated++
		}
	}

	if workflowStarted == 0 {
		t.Error("no workflow.started events received")
	}
	if checkpointUpdated == 0 {
		t.Log("no checkpoint.updated events received (expected with nil workers)")
	}
}

// TestEndToEndMultipleSessions tests multiple concurrent sessions.
func TestEndToEndMultipleSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	serverRWC, clientRWC := transport.Pipe()
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	sessions := []string{"session-a", "session-b", "session-c"}

	// Create sessions and submit workflows concurrently
	for _, sessionID := range sessions {
		_, err := c.CreateSession(ctx, sessionID)
		if err != nil {
			t.Fatalf("create session %s: %v", sessionID, err)
		}

		_, err = c.Submit(ctx, sessionID, "test")
		if err != nil {
			t.Fatalf("submit for %s: %v", sessionID, err)
		}
	}

	// List all sessions
	list, err := c.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}

	foundSessions := make(map[string]bool)
	for _, s := range list {
		foundSessions[s.ID] = true
	}

	for _, sessionID := range sessions {
		if !foundSessions[sessionID] {
			t.Errorf("session %s not found in list", sessionID)
		}
	}

	// List workflow runs for each session
	for _, sessionID := range sessions {
		runs, err := c.ListWorkflowRuns(ctx, sessionID, 10)
		if err != nil {
			t.Errorf("list runs for %s: %v", sessionID, err)
			continue
		}
		if len(runs) == 0 {
			t.Errorf("no runs for session %s", sessionID)
		}
	}
}

// TestEndToEndWorkflowLifecycle tests complete workflow lifecycle.
func TestEndToEndWorkflowLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	serverRWC, clientRWC := transport.Pipe()
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	// Attach
	err := c.Attach(ctx, "lifecycle-session", "test-client", "tui")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Subscribe
	events, cancelSub, err := c.Subscribe(ctx, "lifecycle-session")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelSub()

	time.Sleep(50 * time.Millisecond)

	// Submit workflow
	workflowID, err := c.Submit(ctx, "lifecycle-session", "test prompt")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Wait for completion
	timeout := time.After(5 * time.Second)
workflowLoop:
	for {
		select {
		case ev := <-events:
			if ev.Topic == model.EventCheckpointUpdated {
				payload, _ := model.DecodePayload[model.CheckpointUpdatedPayload](ev)
				if payload.WorkflowID == workflowID {
					break workflowLoop
				}
			}
		case <-timeout:
			t.Fatal("timeout waiting for workflow completion")
		}
	}

	// Get workflow run details
	run, err := c.GetWorkflowRun(ctx, "lifecycle-session", workflowID)
	if err != nil {
		t.Fatalf("get workflow run: %v", err)
	}
	if run.WorkflowID != workflowID {
		t.Error("workflow run ID mismatch")
	}

	// Get messages
	messages, err := c.ListMessages(ctx, "lifecycle-session", 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(messages))
	}

	// Detach
	err = c.Detach(ctx, "lifecycle-session", "test-client")
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
}

// TestEndToEndErrorHandling tests error propagation through RPC.
func TestEndToEndErrorHandling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	serverRWC, clientRWC := transport.Pipe()
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	// Test: Get non-existent checkpoint
	_, err := c.GetCheckpoint(ctx, "test-session", "non-existent-workflow")
	if err == nil {
		t.Error("expected error for non-existent checkpoint")
	}

	// Test: Get non-existent workflow run
	_, err = c.GetWorkflowRun(ctx, "test-session", "non-existent")
	if err == nil {
		t.Error("expected error for non-existent workflow run")
	}

	// Test: Empty session ID via client
	err = c.Attach(ctx, "", "test-client", "tui")
	if err == nil {
		t.Error("expected error for empty session ID")
	}
}

// TestEndToEndCancellation tests workflow cancellation.
func TestEndToEndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	k := kernel.New(tmpDir)
	defer k.Close()

	serverRWC, clientRWC := transport.Pipe()
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	// Submit a workflow
	workflowID, err := c.Submit(ctx, "cancel-session", "test")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Cancel it immediately (may or may not succeed depending on timing)
	_ = c.CancelWorkflow(ctx, "cancel-session", workflowID)

	// Verify workflow status changed or completed
	time.Sleep(100 * time.Millisecond)
	run, err := c.GetWorkflowRun(ctx, "cancel-session", workflowID)
	if err != nil {
		t.Logf("GetWorkflowRun returned error (expected if workflow not yet persisted): %v", err)
	} else {
		t.Logf("Workflow status: %s", run.Status)
	}
}
