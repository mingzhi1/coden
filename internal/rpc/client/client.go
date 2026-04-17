// Package client implements a JSON-RPC 2.0 client that implements api.ClientAPI.
// This is the "attach" side of the Neovim pattern — clients connect to a
// running kernel server and interact via RPC.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

var ErrConnectionClosed = errors.New("rpc connection closed")

// Client is a JSON-RPC 2.0 client that speaks to a CodeN kernel server.
type Client struct {
	codec   *transport.Codec
	nextID  int64
	mu      sync.Mutex
	pending map[int64]chan callResult
	subs    map[string][]chan model.Event
	subMu   sync.Mutex
	done    chan struct{}

	closeMu  sync.Mutex
	closeErr error
}

type callResult struct {
	raw json.RawMessage
	err error
}

// New creates a Client connected over the given stream.
// Starts a background goroutine to read responses/notifications.
func New(rwc io.ReadWriteCloser) *Client {
	c := &Client{
		codec:   transport.NewCodec(rwc),
		pending: make(map[int64]chan callResult),
		subs:    make(map[string][]chan model.Event),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *Client) CreateSession(ctx context.Context, sessionID string) (model.Session, error) {
	var result model.Session
	raw, err := c.call(ctx, protocol.MethodSessionCreate, protocol.SessionCreateParams{
		SessionID: sessionID,
	})
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (c *Client) ListSessions(ctx context.Context, limit int) ([]model.Session, error) {
	raw, err := c.call(ctx, protocol.MethodSessionList, protocol.SessionListParams{Limit: limit})
	if err != nil {
		return nil, err
	}
	var result protocol.SessionListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Sessions, nil
}

// RenameSession sets a human-readable label on a session (R-07).
func (c *Client) RenameSession(ctx context.Context, sessionID, name string) (model.Session, error) {
	raw, err := c.call(ctx, protocol.MethodSessionRename, protocol.SessionRenameParams{
		SessionID: sessionID,
		Name:      name,
	})
	if err != nil {
		return model.Session{}, err
	}
	var result protocol.SessionRenameResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return model.Session{}, err
	}
	return result.Session, nil
}

// Attach attaches this client to a session.
func (c *Client) Attach(ctx context.Context, sessionID, clientName, view string) error {
	raw, err := c.call(ctx, protocol.MethodSessionAttach, protocol.SessionAttachParams{
		SessionID:  sessionID,
		ClientName: clientName,
		View:       view,
	})
	if err != nil {
		return err
	}

	var result protocol.SessionAttachResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Status != "attached" {
		return fmt.Errorf("unexpected attach status: %s", result.Status)
	}
	return nil
}

// Detach detaches this client from a session.
func (c *Client) Detach(ctx context.Context, sessionID, clientName string) error {
	raw, err := c.call(ctx, protocol.MethodSessionDetach, protocol.SessionDetachParams{
		SessionID:  sessionID,
		ClientName: clientName,
	})
	if err != nil {
		return err
	}

	var result protocol.SessionDetachResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Status != "detached" {
		return fmt.Errorf("unexpected detach status: %s", result.Status)
	}
	return nil
}

// Submit sends a workflow.submit request and returns the workflowID immediately.
// The final CheckpointResult is delivered via a checkpoint.updated event.
func (c *Client) Submit(ctx context.Context, sessionID, prompt string) (string, error) {
	raw, err := c.call(ctx, protocol.MethodWorkflowSubmit, protocol.WorkflowSubmitParams{
		SessionID: sessionID,
		Prompt:    prompt,
	})
	if err != nil {
		return "", err
	}
	var result protocol.WorkflowSubmitResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.WorkflowID == "" {
		return "", fmt.Errorf("missing workflow id in submit response")
	}
	return result.WorkflowID, nil
}

func (c *Client) CancelWorkflow(ctx context.Context, sessionID, workflowID string) error {
	raw, err := c.call(ctx, protocol.MethodWorkflowCancel, protocol.WorkflowCancelParams{
		SessionID:  sessionID,
		WorkflowID: workflowID,
	})
	if err != nil {
		return err
	}

	var result protocol.WorkflowCancelResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Status != "canceled" {
		return fmt.Errorf("unexpected cancel status: %s", result.Status)
	}
	return nil
}

func (c *Client) GetWorkflowRun(ctx context.Context, sessionID, workflowID string) (model.WorkflowRun, error) {
	var result model.WorkflowRun
	raw, err := c.call(ctx, protocol.MethodWorkflowGet, protocol.WorkflowGetParams{
		SessionID:  sessionID,
		WorkflowID: workflowID,
	})
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (c *Client) ListWorkflowRuns(ctx context.Context, sessionID string, limit int) ([]model.WorkflowRun, error) {
	var result []model.WorkflowRun
	raw, err := c.call(ctx, protocol.MethodWorkflowList, protocol.WorkflowListParams{
		SessionID: sessionID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (c *Client) ListWorkflowRunObjects(ctx context.Context, sessionID, workflowID string) ([]model.Object, error) {
	var result []model.Object
	raw, err := c.call(ctx, protocol.MethodWorkflowObjects, protocol.WorkflowObjectsParams{
		SessionID:  sessionID,
		WorkflowID: workflowID,
	})
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (c *Client) ReadWorkflowRunObject(ctx context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error) {
	raw, err := c.call(ctx, protocol.MethodWorkflowObjectRead, protocol.WorkflowObjectReadParams{
		SessionID:  sessionID,
		WorkflowID: workflowID,
		ObjectID:   objectID,
	})
	if err != nil {
		return nil, err
	}
	var result protocol.WorkflowObjectReadResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Payload, nil
}

func (c *Client) ListMessages(ctx context.Context, sessionID string, limit int) ([]model.Message, error) {
	raw, err := c.call(ctx, protocol.MethodMessageList, protocol.MessageListParams{
		SessionID: sessionID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}

	var result []protocol.Message
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	messages := make([]model.Message, 0, len(result))
	for _, msg := range result {
		messages = append(messages, model.Message{
			ID:        msg.ID,
			SessionID: msg.SessionID,
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: time.Unix(0, msg.CreatedAt).UTC(),
		})
	}
	return messages, nil
}

func (c *Client) WorkspaceChanges(ctx context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error) {
	raw, err := c.call(ctx, protocol.MethodWorkspaceChanges, protocol.WorkspaceChangesParams{
		SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}

	var result protocol.WorkspaceChangesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	changes := make([]model.WorkspaceChangedPayload, 0, len(result.Changes))
	for _, change := range result.Changes {
		changes = append(changes, model.WorkspaceChangedPayload{
			WorkflowID: change.WorkflowID,
			Path:       change.Path,
			Operation:  change.Operation,
		})
	}
	return changes, nil
}

func (c *Client) GetLatestIntent(ctx context.Context, sessionID string) (model.IntentSpec, error) {
	raw, err := c.call(ctx, protocol.MethodIntentGet, protocol.IntentGetParams{
		SessionID: sessionID,
	})
	if err != nil {
		return model.IntentSpec{}, err
	}
	var result protocol.IntentGetResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return model.IntentSpec{}, err
	}
	return model.IntentSpec{
		ID:              result.IntentID,
		SessionID:       result.SessionID,
		Goal:            result.Goal,
		Kind:            result.Kind,
		SuccessCriteria: result.SuccessCriteria,
		CreatedAt:       time.Unix(0, result.CreatedAt).UTC(),
	}, nil
}

func (c *Client) WorkspaceRead(ctx context.Context, sessionID, path string) ([]byte, error) {
	raw, err := c.call(ctx, protocol.MethodWorkspaceRead, protocol.WorkspaceReadParams{
		SessionID: sessionID,
		Path:      path,
	})
	if err != nil {
		return nil, err
	}
	var result protocol.WorkspaceReadResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return []byte(result.Content), nil
}

func (c *Client) WorkspaceWrite(ctx context.Context, sessionID, path string, content []byte) (string, error) {
	raw, err := c.call(ctx, protocol.MethodWorkspaceWrite, protocol.WorkspaceWriteParams{
		SessionID: sessionID,
		Path:      path,
		Content:   string(content),
	})
	if err != nil {
		return "", err
	}
	var result protocol.WorkspaceWriteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

// WorkspaceDiff returns the list of file diffs for a session.
func (c *Client) WorkspaceDiff(ctx context.Context, sessionID string) (protocol.WorkspaceDiffResult, error) {
	raw, err := c.call(ctx, protocol.MethodWorkspaceDiff, protocol.WorkspaceDiffParams{
		SessionID: sessionID,
	})
	if err != nil {
		return protocol.WorkspaceDiffResult{}, err
	}

	var result protocol.WorkspaceDiffResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.WorkspaceDiffResult{}, err
	}
	return result, nil
}

// SessionSnapshot returns an atomic snapshot of the session's current state.
// Use snap.LastEventSeq with SubscribeSince to achieve zero-gap event replay (R-01/R-02).
func (c *Client) SessionSnapshot(ctx context.Context, sessionID string, messageLimit int) (model.SessionSnapshot, error) {
	raw, err := c.call(ctx, protocol.MethodSessionSnapshot, protocol.SessionSnapshotParams{
		SessionID:    sessionID,
		MessageLimit: messageLimit,
	})
	if err != nil {
		return model.SessionSnapshot{}, err
	}
	var result protocol.SessionSnapshotResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return model.SessionSnapshot{}, err
	}

	msgs := make([]model.Message, 0, len(result.Messages))
	for _, m := range result.Messages {
		msgs = append(msgs, model.Message{
			ID:        m.ID,
			SessionID: m.SessionID,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: time.Unix(0, m.CreatedAt).UTC(),
		})
	}
	changes := make([]model.WorkspaceChangedPayload, 0, len(result.WorkspaceChanges))
	for _, c := range result.WorkspaceChanges {
		changes = append(changes, model.WorkspaceChangedPayload{
			WorkflowID: c.WorkflowID,
			Path:       c.Path,
			Operation:  c.Operation,
		})
	}
	snap := model.SessionSnapshot{
		SessionID:        result.SessionID,
		Messages:         msgs,
		ActiveWorkflow:   result.ActiveWorkflow,
		LatestCheckpoint: result.LatestCheckpoint,
		LatestIntent:     result.LatestIntent,
		WorkspaceChanges: changes,
		LastEventSeq:     result.LastEventSeq,
	}
	return snap, nil
}

func (c *Client) GetCheckpoint(ctx context.Context, sessionID, workflowID string) (model.CheckpointResult, error) {
	var result model.CheckpointResult
	raw, err := c.call(ctx, protocol.MethodCheckpointGet, protocol.CheckpointGetParams{
		SessionID:  sessionID,
		WorkflowID: workflowID,
	})
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// GetWorkflowWorkers returns the live worker state for a workflow (R-06).
func (c *Client) GetWorkflowWorkers(ctx context.Context, sessionID, workflowID string) ([]model.WorkerState, error) {
	raw, err := c.call(ctx, protocol.MethodWorkflowWorkers, protocol.WorkflowWorkersParams{
		SessionID:  sessionID,
		WorkflowID: workflowID,
	})
	if err != nil {
		return nil, err
	}
	var result protocol.WorkflowWorkersResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Workers, nil
}

func (c *Client) ListCheckpoints(ctx context.Context, sessionID string, limit int) ([]model.CheckpointResult, error) {
	var result []model.CheckpointResult
	raw, err := c.call(ctx, protocol.MethodCheckpointList, protocol.CheckpointListParams{
		SessionID: sessionID,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// SkipTask skips a task in the active workflow's task queue (M11-05).
func (c *Client) SkipTask(ctx context.Context, sessionID, taskID string) error {
	raw, err := c.call(ctx, protocol.MethodTaskSkip, protocol.TaskSkipParams{
		SessionID: sessionID,
		TaskID:    taskID,
	})
	if err != nil {
		return err
	}
	var result protocol.TaskSkipResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Status != "skipped" {
		return fmt.Errorf("unexpected task skip status: %s", result.Status)
	}
	return nil
}

// UndoTask undoes the last task operation in the active workflow (M11-05).
func (c *Client) UndoTask(ctx context.Context, sessionID string) (string, error) {
	raw, err := c.call(ctx, protocol.MethodTaskUndo, protocol.TaskUndoParams{
		SessionID: sessionID,
	})
	if err != nil {
		return "", err
	}
	var result protocol.TaskUndoResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return result.Undone, nil
}

// Subscribe returns a channel of events for the given session.
// Also sends an event.subscribe request to the server.
func (c *Client) Subscribe(ctx context.Context, sessionID string) (<-chan model.Event, func(), error) {
	return c.SubscribeSince(ctx, sessionID, 0)
}

// SubscribeSince returns a channel of events for the given session,
// replaying all buffered events with Seq > sinceSeq before live delivery.
// Use sinceSeq = SessionSnapshotResult.LastEventSeq to close the gap
// between a snapshot and live event delivery (R-01).
func (c *Client) SubscribeSince(ctx context.Context, sessionID string, sinceSeq uint64) (<-chan model.Event, func(), error) {
	ch := make(chan model.Event, 64)
	c.subMu.Lock()
	first := len(c.subs[sessionID]) == 0
	c.subs[sessionID] = append(c.subs[sessionID], ch)
	c.subMu.Unlock()

	// Register the subscription before returning so callers don't race
	// submit requests against server-side event attachment.
	if first {
		if ctx == nil {
			ctx = context.Background()
		}
		subscribeCtx, cancelSubscribe := context.WithTimeout(ctx, 3*time.Second)
		_, err := c.call(subscribeCtx, protocol.MethodEventSubscribe, protocol.EventSubscribeParams{
			SessionID: sessionID,
			SinceSeq:  sinceSeq,
		})
		cancelSubscribe()
		if err != nil {
			if removed, _ := c.removeSubscription(sessionID, ch); removed {
				close(ch)
			}
			return nil, func() {}, err
		}
	}

	cancel := func() {
		removed, remaining := c.removeSubscription(sessionID, ch)
		if removed {
			close(ch)
		}
		if removed && remaining == 0 {
			c.unsubscribeRemote(sessionID)
		}
	}
	return ch, cancel, nil
}

// Ping sends a ping request and returns the response.
func (c *Client) Ping(ctx context.Context) error {
	raw, err := c.call(ctx, protocol.MethodPing, nil)
	if err != nil {
		return err
	}
	var result protocol.PingResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Status != "pong" {
		return fmt.Errorf("unexpected ping status: %s", result.Status)
	}
	return nil
}

// DescribeWorker returns the advertised capabilities of a worker process.
func (c *Client) DescribeWorker(ctx context.Context) (protocol.WorkerDescribeResult, error) {
	var result protocol.WorkerDescribeResult
	raw, err := c.call(ctx, protocol.MethodWorkerDescribe, nil)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// ExecuteWorker sends a worker.execute request and returns the worker result.
func (c *Client) ExecuteWorker(ctx context.Context, params protocol.WorkerExecuteParams) (protocol.WorkerExecuteResult, error) {
	var result protocol.WorkerExecuteResult
	raw, err := c.call(ctx, protocol.MethodWorkerExecute, params)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// DescribeTool returns the advertised capabilities of a tool process.
func (c *Client) DescribeTool(ctx context.Context) (protocol.ToolDescribeResult, error) {
	var result protocol.ToolDescribeResult
	raw, err := c.call(ctx, protocol.MethodToolDescribe, nil)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// ExecuteTool sends a tool.exec request and returns the tool result.
func (c *Client) ExecuteTool(ctx context.Context, params protocol.ToolExecParams) (protocol.ToolExecResult, error) {
	var result protocol.ToolExecResult
	raw, err := c.call(ctx, protocol.MethodToolExec, params)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	return c.codec.Close()
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	req, err := protocol.NewRequest(id, method, params)
	if err != nil {
		return nil, err
	}

	ch := make(chan callResult, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.codec.WriteMessage(req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, c.wrapConnectionError(err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case result := <-ch:
		return result.raw, result.err
	case <-c.done:
		return nil, c.connectionClosedError()
	}
}

func (c *Client) readLoop() {
	var readErr error
	defer func() {
		c.setCloseErr(readErr)
		close(c.done)
		c.drainPending()
		c.closeSubscriptions()
	}()
	for {
		raw, err := c.codec.ReadMessage()
		if err != nil {
			readErr = err
			return
		}
		c.handleMessage(raw)
	}
}

// drainPending wakes all goroutines waiting on pending RPC responses
// with an ErrConnectionClosed error, and clears the pending map.
func (c *Client) drainPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- callResult{err: ErrConnectionClosed}
		delete(c.pending, id)
	}
}

func (c *Client) setCloseErr(err error) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closeErr == nil {
		c.closeErr = err
	}
}

func (c *Client) connectionClosedError() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closeErr == nil {
		return ErrConnectionClosed
	}
	return c.wrapConnectionError(c.closeErr)
}

func (c *Client) wrapConnectionError(err error) error {
	if err == nil {
		return ErrConnectionClosed
	}
	if errors.Is(err, ErrConnectionClosed) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrConnectionClosed, err)
}

func (c *Client) unsubscribeRemote(sessionID string) {
	if sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = c.call(ctx, protocol.MethodEventUnsubscribe, protocol.EventUnsubscribeParams{
		SessionID: sessionID,
	})
}

func (c *Client) removeSubscription(sessionID string, ch chan model.Event) (bool, int) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	chans := c.subs[sessionID]
	for i, existing := range chans {
		if existing == ch {
			c.subs[sessionID] = append(chans[:i], chans[i+1:]...)
			remaining := len(c.subs[sessionID])
			if remaining == 0 {
				delete(c.subs, sessionID)
			}
			return true, remaining
		}
	}
	return false, len(c.subs[sessionID])
}

func (c *Client) closeSubscriptions() {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for sessionID, chans := range c.subs {
		for _, ch := range chans {
			close(ch)
		}
		delete(c.subs, sessionID)
	}
}

func (c *Client) handleMessage(raw json.RawMessage) {
	// Probe the raw JSON for "id" and "method" fields to classify the message
	// without relying on Go's lenient struct unmarshaling.
	var probe struct {
		ID     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return // unparseable — drop
	}

	switch {
	case probe.ID != nil && probe.Method == "":
		// Response: has "id", no "method"
		var resp protocol.Response
		if err := json.Unmarshal(raw, &resp); err == nil {
			c.handleResponse(resp)
		}
	case probe.ID == nil && probe.Method != "":
		// Notification: no "id", has "method"
		var notif protocol.Notification
		if err := json.Unmarshal(raw, &notif); err == nil {
			c.handleNotification(notif)
		}
	default:
		// Malformed (both id+method, or neither) — drop
	}
}
