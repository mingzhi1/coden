package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func todoMarker(status string) string {
	switch status {
	case "passed", "done":
		return "✓"
	case "failed":
		return "✗"
	case "abandoned":
		return "~"
	case "skipped":
		return "⊘"
	case "removed":
		return "⊖"
	case "retrying":
		return "↻"
	case "coding", "running":
		return "›"
	case "accepting":
		return "?"
	case "planned":
		return "·"
	default:
		return "-"
	}
}

func (m *Model) scrollChatBy(delta int) {
	if delta == 0 {
		return
	}
	next := clamp(m.chatScroll+delta, 0, m.maxChatScroll())
	m.chatScroll = next
	// Only break follow on intentional upward scroll; never re-enable here.
	// Re-enabling is done exclusively via scrollFocusedTo (G key) or beginSubmit.
	if delta < 0 {
		m.followChat = false
	}
}

func (m *Model) switchChatTab(tab chatTab) {
	if m.chatTabActive == tab {
		return
	}
	m.chatTabActive = tab
	m.turnExpanded = false
	m.turnDetailScroll = 0
}

// browsePromptHistory cycles through submitted prompts.
// delta=-1 goes older, delta=+1 goes newer.
func (m *Model) browsePromptHistory(delta int) {
	if len(m.promptHistory) == 0 {
		return
	}
	// On first browse, save current text as draft.
	if m.promptHistoryIdx == -1 && delta < 0 {
		m.promptDraft = m.ti.Value()
		m.promptHistoryIdx = len(m.promptHistory) // will be decremented below
	}

	next := m.promptHistoryIdx + delta
	if next < 0 {
		next = 0
	}
	if next >= len(m.promptHistory) {
		// Past newest → restore draft
		m.promptHistoryIdx = -1
		m.ti.SetValue(m.promptDraft)
		return
	}
	m.promptHistoryIdx = next
	m.ti.SetValue(m.promptHistory[next])
}

// scrollHistory navigates turns list or scrolls detail when expanded.
func (m *Model) scrollHistory(delta int) {
	if len(m.turns) == 0 {
		return
	}
	if m.turnExpanded {
		maxScroll := max(0, m.turnDetailLines()-m.chatViewportRows())
		m.turnDetailScroll = clamp(m.turnDetailScroll+delta, 0, maxScroll)
		return
	}
	m.turnSel = clamp(m.turnSel+delta, 0, len(m.turns)-1)
}

func (m *Model) pageHistoryBy(delta int) {
	if len(m.turns) == 0 || delta == 0 {
		return
	}
	page := max(1, m.historyPageSize())
	if m.turnExpanded {
		maxScroll := max(0, m.turnDetailLines()-m.chatViewportRows())
		m.turnDetailScroll = clamp(m.turnDetailScroll+delta*page, 0, maxScroll)
		return
	}
	m.turnSel = clamp(m.turnSel+delta*page, 0, len(m.turns)-1)
}

func (m *Model) scrollHistoryToBoundary(bottom bool) {
	if len(m.turns) == 0 {
		return
	}
	if m.turnExpanded {
		if !bottom {
			m.turnDetailScroll = 0
			return
		}
		m.turnDetailScroll = max(0, m.turnDetailLines()-m.chatViewportRows())
		return
	}
	if bottom {
		m.turnSel = len(m.turns) - 1
		return
	}
	m.turnSel = 0
}

func (m *Model) scrollFocusedBy(delta int) {
	switch m.focus {
	case focusChat:
		m.scrollChatBy(delta)
	case focusChanged:
		m.moveChangedSelection(delta)
	}
}

func (m *Model) pageFocusedBy(delta int) {
	switch m.focus {
	case focusChat:
		m.scrollChatBy(delta * m.chatPageSize())
	case focusChanged:
		m.scrollChangedDetailBy(delta * m.changedDetailPageSize())
	}
}

func (m *Model) scrollFocusedTo(offset int) {
	switch m.focus {
	case focusChat:
		m.chatScroll = clamp(offset, 0, m.maxChatScroll())
		// Intent-driven: only follow if explicitly jumping to bottom (G key).
		// offset==0 means g (top), should not follow.
		m.followChat = offset > 0 && m.chatScroll == m.maxChatScroll()
	case focusChanged:
		m.clampChangedSelection()
		if len(m.changed) == 0 {
			return
		}
		m.changedSel = clamp(offset, 0, len(m.changed)-1)
		m.changedDetailScroll = 0
	}
}

func (m *Model) cycleFocus(delta int) {
	order := m.focusOrder()
	if len(order) == 0 {
		return
	}
	idx := 0
	for i, item := range order {
		if item == m.focus {
			idx = i
			break
		}
	}
	next := (idx + delta) % len(order)
	if next < 0 {
		next += len(order)
	}
	m.focus = order[next]
	if m.focus == focusInput && m.acceptsInput() {
		m.ti.Focus()
	} else {
		m.ti.Blur()
	}
}

func (m *Model) focusOrder() []panelFocus {
	order := []panelFocus{focusChat, focusTodo, focusChanged}
	if m.acceptsInput() {
		order = []panelFocus{focusChat, focusInput, focusTodo, focusChanged}
	}
	return order
}

func (m *Model) moveChangedSelection(delta int) {
	if len(m.changed) == 0 || delta == 0 {
		return
	}
	m.clampChangedSelection()
	m.changedSel = clamp(m.changedSel+delta, 0, len(m.changed)-1)
	m.changedDetailScroll = 0
}

func (m *Model) clampChangedSelection() {
	if len(m.changed) == 0 {
		m.changedSel = 0
		return
	}
	m.changedSel = clamp(m.changedSel, 0, len(m.changed)-1)
}

func (m *Model) scrollChangedDetailBy(delta int) {
	if len(m.changed) == 0 || delta == 0 {
		return
	}
	maxScroll := m.maxChangedDetailScroll()
	m.changedDetailScroll = clamp(m.changedDetailScroll+delta, 0, maxScroll)
}

func (m *Model) changedDetailPageSize() int {
	height := max(minHeight, m.height)
	contentHeight := max(10, height-6)
	bottomRightHeight := contentHeight - max(5, contentHeight/2)
	visibleRows := max(1, panelBodyRows(bottomRightHeight, 1))
	listRows := visibleRows / 2
	if listRows < 3 {
		listRows = min(visibleRows, len(m.changed))
	}
	detailRows := visibleRows - listRows - 1
	return max(1, detailRows)
}

func (m *Model) historyPageSize() int {
	return max(1, m.chatViewportRows()-2)
}

func (m *Model) turnDetailLines() int {
	if m.turnSel >= len(m.turns) {
		return 0
	}
	turn := m.turns[m.turnSel]
	total := 3
	total += len(strings.Split(turn.FullPrompt, "\n"))
	if turn.FullResponse != "" {
		total += 2 + len(strings.Split(turn.FullResponse, "\n"))
	}
	if turn.Status != "" {
		total++
	}
	if turn.WorkflowID != "" {
		total++
	}
	if len(turn.ChangedFiles) > 0 {
		total += 2 + len(turn.ChangedFiles)
	}
	return total + 2
}

func (m *Model) maxChangedDetailScroll() int {
	if len(m.changed) == 0 {
		return 0
	}
	item := m.changed[m.changedSel]
	total := 1
	if item.Summary != "" {
		total++
	}
	if item.Path != "" {
		total++
	}
	if preview := strings.TrimSpace(item.Preview); preview != "" {
		total += 1 + len(strings.Split(preview, "\n"))
	}
	detail := strings.TrimSpace(item.Detail)
	if detail != "" {
		total += 1 + len(strings.Split(detail, "\n"))
	}
	return max(0, total-m.changedDetailPageSize())
}

func (m *Model) chatPageSize() int {
	return max(1, m.chatViewportRows())
}

func (m *Model) acceptsInput() bool {
	return m.status != "running" && m.status != "disconnected"
}

func (m *Model) chatViewportRows() int {
	height := max(minHeight, m.height)
	contentHeight := max(10, height-6)
	leftChatHeight, _ := splitLeftColumnHeights(contentHeight)
	return max(1, panelBodyRows(leftChatHeight, 1))
}

func (m *Model) maxChatScroll() int {
	total := len(m.chatLines)
	visible := m.chatViewportRows()
	return max(0, total-visible)
}

func clamp(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}
	return v
}

func splitLeftColumnHeights(contentHeight int) (chatHeight, inputHeight int) {
	inputHeight = 5
	if contentHeight < 12 {
		inputHeight = 4
	}
	chatHeight = contentHeight - inputHeight
	if chatHeight < 6 {
		chatHeight = 6
		inputHeight = max(3, contentHeight-chatHeight)
	}
	return chatHeight, inputHeight
}

func (m *Model) overlaySelectableIndices() []int {
	if m.alert == nil || len(m.alert.items) == 0 {
		return nil
	}
	indices := make([]int, 0, len(m.alert.items))
	for i, item := range m.alert.items {
		// action: 可执行, disabled: 不可见但可选（用于展示不可用动作）
		if strings.TrimSpace(item.action) != "" || item.kind == "disabled" {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *Model) clampOverlayCursor() {
	if m.alert == nil {
		return
	}
	indices := m.overlaySelectableIndices()
	if len(indices) == 0 {
		m.alert.cursor = 0
		return
	}
	maxIndex := len(indices) - 1
	m.alert.cursor = clamp(m.alert.cursor, 0, maxIndex)
}

func (m *Model) moveOverlayCursor(delta int) {
	if m.alert == nil || delta == 0 {
		return
	}
	indices := m.overlaySelectableIndices()
	if len(indices) == 0 {
		m.alert.cursor = 0
		return
	}
	m.alert.cursor = clamp(m.alert.cursor+delta, 0, len(indices)-1)
}

func (m *Model) selectedOverlayAction() string {
	if m.alert == nil {
		return ""
	}
	indices := m.overlaySelectableIndices()
	if len(indices) == 0 {
		return ""
	}
	m.clampOverlayCursor()
	item := m.alert.items[indices[m.alert.cursor]]
	return strings.TrimSpace(item.action)
}

func (m *Model) selectedOverlayItemKind() string {
	if m.alert == nil {
		return ""
	}
	indices := m.overlaySelectableIndices()
	if len(indices) == 0 {
		return ""
	}
	m.clampOverlayCursor()
	return m.alert.items[indices[m.alert.cursor]].kind
}

func (m *Model) activateOverlayAction() tea.Cmd {
	// disabled 类型给出反馈，不执行动作
	if m.selectedOverlayItemKind() == "disabled" {
		m.showUnavailableFeedback()
		return nil
	}

	action := m.selectedOverlayAction()
	switch action {
	case "dismiss":
		m.alert = nil
	case "focus_chat":
		m.alert = nil
		m.focus = focusChat
		m.ti.Blur()
	case "focus_input":
		m.alert = nil
		if m.acceptsInput() {
			m.focus = focusInput
			m.ti.Focus()
		}
	case "focus_changed":
		m.alert = nil
		m.focus = focusChanged
		m.ti.Blur()
	case "new_session":
		m.alert = nil
		return func() tea.Msg { return OverlayRequestNewSessionMsg{} }
	case "close_session":
		m.alert = nil
		return func() tea.Msg { return OverlayRequestCloseSessionMsg{} }
	}
	return nil
}

// showUnavailableFeedback 在浮层底部显示 temporarily unavailable 提示
func (m *Model) showUnavailableFeedback() {
	if m.alert == nil {
		return
	}
	m.alert.footer = "unavailable in current session — see REASON section above"
}
