package rpc_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/server"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// objectStoreKernel is a test kernel that simulates ObjectStore behavior
type objectStoreKernel struct {
	mu      sync.Mutex
	objects map[string][]model.Object
	events  chan model.Event
}

func newObjectStoreKernel() *objectStoreKernel {
	return &objectStoreKernel{
		objects: make(map[string][]model.Object),
		events:  make(chan model.Event, 16),
	}
}

func (k *objectStoreKernel) CreateSession(_ context.Context, sessionID string) (model.Session, error) {
	return model.Session{ID: sessionID, CreatedAt: time.Now()}, nil
}

func (k *objectStoreKernel) ListSessions(_ context.Context, limit int) ([]model.Session, error) {
	return nil, nil
}

func (k *objectStoreKernel) Attach(_ context.Context, sessionID, clientName, view string) error {
	return nil
}

func (k *objectStoreKernel) Detach(_ context.Context, sessionID, clientName string) error {
	return nil
}

func (k *objectStoreKernel) Submit(_ context.Context, sessionID, prompt string) (string, error) {
	workflowID := "wf-" + time.Now().Format("20060102150405")
	
	// Simulate creating objects for this workflow
	obj1 := model.Object{
		ID:           "obj-1-" + workflowID,
		TurnID:       workflowID,
		Kind:         "modify",
		Sequence:     1,
		FilePath:     "test1.go",
		PrevObjectID: "",
		StoragePath:  "/tmp/test1.go",
		ContentHash:  "sha256:abc123",
		CreatedAt:    time.Now(),
	}
	obj2 := model.Object{
		ID:           "obj-2-" + workflowID,
		TurnID:       workflowID,
		Kind:         "modify",
		Sequence:     2,
		FilePath:     "test2.go",
		PrevObjectID: "",
		StoragePath:  "/tmp/test2.go",
		ContentHash:  "sha256:def456",
		CreatedAt:    time.Now(),
	}
	k.mu.Lock()
	k.objects[workflowID] = []model.Object{obj1, obj2}
	k.mu.Unlock()
	
	// Emit events
	k.events <- model.Event{
		Seq:       1,
		SessionID: sessionID,
		Topic:     model.EventWorkflowStarted,
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(model.WorkflowStartedPayload{WorkflowID: workflowID}),
	}
	k.events <- model.Event{
		Seq:       2,
		SessionID: sessionID,
		Topic:     model.EventCheckpointUpdated,
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(model.CheckpointUpdatedPayload{WorkflowID: workflowID, Status: "pass"}),
	}
	
	return workflowID, nil
}

func (k *objectStoreKernel) CancelWorkflow(_ context.Context, sessionID, workflowID string) error {
	return nil
}

func (k *objectStoreKernel) GetWorkflowRun(_ context.Context, sessionID, workflowID string) (model.WorkflowRun, error) {
	return model.WorkflowRun{
		ID:         workflowID,
		SessionID:  sessionID,
		WorkflowID: workflowID,
		Status:     "pass",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (k *objectStoreKernel) ListWorkflowRuns(_ context.Context, sessionID string, limit int) ([]model.WorkflowRun, error) {
	var runs []model.WorkflowRun
	for workflowID := range k.objects {
		runs = append(runs, model.WorkflowRun{
			ID:         workflowID,
			SessionID:  sessionID,
			WorkflowID: workflowID,
			Status:     "pass",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		})
	}
	return runs, nil
}

func (k *objectStoreKernel) ListWorkflowRunObjects(_ context.Context, sessionID, workflowID string) ([]model.Object, error) {
	k.mu.Lock()
	objects, ok := k.objects[workflowID]
	k.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return objects, nil
}

func (k *objectStoreKernel) ReadWorkflowRunObject(_ context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error) {
	k.mu.Lock()
	objects, ok := k.objects[workflowID]
	k.mu.Unlock()
	if !ok {
		return nil, nil
	}
	
	for _, obj := range objects {
		if obj.ID == objectID {
			payload := map[string]any{
				"tool_call_id": "tool-" + objectID,
				"tool":         "write_file",
				"status":       "written",
				"request":      map[string]string{"path": obj.FilePath},
				"response": map[string]any{
					"summary": "wrote " + obj.FilePath,
					"diff":    "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new",
				},
			}
			return json.Marshal(payload)
		}
	}
	return nil, nil
}

func (k *objectStoreKernel) ListMessages(_ context.Context, sessionID string, limit int) ([]model.Message, error) {
	return nil, nil
}

func (k *objectStoreKernel) GetLatestIntent(_ context.Context, _ string) (model.IntentSpec, error) {
	return model.IntentSpec{}, nil
}

func (k *objectStoreKernel) WorkspaceChanges(_ context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error) {
	return nil, nil
}

func (k *objectStoreKernel) WorkspaceRead(_ context.Context, _, _ string) ([]byte, error) {
	return nil, nil
}

func (k *objectStoreKernel) WorkspaceWrite(_ context.Context, _, _ string, _ []byte) (string, error) {
	return "", nil
}

func (k *objectStoreKernel) GetCheckpoint(_ context.Context, sessionID, workflowID string) (model.CheckpointResult, error) {
	return model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     sessionID,
		Status:        "pass",
		ArtifactPaths: []string{"test1.go", "test2.go"},
		CreatedAt:     time.Now(),
	}, nil
}

func (k *objectStoreKernel) ListCheckpoints(_ context.Context, sessionID string, limit int) ([]model.CheckpointResult, error) {
	return nil, nil
}

func (k *objectStoreKernel) RenameSession(_ context.Context, sessionID, _ string) (model.Session, error) {
	return model.Session{ID: sessionID}, nil
}

func (k *objectStoreKernel) SubscribeSince(_ string, _ uint64) (<-chan model.Event, func()) {
	return k.events, func() {}
}

func (k *objectStoreKernel) GetWorkflowWorkers(_ context.Context, _, _ string) ([]model.WorkerState, error) {
	return nil, nil
}

func (k *objectStoreKernel) SkipTask(_ context.Context, _, _ string) error { return nil }

func (k *objectStoreKernel) UndoTask(_ context.Context, _ string) (string, error) { return "", nil }

func (k *objectStoreKernel) Snapshot(_ context.Context, sessionID string, _ int) (model.SessionSnapshot, error) {
	return model.SessionSnapshot{SessionID: sessionID}, nil
}

func (k *objectStoreKernel) Subscribe(_ string) (<-chan model.Event, func()) {
	return k.events, func() {}
}

// TestRPCObjectStoreChain tests the full chain: RPC Client -> Server -> Kernel -> ObjectStore
func TestRPCObjectStoreChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Setup in-memory pipe
	serverRWC, clientRWC := transport.Pipe()

	// Create kernel with object store simulation
	k := newObjectStoreKernel()
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	// Create client
	c := client.New(clientRWC)
	defer c.Close()

	// Test 1: Submit workflow (creates objects)
	t.Run("submit creates objects", func(t *testing.T) {
		workflowID, err := c.Submit(ctx, "test-session", "test prompt")
		if err != nil {
			t.Fatalf("submit failed: %v", err)
		}
		if workflowID == "" {
			t.Fatal("workflowID is empty")
		}

		// Wait for checkpoint event (skip workflow.started)
		for {
			select {
			case ev := <-k.events:
				if ev.Topic == model.EventCheckpointUpdated {
					return // Success
				}
				// Continue waiting for checkpoint event
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for checkpoint event")
			}
		}
	})

	// Test 2: List workflow objects
	t.Run("list workflow objects", func(t *testing.T) {
		// First submit to create objects
		workflowID, _ := c.Submit(ctx, "test-session", "test prompt 2")
		
		objects, err := c.ListWorkflowRunObjects(ctx, "test-session", workflowID)
		if err != nil {
			t.Fatalf("list objects failed: %v", err)
		}
		if len(objects) != 2 {
			t.Fatalf("expected 2 objects, got %d", len(objects))
		}
		
		// Verify object structure
		for i, obj := range objects {
			if obj.ID == "" {
				t.Errorf("object %d has empty ID", i)
			}
			if obj.TurnID != workflowID {
				t.Errorf("object %d TurnID mismatch: got %s, want %s", i, obj.TurnID, workflowID)
			}
			if obj.Kind != "modify" {
				t.Errorf("object %d Kind = %s, want modify", i, obj.Kind)
			}
		}
	})

	// Test 3: Read object payload
	t.Run("read object payload", func(t *testing.T) {
		workflowID, _ := c.Submit(ctx, "test-session", "test prompt 3")
		objects, _ := c.ListWorkflowRunObjects(ctx, "test-session", workflowID)
		
		if len(objects) == 0 {
			t.Fatal("no objects to read")
		}
		
		objectID := objects[0].ID
		payload, err := c.ReadWorkflowRunObject(ctx, "test-session", workflowID, objectID)
		if err != nil {
			t.Fatalf("read object failed: %v", err)
		}
		if !json.Valid(payload) {
			t.Errorf("payload is not valid JSON: %s", string(payload))
		}
		
		// Verify payload structure
		var data map[string]any
		if err := json.Unmarshal(payload, &data); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if data["tool"] != "write_file" {
			t.Errorf("expected tool=write_file, got %v", data["tool"])
		}
		if data["status"] != "written" {
			t.Errorf("expected status=written, got %v", data["status"])
		}
	})

	// Test 4: List objects for non-existent workflow
	t.Run("list non-existent workflow", func(t *testing.T) {
		objects, err := c.ListWorkflowRunObjects(ctx, "test-session", "non-existent-wf")
		if err != nil {
			t.Fatalf("list objects for non-existent workflow failed: %v", err)
		}
		if len(objects) != 0 {
			t.Errorf("expected 0 objects for non-existent workflow, got %d", len(objects))
		}
	})

	// Test 5: Read non-existent object
	t.Run("read non-existent object", func(t *testing.T) {
		workflowID, _ := c.Submit(ctx, "test-session", "test prompt 4")
		payload, err := c.ReadWorkflowRunObject(ctx, "test-session", workflowID, "non-existent-obj")
		if err != nil {
			t.Fatalf("read non-existent object returned error: %v", err)
		}
		// Kernel returns nil or "null" JSON for non-existent objects
		if payload != nil && len(payload) > 0 && string(payload) != "null" {
			t.Errorf("expected empty or null payload for non-existent object, got %s", string(payload))
		}
	})
}

// TestObjectStoreConcurrency tests concurrent access to objects
func TestObjectStoreConcurrency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	k := newObjectStoreKernel()
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	// Submit multiple workflows concurrently
	workflowCount := 5
	done := make(chan string, workflowCount)
	
	for i := 0; i < workflowCount; i++ {
		go func(idx int) {
			workflowID, err := c.Submit(ctx, "concurrent-session", "prompt")
			if err != nil {
				t.Errorf("submit %d: %v", idx, err)
			}
			done <- workflowID
		}(i)
	}

	// Collect workflow IDs
	workflowIDs := make([]string, 0, workflowCount)
	for i := 0; i < workflowCount; i++ {
		select {
		case wfID := <-done:
			if wfID != "" {
				workflowIDs = append(workflowIDs, wfID)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for workflow submissions")
		}
	}

	// Verify each workflow has its own objects
	if len(workflowIDs) != workflowCount {
		t.Fatalf("expected %d workflows, got %d", workflowCount, len(workflowIDs))
	}

	for _, wfID := range workflowIDs {
		objects, err := c.ListWorkflowRunObjects(ctx, "concurrent-session", wfID)
		if err != nil {
			t.Fatalf("list objects for %s: %v", wfID, err)
		}
		if len(objects) != 2 {
			t.Errorf("workflow %s: expected 2 objects, got %d", wfID, len(objects))
		}
	}
}
