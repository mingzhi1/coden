package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// registerBuiltins wires up the built-in RPC methods.
func (s *Server) registerBuiltins() {
	s.handlers[protocol.MethodSessionCreate] = s.handleSessionCreate
	s.handlers[protocol.MethodSessionList] = s.handleSessionList
	s.handlers[protocol.MethodSessionAttach] = s.handleSessionAttach
	s.handlers[protocol.MethodSessionDetach] = s.handleSessionDetach
	s.handlers[protocol.MethodWorkflowSubmit] = s.handleWorkflowSubmit
	s.handlers[protocol.MethodWorkflowCancel] = s.handleWorkflowCancel
	s.handlers[protocol.MethodWorkflowGet] = s.handleWorkflowGet
	s.handlers[protocol.MethodWorkflowList] = s.handleWorkflowList
	s.handlers[protocol.MethodWorkflowObjects] = s.handleWorkflowObjects
	s.handlers[protocol.MethodWorkflowObjectRead] = s.handleWorkflowObjectRead
	s.handlers[protocol.MethodMessageList] = s.handleMessageList
	s.handlers[protocol.MethodIntentGet] = s.handleIntentGet
	s.handlers[protocol.MethodWorkspaceChanges] = s.handleWorkspaceChanges
	s.handlers[protocol.MethodWorkspaceRead] = s.handleWorkspaceRead
	s.handlers[protocol.MethodWorkspaceWrite] = s.handleWorkspaceWrite
	s.handlers[protocol.MethodWorkspaceDiff] = s.handleWorkspaceDiff
	s.handlers[protocol.MethodCheckpointGet] = s.handleCheckpointGet
	s.handlers[protocol.MethodCheckpointList] = s.handleCheckpointList
	s.handlers[protocol.MethodEventSubscribe] = s.handleEventSubscribe
	s.handlers[protocol.MethodEventUnsubscribe] = s.handleEventUnsubscribe
	s.handlers[protocol.MethodSessionSnapshot] = s.handleSessionSnapshot
	s.handlers[protocol.MethodSessionRename] = s.handleSessionRename
	s.handlers[protocol.MethodPing] = handlePing
	s.handlers[protocol.MethodWorkflowWorkers] = s.handleWorkflowWorkers
	// M11-05: task management
	s.handlers[protocol.MethodTaskSkip] = s.handleTaskSkip
	s.handlers[protocol.MethodTaskUndo] = s.handleTaskUndo
}

func (s *Server) handleSessionCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.SessionCreateParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	sess, err := s.kernel.CreateSession(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}
	s.Broadcast(protocol.MethodEventPush, model.Event{
		Topic:     model.EventSessionCreated,
		SessionID: "",
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(model.SessionCreatedPayload{SessionID: sess.ID}),
	})
	return sess, nil
}

func (s *Server) handleSessionList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.SessionListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	sessions, err := s.kernel.ListSessions(ctx, p.Limit)
	if err != nil {
		return nil, err
	}
	return protocol.SessionListResult{Sessions: sessions}, nil
}

func (s *Server) handleSessionAttach(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.SessionAttachParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	if err := s.kernel.Attach(ctx, p.SessionID, p.ClientName, p.View); err != nil {
		return nil, err
	}
	// R-03: broadcast session.attached so other clients know a new viewer joined.
	s.Broadcast(protocol.MethodEventPush, model.Event{
		Topic:     model.EventSessionAttached,
		SessionID: p.SessionID,
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(model.SessionAttachedPayload{ClientName: p.ClientName, View: p.View}),
	})
	return protocol.SessionAttachResult{
		Status:     "attached",
		SessionID:  p.SessionID,
		ClientName: p.ClientName,
		View:       p.View,
	}, nil
}

func (s *Server) handleSessionDetach(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.SessionDetachParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	if err := s.kernel.Detach(ctx, p.SessionID, p.ClientName); err != nil {
		return nil, err
	}
	// R-05: 清理所有客户端对该 session 的订阅，不仅是发起 detach 的客户端。
	s.mu.Lock()
	for cc := range s.clients {
		cc.cancelSubscription(p.SessionID)
	}
	s.mu.Unlock()
	s.Broadcast(protocol.MethodEventPush, model.Event{
		Topic:     model.EventSessionDetached,
		SessionID: "",
		Timestamp: time.Now(),
		Payload:   model.EncodePayload(map[string]string{"session_id": p.SessionID}),
	})
	return protocol.SessionDetachResult{
		Status:     "detached",
		SessionID:  p.SessionID,
		ClientName: p.ClientName,
	}, nil
}

func (s *Server) handleWorkflowSubmit(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkflowSubmitParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" || p.Prompt == "" {
		return nil, protocol.InvalidParamsError("session_id and prompt are required")
	}
	workflowID, err := s.kernel.Submit(ctx, p.SessionID, p.Prompt)
	if err != nil {
		return nil, err
	}
	return protocol.WorkflowSubmitResult{
		Status:     "accepted",
		WorkflowID: workflowID,
	}, nil
}

func (s *Server) handleWorkflowCancel(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkflowCancelParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	if err := s.kernel.CancelWorkflow(ctx, p.SessionID, p.WorkflowID); err != nil {
		return nil, err
	}
	return protocol.WorkflowCancelResult{
		Status:     "canceled",
		SessionID:  p.SessionID,
		WorkflowID: p.WorkflowID,
	}, nil
}

func (s *Server) handleWorkflowGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkflowGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" || p.WorkflowID == "" {
		return nil, protocol.InvalidParamsError("session_id and workflow_id are required")
	}
	run, err := s.kernel.GetWorkflowRun(ctx, p.SessionID, p.WorkflowID)
	if err != nil {
		return nil, err
	}
	workers, _ := s.kernel.GetWorkflowWorkers(ctx, p.SessionID, p.WorkflowID)
	return protocol.WorkflowGetResult{WorkflowRun: run, Workers: workers}, nil
}

func (s *Server) handleWorkflowList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkflowListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	return s.kernel.ListWorkflowRuns(ctx, p.SessionID, p.Limit)
}

func (s *Server) handleWorkflowObjects(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkflowObjectsParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" || p.WorkflowID == "" {
		return nil, protocol.InvalidParamsError("session_id and workflow_id are required")
	}
	return s.kernel.ListWorkflowRunObjects(ctx, p.SessionID, p.WorkflowID)
}

func (s *Server) handleWorkflowObjectRead(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkflowObjectReadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" || p.WorkflowID == "" || p.ObjectID == "" {
		return nil, protocol.InvalidParamsError("session_id, workflow_id and object_id are required")
	}
	payload, err := s.kernel.ReadWorkflowRunObject(ctx, p.SessionID, p.WorkflowID, p.ObjectID)
	if err != nil {
		return nil, err
	}
	return protocol.WorkflowObjectReadResult{
		ObjectID: p.ObjectID,
		Payload:  payload,
	}, nil
}

// handleWorkflowWorkers returns the live worker state for an active workflow (R-06).
// If the workflow has already completed, workers will be an empty slice (not an error).
func (s *Server) handleWorkflowWorkers(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkflowWorkersParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" || p.WorkflowID == "" {
		return nil, protocol.InvalidParamsError("session_id and workflow_id are required")
	}
	workers, err := s.kernel.GetWorkflowWorkers(ctx, p.SessionID, p.WorkflowID)
	if err != nil {
		return nil, err
	}
	if workers == nil {
		workers = []model.WorkerState{}
	}
	return protocol.WorkflowWorkersResult{
		WorkflowID: p.WorkflowID,
		Workers:    workers,
	}, nil
}

func (s *Server) handleMessageList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.MessageListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}

	messages, err := s.kernel.ListMessages(ctx, p.SessionID, p.Limit)
	if err != nil {
		return nil, err
	}

	result := make([]protocol.Message, 0, len(messages))
	for _, msg := range messages {
		result = append(result, protocol.Message{
			ID:        msg.ID,
			SessionID: msg.SessionID,
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: msg.CreatedAt.UnixNano(),
		})
	}
	return result, nil
}

func (s *Server) handleWorkspaceChanges(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkspaceChangesParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}

	changes, err := s.kernel.WorkspaceChanges(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}

	result := protocol.WorkspaceChangesResult{
		SessionID: p.SessionID,
		Changes:   make([]protocol.WorkspaceChange, 0, len(changes)),
	}
	for _, change := range changes {
		result.Changes = append(result.Changes, protocol.WorkspaceChange{
			WorkflowID: change.WorkflowID,
			Path:       change.Path,
			Operation:  change.Operation,
		})
	}
	return result, nil
}

func (s *Server) handleCheckpointGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.CheckpointGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	return s.kernel.GetCheckpoint(ctx, p.SessionID, p.WorkflowID)
}

func (s *Server) handleCheckpointList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.CheckpointListParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	return s.kernel.ListCheckpoints(ctx, p.SessionID, p.Limit)
}

// handleEventSubscribe starts pushing events for a session
// to the calling client as notifications. The subscription
// lives until the client disconnects.
func (s *Server) handleEventSubscribe(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.EventSubscribeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	cc := clientConnFromContext(ctx)
	if cc == nil {
		return nil, fmt.Errorf("client connection is required")
	}
	// Use the connection-level context (not the per-request context) so the
	// subscription goroutine survives after the handler returns.
	subCtx, subCancel := context.WithCancel(cc.connCtx)
	sub := subscription{
		cancel: subCancel,
		done:   make(chan struct{}),
	}
	if !cc.addSubscription(p.SessionID, sub) {
		subCancel()
		return protocol.EventSubscribeResult{Status: "subscribed", SessionID: p.SessionID}, nil
	}

	var events <-chan model.Event
	var cancel func()
	if p.SinceSeq > 0 {
		events, cancel = s.kernel.SubscribeSince(p.SessionID, p.SinceSeq)
	} else {
		events, cancel = s.kernel.Subscribe(p.SessionID)
	}

	go func() {
		defer func() {
			cancel()
			cc.dropSubscription(p.SessionID)
			close(sub.done)
		}()
		// R-08: send a heartbeat ping every 30 s to keep the connection
		// alive and let both sides detect a dead peer promptly.
		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-subCtx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				s.notifyClient(cc, protocol.MethodEventPush, ev)
			case <-heartbeat.C:
				s.notifyClient(cc, protocol.MethodPing, protocol.PingResult{Status: "heartbeat"})
			}
		}
	}()

	return protocol.EventSubscribeResult{Status: "subscribed", SessionID: p.SessionID}, nil
}

func (s *Server) handleEventUnsubscribe(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.EventUnsubscribeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	cc := clientConnFromContext(ctx)
	if cc == nil {
		return nil, fmt.Errorf("client connection is required")
	}
	status := "not_subscribed"
	if cc.cancelSubscription(p.SessionID) {
		status = "unsubscribed"
	}
	return protocol.EventUnsubscribeResult{Status: status, SessionID: p.SessionID}, nil
}

func (s *Server) handleSessionRename(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.SessionRenameParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	sess, err := s.kernel.RenameSession(ctx, p.SessionID, p.Name)
	if err != nil {
		return nil, err
	}
	return protocol.SessionRenameResult{Session: sess}, nil
}

func (s *Server) handleSessionSnapshot(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.SessionSnapshotParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	snap, err := s.kernel.Snapshot(ctx, p.SessionID, p.MessageLimit)
	if err != nil {
		return nil, err
	}

	msgs := make([]protocol.Message, 0, len(snap.Messages))
	for _, m := range snap.Messages {
		msgs = append(msgs, protocol.Message{
			ID:        m.ID,
			SessionID: m.SessionID,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.UnixNano(),
		})
	}
	changes := make([]protocol.WorkspaceChange, 0, len(snap.WorkspaceChanges))
	for _, c := range snap.WorkspaceChanges {
		changes = append(changes, protocol.WorkspaceChange{
			WorkflowID: c.WorkflowID,
			Path:       c.Path,
			Operation:  c.Operation,
		})
	}
	return protocol.SessionSnapshotResult{
		SessionID:        snap.SessionID,
		Messages:         msgs,
		ActiveWorkflow:   snap.ActiveWorkflow,
		LatestCheckpoint: snap.LatestCheckpoint,
		LatestIntent:     snap.LatestIntent,
		WorkspaceChanges: changes,
		LastEventSeq:     snap.LastEventSeq,
	}, nil
}

func handlePing(_ context.Context, _ json.RawMessage) (any, error) {
	return protocol.PingResult{Status: "pong"}, nil
}

func (s *Server) handleIntentGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.IntentGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	spec, err := s.kernel.GetLatestIntent(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}
	return protocol.IntentGetResult{
		IntentID:        spec.ID,
		SessionID:       spec.SessionID,
		Goal:            spec.Goal,
		Kind:            spec.Kind,
		SuccessCriteria: spec.SuccessCriteria,
		CreatedAt:       spec.CreatedAt.UnixNano(),
	}, nil
}

func (s *Server) handleWorkspaceRead(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkspaceReadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" || p.Path == "" {
		return nil, protocol.InvalidParamsError("session_id and path are required")
	}
	data, err := s.kernel.WorkspaceRead(ctx, p.SessionID, p.Path)
	if err != nil {
		return nil, err
	}
	return protocol.WorkspaceReadResult{
		Path:    p.Path,
		Content: string(data),
	}, nil
}

func (s *Server) handleWorkspaceWrite(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkspaceWriteParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" || p.Path == "" {
		return nil, protocol.InvalidParamsError("session_id and path are required")
	}
	_, err := s.kernel.WorkspaceWrite(ctx, p.SessionID, p.Path, []byte(p.Content))
	if err != nil {
		return nil, err
	}
	return protocol.WorkspaceWriteResult{
		Path:   p.Path,
		Status: "written",
	}, nil
}

func (s *Server) handleWorkspaceDiff(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.WorkspaceDiffParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	changes, err := s.kernel.WorkspaceChanges(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}
	diffs := make([]protocol.FileDiff, 0, len(changes))
	for _, c := range changes {
		diffs = append(diffs, protocol.FileDiff{
			WorkflowID: c.WorkflowID,
			Path:       c.Path,
			Operation:  c.Operation,
		})
	}
	return protocol.WorkspaceDiffResult{
		SessionID: p.SessionID,
		Diffs:     diffs,
	}, nil
}

// M11-05: handleTaskSkip skips a task in the active workflow.
func (s *Server) handleTaskSkip(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.TaskSkipParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	if err := s.kernel.SkipTask(ctx, p.SessionID, p.TaskID); err != nil {
		return nil, err
	}
	taskID := p.TaskID
	if taskID == "" {
		taskID = "next"
	}
	return protocol.TaskSkipResult{Status: "skipped", TaskID: taskID}, nil
}

// M11-05: handleTaskUndo reverses the last queue operation.
func (s *Server) handleTaskUndo(ctx context.Context, raw json.RawMessage) (any, error) {
	var p protocol.TaskUndoParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if p.SessionID == "" {
		return nil, protocol.InvalidParamsError("session_id is required")
	}
	undone, err := s.kernel.UndoTask(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}
	return protocol.TaskUndoResult{Status: "undone", Undone: undone}, nil
}
