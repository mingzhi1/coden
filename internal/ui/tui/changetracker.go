package tui

import (
	"path/filepath"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
)

// --- changeItem CRUD helpers ---
// These functions manage the changed files list as a pure data operation,
// separated from event rendering and UI concerns.

func (m *Model) addChangedPath(path, status string) {
	m.upsertChangedPath(path, changeItem{Status: status}, true)
}

func (m *Model) ensureChangedPath(path, status string) {
	m.upsertChangedPath(path, changeItem{Status: status}, false)
}

func (m *Model) upsertChangedPath(path string, meta changeItem, increment bool) {
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = path
	}

	for i := range m.changed {
		if m.changed[i].Path == path {
			if increment {
				m.changed[i].Count++
			}
			mergeChangeItem(&m.changed[i], meta)
			return
		}
	}

	status := meta.Status
	if status == "" {
		status = "written"
	}
	m.changed = append(m.changed, changeItem{
		Path: path, Name: base, Status: status, Count: 1,
		Tool: meta.Tool, ToolCallID: meta.ToolCallID,
		Summary: meta.Summary, Detail: meta.Detail,
		ExitCode: meta.ExitCode, DurationMS: meta.DurationMS,
		HasDuration: meta.HasDuration,
	})
}

func (m *Model) findChangedByToolCallID(toolCallID string) int {
	if toolCallID == "" {
		return -1
	}
	for i := range m.changed {
		if m.changed[i].ToolCallID == toolCallID {
			return i
		}
	}
	return -1
}

func (m *Model) removeChangedPlaceholder(toolCallID string) {
	idx := m.findChangedByToolCallID(toolCallID)
	if idx == -1 || m.changed[idx].Path != "" {
		return
	}
	m.changed = append(m.changed[:idx], m.changed[idx+1:]...)
	// Adjust selection index if it was pointing at or after the removed item
	if m.changedSel >= idx && m.changedSel > 0 {
		m.changedSel--
	}
}

func (m *Model) rememberWorkspaceEchoSuppression(workflowID, path string) {
	m.lastToolChange = workspaceEchoSuppression{
		WorkflowID: strings.TrimSpace(workflowID),
		Path:       strings.TrimSpace(path),
	}
}

func (m *Model) consumeWorkspaceEchoSuppression(payload model.WorkspaceChangedPayload) bool {
	if strings.TrimSpace(payload.Path) == "" || m.lastToolChange.Path == "" {
		return false
	}
	match := m.lastToolChange.Path == strings.TrimSpace(payload.Path) &&
		m.lastToolChange.WorkflowID == strings.TrimSpace(payload.WorkflowID)
	if match {
		m.lastToolChange = workspaceEchoSuppression{}
		return true
	}
	return false
}

// mergeChangeItem applies non-zero fields from src into dst.
func mergeChangeItem(dst *changeItem, src changeItem) {
	if src.Status != "" {
		dst.Status = src.Status
	}
	if src.Tool != "" {
		dst.Tool = src.Tool
	}
	if src.ToolCallID != "" {
		dst.ToolCallID = src.ToolCallID
	}
	if src.Summary != "" {
		dst.Summary = src.Summary
	}
	if src.Detail != "" {
		dst.Detail = src.Detail
	}
	if src.ExitCode != 0 {
		dst.ExitCode = src.ExitCode
	}
	if src.HasDuration {
		dst.DurationMS = src.DurationMS
		dst.HasDuration = true
	}
}
