package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mingzhi1/coden/internal/ui/styles"
)

// View renders the full AppModel view: tab bar (if multi-session) + active session content.
func (app *AppModel) View() tea.View {
	active := app.activeSession()
	if active == nil {
		v := tea.NewView("No sessions open. Press Alt+n to create one.")
		v.AltScreen = true
		return v
	}

	width := max(minWidth, app.width)
	height := max(minHeight, app.height)

	var content string
	if len(app.sessions) > 1 {
		tabBar := app.renderTabBar(width)
		// Session gets height minus tab bar line
		sessionContent := active.RenderContent(width, height-1)
		content = lipgloss.JoinVertical(lipgloss.Top, tabBar, sessionContent)
	} else {
		// Single session: no tab bar, full height
		content = active.RenderContent(width, height)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// renderTabBar renders the session tab bar.
func (app *AppModel) renderTabBar(width int) string {
	tabActive := lipgloss.NewStyle().
		Background(styles.ColorBgHighlight).
		Foreground(styles.ColorText).
		Bold(true).
		Padding(0, 1)

	tabInactive := lipgloss.NewStyle().
		Foreground(styles.ColorMuted).
		Padding(0, 1)

	tabRunning := lipgloss.NewStyle().
		Foreground(styles.ColorWarning).
		Padding(0, 1)

	var tabs []string
	for i, s := range app.sessions {
		label := fmt.Sprintf("%d:%s", i+1, s.sessionID)
		if i == app.activeIdx {
			tabs = append(tabs, tabActive.Render(label))
		} else if s.status == "running" {
			tabs = append(tabs, tabRunning.Render(label+"*"))
		} else {
			tabs = append(tabs, tabInactive.Render(label))
		}
	}

	// Overflow: if >9 tabs, show first 4 + ... + last 4
	if len(tabs) > 9 {
		mid := []string{"…"}
		tabs = append(append(tabs[:4], mid...), tabs[len(tabs)-4:]...)
	}

	hint := styles.MutedText.Render(" Alt+n new  Alt+[/] switch")
	bar := strings.Join(tabs, "") + hint

	return lipgloss.NewStyle().
		Background(styles.ColorBg).
		MaxWidth(width).
		Width(width).
		Render(bar)
}
