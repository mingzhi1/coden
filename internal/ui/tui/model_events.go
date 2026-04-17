package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"
)

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitForEvent(m.sessionID, m.eventStream), textarea.Blink}
	if m.snapshotLoader != nil {
		cmds = append(cmds, m.snapshotLoader())
	}
	if strings.TrimSpace(m.ti.Value()) != "" && m.submitter != nil {
		prompt := m.ti.Value()
		cmds = append(cmds, func() tea.Msg {
			return startSubmitMsg{Prompt: prompt}
		})
	}
	return tea.Batch(cmds...)
}

func waitForEvent(sessionID string, ch <-chan model.Event) tea.Cmd {
	if ch == nil {
		return nil
	}

	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return StreamClosedMsg{}
		}
		return EventMsg{SessionID: sessionID, Event: event}
	}
}

func (m *Model) beginSubmit(prompt string) tea.Cmd {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || m.submitter == nil || !m.acceptsInput() {
		return nil
	}

	// M11-05 / M12-03h: Handle slash commands locally.
	if strings.HasPrefix(prompt, "/") {
		return m.handleSlashCommand(prompt)
	}

	m.status = "running"
	m.spinnerActive = true
	m.err = nil
	m.checkpoint = nil
	m.workers = nil
	m.currentStep = ""
	m.todos = nil
	m.changed = nil
	m.activeToolName = ""
	m.toolCallCount = 0
	m.lastSubmittedPrompt = prompt
	m.workflowStartedAt = time.Now()
	// Record prompt in history for up-arrow recall.
	m.promptHistory = append(m.promptHistory, prompt)
	if len(m.promptHistory) > 50 {
		m.promptHistory = m.promptHistory[len(m.promptHistory)-50:]
	}
	m.promptHistoryIdx = -1
	m.promptDraft = ""
	m.ti.SetValue("")
	m.ti.Blur()
	m.focus = focusChat
	m.followChat = true
	m.chatScroll = m.maxChatScroll()
	m.alert = nil

	return tea.Batch(m.submitter(prompt), m.spinner.Tick)
}

// handleSlashCommand processes TUI-local slash commands (M11-05, M12-03h).
func (m *Model) handleSlashCommand(prompt string) tea.Cmd {
	parts := strings.Fields(prompt)
	cmd := strings.ToLower(parts[0])

	m.ti.SetValue("")

	switch cmd {
	case "/skip":
		taskID := ""
		if len(parts) > 1 {
			taskID = parts[1]
		}
		label := "next task"
		if taskID != "" {
			label = taskID
		}
		m.chatLines = append(m.chatLines, chatMetaLine("system", fmt.Sprintf("skipping %s...", label)))
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		if m.taskSkipper != nil {
			return m.taskSkipper(taskID)
		}
		m.chatLines = append(m.chatLines, chatMetaLine("warn", "skip not available (no active workflow)"))
		return nil

	case "/undo":
		m.chatLines = append(m.chatLines, chatMetaLine("system", "undoing last task operation..."))
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		if m.taskUndoer != nil {
			return m.taskUndoer()
		}
		m.chatLines = append(m.chatLines, chatMetaLine("warn", "undo not available (no active workflow)"))
		return nil

	case "/compact":
		m.chatLines = append(m.chatLines, chatMetaLine("system", "context compaction is handled automatically by the agentic loop"))
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return nil

	case "/clear":
		m.chatLines = nil
		m.chatScroll = 0
		m.followChat = true
		m.chatLines = append(m.chatLines, chatMetaLine("system", "chat cleared"))
		return nil

	case "/status":
		m.chatLines = append(m.chatLines, m.buildStatusLines()...)
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return nil

	case "/rename":
		if len(parts) < 2 {
			m.chatLines = append(m.chatLines, chatMetaLine("warn", "usage: /rename <new-name>"))
			return nil
		}
		newName := strings.Join(parts[1:], " ")
		m.chatLines = append(m.chatLines, chatMetaLine("system", fmt.Sprintf("renaming session to %q...", newName)))
		if m.sessionRenamer != nil {
			return m.sessionRenamer(newName)
		}
		m.chatLines = append(m.chatLines, chatMetaLine("warn", "rename not available"))
		return nil

	case "/help":
		m.alert = &alertState{level: "info", title: "Help", lines: m.helpLines(), footer: "esc dismiss"}
		return nil

	default:
		m.chatLines = append(m.chatLines, chatMetaLine("warn", fmt.Sprintf("unknown command: %s (try /help)", cmd)))
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return nil
	}
}

// buildStatusLines generates inline status summary lines.
func (m *Model) buildStatusLines() []string {
	lines := []string{chatMetaLine("system", "── status ──")}
	lines = append(lines, chatMetaLine("system", fmt.Sprintf("session: %s  status: %s", m.sessionID, m.status)))
	if m.activeWorkflowID != "" {
		lines = append(lines, chatMetaLine("system", fmt.Sprintf("workflow: %s", m.activeWorkflowID)))
	}
	if !m.workflowStartedAt.IsZero() && m.status == "running" {
		elapsed := time.Since(m.workflowStartedAt).Truncate(time.Second)
		lines = append(lines, chatMetaLine("system", fmt.Sprintf("elapsed: %s", elapsed)))
	}
	if len(m.workers) > 0 {
		running, done := 0, 0
		for _, w := range m.workers {
			if w.Status == "running" {
				running++
			} else {
				done++
			}
		}
		lines = append(lines, chatMetaLine("system", fmt.Sprintf("workers: %d running, %d done", running, done)))
	}
	if len(m.todos) > 0 {
		doneT, totalT := 0, len(m.todos)
		for _, t := range m.todos {
			if t.Status == "passed" || t.Status == "done" {
				doneT++
			}
		}
		lines = append(lines, chatMetaLine("system", fmt.Sprintf("tasks: %d/%d completed", doneT, totalT)))
	}
	if len(m.changed) > 0 {
		lines = append(lines, chatMetaLine("system", fmt.Sprintf("changed files: %d", len(m.changed))))
	}
	if m.checkpoint != nil {
		lines = append(lines, chatMetaLine("system", fmt.Sprintf("checkpoint: %s (artifacts=%d)", m.checkpoint.Status, len(m.checkpoint.ArtifactPaths))))
	}
	lines = append(lines, chatMetaLine("system", fmt.Sprintf("chat lines: %d  history turns: %d", len(m.chatLines), len(m.turns))))
	return lines
}

func (m *Model) applyEvent(event model.Event) {
	switch event.Topic {
	case model.EventWorkerStarted, model.EventWorkerFinished:
		m.updateWorker(event)
	case model.EventWorkflowTasks:
		m.updateTasks(event)
	case model.EventToolStarted:
		m.markToolStarted(event)
		if p, ok := decodeEventPayload[model.ToolStartedPayload](event); ok {
			m.activeToolName = p.Tool
			m.toolCallCount++
		}
	case model.EventToolFinished:
		m.markToolFinished(event)
		m.activeToolName = ""
	case model.EventWorkspaceChanged:
		m.markWorkspaceChanged(event)
	case model.EventWorkflowStepUpdate:
		if p, ok := decodeEventPayload[model.WorkflowStepUpdatedPayload](event); ok {
			if p.Status == "running" {
				m.currentStep = p.Step
			} else if p.Status == "done" || p.Status == "skipped" {
				m.currentStep = ""
			}
		}
	case model.EventWorkflowStarted:
		if p, ok := decodeEventPayload[model.WorkflowStartedPayload](event); ok && p.WorkflowID != "" {
			m.latestRun = &model.WorkflowRun{
				WorkflowID: p.WorkflowID,
				SessionID:  m.sessionID,
				Status:     "running",
			}
			// If the workflow was submitted from another client (not via local
			// beginSubmit), promote status to running and start the timer.
			if m.status != "running" {
				m.status = "running"
				m.spinnerActive = true
				m.workflowStartedAt = time.Now()
			}
		}
	case model.EventCheckpointUpdated:
		if p, ok := decodeEventPayload[model.CheckpointUpdatedPayload](event); ok {
			if m.latestRun != nil && p.WorkflowID == m.latestRun.WorkflowID {
				m.latestRun.Status = p.Status
			} else if m.latestRun != nil && m.latestRun.Status == "running" {
				m.latestRun.Status = p.Status
			}
		}
	case model.EventWorkflowRetry:
		m.applyWorkflowRetry(event)
	case model.EventWorkflowFailed:
		if p, ok := decodeEventPayload[model.WorkflowFailedPayload](event); ok {
			m.applyWorkflowTerminated(p.WorkflowID)
		} else {
			m.applyWorkflowTerminated("")
		}
	case model.EventWorkflowCanceled:
		if p, ok := decodeEventPayload[model.WorkflowCanceledPayload](event); ok {
			m.applyWorkflowTerminated(p.WorkflowID)
		} else {
			m.applyWorkflowTerminated("")
		}
	case model.EventWorkerMessage:
		if p, ok := decodeEventPayload[model.WorkerMessagePayload](event); ok && p.WorkerID != "" {
			// Mark the worker with a warn/error badge if the message kind warrants it.
			if p.Kind == "warn" || p.Kind == "error" {
				for i := range m.workers {
					if m.workers[i].ID == p.WorkerID {
						m.workers[i].Status = p.Kind // "warn" or "error" as terminal badge
						break
					}
				}
			}
		}
	default:
		// Known events that don't need state tracking:
		// message.created, workflow.step_updated, worker.message
	}
}

func (m *Model) applyLoadedObjectDetails(items []api.ObjectDetail) {
	for _, item := range items {
		if item.ToolCallID == "" && item.Path == "" {
			continue
		}
		idx := -1
		if item.ToolCallID != "" {
			idx = m.findChangedByToolCallID(item.ToolCallID)
		}
		if idx == -1 && item.Path != "" {
			for i := range m.changed {
				if m.changed[i].Path == item.Path {
					idx = i
					break
				}
			}
		}
		if idx == -1 {
			name := item.Tool
			if item.Path != "" {
				name = filepath.Base(item.Path)
			}
			m.changed = append(m.changed, changeItem{
				Path:       item.Path,
				Name:       name,
				Status:     item.Status,
				Summary:    item.Summary,
				Detail:     item.Detail,
				Preview:    item.Preview,
				ExitCode:   item.ExitCode,
				Count:      1,
				Tool:       item.Tool,
				ToolCallID: item.ToolCallID,
			})
			m.clampChangedSelection()
			continue
		}

		if item.Path != "" {
			m.changed[idx].Path = item.Path
			m.changed[idx].Name = filepath.Base(item.Path)
		}
		mergeChangeItem(&m.changed[idx], changeItem{
			Status:     item.Status,
			Summary:    item.Summary,
			Detail:     item.Detail,
			ExitCode:   item.ExitCode,
			Tool:       item.Tool,
			ToolCallID: item.ToolCallID,
		})
		if item.Preview != "" {
			m.changed[idx].Preview = item.Preview
		}
	}
}

func (m *Model) applySessionSnapshot(snapshot api.SessionSnapshot) {
	m.snapshotLoaded = true
	if len(m.chatLines) == 0 {
		for _, msg := range snapshot.Messages {
			role := strings.TrimSpace(msg.Role)
			content := strings.TrimSpace(msg.Content)
			if role == "" || content == "" {
				continue
			}
			m.chatLines = append(m.chatLines, renderMessageBlock(role, content)...)
			m.appendTurnFromMessage(role, content)
		}
	}

	if snapshot.LatestCheckpoint != nil {
		cp := *snapshot.LatestCheckpoint
		m.checkpoint = &cp
		if cp.WorkflowID != "" {
			m.activeWorkflowID = cp.WorkflowID
		}
		for _, path := range cp.ArtifactPaths {
			m.ensureChangedPath(path, "written")
		}
	}

	if snapshot.LatestRun != nil {
		run := *snapshot.LatestRun
		m.latestRun = &run
	}
	if snapshot.LatestIntent != nil {
		intent := *snapshot.LatestIntent
		m.latestIntent = &intent
	}

	for _, change := range snapshot.Changes {
		status := change.Operation
		if status == "" {
			status = "updated"
		}
		m.ensureChangedPath(change.Path, status)
	}

	if snapshot.LatestWorkflowID != "" && m.activeWorkflowID == "" {
		m.activeWorkflowID = snapshot.LatestWorkflowID
	}
	if len(snapshot.ObjectDetails) > 0 {
		m.applyLoadedObjectDetails(snapshot.ObjectDetails)
	}
	if m.followChat {
		m.chatScroll = m.maxChatScroll()
	}
}


func (m *Model) updateTasks(event model.Event) {
	payload, ok := decodeEventPayload[model.WorkflowTasksUpdatedPayload](event)
	if !ok {
		return
	}

	next := make([]todoItem, 0, len(payload.Tasks))
	for _, task := range payload.Tasks {
		next = append(next, todoItem{
			ID:     task.ID,
			Name:   task.Title,
			Status: strings.ToLower(task.Status),
		})
	}
	m.todos = next
}

func (m *Model) updateWorker(event model.Event) {
	var (
		id         string
		role       string
		step       string
		toolCallID string
		durationMS int64
	)

	switch event.Topic {
	case model.EventWorkerStarted:
		payload, ok := decodeEventPayload[model.WorkerStartedPayload](event)
		if !ok || payload.WorkerID == "" {
			return
		}
		id = payload.WorkerID
		role = payload.WorkerRole
		step = payload.Step
	case model.EventWorkerFinished:
		payload, ok := decodeEventPayload[model.WorkerFinishedPayload](event)
		if !ok || payload.WorkerID == "" {
			return
		}
		id = payload.WorkerID
		role = payload.WorkerRole
		step = payload.Step
		toolCallID = payload.ToolCallID
		durationMS = payload.DurationMS
	default:
		return
	}

	idx := -1
	for i := range m.workers {
		if m.workers[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		m.workers = append(m.workers, workerItem{ID: id})
		idx = len(m.workers) - 1
	}

	item := &m.workers[idx]
	if role != "" {
		item.Role = role
	}
	if step != "" {
		item.Step = step
	}
	if toolCallID != "" {
		item.ToolCallID = toolCallID
	}

	switch event.Topic {
	case model.EventWorkerStarted:
		item.Status = "running"
	case model.EventWorkerFinished:
		item.Status = "done"
		item.DurationMS = durationMS
		item.HasDuration = true
	}
}

func (m *Model) markToolStarted(event model.Event) {
	payload, ok := decodeEventPayload[model.ToolStartedPayload](event)
	if !ok || payload.ToolCallID == "" {
		return
	}
	toolCallID := payload.ToolCallID

	idx := m.findChangedByToolCallID(toolCallID)
	if idx == -1 {
		name := payload.Tool
		if name == "" {
			name = toolCallID
		}
		m.changed = append(m.changed, changeItem{
			Name:       name,
			Status:     "running",
			Count:      1,
			Tool:       payload.Tool,
			ToolCallID: toolCallID,
		})
		m.clampChangedSelection()
		return
	}

	item := &m.changed[idx]
	item.Status = "running"
	if payload.Tool != "" {
		item.Tool = payload.Tool
	}
}

func (m *Model) markToolFinished(event model.Event) {
	payload, ok := decodeEventPayload[model.ToolFinishedPayload](event)
	if !ok {
		return
	}
	toolCallID := payload.ToolCallID
	path := payload.Path
	status := payload.Status
	if status == "" {
		status = "written"
	}
	if strings.EqualFold(status, "denied") {
		title := "Permission Required"
		items := []overlayItem{
			{kind: "section", text: "REQUEST BLOCKED"},
			{kind: "kv", text: "tool: " + payload.Tool},
		}
		if strings.TrimSpace(payload.Path) != "" {
			items = append(items, overlayItem{kind: "kv", text: "target: " + payload.Path})
		}
		detail := strings.TrimSpace(payload.Detail)
		if detail != "" {
			items = append(items,
				overlayItem{kind: "spacer"},
				overlayItem{kind: "section", text: "REASON"},
				overlayItem{kind: "warn", text: truncateSingleLine(detail, 180)},
			)
		}
		if strings.Contains(detail, "--allow-shell") {
			items = append(items,
				overlayItem{kind: "spacer"},
				overlayItem{kind: "section", text: "NEXT"},
				overlayItem{kind: "action", text: "[do] restart coden with --allow-shell"},
				overlayItem{kind: "action", text: "[do] rerun the task after reconnecting"},
			)
		}
		items = append(items,
			overlayItem{kind: "spacer"},
			overlayItem{kind: "section", text: "ACTIONS"},
			overlayItem{kind: "disabled", text: "[unavailable] approve in current session"},
			overlayItem{kind: "action", text: "[open] focus chat panel", action: "focus_chat"},
			overlayItem{kind: "action", text: "[do] dismiss overlay", action: "dismiss"},
		)
		// 添加原因说明，解释为什么不能批准
		items = append(items,
			overlayItem{kind: "spacer"},
			overlayItem{kind: "kv-muted", text: "reason: approval flow is not wired in this session"},
		)
		m.alert = &alertState{
			level:  "warning",
			title:  title,
			items:  items,
			footer: "j/k move  enter select  esc dismiss",
		}
		// Also add a visible chat line so the user sees the denial in the stream
		denyLine := chatMetaLine("warn", fmt.Sprintf("%s denied: %s %s", payload.Tool, payload.Path, detail))
		m.chatLines = append(m.chatLines, denyLine)
	}

	meta := changeItem{
		Status:      status,
		Summary:     payload.Summary,
		Detail:      payload.Detail,
		ExitCode:    payload.ExitCode,
		Tool:        payload.Tool,
		ToolCallID:  toolCallID,
		DurationMS:  payload.DurationMS,
		HasDuration: true,
	}

	if toolCallID != "" && path != "" {
		m.removeChangedPlaceholder(toolCallID)
	}

	if path != "" {
		m.rememberWorkspaceEchoSuppression(payload.WorkflowID, path)
		m.upsertChangedPath(path, meta, true)
		return
	}

	idx := m.findChangedByToolCallID(toolCallID)
	if idx != -1 {
		mergeChangeItem(&m.changed[idx], meta)
		if m.changed[idx].Name == "" {
			m.changed[idx].Name = payload.Tool
		}
		return
	}

	name := payload.Tool
	if name == "" {
		name = "tool"
	}
	m.changed = append(m.changed, changeItem{
		Name:        name,
		Status:      meta.Status,
		Summary:     meta.Summary,
		Detail:      meta.Detail,
		ExitCode:    meta.ExitCode,
		Count:       1,
		Tool:        meta.Tool,
		ToolCallID:  meta.ToolCallID,
		DurationMS:  meta.DurationMS,
		HasDuration: meta.HasDuration,
	})
	m.clampChangedSelection()
}

func (m *Model) markWorkspaceChanged(event model.Event) {
	payload, ok := decodeEventPayload[model.WorkspaceChangedPayload](event)
	if !ok || strings.TrimSpace(payload.Path) == "" {
		return
	}
	status := strings.TrimSpace(payload.Operation)
	if status == "" {
		status = "updated"
	}
	m.upsertChangedPath(payload.Path, changeItem{Status: status}, true)
}

// applyWorkflowRetry resets worker state to prepare for the retry round.
func (m *Model) applyWorkflowRetry(event model.Event) {
	_, ok := decodeEventPayload[model.WorkflowRetryPayload](event)
	if !ok {
		return
	}
	// Clear workers so the retry round re-populates them fresh.
	m.workers = nil
}

// applyWorkflowTerminated handles workflow failure and cancellation:
// stops the spinner, marks in-flight workers as done, and restores input focus.
// workflowID is the ID from the event payload; pass "" when unknown.
// A stale event from a previous workflow must not halt a new running workflow.
func (m *Model) applyWorkflowTerminated(workflowID string) {
	// Only act if the event belongs to the currently active workflow, or if we
	// cannot determine which workflow is active (either ID is empty).
	if m.status == "running" &&
		m.activeWorkflowID != "" &&
		workflowID != "" &&
		workflowID != m.activeWorkflowID {
		return
	}
	m.status = "idle"
	m.spinnerActive = false
	m.currentStep = ""
	for i := range m.workers {
		if m.workers[i].Status == "running" {
			m.workers[i].Status = "done"
		}
	}
	if m.followChat && m.acceptsInput() {
		m.ti.Focus()
		m.focus = focusInput
	}
}

func decodeEventPayload[T any](event model.Event) (T, bool) {
	payload, err := model.DecodePayload[T](event)
	return payload, err == nil
}

// appendTurnFromMessage tracks user/assistant messages as turns for the History tab.
func (m *Model) appendTurnFromMessage(role, content string) {
	switch role {
	case "user":
		m.turns = append(m.turns, turnEntry{
			Prompt:     truncateSingleLine(content, 60),
			FullPrompt: content,
		})
	case "assistant", "code":
		if len(m.turns) == 0 {
			return
		}
		last := &m.turns[len(m.turns)-1]
		last.Response = truncateSingleLine(content, 60)
		last.FullResponse = content
	}
}

// updateLastTurnFromCheckpoint enriches the last turn with workflow result info.
func (m *Model) updateLastTurnFromCheckpoint(cp model.CheckpointResult) {
	if len(m.turns) == 0 {
		return
	}
	last := &m.turns[len(m.turns)-1]
	last.Status = cp.Status
	last.WorkflowID = cp.WorkflowID
	last.FileCount = len(cp.ArtifactPaths)
	last.ChangedFiles = cp.ArtifactPaths
}
