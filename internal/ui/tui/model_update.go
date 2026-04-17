package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/core/model"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.spinnerActive {
			return m, tea.Batch(cmds...)
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		width := max(minWidth, m.width)
		height := max(minHeight, m.height)
		leftWidth := max(48, (width*2)/3)
		contentHeight := max(10, height-6)
		_, leftInputHeight := splitLeftColumnHeights(contentHeight)
		m.ti.SetWidth(max(12, leftWidth-4))
		m.ti.SetHeight(max(1, panelBodyRows(leftInputHeight, 2)))
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		} else {
			// Ensure scroll position stays valid when not following
			m.chatScroll = min(m.chatScroll, m.maxChatScroll())
		}
		return m, tea.Batch(cmds...)

	case tea.MouseWheelMsg:
		delta := 3 // scroll 3 lines per wheel tick
		if msg.Button == tea.MouseWheelUp {
			delta = -3
		}
		if m.chatTabActive == tabHistory && m.focus == focusChat {
			m.scrollHistory(delta)
		} else {
			m.scrollFocusedBy(delta)
		}
		return m, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		if m.alert != nil {
			switch {
			case msg.Code == tea.KeyEsc:
				m.alert = nil
			case msg.Code == tea.KeyUp:
				m.moveOverlayCursor(-1)
			case msg.Code == tea.KeyDown:
				m.moveOverlayCursor(1)
			case msg.Text == "j":
				m.moveOverlayCursor(1)
			case msg.Text == "k":
				m.moveOverlayCursor(-1)
			case msg.Code == tea.KeyEnter:
				if cmd := m.activateOverlayAction(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return m, tea.Batch(cmds...)
		}

		switch {
		case msg.Code == tea.KeyEsc:
			if m.chatTabActive == tabHistory && m.turnExpanded {
				m.turnExpanded = false
				m.turnDetailScroll = 0
			} else {
				m.alert = nil
			}
		case msg.Text == "?":
			m.alert = &alertState{level: "info", title: "Help", lines: m.helpLines(), footer: "esc dismiss"}
		case msg.Text == "c":
			if m.focus != focusInput {
				m.alert = m.runtimeOverlay()
			}
		case msg.Text == "m":
			if m.focus != focusInput {
				m.alert = m.runtimeOverlay()
			}
		case msg.Text == "1":
			if m.focus != focusInput {
				m.switchChatTab(tabChat)
			}
		case msg.Text == "2":
			if m.focus != focusInput {
				m.switchChatTab(tabHistory)
			}
		case msg.Code == tea.KeyTab:
			m.cycleFocus(1)
		case msg.String() == "shift+tab":
			m.cycleFocus(-1)
		case msg.String() == "ctrl+c":
			return m, tea.Quit
		case msg.String() == "ctrl+x":
			if m.status == "running" && m.activeWorkflowID != "" && m.canceler != nil {
				cmds = append(cmds, m.canceler(m.activeWorkflowID))
			}
		case msg.Text == "q":
			if m.focus != focusInput && m.status != "running" {
				return m, tea.Quit
			}
		case msg.Code == tea.KeyUp:
			if m.focus == focusInput && m.acceptsInput() {
				// Single-line input: browse prompt history instead of cursor
				if !strings.Contains(m.ti.Value(), "\n") && len(m.promptHistory) > 0 {
					m.browsePromptHistory(-1)
				} else {
					var tiCmd tea.Cmd
					m.ti, tiCmd = m.ti.Update(msg)
					cmds = append(cmds, tiCmd)
				}
			} else if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.scrollHistory(-1)
			} else {
				m.scrollFocusedBy(-1)
			}
		case msg.Code == tea.KeyDown:
			if m.focus == focusInput && m.acceptsInput() {
				if !strings.Contains(m.ti.Value(), "\n") && m.promptHistoryIdx >= 0 {
					m.browsePromptHistory(1)
				} else {
					var tiCmd tea.Cmd
					m.ti, tiCmd = m.ti.Update(msg)
					cmds = append(cmds, tiCmd)
				}
			} else if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.scrollHistory(1)
			} else {
				m.scrollFocusedBy(1)
			}
		case msg.Code == tea.KeyPgUp:
			if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.pageHistoryBy(-1)
			} else {
				m.pageFocusedBy(-1)
			}
		case msg.Code == tea.KeyPgDown:
			if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.pageHistoryBy(1)
			} else {
				m.pageFocusedBy(1)
			}
		case msg.Text == "j":
			if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.scrollHistory(1)
			} else if m.focus != focusInput {
				m.scrollFocusedBy(1)
			}
		case msg.Text == "k":
			if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.scrollHistory(-1)
			} else if m.focus != focusInput {
				m.scrollFocusedBy(-1)
			}
		case msg.Text == "g":
			if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.scrollHistoryToBoundary(false)
			} else if m.focus != focusInput {
				m.scrollFocusedTo(0)
			}
		case msg.Text == "G":
			if m.chatTabActive == tabHistory && m.focus == focusChat {
				m.scrollHistoryToBoundary(true)
			} else if m.focus != focusInput {
				m.scrollFocusedTo(m.maxChatScroll())
			}
		case msg.String() == "shift+enter":
			if m.focus == focusInput && m.acceptsInput() {
				m.ti.SetValue(m.ti.Value() + "\n")
			}
		case msg.Code == tea.KeyEnter:
			if m.chatTabActive == tabHistory && m.focus == focusChat && len(m.turns) > 0 {
				m.turnExpanded = !m.turnExpanded
				m.turnDetailScroll = 0
			} else if m.focus == focusInput && m.acceptsInput() && strings.TrimSpace(m.ti.Value()) != "" {
				cmds = append(cmds, m.beginSubmit(m.ti.Value()))
			} else if m.focus == focusChanged && len(m.changed) > 0 && m.workspaceReader != nil {
				m.clampChangedSelection()
				if path := strings.TrimSpace(m.changed[m.changedSel].Path); path != "" {
					cmds = append(cmds, m.workspaceReader(path))
				}
			}
		default:
			if m.acceptsInput() && m.focus == focusInput {
				var tiCmd tea.Cmd
				m.ti, tiCmd = m.ti.Update(msg)
				cmds = append(cmds, tiCmd)
			}
		}
		return m, tea.Batch(cmds...)

	case startSubmitMsg:
		cmds = append(cmds, m.beginSubmit(msg.Prompt))
		return m, tea.Batch(cmds...)

	case EventMsg:
		m.chatLines = append(m.chatLines, m.renderEventLines(msg.Event)...)
		if len(m.chatLines) > maxChatLines {
			// Calculate how many lines we're removing to adjust scroll position
			removedLines := len(m.chatLines) - maxChatLines
			m.chatLines = m.chatLines[removedLines:]
			// Adjust scroll position to maintain relative view
			if m.chatScroll > 0 {
				m.chatScroll = max(0, m.chatScroll-removedLines)
			}
		}
		m.applyEvent(msg.Event)
		// Track turns for History tab from live message events.
		if msg.Event.Topic == model.EventMessageCreated {
			if p, ok := decodeEventPayload[model.MessageCreatedPayload](msg.Event); ok {
				m.appendTurnFromMessage(strings.TrimSpace(p.Role), strings.TrimSpace(p.Content))
			}
		}
		if payload, ok := decodeEventPayload[model.CheckpointUpdatedPayload](msg.Event); ok && payload.WorkflowID != "" {
			if payload.WorkflowID == m.activeWorkflowID {
				if m.objectsLoader != nil {
					cmds = append(cmds, m.objectsLoader(payload.WorkflowID))
				}
				if m.checkpointLoader != nil {
					cmds = append(cmds, m.checkpointLoader(payload.WorkflowID))
				}
			} else if m.status == "running" {
				// WorkflowAcceptedMsg may not have arrived yet — park the ID so
				// the accepted handler can fire the loader when it catches up.
				m.pendingCheckpointWorkflowID = payload.WorkflowID
			}
		}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		cmds = append(cmds, waitForEvent(m.sessionID, m.eventStream))
		return m, tea.Batch(cmds...)

	case CheckpointMsg:
		// Only stop the spinner / reset state if this checkpoint belongs to the
		// currently active workflow, or if nothing is running yet.  A stale
		// checkpoint from a previous workflow that was loaded after the new
		// workflow already started must not prematurely halt the new run.
		// We can only detect staleness when we know both IDs; if either is
		// unknown we allow the reset.
		isStaleCheckpoint := m.status == "running" &&
			m.activeWorkflowID != "" &&
			msg.Result.WorkflowID != "" &&
			msg.Result.WorkflowID != m.activeWorkflowID
		isCurrentWorkflow := !isStaleCheckpoint
		if isCurrentWorkflow {
			m.status = "idle"
			m.spinnerActive = false
			for i := range m.workers {
				if m.workers[i].Status != "done" {
					m.workers[i].Status = "done"
				}
			}
		}
		m.checkpoint = &msg.Result
		m.updateLastTurnFromCheckpoint(msg.Result)
		// Keep latestRun.Status in sync with the completed checkpoint.
		if m.latestRun != nil && msg.Result.WorkflowID != "" &&
			(m.latestRun.WorkflowID == msg.Result.WorkflowID || m.latestRun.Status == "running") {
			m.latestRun.Status = msg.Result.Status
		}
		// Refresh latestIntent via snapshotLoader after workflow completes.
		if isCurrentWorkflow && m.snapshotLoader != nil {
			cmds = append(cmds, m.snapshotLoader())
		}
		summary := fmt.Sprintf("checkpoint %s", msg.Result.Status)
		if msg.Result.WorkflowID != "" {
			summary += fmt.Sprintf(" (%s)", msg.Result.WorkflowID)
		}
		if len(msg.Result.ArtifactPaths) > 0 || len(msg.Result.Evidence) > 0 {
			summary += fmt.Sprintf("  artifacts=%d evidence=%d", len(msg.Result.ArtifactPaths), len(msg.Result.Evidence))
		}
		m.chatLines = append(m.chatLines, chatMetaLine("system", summary))
		for _, path := range msg.Result.ArtifactPaths {
			m.ensureChangedPath(path, "written")
		}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
			// Only focus input if user was following the chat (not scrolled up reading)
			m.ti.Focus()
			m.focus = focusInput
		} else {
			// User scrolled up — keep focus on chat so they can keep reading
			m.focus = focusChat
		}
		return m, tea.Batch(cmds...)

	case WorkflowObjectsLoadedMsg:
		if msg.WorkflowID == "" || msg.WorkflowID != m.activeWorkflowID {
			return m, tea.Batch(cmds...)
		}
		m.applyLoadedObjectDetails(msg.Items)
		return m, tea.Batch(cmds...)

	case SessionSnapshotLoadedMsg:
		m.applySessionSnapshot(msg.Snapshot)
		return m, tea.Batch(cmds...)

	case WorkflowAcceptedMsg:
		m.activeWorkflowID = msg.WorkflowID
		// If a checkpoint event arrived before us (race condition: very fast
		// workflow), fire the loader now that we know the active workflow ID.
		if m.pendingCheckpointWorkflowID == msg.WorkflowID && msg.WorkflowID != "" {
			m.pendingCheckpointWorkflowID = ""
			if m.objectsLoader != nil {
				cmds = append(cmds, m.objectsLoader(msg.WorkflowID))
			}
			if m.checkpointLoader != nil {
				cmds = append(cmds, m.checkpointLoader(msg.WorkflowID))
			}
		}
		if msg.WorkflowID != "" {
			m.chatLines = append(m.chatLines, chatMetaLine("system", fmt.Sprintf("working... (%s)", msg.WorkflowID)))
		} else {
			m.chatLines = append(m.chatLines, chatMetaLine("system", "working..."))
		}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return m, tea.Batch(cmds...)

	case WorkflowCancelRequestedMsg:
		text := "cancel requested"
		if msg.WorkflowID != "" {
			text += " (" + msg.WorkflowID + ")"
		}
		m.chatLines = append(m.chatLines, chatMetaLine("system", text))
		m.alert = &alertState{level: "warning", title: "Cancel Requested", lines: []string{"workflow cancellation was sent to the kernel"}}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return m, tea.Batch(cmds...)

	case WorkflowCancelFailedMsg:
		text := "cancel failed"
		if msg.WorkflowID != "" {
			text += " (" + msg.WorkflowID + ")"
		}
		if msg.Err != nil {
			text += ": " + msg.Err.Error()
		}
		m.chatLines = append(m.chatLines, chatMetaLine("system", text))
		m.alert = &alertState{level: "error", title: "Cancel Failed", lines: []string{truncateSingleLine(text, 180)}}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return m, tea.Batch(cmds...)

	case WorkspaceReadLoadedMsg:
		if msg.Err != nil {
			m.alert = &alertState{level: "error", title: "Preview Failed", lines: []string{truncateSingleLine(msg.Err.Error(), 180)}}
			return m, tea.Batch(cmds...)
		}
		for i := range m.changed {
			if m.changed[i].Path == msg.Path {
				m.changed[i].Preview = msg.Content
				m.changedSel = i
				m.changedDetailScroll = 0
				break
			}
		}
		return m, tea.Batch(cmds...)

	case TaskSkipResultMsg:
		text := "task skipped"
		if msg.TaskID != "" {
			text += ": " + msg.TaskID
		}
		m.chatLines = append(m.chatLines, chatMetaLine("system", text))
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return m, tea.Batch(cmds...)

	case TaskUndoResultMsg:
		text := "task undo completed"
		if msg.RestoredTaskID != "" {
			text += ": restored " + msg.RestoredTaskID
		}
		m.chatLines = append(m.chatLines, chatMetaLine("system", text))
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return m, tea.Batch(cmds...)

	case SessionRenameResultMsg:
		if msg.Err != nil {
			m.chatLines = append(m.chatLines, chatMetaLine("warn", fmt.Sprintf("rename failed: %v", msg.Err)))
		} else {
			m.sessionID = msg.Name
			m.chatLines = append(m.chatLines, chatMetaLine("system", fmt.Sprintf("session renamed to %q", msg.Name)))
		}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return m, tea.Batch(cmds...)

	case StreamClosedMsg:
		m.status = "disconnected"
		m.spinnerActive = false
		m.ti.Blur()
		m.focus = focusChat
		m.chatLines = append(m.chatLines, chatMetaLine("system", "connection closed"))
		m.alert = &alertState{level: "warning", title: "Connection Closed", lines: []string{"event stream ended", "reconnect or restart the session to continue"}}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
		}
		return m, tea.Batch(cmds...)

	case ErrMsg:
		// If the error came from a submit attempt (status was "running"), return
		// to idle so the user can correct the prompt and retry. For errors that
		// arrive when status is already idle/disconnected, preserve that status.
		if m.status == "running" {
			m.status = "idle"
		}
		m.spinnerActive = false
		m.err = msg.Err
		if m.lastSubmittedPrompt != "" && strings.TrimSpace(m.ti.Value()) == "" {
			m.ti.SetValue(m.lastSubmittedPrompt)
		}
		m.chatLines = append(m.chatLines, chatMetaLine("system", fmt.Sprintf("error: %v", msg.Err)))
		m.alert = &alertState{level: "error", title: "Error", lines: []string{truncateSingleLine(msg.Err.Error(), 180)}}
		if m.followChat {
			m.chatScroll = m.maxChatScroll()
			if m.acceptsInput() {
				m.ti.Focus()
				m.focus = focusInput
			}
		} else {
			m.focus = focusChat
		}
		return m, tea.Batch(cmds...)

	default:
		if m.acceptsInput() && m.focus == focusInput {
			var tiCmd tea.Cmd
			m.ti, tiCmd = m.ti.Update(msg)
			cmds = append(cmds, tiCmd)
		}
		return m, tea.Batch(cmds...)
	}
}
