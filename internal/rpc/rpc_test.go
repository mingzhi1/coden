package rpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/server"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// stubKernel implements server.KernelAPI for testing.
type stubKernel struct {
	events         chan model.Event
	attached       []string
	detached       []string
	attachedViews  []string
	canceled       []string
	workspace      []model.WorkspaceChangedPayload
	messages       []model.Message
	subscribeCalls int
	cancelCalls    int
	checkpoints    []model.CheckpointResult
	sessions       []model.Session
}

func (k *stubKernel) CreateSession(_ context.Context, sessionID string) (model.Session, error) {
	if sessionID == "" {
		sessionID = "sess-test"
	}
	item := model.Session{ID: sessionID, ProjectID: sessionID, ProjectRoot: "/tmp/project", CreatedAt: time.Now()}
	k.sessions = append(k.sessions, item)
	return item, nil
}

func (k *stubKernel) ListSessions(_ context.Context, limit int) ([]model.Session, error) {
	out := append([]model.Session(nil), k.sessions...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (k *stubKernel) Submit(_ context.Context, sessionID, prompt string) (string, error) {
	// Emit a couple of events to simulate async workflow execution.
	k.events <- model.Event{
		Seq:       1,
		SessionID: sessionID,
		Topic:     model.EventWorkflowStarted,
		Timestamp: time.Now(),
		Payload: model.EncodePayload(struct {
			Prompt string `json:"prompt"`
		}{Prompt: prompt}),
	}
	k.events <- model.Event{
		Seq:       2,
		SessionID: sessionID,
		Topic:     model.EventCheckpointUpdated,
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(model.CheckpointUpdatedPayload{Status: "pass"}),
	}
	return "wf-test", nil
}

func (k *stubKernel) CancelWorkflow(_ context.Context, sessionID, workflowID string) error {
	k.canceled = append(k.canceled, sessionID+":"+workflowID)
	return nil
}

func (k *stubKernel) WorkspaceChanges(_ context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error) {
	return append([]model.WorkspaceChangedPayload(nil), k.workspace...), nil
}

func (k *stubKernel) ListMessages(_ context.Context, sessionID string, limit int) ([]model.Message, error) {
	out := append([]model.Message(nil), k.messages...)
	if limit > 0 && limit < len(out) {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (k *stubKernel) GetLatestIntent(_ context.Context, _ string) (model.IntentSpec, error) {
	return model.IntentSpec{}, nil
}

func (k *stubKernel) WorkspaceRead(_ context.Context, _, _ string) ([]byte, error) {
	return nil, nil
}

func (k *stubKernel) WorkspaceWrite(_ context.Context, _, _ string, _ []byte) (string, error) {
	return "", nil
}

func (k *stubKernel) GetWorkflowRun(_ context.Context, sessionID, workflowID string) (model.WorkflowRun, error) {
	return model.WorkflowRun{
		ID:         workflowID,
		SessionID:  sessionID,
		WorkflowID: workflowID,
		Prompt:     "hello world",
		Status:     "pass",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}, nil
}

func (k *stubKernel) ListWorkflowRuns(_ context.Context, sessionID string, limit int) ([]model.WorkflowRun, error) {
	out := []model.WorkflowRun{{
		ID:         "wf-test",
		SessionID:  sessionID,
		WorkflowID: "wf-test",
		Prompt:     "hello world",
		Status:     "pass",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (k *stubKernel) ListWorkflowRunObjects(_ context.Context, sessionID, workflowID string) ([]model.Object, error) {
	return []model.Object{{
		ID:           "obj-1",
		TurnID:       workflowID,
		Kind:         "modify",
		Sequence:     1,
		FilePath:     "test.md",
		PrevObjectID: "",
		StoragePath:  "/tmp/test.md",
		ContentHash:  "sha256:test",
		CreatedAt:    time.Now(),
	}}, nil
}

func (k *stubKernel) ReadWorkflowRunObject(_ context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error) {
	return json.RawMessage(`{
		"tool_call_id":"tool-wf-test-write",
		"tool":"write_file",
		"status":"written",
		"request":{"path":"test.md"},
		"response":{"summary":"wrote test.md","diff":"--- a\n+++ b"}
	}`), nil
}

func (k *stubKernel) RenameSession(_ context.Context, sessionID, _ string) (model.Session, error) {
	return model.Session{ID: sessionID}, nil
}

func (k *stubKernel) SubscribeSince(_ string, _ uint64) (<-chan model.Event, func()) {
	return k.events, func() {}
}

func (k *stubKernel) GetWorkflowWorkers(_ context.Context, _, _ string) ([]model.WorkerState, error) {
	return nil, nil
}

func (k *stubKernel) SkipTask(_ context.Context, _, _ string) error { return nil }

func (k *stubKernel) UndoTask(_ context.Context, _ string) (string, error) { return "", nil }

func (k *stubKernel) ListHooks(_ context.Context, _ string) ([]protocol.HookInfo, error) {
	return nil, nil
}
func (k *stubKernel) RegisterHook(_ context.Context, _ protocol.HookRegisterParams) error {
	return nil
}
func (k *stubKernel) RemoveHook(_ context.Context, _ string) (bool, error) { return false, nil }

func (k *stubKernel) Snapshot(_ context.Context, sessionID string, _ int) (model.SessionSnapshot, error) {
	return model.SessionSnapshot{SessionID: sessionID}, nil
}

func (k *stubKernel) Subscribe(_ string) (<-chan model.Event, func()) {
	k.subscribeCalls++
	return k.events, func() { k.cancelCalls++ }
}

func (k *stubKernel) GetCheckpoint(_ context.Context, sessionID, workflowID string) (model.CheckpointResult, error) {
	for i := len(k.checkpoints) - 1; i >= 0; i-- {
		cp := k.checkpoints[i]
		if cp.SessionID != sessionID {
			continue
		}
		if workflowID == "" || cp.WorkflowID == workflowID {
			return cp, nil
		}
	}
	return model.CheckpointResult{}, context.Canceled
}

func (k *stubKernel) ListCheckpoints(_ context.Context, sessionID string, limit int) ([]model.CheckpointResult, error) {
	out := make([]model.CheckpointResult, 0, len(k.checkpoints))
	for i := len(k.checkpoints) - 1; i >= 0; i-- {
		cp := k.checkpoints[i]
		if cp.SessionID == sessionID {
			out = append(out, cp)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (k *stubKernel) Attach(_ context.Context, sessionID, clientName, view string) error {
	k.attached = append(k.attached, sessionID+":"+clientName)
	k.attachedViews = append(k.attachedViews, view)
	return nil
}

func (k *stubKernel) Detach(_ context.Context, sessionID, clientName string) error {
	k.detached = append(k.detached, sessionID+":"+clientName)
	return nil
}

func TestRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create in-memory pipe
	serverRWC, clientRWC := transport.Pipe()

	// Start server
	k := &stubKernel{events: make(chan model.Event, 16)}
	k.checkpoints = []model.CheckpointResult{{
		WorkflowID:    "wf-test",
		SessionID:     "test-session",
		Status:        "pass",
		ArtifactPaths: []string{"test.md"},
		Evidence:      []string{"test passed"},
		CreatedAt:     time.Now(),
	}}
	k.workspace = []model.WorkspaceChangedPayload{{
		WorkflowID: "wf-test",
		Path:       "test.md",
		Operation:  "write",
	}}
	k.messages = []model.Message{
		{ID: "m1", SessionID: "test-session", Role: "user", Content: "hello world", CreatedAt: time.Now()},
		{ID: "m2", SessionID: "test-session", Role: "assistant", Content: "workflow wf-test finished with pass", CreatedAt: time.Now().Add(time.Second)},
	}
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	// Create client
	c := client.New(clientRWC)
	defer c.Close()

	// Test ping
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping failed: %v", err)
	}

	if err := c.Attach(ctx, "test-session", "rpc-test", "tui"); err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	session, err := c.CreateSession(ctx, "test-session")
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	if session.ID != "test-session" {
		t.Fatalf("unexpected session: %+v", session)
	}

	sessions, err := c.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("list sessions failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "test-session" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}

	// Test subscribe + submit
	events, cancelSub, err := c.Subscribe(ctx, "test-session")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer cancelSub()

	// Give subscription time to register
	time.Sleep(50 * time.Millisecond)

	workflowID, err := c.Submit(ctx, "test-session", "hello world")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if workflowID != "wf-test" {
		t.Fatalf("expected wf-test workflowID, got %s", workflowID)
	}

	got, err := c.GetCheckpoint(ctx, "test-session", "wf-test")
	if err != nil {
		t.Fatalf("get checkpoint failed: %v", err)
	}
	if got.WorkflowID != "wf-test" {
		t.Fatalf("expected wf-test checkpoint, got %q", got.WorkflowID)
	}

	list, err := c.ListCheckpoints(ctx, "test-session", 1)
	if err != nil {
		t.Fatalf("list checkpoints failed: %v", err)
	}
	if len(list) != 1 || list[0].WorkflowID != "wf-test" {
		t.Fatalf("unexpected checkpoint list: %+v", list)
	}

	run, err := c.GetWorkflowRun(ctx, "test-session", "wf-test")
	if err != nil {
		t.Fatalf("get workflow run failed: %v", err)
	}
	if run.WorkflowID != "wf-test" || run.SessionID != "test-session" {
		t.Fatalf("unexpected workflow run: %+v", run)
	}

	runs, err := c.ListWorkflowRuns(ctx, "test-session", 1)
	if err != nil {
		t.Fatalf("list workflow runs failed: %v", err)
	}
	if len(runs) != 1 || runs[0].WorkflowID != "wf-test" {
		t.Fatalf("unexpected workflow runs: %+v", runs)
	}

	objects, err := c.ListWorkflowRunObjects(ctx, "test-session", "wf-test")
	if err != nil {
		t.Fatalf("list workflow run objects failed: %v", err)
	}
	if len(objects) != 1 || objects[0].TurnID != "wf-test" {
		t.Fatalf("unexpected workflow objects: %+v", objects)
	}

	objectPayload, err := c.ReadWorkflowRunObject(ctx, "test-session", "wf-test", "obj-1")
	if err != nil {
		t.Fatalf("read workflow object failed: %v", err)
	}
	if !json.Valid(objectPayload) {
		t.Fatalf("expected json payload, got %s", string(objectPayload))
	}

	// Check that we received events
	timeout := time.After(2 * time.Second)
	var received []model.Event
	for len(received) < 2 {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			t.Fatalf("timed out waiting for events, got %d", len(received))
		}
	}

	if received[0].Topic != model.EventWorkflowStarted {
		t.Fatalf("expected workflow.started, got %s", received[0].Topic)
	}
	if received[1].Topic != model.EventCheckpointUpdated {
		t.Fatalf("expected checkpoint.updated, got %s", received[1].Topic)
	}

	if len(k.attached) != 1 || k.attached[0] != "test-session:rpc-test" {
		t.Fatalf("unexpected attachments: %+v", k.attached)
	}
	if len(k.attachedViews) != 1 || k.attachedViews[0] != "tui" {
		t.Fatalf("unexpected attached views: %+v", k.attachedViews)
	}

	if err := c.Detach(ctx, "test-session", "rpc-test"); err != nil {
		t.Fatalf("detach failed: %v", err)
	}
	if len(k.detached) != 1 || k.detached[0] != "test-session:rpc-test" {
		t.Fatalf("unexpected detachments: %+v", k.detached)
	}

	if err := c.CancelWorkflow(ctx, "test-session", "wf-test"); err != nil {
		t.Fatalf("cancel workflow failed: %v", err)
	}
	if len(k.canceled) != 1 || k.canceled[0] != "test-session:wf-test" {
		t.Fatalf("unexpected workflow cancellations: %+v", k.canceled)
	}

	changes, err := c.WorkspaceChanges(ctx, "test-session")
	if err != nil {
		t.Fatalf("workspace changes failed: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != "test.md" {
		t.Fatalf("unexpected workspace changes: %+v", changes)
	}

	messages, err := c.ListMessages(ctx, "test-session", 0)
	if err != nil {
		t.Fatalf("list messages failed: %v", err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" {
		t.Fatalf("unexpected messages: %+v", messages)
	}

	t.Logf("round-trip OK: ping, subscribe, submit, 2 events received")
}

func TestAttachValidationError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	k := &stubKernel{events: make(chan model.Event, 4)}
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	if err := c.Attach(ctx, "", "rpc-test", "tui"); err == nil {
		t.Fatal("expected attach validation error")
	} else if rpcErr, ok := err.(*protocol.Error); !ok || rpcErr.Code != protocol.CodeInvalidParams {
		t.Fatalf("expected invalid params error, got %#v", err)
	}
}

func TestSubscribeReturnsErrorWhenConnectionClosed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	k := &stubKernel{events: make(chan model.Event, 4)}
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	_ = serverRWC.Close()
	defer c.Close()

	if _, cancelSub, err := c.Subscribe(ctx, "test-session"); err == nil {
		cancelSub()
		t.Fatal("expected subscribe error")
	} else if !errors.Is(err, client.ErrConnectionClosed) {
		t.Fatalf("expected ErrConnectionClosed, got %#v", err)
	}
}

func TestClientCancelUnsubscribesRemoteWhenLastLocalSubscriberLeaves(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	k := &stubKernel{events: make(chan model.Event, 8)}
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	c := client.New(clientRWC)
	defer c.Close()

	_, cancel1, err := c.Subscribe(ctx, "test-session")
	if err != nil {
		t.Fatalf("first subscribe failed: %v", err)
	}
	_, cancel2, err := c.Subscribe(ctx, "test-session")
	if err != nil {
		t.Fatalf("second subscribe failed: %v", err)
	}

	if k.subscribeCalls != 1 {
		t.Fatalf("expected one remote subscribe, got %d", k.subscribeCalls)
	}

	cancel1()
	time.Sleep(50 * time.Millisecond)
	if k.cancelCalls != 0 {
		t.Fatalf("expected no remote unsubscribe yet, got %d", k.cancelCalls)
	}

	cancel2()
	time.Sleep(50 * time.Millisecond)
	if k.cancelCalls != 1 {
		t.Fatalf("expected one remote unsubscribe, got %d", k.cancelCalls)
	}
}

func TestKernelServerRejectsWorkerSurfaceMethods(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverRWC, clientRWC := transport.Pipe()
	k := &stubKernel{events: make(chan model.Event, 4)}
	srv := server.New(k)
	go srv.ServeConn(ctx, serverRWC)

	codec := transport.NewCodec(clientRWC)
	defer codec.Close()

	req, err := protocol.NewRequest(1, protocol.MethodWorkerDescribe, nil)
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
	if resp.Error == nil || resp.Error.Code != protocol.CodeMethodNotFound {
		t.Fatalf("expected method not found, got %+v", resp.Error)
	}
}
