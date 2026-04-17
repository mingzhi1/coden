package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mingzhi1/coden/internal/ui/styles"
)

// RenderContent returns the fully rendered session content as a plain string.
// AppModel calls this to compose the final view with tab bar and AltScreen.
func (m *Model) RenderContent(width, height int) string {
	if width > 0 && height > 0 && (width < minWidth || height < minHeight) {
		msg := fmt.Sprintf(
			"Terminal too small: %dx%d\nMinimum required: %dx%d\n\nPlease resize your terminal.",
			width, height, minWidth, minHeight,
		)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
			styles.WarningText.Render(msg))
	}

	w := max(minWidth, width)
	h := max(minHeight, height)
	leftWidth := max(48, (w*2)/3)
	rightWidth := max(26, w-leftWidth-1)
	contentHeight := max(10, h-6)
	leftChatHeight, leftInputHeight := splitLeftColumnHeights(contentHeight)
	topRightHeight := max(5, contentHeight/2)
	bottomRightHeight := contentHeight - topRightHeight
	leftChatBodyRows := max(1, panelBodyRows(leftChatHeight, 1))
	rightTopBodyRows := max(1, panelBodyRows(topRightHeight, 1))
	rightBottomBodyRows := max(1, panelBodyRows(bottomRightHeight, 1))
	rightContentWidth := panelContentWidth(rightWidth)

	todoLines := m.renderTodoLines(rightContentWidth, rightTopBodyRows)
	chatContent := m.renderActivePanelLines(leftChatBodyRows)
	changedLines := m.renderChangedPanelLines(rightContentWidth, rightBottomBodyRows)

	leftChatPanelStyle := styles.Panel
	if m.focus == focusChat {
		leftChatPanelStyle = styles.PanelFocus
	}
	tabHeader := m.renderTabHeader()
	leftChatPanel := leftChatPanelStyle.Width(leftWidth).Height(leftChatHeight).Render(
		tabHeader + "\n" + strings.Join(chatContent, "\n"),
	)
	inputPanelStyle := styles.Panel
	if m.focus == focusInput && m.acceptsInput() {
		inputPanelStyle = styles.PanelFocus
	}
	inputHint := m.renderInputHint()
	leftInputPanel := inputPanelStyle.Width(leftWidth).Height(leftInputHeight).Render(
		styles.BoldText.Render("Input") + "\n" + m.ti.View() + "\n" + styles.MutedText.Render(inputHint),
	)
	todoPanelStyle := styles.Panel
	if m.focus == focusTodo {
		todoPanelStyle = styles.PanelFocus
	}
	todoTitle := "Workers + Tasks"
	if m.currentStep != "" {
		todoTitle = fmt.Sprintf("Workers + Tasks  [%s]", m.currentStep)
	} else if len(m.todos) > 0 {
		done := 0
		for _, t := range m.todos {
			if t.Status == "passed" || t.Status == "done" {
				done++
			}
		}
		todoTitle = fmt.Sprintf("Workers + Tasks  %d/%d", done, len(m.todos))
	}
	todoPanel := todoPanelStyle.Width(rightWidth).Height(topRightHeight).Render(
		styles.BoldText.Render(todoTitle) + "\n" + strings.Join(todoLines, "\n"),
	)
	changedPanelStyle := styles.Panel
	if m.focus == focusChanged {
		changedPanelStyle = styles.PanelFocus
	}
	changedTitle := "Changed Code"
	if len(m.changed) > 0 {
		changedTitle = fmt.Sprintf("Changed Code  (%d)", len(m.changed))
	}
	changedPanel := changedPanelStyle.Width(rightWidth).Height(bottomRightHeight).Render(
		styles.BoldText.Render(changedTitle) + "\n" + strings.Join(changedLines, "\n"),
	)

	leftPanel := lipgloss.JoinVertical(lipgloss.Top, leftChatPanel, leftInputPanel)
	rightPanel := lipgloss.JoinVertical(lipgloss.Top, todoPanel, changedPanel)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)

	statusLine := m.renderStatusBar(w)
	helpLine := m.renderHelpLine()

	result := lipgloss.JoinVertical(lipgloss.Top, body, statusLine, helpLine)
	if m.alert != nil {
		result = overlayCenter(w, h, result, m.renderAlertBox(min(72, max(42, w-12))))
	}
	return result
}

// View implements tea.Model. Uses m.width/m.height and sets AltScreen.
// When used via AppModel, this is not called; AppModel calls RenderContent directly.
func (m *Model) View() tea.View {
	v := tea.NewView(m.RenderContent(m.width, m.height))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func panelContentWidth(totalWidth int) int {
	return max(1, totalWidth-panelHorizontalFrame)
}

func panelBodyRows(totalHeight int, headerLines int) int {
	return max(1, totalHeight-2-headerLines)
}

func (m *Model) renderTabHeader() string {
	labels := []struct {
		name  string
		tab   chatTab
		count int
	}{
		{"Chat", tabChat, 0},
		{"History", tabHistory, len(m.turns)},
	}
	var parts []string
	for _, l := range labels {
		name := l.name
		if l.count > 0 {
			name += fmt.Sprintf("(%d)", l.count)
		}
		if m.chatTabActive == l.tab {
			parts = append(parts, styles.BoldText.Render("["+name+"]"))
		} else {
			parts = append(parts, styles.MutedText.Render(" "+name+" "))
		}
	}
	return strings.Join(parts, " ") + styles.MutedText.Render("  ← 1/2")
}

func (m *Model) renderActivePanelLines(visibleRows int) []string {
	switch m.chatTabActive {
	case tabHistory:
		return m.renderHistoryLines(visibleRows)
	default:
		return m.renderChatLines(visibleRows)
	}
}

func (m *Model) renderHistoryLines(visibleRows int) []string {
	if len(m.turns) == 0 {
		return []string{styles.MutedText.Render("no turns yet")}
	}

	// Expanded detail view for selected turn.
	if m.turnExpanded {
		return m.renderTurnDetail(visibleRows)
	}

	// Summary list view.
	lines := make([]string, 0, len(m.turns))
	for i, turn := range m.turns {
		prefix := "  "
		if i == m.turnSel {
			prefix = "> "
		}
		statusIcon := "●"
		statusStyle := styles.MutedText
		switch turn.Status {
		case "pass":
			statusIcon = "✓"
			statusStyle = styles.SuccessText
		case "fail":
			statusIcon = "✗"
			statusStyle = styles.ErrorText
		case "failed":
			statusIcon = "✗"
			statusStyle = styles.ErrorText
		case "canceled":
			statusIcon = "⊘"
			statusStyle = styles.MutedText
		case "crashed":
			statusIcon = "!"
			statusStyle = styles.ErrorText
		case "running":
			statusIcon = "◎"
			statusStyle = styles.PrimaryText
		}
		prompt := turn.Prompt
		if prompt == "" {
			prompt = "(empty)"
		}

		summary := prefix + statusStyle.Render(statusIcon) + " "
		summary += styles.NormalText.Render(prompt)
		if turn.FileCount > 0 {
			summary += styles.MutedText.Render(fmt.Sprintf(" [%d files]", turn.FileCount))
		}
		if turn.Response != "" {
			summary += styles.MutedText.Render(" → " + turn.Response)
		}
		lines = append(lines, summary)
	}

	lines = append(lines, "", styles.MutedText.Render("enter expand  esc collapse  ↑↓ navigate"))

	if visibleRows <= 0 || len(lines) <= visibleRows {
		return lines
	}
	// Keep selected turn visible.
	start := 0
	if m.turnSel > visibleRows-3 {
		start = m.turnSel - visibleRows + 3
	}
	end := start + visibleRows
	if end > len(lines) {
		end = len(lines)
		start = max(0, end-visibleRows)
	}
	return lines[start:end]
}

func (m *Model) renderTurnDetail(visibleRows int) []string {
	if m.turnSel >= len(m.turns) {
		return []string{styles.MutedText.Render("no turn selected")}
	}
	turn := m.turns[m.turnSel]

	lines := []string{
		styles.BoldText.Render(fmt.Sprintf("Turn %d/%d", m.turnSel+1, len(m.turns))),
		"",
		styles.PrimaryText.Render("▷ Prompt:"),
	}
	for _, l := range strings.Split(turn.FullPrompt, "\n") {
		lines = append(lines, "  "+l)
	}
	if turn.FullResponse != "" {
		lines = append(lines, "", styles.PrimaryText.Render("◁ Response:"))
		for _, l := range strings.Split(turn.FullResponse, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	if turn.Status != "" {
		lines = append(lines, "", styles.MutedText.Render("status: "+turn.Status))
	}
	if turn.WorkflowID != "" {
		lines = append(lines, styles.MutedText.Render("workflow: "+turn.WorkflowID))
	}
	if len(turn.ChangedFiles) > 0 {
		lines = append(lines, "", styles.PrimaryText.Render("Changed files:"))
		for _, f := range turn.ChangedFiles {
			lines = append(lines, "  "+styles.MutedText.Render(f))
		}
	}
	lines = append(lines, "", styles.MutedText.Render("esc back to list"))

	if visibleRows <= 0 || len(lines) <= visibleRows {
		return lines
	}
	offset := clamp(m.turnDetailScroll, 0, max(0, len(lines)-visibleRows))
	return lines[offset : offset+visibleRows]
}

func (m *Model) renderChatLines(visibleRows int) []string {
	all := append([]string(nil), m.chatLines...)
	if len(all) == 0 {
		all = append(all, chatMetaLine("system", "start chatting"))
	}
	if m.spinnerActive {
		stepText := "thinking..."
		if m.activeToolName != "" {
			stepText = m.activeToolName
		} else if m.currentStep != "" {
			stepText = m.currentStep
		}
		// Show task progress if available.
		progressText := ""
		if len(m.todos) > 0 {
			done := 0
			for _, t := range m.todos {
				if t.Status == "passed" || t.Status == "done" || t.Status == "skipped" {
					done++
				}
			}
			progressText = fmt.Sprintf(" [%d/%d]", done, len(m.todos))
		}
		all = append(all, "", fmt.Sprintf("%s %s%s", m.spinner.View(), stepText, progressText))
	}

	if visibleRows <= 0 || len(all) <= visibleRows {
		return all
	}

	maxOffset := len(all) - visibleRows
	offset := clamp(m.chatScroll, 0, maxOffset)
	visible := append([]string(nil), all[offset:offset+visibleRows]...)

	// Replace first/last lines with scroll indicators when content extends beyond view
	if offset > 0 {
		visible[0] = styles.MutedText.Render(fmt.Sprintf("  ↑ %d more lines above", offset))
	}
	remaining := len(all) - offset - visibleRows
	if remaining > 0 {
		visible[visibleRows-1] = styles.MutedText.Render(fmt.Sprintf("  ↓ %d more lines below", remaining))
	}
	return visible
}

func (m *Model) renderTodoLines(width, visibleRows int) []string {
	if len(m.workers) == 0 && len(m.todos) == 0 {
		return m.renderTodoSummaryLines(width)
	}

	lines := make([]string, 0, len(m.workers)+len(m.todos)+1)
	for _, worker := range m.workers {
		lines = append(lines, renderWorkerLine(worker, width))
	}
	if len(m.workers) > 0 && len(m.todos) > 0 {
		lines = append(lines, "")
	}
	for _, todo := range m.todos {
		text := truncatePanelText(todo.Name, max(6, width-2))
		line := fmt.Sprintf("%s %s", todoMarker(todo.Status), text)
		switch todo.Status {
		case "passed", "done":
			line = styles.MutedText.Render(line)
		case "failed":
			line = styles.ErrorText.Render(line)
		case "abandoned", "skipped", "removed":
			line = styles.MutedText.Render(line)
		case "retrying":
			line = styles.WarningText.Render(line)
		case "coding", "accepting", "running":
			line = styles.PrimaryText.Render(line)
		default:
			line = styles.NormalText.Render(line)
		}
		lines = append(lines, line)
	}
	return fitPanelLines(lines, visibleRows)
}

func (m *Model) renderTodoSummaryLines(width int) []string {
	lines := make([]string, 0, 6)
	if !m.snapshotLoaded {
		return []string{styles.MutedText.Render("loading session snapshot...")}
	}
	if m.latestRun == nil && m.checkpoint == nil {
		return []string{styles.MutedText.Render("no workflow history yet")}
	}

	if m.latestRun != nil {
		lines = append(lines, styles.NormalText.Render(truncatePanelText(fmt.Sprintf("latest workflow: %s", m.latestRun.WorkflowID), width)))
		statusStyle := styles.MutedText
		switch m.latestRun.Status {
		case "pass":
			statusStyle = styles.SuccessText
		case "fail", "failed":
			statusStyle = styles.ErrorText
		case "running":
			statusStyle = styles.PrimaryText
		case "canceled", "crashed":
			statusStyle = styles.WarningText
		}
		lines = append(lines, statusStyle.Render(truncatePanelText(fmt.Sprintf("status: %s", m.latestRun.Status), width)))
		if prompt := strings.TrimSpace(m.latestRun.Prompt); prompt != "" {
			lines = append(lines, styles.MutedText.Render("prompt: "+truncatePanelText(prompt, max(6, width-8))))
		}
	}
	if m.latestIntent != nil {
		if goal := strings.TrimSpace(m.latestIntent.Goal); goal != "" {
			kindTag := ""
			if k := strings.TrimSpace(m.latestIntent.Kind); k != "" {
				kindTag = " [" + k + "]"
			}
			lines = append(lines, styles.NormalText.Render("intent: "+truncatePanelText(goal+kindTag, max(6, width-8))))
		}
		if len(m.latestIntent.SuccessCriteria) > 0 {
			lines = append(lines, styles.MutedText.Render(truncatePanelText(fmt.Sprintf("criteria: %d", len(m.latestIntent.SuccessCriteria)), width)))
		}
	}
	if m.checkpoint != nil {
		lines = append(lines, styles.MutedText.Render(truncatePanelText(fmt.Sprintf("artifacts: %d  evidence: %d", len(m.checkpoint.ArtifactPaths), len(m.checkpoint.Evidence)), width)))
	}
	if len(m.changed) > 0 {
		lines = append(lines, styles.MutedText.Render(truncatePanelText(fmt.Sprintf("tracked changes: %d", len(m.changed)), width)))
	}
	if len(lines) == 0 {
		lines = append(lines, styles.MutedText.Render("ready — enter a prompt to begin"))
	} else {
		lines = append(lines, styles.MutedText.Render(truncatePanelText("workers & tasks appear during active runs", width)))
	}
	return lines
}

func (m *Model) renderChangedPanelLines(width, visibleRows int) []string {
	if visibleRows <= 0 {
		visibleRows = 1
	}
	if len(m.changed) == 0 {
		return []string{"no changed files yet"}
	}
	m.clampChangedSelection()

	listRows := visibleRows / 2
	if listRows < 3 {
		listRows = min(visibleRows, len(m.changed))
	}
	if listRows > len(m.changed) {
		listRows = len(m.changed)
	}
	detailRows := visibleRows - listRows - 1
	if detailRows < 0 {
		detailRows = 0
	}

	start := 0
	if m.changedSel >= listRows {
		start = m.changedSel - listRows + 1
	}
	if maxStart := len(m.changed) - listRows; start > maxStart {
		start = max(0, maxStart)
	}

	lines := make([]string, 0, visibleRows)
	for i := start; i < min(len(m.changed), start+listRows); i++ {
		line := renderChangedLine(m.changed[i], max(6, width-2))
		prefix := "  "
		if i == m.changedSel {
			prefix = "> "
		}
		lines = append(lines, prefix+line)
	}

	if detailRows > 0 {
		lines = append(lines, styles.MutedText.Render(strings.Repeat("-", 12)))
		lines = append(lines, m.renderChangedDetailLines(detailRows)...)
	}

	if len(lines) > visibleRows {
		lines = lines[:visibleRows]
	}
	return lines
}

func (m *Model) renderChangedDetailLines(visibleRows int) []string {
	if visibleRows <= 0 || len(m.changed) == 0 {
		return nil
	}
	item := m.changed[m.changedSel]
	width := panelContentWidth(max(26, m.width-max(48, (max(minWidth, m.width)*2)/3)-1))
	allLines := []string{styles.BoldText.Render(truncatePanelText(item.Name, width))}
	if item.Summary != "" {
		allLines = append(allLines, styles.MutedText.Render(truncatePanelText(item.Summary, width)))
	}
	if item.Path != "" {
		allLines = append(allLines, styles.MutedText.Render(truncatePathMiddle(item.Path, width)))
	}
	if preview := strings.TrimSpace(item.Preview); preview != "" {
		allLines = append(allLines, styles.PrimaryText.Render("preview"))
		for _, line := range strings.Split(preview, "\n") {
			allLines = append(allLines, styles.NormalText.Render(truncatePanelText(line, width)))
		}
	}
	detail := strings.TrimSpace(item.Detail)
	if detail != "" {
		if len(allLines) > 0 {
			allLines = append(allLines, styles.MutedText.Render(changedDetailLabel(item)))
		}
		for _, line := range strings.Split(detail, "\n") {
			allLines = append(allLines, renderDiffDetailLine(truncatePanelText(line, width)))
		}
	}
	maxScroll := max(0, len(allLines)-visibleRows)
	m.changedDetailScroll = clamp(m.changedDetailScroll, 0, maxScroll)
	start := m.changedDetailScroll
	end := min(len(allLines), start+visibleRows)
	lines := append([]string(nil), allLines[start:end]...)
	for len(lines) < min(visibleRows, len(allLines)) {
		lines = append(lines, "")
	}
	return lines
}

func fitPanelLines(lines []string, visibleRows int) []string {
	if visibleRows <= 0 || len(lines) <= visibleRows {
		return lines
	}
	return lines[:visibleRows]
}

func changedDetailLabel(item changeItem) string {
	switch item.Tool {
	case "write_file":
		return "diff"
	case "read_file":
		return "output"
	}
	detail := strings.TrimSpace(item.Detail)
	if strings.HasPrefix(detail, "---") || strings.HasPrefix(detail, "+++") || strings.HasPrefix(detail, "@@") {
		return "diff"
	}
	return "detail"
}

// renderStatusBar composes the full-width status bar at the bottom of the layout.
func (m *Model) renderStatusBar(w int) string {
	statusText := fmt.Sprintf(" CodeN │ %s │ %s ", m.sessionID, m.status)
	if m.status == "running" && !m.workflowStartedAt.IsZero() {
		elapsed := time.Since(m.workflowStartedAt).Truncate(time.Second)
		statusText += fmt.Sprintf("│ %s ", elapsed)
	}
	if m.status == "running" && m.activeToolName != "" {
		statusText += fmt.Sprintf("│ %s %s ", m.spinner.View(), m.activeToolName)
	} else if m.status == "running" && m.currentStep != "" {
		statusText += fmt.Sprintf("│ %s %s ", m.spinner.View(), m.currentStep)
	}
	if m.toolCallCount > 0 {
		statusText += fmt.Sprintf("│ %d calls ", m.toolCallCount)
	}
	if !m.followChat {
		statusText += "│ PAUSED "
	}
	if extra := m.renderRuntimeSummary(); extra != "" {
		statusText += "│ " + extra + " "
	}

	var statusLine string
	switch m.status {
	case "running":
		statusLine = styles.StatusBar.Background(styles.ColorWarning).Foreground(styles.ColorBg).Render(statusText)
	case "disconnected":
		statusLine = styles.StatusBar.Background(styles.ColorMuted).Foreground(styles.ColorText).Render(statusText)
	case "error":
		statusLine = styles.StatusBar.Background(styles.ColorError).Foreground(styles.ColorText).Render(statusText)
	default:
		statusLine = styles.StatusBar.Render(statusText)
	}
	// Pad to full width
	statusLine += styles.StatusBar.Render(strings.Repeat(" ", max(0, w-lipgloss.Width(statusLine))))
	return statusLine
}

// renderHelpLine returns the bottom help text with context-sensitive shortcuts.
func (m *Model) renderHelpLine() string {
	if m.status == "running" {
		return styles.MutedText.Render("tab panel  j/k scroll  ? help  ctrl+x cancel  Alt+n session")
	}
	return styles.MutedText.Render("tab panel  j/k scroll  ? help  c config  q quit  Alt+n session")
}

// renderInputHint returns context-appropriate hints for the input panel.
func (m *Model) renderInputHint() string {
	if m.status == "running" {
		return "ctrl+x cancel  /status info  /skip task  /undo revert"
	}
	return "enter submit  shift+enter newline  ↑/↓ history  /help  /skip  /undo  /clear"
}

func (m *Model) renderRuntimeSummary() string {
	parts := make([]string, 0, 4)
	if mode := strings.TrimSpace(m.runtimeInfo.Mode); mode != "" {
		parts = append(parts, "mode: "+mode)
	}
	if provider := strings.TrimSpace(m.runtimeInfo.Provider); provider != "" {
		if model := strings.TrimSpace(m.runtimeInfo.Model); model != "" {
			parts = append(parts, fmt.Sprintf("llm: %s/%s", provider, model))
		} else {
			parts = append(parts, "llm: "+provider)
		}
	} else if model := strings.TrimSpace(m.runtimeInfo.Model); model != "" {
		parts = append(parts, "model: "+model)
	}
	if m.runtimeInfo.AllowShellKnown {
		if m.runtimeInfo.AllowShell {
			parts = append(parts, "shell: allowed")
		} else {
			parts = append(parts, "shell: blocked")
		}
	}
	if source := strings.TrimSpace(m.runtimeInfo.ConfigSource); source != "" {
		parts = append(parts, "config: "+source)
	}
	return strings.Join(parts, " | ")
}

func (m *Model) helpLines() []string {
	lines := []string{
		"── Navigation ──",
		"tab / shift+tab  switch focused panel",
		"1/2  switch Chat/History tab",
		"j/k, up/down, pgup/pgdn, g/G  scroll panel",
		"",
		"── Input ──",
		"enter  submit prompt",
		"shift+enter  newline in input",
		"↑/↓  browse prompt history (single-line mode)",
		"enter  (on Changed panel) preview selected file",
		"",
		"── Slash Commands ──",
		"/skip [id]  skip current or specified task",
		"/undo       undo last task operation",
		"/clear      clear chat log",
		"/status     show workflow stats inline",
		"/rename <n> rename current session",
		"/compact    info about context compaction",
		"/help       show this help",
		"",
		"── Actions ──",
		"c or m  open model/config overlay",
		"ctrl+x  cancel active workflow",
		"q  quit (when not running)",
		"",
		"── Sessions (Alt+key) ──",
		"Alt+n  new session  Alt+w  close session",
		"Alt+[/]  switch session  Alt+1-9  jump to session",
	}
	return lines
}

func (m *Model) runtimeOverlay() *alertState {
	items := make([]overlayItem, 0, 24)

	items = append(items, overlayItem{kind: "section", text: "LIVE RUNTIME"})
	if mode := strings.TrimSpace(m.runtimeInfo.Mode); mode != "" {
		items = append(items, overlayItem{kind: "kv", text: "mode: " + mode})
	}
	if provider := strings.TrimSpace(m.runtimeInfo.Provider); provider != "" {
		items = append(items, overlayItem{kind: "kv", text: "provider: " + provider})
	} else {
		items = append(items, overlayItem{kind: "kv-muted", text: "provider: unknown"})
	}
	if modelName := strings.TrimSpace(m.runtimeInfo.Model); modelName != "" {
		items = append(items, overlayItem{kind: "kv", text: "primary model: " + modelName})
	} else {
		items = append(items, overlayItem{kind: "kv-muted", text: "primary model: unknown"})
	}
	if lightModel := strings.TrimSpace(m.runtimeInfo.LightModel); lightModel != "" && lightModel != strings.TrimSpace(m.runtimeInfo.Model) {
		items = append(items, overlayItem{kind: "kv", text: "light model: " + lightModel})
	}

	switch {
	case !m.runtimeInfo.AllowShellKnown:
		items = append(items, overlayItem{kind: "kv-muted", text: "shell execution: unknown"})
	case m.runtimeInfo.AllowShell:
		items = append(items, overlayItem{kind: "ok", text: "shell execution: allowed"})
	default:
		items = append(items, overlayItem{kind: "warn", text: "shell execution: blocked"})
	}

	if source := strings.TrimSpace(m.runtimeInfo.ConfigSource); source != "" {
		items = append(items, overlayItem{kind: "kv", text: "config source: " + source})
	}
	if strings.TrimSpace(m.sessionID) != "" {
		items = append(items, overlayItem{kind: "kv", text: "session: " + m.sessionID})
	}
	if strings.TrimSpace(m.activeWorkflowID) != "" {
		items = append(items, overlayItem{kind: "kv", text: "active workflow: " + m.activeWorkflowID})
	}

	items = append(items, overlayItem{kind: "spacer"})
	items = append(items, overlayItem{kind: "section", text: "AVAILABLE NOW"})
	items = append(items,
		overlayItem{kind: "action", text: "[open] focus chat panel", action: "focus_chat"},
		overlayItem{kind: "action", text: "[open] focus input panel", action: "focus_input"},
		overlayItem{kind: "action", text: "[open] focus changed code panel", action: "focus_changed"},
		overlayItem{kind: "action", text: "[do] dismiss overlay", action: "dismiss"},
	)

	items = append(items, overlayItem{kind: "spacer"})
	items = append(items, overlayItem{kind: "section", text: "SESSIONS"})
	items = append(items,
		overlayItem{kind: "action", text: "[Alt+n] new session", action: "new_session"},
		overlayItem{kind: "action", text: "[Alt+w] close session", action: "close_session"},
		overlayItem{kind: "action", text: "[Alt+[/]] switch session"},
	)

	items = append(items, overlayItem{kind: "spacer"})
	items = append(items, overlayItem{kind: "section", text: "PLANNED"})
	items = append(items,
		overlayItem{kind: "disabled", text: "[unavailable] switch model at runtime"},
		overlayItem{kind: "disabled", text: "[unavailable] edit runtime config"},
		overlayItem{kind: "todo", text: "[todo] interactive permission approval"},
	)
	items = append(items, overlayItem{kind: "spacer"})
	items = append(items, overlayItem{kind: "section", text: "CURRENT MVP PATH"})
	items = append(items,
		overlayItem{kind: "kv", text: "restart with CLI/env to change model/config"},
		overlayItem{kind: "kv", text: "use --allow-shell when shell tools must be permitted"},
	)

	return &alertState{
		level:  "info",
		title:  "Model / Config",
		items:  items,
		footer: "j/k move  enter select  esc dismiss",
	}
}

func (m *Model) focusLabels() []string {
	order := m.focusOrder()
	labels := make([]string, 0, len(order))
	for _, item := range order {
		switch item {
		case focusChat:
			labels = append(labels, "Chat")
		case focusInput:
			labels = append(labels, "Input")
		case focusTodo:
			labels = append(labels, "Workers + Tasks")
		case focusChanged:
			labels = append(labels, "Changed Code")
		}
	}
	return labels
}

func (m *Model) renderAlertBox(width int) string {
	if m.alert == nil {
		return ""
	}
	borderColor := styles.ColorWarning
	titleBarStyle := styles.WarningText.
		Background(styles.ColorBg).
		Bold(true).
		Padding(0, 1)
	if m.alert.level == "error" {
		borderColor = styles.ColorError
		titleBarStyle = styles.ErrorText.Background(styles.ColorBg).Bold(true).Padding(0, 1)
	} else if m.alert.level == "info" {
		borderColor = styles.ColorPrimary
		titleBarStyle = styles.PrimaryText.Background(styles.ColorBg).Bold(true).Padding(0, 1)
	}
	bodyLines := []string{
		titleBarStyle.Width(width - 4).Render(m.alert.title),
		"",
	}
	if len(m.alert.items) > 0 {
		selectable := m.overlaySelectableIndices()
		selectedItem := -1
		if len(selectable) > 0 {
			m.clampOverlayCursor()
			selectedItem = selectable[m.alert.cursor]
		}
		for index, item := range m.alert.items {
			selected := index == selectedItem
			switch item.kind {
			case "spacer":
				bodyLines = append(bodyLines, "")
			case "section":
				bodyLines = append(bodyLines, styles.BoldText.Render(item.text))
			case "action":
				line := "  " + item.text
				if selected {
					line = styles.PanelFocus.Padding(0, 1).Render("> " + item.text)
				} else {
					line = styles.PrimaryText.Render("  " + item.text)
				}
				bodyLines = append(bodyLines, line)
			case "disabled":
				// disabled: 视觉上弱于 action，选中态用不同前缀
				line := "  " + item.text
				if selected {
					line = styles.MutedText.Render("> " + item.text)
				} else {
					line = styles.MutedText.Render("  " + item.text)
				}
				bodyLines = append(bodyLines, line)
			case "todo":
				bodyLines = append(bodyLines, styles.MutedText.Render("  "+item.text))
			case "warn":
				bodyLines = append(bodyLines, styles.WarningText.Render("  "+item.text))
			case "ok":
				bodyLines = append(bodyLines, styles.SuccessText.Render("  "+item.text))
			case "kv-muted":
				bodyLines = append(bodyLines, styles.MutedText.Render("  "+item.text))
			default:
				bodyLines = append(bodyLines, styles.NormalText.Render("  "+item.text))
			}
		}
	} else {
		for _, line := range m.alert.lines {
			raw := strings.TrimRight(line, "\r")
			if strings.TrimSpace(raw) == "" {
				continue
			}
			switch {
			case raw == strings.ToUpper(strings.TrimSpace(raw)):
				bodyLines = append(bodyLines, styles.BoldText.Render(raw))
			case strings.Contains(raw, "[todo]"):
				bodyLines = append(bodyLines, styles.MutedText.Render(raw))
			case strings.Contains(raw, "[do]"), strings.Contains(raw, "[open]"), strings.Contains(raw, "[read]"):
				bodyLines = append(bodyLines, styles.PrimaryText.Render(raw))
			default:
				bodyLines = append(bodyLines, styles.NormalText.Render(raw))
			}
		}
	}
	footer := m.alert.footer
	if strings.TrimSpace(footer) == "" {
		footer = "esc dismiss"
	}
	bodyLines = append(bodyLines, "", styles.MutedText.Render(footer))
	box := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(borderColor).
		Background(styles.ColorBgElevated).
		Padding(1, 2).
		Width(width).
		Render(strings.Join(bodyLines, "\n"))
	return box
}

func overlayCenter(width, height int, base, overlay string) string {
	// Render the overlay box with border and padding
	overlayBox := lipgloss.NewStyle().
		MaxWidth(width-8).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		Render(overlay)

	// Use lipgloss.Place to center the overlay on a blank canvas
	placed := lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, overlayBox)

	// Determine the vertical bounding box of the overlay by finding the first
	// and last lines with non-whitespace content in the placed canvas.
	baseLines := strings.Split(base, "\n")
	placedLines := strings.Split(placed, "\n")

	firstOverlay, lastOverlay := -1, -1
	for i, line := range placedLines {
		if strings.TrimSpace(line) != "" {
			if firstOverlay == -1 {
				firstOverlay = i
			}
			lastOverlay = i
		}
	}

	maxLines := max(len(baseLines), len(placedLines))
	out := make([]string, 0, maxLines)

	for i := 0; i < maxLines; i++ {
		var baseLine, placedLine string
		if i < len(baseLines) {
			baseLine = baseLines[i]
		}
		if i < len(placedLines) {
			placedLine = placedLines[i]
		}

		// Within the overlay bounding box, always use placed lines (even blank
		// ones that are intentional padding inside the overlay dialog).
		if firstOverlay >= 0 && i >= firstOverlay && i <= lastOverlay {
			out = append(out, placedLine)
		} else if baseLine != "" {
			out = append(out, baseLine)
		} else {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}
