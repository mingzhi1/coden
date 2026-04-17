package kernel

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/taskqueue"
)

// CreateSession 创建新会话。
func (k *Kernel) CreateSession(_ context.Context, sessionID string) (model.Session, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.createSessionLocked(sessionID)
}

// ListSessions 返回最近创建的会话列表。
func (k *Kernel) ListSessions(_ context.Context, limit int) ([]model.Session, error) {
	return k.sessionStore.List(limit), nil
}

// RenameSession 设置会话的人类可读标签 (R-07)。
func (k *Kernel) RenameSession(_ context.Context, sessionID, name string) (model.Session, error) {
	if sessionID == "" {
		return model.Session{}, fmt.Errorf("sessionID is required")
	}
	if err := k.sessionStore.Rename(sessionID, name); err != nil {
		return model.Session{}, err
	}
	sess, ok := k.sessionStore.Get(sessionID)
	if !ok {
		return model.Session{}, fmt.Errorf("session not found after rename: %s", sessionID)
	}
	return sess, nil
}

// GetCheckpoint 返回指定工作流的 checkpoint。
func (k *Kernel) GetCheckpoint(_ context.Context, sessionID, workflowID string) (model.CheckpointResult, error) {
	if sessionID == "" {
		return model.CheckpointResult{}, fmt.Errorf("sessionID is required")
	}

	result, ok := k.checkpoints.Get(sessionID, workflowID)
	if !ok {
		return model.CheckpointResult{}, fmt.Errorf("checkpoint not found")
	}

	return result, nil
}

// ListCheckpoints 返回会话的 checkpoint 列表。
func (k *Kernel) ListCheckpoints(_ context.Context, sessionID string, limit int) ([]model.CheckpointResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}
	return k.checkpoints.List(sessionID, limit), nil
}

// ListMessages 返回会话的消息列表。
func (k *Kernel) ListMessages(_ context.Context, sessionID string, limit int) ([]model.Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}
	return k.messages.List(sessionID, limit), nil
}

// GetWorkflowRun 返回指定的工作流运行记录。
func (k *Kernel) GetWorkflowRun(_ context.Context, sessionID, workflowID string) (model.WorkflowRun, error) {
	if sessionID == "" {
		return model.WorkflowRun{}, fmt.Errorf("sessionID is required")
	}
	if workflowID == "" {
		return model.WorkflowRun{}, fmt.Errorf("workflowID is required")
	}
	item, ok := k.turns.Get(workflowID)
	if !ok || item.SessionID != sessionID {
		return model.WorkflowRun{}, fmt.Errorf("workflow run not found")
	}
	return item, nil
}

// ListWorkflowRuns 返回工作流运行列表。
func (k *Kernel) ListWorkflowRuns(_ context.Context, sessionID string, limit int) ([]model.WorkflowRun, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}
	items := k.turns.ListBySession(sessionID, limit)
	out := make([]model.WorkflowRun, len(items))
	copy(out, items)
	return out, nil
}

// ListWorkflowRunObjects 返回工作流运行相关的对象列表。
func (k *Kernel) ListWorkflowRunObjects(_ context.Context, sessionID, workflowID string) ([]model.Object, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}
	if workflowID == "" {
		return nil, fmt.Errorf("workflowID is required")
	}
	item, ok := k.turns.Get(workflowID)
	if !ok || item.SessionID != sessionID {
		return nil, fmt.Errorf("workflow run not found")
	}
	return k.objects.ListByTurn(workflowID), nil
}

// ReadWorkflowRunObject 读取工作流运行对象的 payload。
func (k *Kernel) ReadWorkflowRunObject(_ context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}
	if workflowID == "" {
		return nil, fmt.Errorf("workflowID is required")
	}
	if objectID == "" {
		return nil, fmt.Errorf("objectID is required")
	}
	item, ok := k.turns.Get(workflowID)
	if !ok || item.SessionID != sessionID {
		return nil, fmt.Errorf("workflow run not found")
	}
	found := false
	for _, object := range k.objects.ListByTurn(workflowID) {
		if object.ID == objectID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("workflow object not found")
	}
	raw, err := k.objects.ReadPayload(objectID)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// WorkspaceChanges 返回会话的工作区变更记录。
func (k *Kernel) WorkspaceChanges(_ context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}
	k.mu.Lock()
	defer k.mu.Unlock()

	changes := k.workspaceChanges[sessionID]
	if len(changes) == 0 {
		return nil, nil
	}
	out := make([]model.WorkspaceChangedPayload, len(changes))
	copy(out, changes)
	return out, nil
}

// Snapshot 返回会话的原子时间点视图 (R-02)。
// 它首先读取当前事件总线序列号，然后在持有 k.mu 的同时复制正在运行的工作流状态和工作区变更，
// 最后在锁外执行存储读取。结果是 ActiveWorkflow / WorkspaceChanges 彼此一致，
// 且返回的 LastEventSeq <= 调用者将错过的第一个事件的 seq — 使用 since_seq=LastEventSeq 订阅来关闭间隙 (R-01)。
func (k *Kernel) Snapshot(_ context.Context, sessionID string, messageLimit int) (model.SessionSnapshot, error) {
	if sessionID == "" {
		return model.SessionSnapshot{}, fmt.Errorf("sessionID is required")
	}
	if messageLimit <= 0 {
		messageLimit = 50
	}

	// Capture event seq before the lock so we never miss an event.
	lastSeq := k.events.Seq()

	// Under k.mu: snapshot the mutable in-flight state atomically.
	k.mu.Lock()
	activeWorkflowID := k.activeSessionWorkflows[sessionID]
	rawChanges := k.workspaceChanges[sessionID]
	changes := make([]model.WorkspaceChangedPayload, len(rawChanges))
	copy(changes, rawChanges)
	k.mu.Unlock()

	// Store reads are individually thread-safe; no kernel lock needed.
	msgs := k.messages.List(sessionID, messageLimit)

	var activeWorkflow *model.WorkflowRun
	if activeWorkflowID != "" {
		if run, ok := k.turns.Get(activeWorkflowID); ok && run.SessionID == sessionID {
			cp := run
			activeWorkflow = &cp
		}
	}

	var latestCheckpoint *model.CheckpointResult
	if cp, ok := k.checkpoints.Latest(sessionID); ok {
		cpCopy := cp
		latestCheckpoint = &cpCopy
	}

	var latestIntent *model.IntentSpec
	if spec, ok := k.intents.Latest(sessionID); ok {
		specCopy := spec
		latestIntent = &specCopy
	}

	return model.SessionSnapshot{
		SessionID:        sessionID,
		Messages:         msgs,
		ActiveWorkflow:   activeWorkflow,
		LatestCheckpoint: latestCheckpoint,
		LatestIntent:     latestIntent,
		WorkspaceChanges: changes,
		LastEventSeq:     lastSeq,
	}, nil
}

// GetWorkflowWorkers 返回指定工作流的实时 worker 状态快照 (R-06)。
// 当工作流不再处于活动状态时返回 nil（非错误）— 调用者将空切片解释为 "工作流已完成"。
func (k *Kernel) GetWorkflowWorkers(_ context.Context, sessionID, workflowID string) ([]model.WorkerState, error) {
	if sessionID == "" || workflowID == "" {
		return nil, fmt.Errorf("sessionID and workflowID are required")
	}
	k.mu.Lock()
	aw := k.activeWorkflows[workflowID]
	k.mu.Unlock()
	if aw == nil || aw.sessionID != sessionID {
		return nil, nil
	}
	aw.mu.Lock()
	out := make([]model.WorkerState, len(aw.workers))
	copy(out, aw.workers)
	aw.mu.Unlock()
	return out, nil
}

// GetLatestIntent 返回会话的最新 intent。
func (k *Kernel) GetLatestIntent(_ context.Context, sessionID string) (model.IntentSpec, error) {
	if sessionID == "" {
		return model.IntentSpec{}, fmt.Errorf("sessionID is required")
	}
	spec, ok := k.intents.Latest(sessionID)
	if !ok {
		return model.IntentSpec{}, fmt.Errorf("no intent found for session %s", sessionID)
	}
	return spec, nil
}

// WorkspaceRead 从工作区读取文件。
func (k *Kernel) WorkspaceRead(_ context.Context, sessionID, path string) ([]byte, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("sessionID is required")
	}
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	return k.workspace.Read(path)
}

// WorkspaceWrite 写入文件到工作区并记录变更。
func (k *Kernel) WorkspaceWrite(_ context.Context, sessionID, path string, content []byte) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sessionID is required")
	}
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	absPath, err := k.workspace.Write(path, content)
	if err != nil {
		return "", err
	}
	// KA-03: Look up the active workflow for this session so that the change
	// is correctly attributed in TurnSummary and ChangedFiles tracking.
	k.mu.Lock()
	activeWfID := k.activeSessionWorkflows[sessionID]
	k.mu.Unlock()
	change := model.WorkspaceChangedPayload{
		WorkflowID: activeWfID,
		Path:       path,
		Operation:  "write",
	}
	k.recordWorkspaceChange(sessionID, change)
	k.events.Emit(sessionID, model.EventWorkspaceChanged, change)
	return absPath, nil
}

// Attach 将客户端连接到会话。
func (k *Kernel) Attach(_ context.Context, sessionID, clientName, view string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if sessionID == "" {
		return fmt.Errorf("sessionID is required")
	}
	if clientName == "" {
		clientName = "unknown"
	}
	if view == "" {
		view = "default"
	}
	if err := k.ensureSessionLocked(sessionID); err != nil {
		return err
	}

	if _, ok := k.sessions[sessionID]; !ok {
		k.sessions[sessionID] = make(map[string]string)
	}
	k.sessions[sessionID][clientName] = view
	k.events.Emit(sessionID, model.EventSessionAttached, model.SessionAttachedPayload{
		ClientName: clientName,
		View:       view,
	})
	return nil
}

// Detach 将客户端从会话分离。
func (k *Kernel) Detach(_ context.Context, sessionID, clientName string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if sessionID == "" {
		return fmt.Errorf("sessionID is required")
	}
	if clientName == "" {
		clientName = "unknown"
	}

	if clients, ok := k.sessions[sessionID]; ok {
		delete(clients, clientName)
		if len(clients) == 0 {
			delete(k.sessions, sessionID)
		}
	}
	k.events.Emit(sessionID, model.EventSessionDetached, model.SessionDetachedPayload{
		ClientName: clientName,
	})
	return nil
}

// CancelWorkflow 取消指定会话的活动工作流。
func (k *Kernel) CancelWorkflow(_ context.Context, sessionID, workflowID string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if sessionID == "" {
		return fmt.Errorf("sessionID is required")
	}

	if workflowID != "" {
		active, ok := k.activeWorkflows[workflowID]
		if !ok || active.sessionID != sessionID {
			return fmt.Errorf("active workflow not found")
		}
		// Bump generation so the cancelled workflow's finishWorkflow sees a
		// mismatch and skips session-level cleanup that could collide with a
		// newer workflow registered for the same session.
		k.workflowGeneration[sessionID]++
		active.cancel()
		return nil
	}

	for _, active := range k.activeWorkflows {
		if active.sessionID != sessionID {
			continue
		}
		k.workflowGeneration[sessionID]++
		active.cancel()
		return nil
	}

	return fmt.Errorf("active workflow not found")
}

// SkipTask skips a task in the active workflow's TaskQueue (M11-05).
// taskID identifies which task to skip. If empty, the next planned task is skipped.
func (k *Kernel) SkipTask(_ context.Context, sessionID, taskID string) error {
	if sessionID == "" {
		return fmt.Errorf("sessionID is required")
	}
	queue, workflowID, err := k.activeQueue(sessionID)
	if err != nil {
		return err
	}
	if taskID == "" {
		// Skip the next planned task.
		remaining := queue.Remaining()
		if len(remaining) == 0 {
			return fmt.Errorf("no remaining tasks to skip")
		}
		taskID = remaining[0].ID
	}
	if err := queue.Skip(taskID); err != nil {
		return fmt.Errorf("skip task: %w", err)
	}
	k.emitTasksUpdated(sessionID, workflowID, queue.Snapshot())
	k.events.Emit(sessionID, model.EventWorkflowTaskSkipped, model.TaskSkippedPayload{
		TaskID:     taskID,
		WorkflowID: workflowID,
	})
	return nil
}

// UndoTask reverses the most recent append/skip/remove on the active workflow (M11-05).
func (k *Kernel) UndoTask(_ context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("sessionID is required")
	}
	queue, workflowID, err := k.activeQueue(sessionID)
	if err != nil {
		return "", err
	}
	op, err := queue.Undo()
	if err != nil {
		return "", fmt.Errorf("undo: %w", err)
	}
	k.emitTasksUpdated(sessionID, workflowID, queue.Snapshot())
	return string(op.Kind) + ":" + op.TaskID, nil
}

// activeQueue returns the TaskQueue for the currently active workflow in the session.
func (k *Kernel) activeQueue(sessionID string) (*taskqueue.Queue, string, error) {
	k.mu.Lock()
	workflowID := k.activeSessionWorkflows[sessionID]
	aw := k.activeWorkflows[workflowID]
	k.mu.Unlock()

	if aw == nil || aw.sessionID != sessionID {
		return nil, "", fmt.Errorf("no active workflow for session %s", sessionID)
	}
	aw.mu.Lock()
	q := aw.queue
	aw.mu.Unlock()
	if q == nil {
		return nil, "", fmt.Errorf("workflow has not reached the task execution phase yet")
	}
	return q, workflowID, nil
}
