package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
)

// renderEventLines converts a kernel event into chat-panel display lines.
// This is the event-to-presentation layer; state mutations live in model_events.go.
func (m *Model) renderEventLines(event model.Event) []string {
	switch event.Topic {
	case model.EventMessageCreated:
		return m.renderEventMessage(event)
	case model.EventWorkflowStarted:
		return m.renderEventWorkflowStarted(event)
	case model.EventWorkflowStepUpdate:
		return m.renderEventWorkflowStep(event)
	case model.EventWorkflowTasks:
		return m.renderEventWorkflowTasks(event)
	case model.EventWorkerMessage:
		return m.renderEventWorkerMessage(event)
	case model.EventToolStarted:
		return m.renderEventToolStarted(event)
	case model.EventToolFinished:
		return m.renderEventToolFinished(event)
	case model.EventSessionAttached, model.EventSessionDetached, model.EventSessionCreated:
		return nil
	case model.EventWorkflowCanceled:
		return m.renderEventWorkflowCanceled(event)
	case model.EventWorkflowFailed:
		return m.renderEventWorkflowFailed(event)
	case model.EventWorkflowRetry:
		return m.renderEventWorkflowRetry(event)
	case model.EventCheckpointUpdated:
		return m.renderEventCheckpointUpdated(event)
	case model.EventWorkspaceChanged:
		return m.renderEventWorkspaceChanged(event)
	default:
		return nil
	}
}

func (m *Model) renderEventMessage(event model.Event) []string {
	payload, ok := decodeEventPayload[model.MessageCreatedPayload](event)
	if !ok {
		return nil
	}
	role := strings.TrimSpace(payload.Role)
	content := strings.TrimSpace(payload.Content)
	if role == "" || content == "" {
		return nil
	}
	lines := renderMessageBlock(role, content)
	if sameChatBlock(m.chatLines, lines) {
		return nil
	}
	return lines
}

func (m *Model) renderEventWorkflowStarted(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkflowStartedPayload](event)
	if !ok {
		return nil
	}
	return renderMetaBlock("system", "workflow started", payload.WorkflowID)
}

func (m *Model) renderEventWorkflowStep(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkflowStepUpdatedPayload](event)
	if !ok || payload.Step == "" {
		return nil
	}
	text := fmt.Sprintf("%s %s", payload.Step, payload.Status)
	if payload.TaskCount > 0 {
		text += fmt.Sprintf(" (%d tasks)", payload.TaskCount)
	}
	return renderMetaBlock("plan", "workflow step", text)
}

func (m *Model) renderEventWorkflowTasks(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkflowTasksUpdatedPayload](event)
	if !ok || len(payload.Tasks) == 0 {
		return nil
	}
	lines := renderMetaBlock("plan", "task list", fmt.Sprintf("%d tasks", len(payload.Tasks)))
	for _, task := range payload.Tasks {
		lines = append(lines, chatDetailLine("plan", fmt.Sprintf("%s %s", todoMarker(strings.ToLower(task.Status)), truncateSingleLine(task.Title, 72))))
	}
	return lines
}

func (m *Model) renderEventWorkerMessage(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkerMessagePayload](event)
	if !ok {
		return nil
	}
	content := strings.TrimSpace(payload.Content)
	if content == "" {
		return nil
	}
	label := "system"
	if payload.WorkerRole == "planner" || payload.WorkerRole == "input" {
		label = "plan"
	}
	title := "worker update"
	if payload.WorkerRole != "" {
		title = payload.WorkerRole
	}
	return renderMetaBlock(label, title, truncateSingleLine(content, 100))
}

func (m *Model) renderEventToolStarted(event model.Event) []string {
	payload, ok := decodeEventPayload[model.ToolStartedPayload](event)
	if !ok || payload.Tool == "" {
		return nil
	}
	label := "tool"
	headline := payload.Tool
	if payload.Path != "" {
		headline += " " + filepath.Base(payload.Path)
	}
	if payload.Tool == "write_file" || payload.Tool == "edit_file" {
		label = "edit"
	}
	return []string{chatMetaLine(label, "⟳ "+headline)}
}

func (m *Model) renderEventToolFinished(event model.Event) []string {
	payload, ok := decodeEventPayload[model.ToolFinishedPayload](event)
	if !ok || payload.Tool == "" {
		return nil
	}
	label := "tool"
	if payload.Tool == "write_file" || payload.Tool == "edit_file" {
		label = "edit"
	}
	target := payload.Tool
	if payload.Path != "" {
		target = filepath.Base(payload.Path)
	}
	// Compact headline: "✓ tool_name file.go  120ms"
	marker := "✓"
	if payload.Status == "denied" || payload.Status == "failed" || payload.Status == "error" {
		marker = "✗"
	}
	headline := fmt.Sprintf("%s %s", marker, target)
	if payload.DurationMS > 0 {
		headline += fmt.Sprintf("  %dms", payload.DurationMS)
	}
	lines := []string{chatMetaLine(label, headline)}
	if payload.Path != "" {
		lines = append(lines, chatDetailLine(label, payload.Path))
	}
	if summary := strings.TrimSpace(payload.Summary); summary != "" {
		lines = append(lines, chatDetailLine(label, truncateSingleLine(summary, 96)))
	}
	if detail := strings.TrimSpace(payload.Detail); detail != "" {
		for _, line := range strings.Split(truncateDetail(detail, 4), "\n") {
			lines = append(lines, chatDetailLine(label, line))
		}
	}
	lines = append(lines, "")
	return lines
}

func (m *Model) renderEventWorkflowCanceled(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkflowCanceledPayload](event)
	if !ok {
		return nil
	}
	line := chatMetaLine("system", "workflow canceled")
	if payload.Reason != "" {
		line = chatMetaLine("system", "workflow canceled: "+payload.Reason)
	}
	return []string{line}
}

func (m *Model) renderEventWorkflowFailed(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkflowFailedPayload](event)
	if !ok {
		return nil
	}
	lines := renderMetaBlock("error", "error", "workflow failed")
	if payload.Reason != "" {
		lines = append(lines, chatDetailLine("error", "reason: "+payload.Reason))
	}
	if payload.Error != "" {
		lines = append(lines, chatDetailLine("error", truncateSingleLine(payload.Error, 120)))
	}
	return lines
}

func (m *Model) renderEventWorkflowRetry(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkflowRetryPayload](event)
	if !ok {
		return nil
	}
	lines := renderMetaBlock("warn", "retry", fmt.Sprintf("attempt %d/%d — acceptor rejected", payload.Attempt, payload.MaxRetries))
	if payload.Reason != "" {
		lines = append(lines, chatDetailLine("warn", "reason: "+payload.Reason))
	}
	for _, ev := range payload.Evidence {
		ev = strings.TrimSpace(ev)
		if ev != "" {
			lines = append(lines, chatDetailLine("warn", "• "+truncateSingleLine(ev, 90)))
		}
	}
	return lines
}

func (m *Model) renderEventCheckpointUpdated(event model.Event) []string {
	payload, ok := decodeEventPayload[model.CheckpointUpdatedPayload](event)
	if !ok {
		return nil
	}
	// Show a brief status line so the user sees checkpoint completion
	return []string{chatMetaLine("system", "checkpoint: "+payload.Status)}
}

func (m *Model) renderEventWorkspaceChanged(event model.Event) []string {
	payload, ok := decodeEventPayload[model.WorkspaceChangedPayload](event)
	if !ok || strings.TrimSpace(payload.Path) == "" {
		return nil
	}
	if m.consumeWorkspaceEchoSuppression(payload) {
		return nil
	}
	status := strings.TrimSpace(payload.Operation)
	if status == "" {
		status = "updated"
	}
	lines := renderMetaBlock("edit", "workspace", fmt.Sprintf("%s %s", statusWord(status), filepath.Base(payload.Path)))
	lines = append(lines, chatDetailLine("edit", "path: "+payload.Path))
	return lines
}

