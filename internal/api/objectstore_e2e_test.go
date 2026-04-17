package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// TestEndToEndObjectStoreChain tests the complete chain:
// API -> Kernel -> ObjectStore -> Kernel -> API
func TestEndToEndObjectStoreChain(t *testing.T) {
	ctx := context.Background()

	// Setup test environment
	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspace")
	_ = os.MkdirAll(workspaceRoot, 0755)

	ws := workspace.New(workspaceRoot)

	// Setup stores
	sessionStore := session.NewStore()
	intentStore := intent.NewStore()
	messageStore := message.NewStore()
	checkpointStore := checkpoint.NewStore()
	turnStore := turn.NewStore()
	objectStore := objectstore.NewStore()

	// Setup kernel with real tool runtime
	tools := toolruntime.New(ws)
	k := kernel.NewWithStores(
		workspaceRoot, "",
		sessionStore, intentStore, messageStore, checkpointStore, turnStore, turnsummary.NewStore(), objectStore, insight.NewStore(),
		nil, nil, nil, tools,
	)
	defer k.Close()

	// Create API service
	svc := api.New(k)

	// Create session
	sessionID := "e2e-test-session"
	_, err := svc.CreateSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Subscribe to events
	events, cancel, err := svc.Subscribe(ctx, sessionID)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	// Test 1: Submit workflow and wait for completion
	t.Run("workflow produces objects", func(t *testing.T) {
		workflowID, err := svc.Submit(ctx, sessionID, "test prompt")
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		if workflowID == "" {
			t.Fatal("workflowID is empty")
		}

		// Wait for checkpoint event
		var checkpointPayload *model.CheckpointUpdatedPayload
		timeout := time.After(5 * time.Second)

	waitLoop:
		for {
			select {
			case ev := <-events:
				if ev.Topic == model.EventCheckpointUpdated {
					payload, _ := model.DecodePayload[model.CheckpointUpdatedPayload](ev)
					if payload.WorkflowID == workflowID {
						checkpointPayload = &payload
						break waitLoop
					}
				}
			case <-timeout:
				t.Fatal("timeout waiting for checkpoint event")
			}
		}

		if checkpointPayload == nil {
			t.Fatal("no checkpoint payload received")
		}
		t.Logf("Checkpoint status: %s", checkpointPayload.Status)
	})

	// Test 2: List workflow runs
	t.Run("list workflow runs", func(t *testing.T) {
		runs, err := svc.ListWorkflowRuns(ctx, sessionID, 10)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) == 0 {
			t.Fatal("no workflow runs found")
		}
		t.Logf("Found %d workflow runs", len(runs))
	})

	// Test 3: Get specific workflow run
	t.Run("get workflow run", func(t *testing.T) {
		runs, _ := svc.ListWorkflowRuns(ctx, sessionID, 1)
		if len(runs) == 0 {
			t.Skip("no runs to get")
		}

		workflowID := runs[0].WorkflowID
		run, err := svc.GetWorkflowRun(ctx, sessionID, workflowID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.WorkflowID != workflowID {
			t.Errorf("workflowID mismatch: got %s, want %s", run.WorkflowID, workflowID)
		}
	})

	// Test 4: List objects for workflow
	t.Run("list workflow objects", func(t *testing.T) {
		runs, _ := svc.ListWorkflowRuns(ctx, sessionID, 1)
		if len(runs) == 0 {
			t.Skip("no runs to query objects")
		}

		workflowID := runs[0].WorkflowID
		objects, err := svc.ListWorkflowRunObjects(ctx, sessionID, workflowID)
		if err != nil {
			t.Fatalf("list objects: %v", err)
		}

		t.Logf("Found %d objects for workflow %s", len(objects), workflowID)
		for i, obj := range objects {
			t.Logf("Object %d: ID=%s, Path=%s, Kind=%s", i, obj.ID, obj.FilePath, obj.Kind)
		}
	})

	// Test 5: Read object payload
	t.Run("read object payload", func(t *testing.T) {
		runs, _ := svc.ListWorkflowRuns(ctx, sessionID, 1)
		if len(runs) == 0 {
			t.Skip("no runs to query objects")
		}

		workflowID := runs[0].WorkflowID
		objects, _ := svc.ListWorkflowRunObjects(ctx, sessionID, workflowID)
		if len(objects) == 0 {
			t.Skip("no objects to read")
		}

		objectID := objects[0].ID
		payload, err := svc.ReadWorkflowRunObject(ctx, sessionID, workflowID, objectID)
		if err != nil {
			t.Fatalf("read object: %v", err)
		}

		if !json.Valid(payload) {
			t.Errorf("payload is not valid JSON: %s", string(payload))
		}

		// Verify payload structure
		var data map[string]any
		if err := json.Unmarshal(payload, &data); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}

		// Check for expected fields
		if _, ok := data["schema"]; !ok {
			t.Log("Warning: payload missing 'schema' field")
		}
	})

	// Test 6: Query checkpoints
	t.Run("checkpoint operations", func(t *testing.T) {
		checkpoints, err := svc.ListCheckpoints(ctx, sessionID, 10)
		if err != nil {
			t.Fatalf("list checkpoints: %v", err)
		}

		t.Logf("Found %d checkpoints", len(checkpoints))

		if len(checkpoints) > 0 {
			workflowID := checkpoints[0].WorkflowID
			cp, err := svc.GetCheckpoint(ctx, sessionID, workflowID)
			if err != nil {
				t.Fatalf("get checkpoint: %v", err)
			}
			if cp.WorkflowID != workflowID {
				t.Errorf("checkpoint workflowID mismatch")
			}
		}
	})
}

// TestObjectStoreWithSQLite tests ObjectStore persistence with SQLite backend
func TestObjectStoreWithSQLite(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspace")
	stateDBPath := filepath.Join(tmpDir, "state.sqlite")
	_ = os.MkdirAll(workspaceRoot, 0755)

	// Create persistent kernel with SQLite stores
	k, err := kernel.NewPersistentWithWorkflowDependencies(
		workspaceRoot, stateDBPath,
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("create persistent kernel: %v", err)
	}
	defer k.Close()

	svc := api.New(k)
	sessionID := "sqlite-test-session"

	// Create session and submit workflow
	_, err = svc.CreateSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Submit one workflow per session; R-04 fast-fails on concurrent same-session
	// submits so each workflow gets its own session.
	sessIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		sessIDs[i] = fmt.Sprintf("%s-%d", sessionID, i)
		_, _ = svc.CreateSession(ctx, sessIDs[i])
		_, err := svc.Submit(ctx, sessIDs[i], "prompt")
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}

	// Poll until all workflows reach a terminal status (anything other than
	// "running"). Handles both success and failure outcomes without relying
	// on specific events that may not fire for failed workflows.
	deadline := time.Now().Add(10 * time.Second)
	for _, sid := range sessIDs {
		for time.Now().Before(deadline) {
			runs, err := svc.ListWorkflowRuns(ctx, sid, 1)
			if err == nil && len(runs) > 0 && runs[0].Status != "running" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Query objects from SQLite-backed store for each session
	for _, sid := range sessIDs {
		runs, err := svc.ListWorkflowRuns(ctx, sid, 10)
		if err != nil {
			t.Fatalf("list runs for %s: %v", sid, err)
		}
		for _, run := range runs {
			objects, err := svc.ListWorkflowRunObjects(ctx, sid, run.WorkflowID)
			if err != nil {
				t.Errorf("list objects for %s: %v", run.WorkflowID, err)
				continue
			}
			t.Logf("Workflow %s: %d objects", run.WorkflowID, len(objects))
			for _, obj := range objects {
				payload, err := svc.ReadWorkflowRunObject(ctx, sid, run.WorkflowID, obj.ID)
				if err != nil {
					t.Errorf("read object %s: %v", obj.ID, err)
					continue
				}
				if len(payload) == 0 {
					t.Errorf("empty payload for object %s", obj.ID)
				}
			}
		}
	}
}

// TestConcurrentObjectAccess tests concurrent access to objects
func TestConcurrentObjectAccess(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspace")
	_ = os.MkdirAll(workspaceRoot, 0755)

	ws := workspace.New(workspaceRoot)
	sessionStore := session.NewStore()
	intentStore := intent.NewStore()
	messageStore := message.NewStore()
	checkpointStore := checkpoint.NewStore()
	turnStore := turn.NewStore()
	objectStore := objectstore.NewStore()

	tools := toolruntime.New(ws)
	k := kernel.NewWithStores(
		workspaceRoot, "",
		sessionStore, intentStore, messageStore, checkpointStore, turnStore, turnsummary.NewStore(), objectStore, insight.NewStore(),
		nil, nil, nil, tools,
	)
	defer k.Close()

	svc := api.New(k)
	sessionID := "concurrent-session"
	_, _ = svc.CreateSession(ctx, sessionID)

	// Submit multiple workflows concurrently; each goroutine uses its own session
	// (R-04: concurrent submit to same session fast-fails).
	type workflowEntry struct{ sessID, workflowID string }
	workflowCount := 5
	done := make(chan workflowEntry, workflowCount)

	for i := 0; i < workflowCount; i++ {
		go func(idx int) {
			sessID := fmt.Sprintf("%s-%d", sessionID, idx)
			_, _ = svc.CreateSession(ctx, sessID)
			wfID, err := svc.Submit(ctx, sessID, "concurrent prompt")
			if err != nil {
				t.Errorf("submit %d: %v", idx, err)
			}
			done <- workflowEntry{sessID, wfID}
		}(i)
	}

	// Collect (session, workflow) pairs
	entries := make([]workflowEntry, 0, workflowCount)
	for i := 0; i < workflowCount; i++ {
		select {
		case e := <-done:
			if e.workflowID != "" {
				entries = append(entries, e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for submissions")
		}
	}

	// Give workflows time to complete
	time.Sleep(200 * time.Millisecond)

	// Concurrently query objects using the correct session per workflow
	results := make(chan int, len(entries))
	for _, e := range entries {
		go func(e workflowEntry) {
			objects, err := svc.ListWorkflowRunObjects(ctx, e.sessID, e.workflowID)
			if err != nil {
				t.Errorf("list objects for %s: %v", e.workflowID, err)
			}
			results <- len(objects)
		}(e)
	}

	totalObjects := 0
	for i := 0; i < len(entries); i++ {
		select {
		case count := <-results:
			totalObjects += count
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for object queries")
		}
	}

	t.Logf("Total objects across all workflows: %d", totalObjects)
}

// TestObjectStoreEdgeCases tests edge cases in object operations
func TestObjectStoreEdgeCases(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	workspaceRoot := filepath.Join(tmpDir, "workspace")
	_ = os.MkdirAll(workspaceRoot, 0755)

	ws := workspace.New(workspaceRoot)
	sessionStore := session.NewStore()
	intentStore := intent.NewStore()
	messageStore := message.NewStore()
	checkpointStore := checkpoint.NewStore()
	turnStore := turn.NewStore()
	objectStore := objectstore.NewStore()

	tools := toolruntime.New(ws)
	k := kernel.NewWithStores(
		workspaceRoot, "",
		sessionStore, intentStore, messageStore, checkpointStore, turnStore, turnsummary.NewStore(), objectStore, insight.NewStore(),
		nil, nil, nil, tools,
	)
	defer k.Close()

	svc := api.New(k)

	// Test 1: List objects for non-existent workflow
	t.Run("non-existent workflow", func(t *testing.T) {
		_, err := svc.ListWorkflowRunObjects(ctx, "test", "non-existent-wf")
		if err == nil {
			t.Fatal("expected error for non-existent workflow")
		}
	})

	// Test 2: Read non-existent object
	t.Run("non-existent object", func(t *testing.T) {
		_, err := svc.ReadWorkflowRunObject(ctx, "test", "wf-1", "non-existent-obj")
		// This might error or return empty depending on implementation
		t.Logf("Read non-existent object result: %v", err)
	})

	// Test 3: Empty session ID validation
	t.Run("empty session ID", func(t *testing.T) {
		_, err := svc.ListWorkflowRunObjects(ctx, "", "wf-1")
		if err == nil {
			t.Error("expected error for empty session ID")
		}
	})

	// Test 4: Empty workflow ID validation
	t.Run("empty workflow ID", func(t *testing.T) {
		_, err := svc.ListWorkflowRunObjects(ctx, "test", "")
		if err == nil {
			t.Error("expected error for empty workflow ID")
		}
	})
}
