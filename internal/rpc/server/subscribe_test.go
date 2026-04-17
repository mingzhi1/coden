package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

type subscribeKernel struct {
	mu   sync.Mutex
	subs map[string]map[chan model.Event]struct{}
}

func newSubscribeKernel() *subscribeKernel {
	return &subscribeKernel{
		subs: map[string]map[chan model.Event]struct{}{},
	}
}

func (k *subscribeKernel) Attach(_ context.Context, _, _, _ string) error {
	return nil
}

func (k *subscribeKernel) CreateSession(_ context.Context, sessionID string) (model.Session, error) {
	return model.Session{ID: sessionID}, nil
}

func (k *subscribeKernel) ListSessions(_ context.Context, limit int) ([]model.Session, error) {
	return nil, nil
}

func (k *subscribeKernel) Detach(_ context.Context, _, _ string) error {
	return nil
}

func (k *subscribeKernel) Submit(_ context.Context, sessionID, _ string) (string, error) {
	return "wf-stub", nil
}

func (k *subscribeKernel) CancelWorkflow(_ context.Context, _, _ string) error {
	return nil
}

func (k *subscribeKernel) ListMessages(_ context.Context, _ string, _ int) ([]model.Message, error) {
	return nil, nil
}

func (k *subscribeKernel) GetWorkflowRun(_ context.Context, sessionID, workflowID string) (model.WorkflowRun, error) {
	return model.WorkflowRun{ID: workflowID, SessionID: sessionID, WorkflowID: workflowID, Status: "pass"}, nil
}

func (k *subscribeKernel) ListWorkflowRuns(_ context.Context, sessionID string, limit int) ([]model.WorkflowRun, error) {
	return []model.WorkflowRun{{ID: "wf-test", SessionID: sessionID, WorkflowID: "wf-test", Status: "pass"}}, nil
}

func (k *subscribeKernel) ListWorkflowRunObjects(_ context.Context, _ string, workflowID string) ([]model.Object, error) {
	return []model.Object{{ID: "obj-1", TurnID: workflowID, Kind: "modify", Sequence: 1}}, nil
}

func (k *subscribeKernel) ReadWorkflowRunObject(_ context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (k *subscribeKernel) GetLatestIntent(_ context.Context, _ string) (model.IntentSpec, error) {
	return model.IntentSpec{}, nil
}

func (k *subscribeKernel) WorkspaceChanges(_ context.Context, _ string) ([]model.WorkspaceChangedPayload, error) {
	return nil, nil
}

func (k *subscribeKernel) WorkspaceRead(_ context.Context, _, _ string) ([]byte, error) {
	return nil, nil
}

func (k *subscribeKernel) WorkspaceWrite(_ context.Context, _, _ string, _ []byte) (string, error) {
	return "", nil
}

func (k *subscribeKernel) GetCheckpoint(_ context.Context, sessionID, workflowID string) (model.CheckpointResult, error) {
	return model.CheckpointResult{
		SessionID:  sessionID,
		WorkflowID: workflowID,
		Status:     "pass",
	}, nil
}

func (k *subscribeKernel) ListCheckpoints(_ context.Context, sessionID string, limit int) ([]model.CheckpointResult, error) {
	result := []model.CheckpointResult{{
		SessionID:  sessionID,
		WorkflowID: "wf-test",
		Status:     "pass",
	}}
	if limit > 0 && limit < len(result) {
		result = result[:limit]
	}
	return result, nil
}

func (k *subscribeKernel) RenameSession(_ context.Context, sessionID, _ string) (model.Session, error) {
	return model.Session{ID: sessionID}, nil
}

func (k *subscribeKernel) SubscribeSince(sessionID string, _ uint64) (<-chan model.Event, func()) {
	return k.Subscribe(sessionID)
}

func (k *subscribeKernel) GetWorkflowWorkers(_ context.Context, _, _ string) ([]model.WorkerState, error) {
	return nil, nil
}

func (k *subscribeKernel) SkipTask(_ context.Context, _, _ string) error { return nil }

func (k *subscribeKernel) UndoTask(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (k *subscribeKernel) Snapshot(_ context.Context, sessionID string, _ int) (model.SessionSnapshot, error) {
	return model.SessionSnapshot{SessionID: sessionID}, nil
}

func (k *subscribeKernel) Subscribe(sessionID string) (<-chan model.Event, func()) {
	ch := make(chan model.Event, 4)
	k.mu.Lock()
	if _, ok := k.subs[sessionID]; !ok {
		k.subs[sessionID] = make(map[chan model.Event]struct{})
	}
	k.subs[sessionID][ch] = struct{}{}
	k.mu.Unlock()
	return ch, func() {
		k.mu.Lock()
		if subs := k.subs[sessionID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(k.subs, sessionID)
			}
		}
		k.mu.Unlock()
		close(ch)
	}
}

func (k *subscribeKernel) emit(sessionID string, ev model.Event) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for ch := range k.subs[sessionID] {
		ch <- ev
	}
}

func TestEventSubscribeTargetsCallingConnection(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	k := newSubscribeKernel()
	srv := New(k)

	server1, client1 := transport.Pipe()
	server2, client2 := transport.Pipe()
	go srv.ServeConn(ctx, server1)
	go srv.ServeConn(ctx, server2)

	codec1 := transport.NewCodec(client1)
	codec2 := transport.NewCodec(client2)
	defer codec1.Close()
	defer codec2.Close()

	subscribe := func(t *testing.T, codec *transport.Codec, sessionID string) {
		t.Helper()
		req, err := protocol.NewRequest(1, protocol.MethodEventSubscribe, protocol.EventSubscribeParams{
			SessionID: sessionID,
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
			t.Fatalf("subscribe returned error: %v", resp.Error)
		}
	}

	subscribe(t, codec1, "session-1")
	subscribe(t, codec2, "session-2")

	k.emit("session-1", model.Event{
		Seq:       1,
		SessionID: "session-1",
		Topic:     model.EventWorkflowStarted,
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(model.WorkflowStartedPayload{WorkflowID: "wf-1"}),
	})

	readNotif := func(codec *transport.Codec, timeout time.Duration) (protocol.Notification, error) {
		type result struct {
			notif protocol.Notification
			err   error
		}
		done := make(chan result, 1)
		go func() {
			raw, err := codec.ReadMessage()
			if err != nil {
				done <- result{err: err}
				return
			}
			var notif protocol.Notification
			err = json.Unmarshal(raw, &notif)
			done <- result{notif: notif, err: err}
		}()

		select {
		case got := <-done:
			return got.notif, got.err
		case <-time.After(timeout):
			return protocol.Notification{}, context.DeadlineExceeded
		}
	}

	notif, err := readNotif(codec1, 2*time.Second)
	if err != nil {
		t.Fatalf("expected session-1 notification, got error: %v", err)
	}
	if notif.Method != protocol.MethodEventPush {
		t.Fatalf("unexpected method: %q", notif.Method)
	}

	var ev model.Event
	if err := json.Unmarshal(notif.Params, &ev); err != nil {
		t.Fatalf("unmarshal event failed: %v", err)
	}
	if ev.SessionID != "session-1" {
		t.Fatalf("unexpected session id: %q", ev.SessionID)
	}

	if _, err := readNotif(codec2, 200*time.Millisecond); err == nil {
		t.Fatal("unexpected notification delivered to unsubscribed connection")
	}
}

func TestEventUnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	k := newSubscribeKernel()
	srv := New(k)

	serverConn, clientConn := transport.Pipe()
	go srv.ServeConn(ctx, serverConn)

	codec := transport.NewCodec(clientConn)
	defer codec.Close()

	writeRequest := func(t *testing.T, id int64, method string, params any) {
		t.Helper()
		req, err := protocol.NewRequest(id, method, params)
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
			t.Fatalf("request returned error: %v", resp.Error)
		}
	}

	writeRequest(t, 1, protocol.MethodEventSubscribe, protocol.EventSubscribeParams{SessionID: "session-1"})
	writeRequest(t, 2, protocol.MethodEventUnsubscribe, protocol.EventUnsubscribeParams{SessionID: "session-1"})

	k.emit("session-1", model.Event{
		Seq:       2,
		SessionID: "session-1",
		Topic:     model.EventWorkflowStarted,
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(model.WorkflowStartedPayload{WorkflowID: "wf-2"}),
	})

	done := make(chan struct{}, 1)
	go func() {
		_, _ = codec.ReadMessage()
		done <- struct{}{}
	}()

	select {
	case <-done:
		t.Fatal("unexpected notification after unsubscribe")
	case <-time.After(200 * time.Millisecond):
	}
}
