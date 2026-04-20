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
		sessionContent := active.RenderContent(width, height-1)
		content = lipgloss.JoinVertical(lipgloss.Top, tabBar, sessionContent)
	} else {
		content = active.RenderContent(width, height)
	}

	if app.appAlert != nil {
		content = overlayCenter(width, height, content, app.renderAppAlertBox(min(72, max(42, width-12))))
	}

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// renderAppAlertBox renders the app-level overlay box (session picker, system errors).
func (app *AppModel) renderAppAlertBox(width int) string {
	if app.appAlert == nil {
		return ""
	}
	titleBarStyle := styles.PrimaryText.Background(styles.ColorBg).Bold(true).Padding(0, 1)
	borderColor := styles.ColorPrimary
	if app.appAlert.level == "error" {
		borderColor = styles.ColorError
		titleBarStyle = styles.ErrorText.Background(styles.ColorBg).Bold(true).Padding(0, 1)
	}

	bodyLines := []string{
		titleBarStyle.Width(width - 4).Render(app.appAlert.title),
		"",
	}

	// Compute selectable indices for cursor rendering
	var selectable []int
	for i, item := range app.appAlert.items {
		if strings.TrimSpace(item.action) != "" {
			selectable = append(selectable, i)
		}
	}
	selectedItem := -1
	if len(selectable) > 0 {
		cursor := clamp(app.appAlert.cursor, 0, len(selectable)-1)
		selectedItem = selectable[cursor]
	}

	for idx, item := range app.appAlert.items {
		selected := idx == selectedItem
		switch item.kind {
		case "section":
			bodyLines = append(bodyLines, styles.MutedText.Render("── "+item.text+" ──"))
		case "spacer":
			bodyLines = append(bodyLines, "")
		case "kv":
			bodyLines = append(bodyLines, styles.MutedText.Render(item.text))
		case "action":
			if selected {
				bodyLines = append(bodyLines, styles.PrimaryText.Bold(true).Render("▶ "+item.text))
			} else {
				bodyLines = append(bodyLines, "  "+item.text)
			}
		default:
			bodyLines = append(bodyLines, item.text)
		}
	}

	footer := app.appAlert.footer
	if strings.TrimSpace(footer) == "" {
		footer = "esc dismiss"
	}
	bodyLines = append(bodyLines, "", styles.MutedText.Render(footer))

	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(borderColor).
		Background(styles.ColorBgElevated).
		Padding(1, 2).
		Width(width).
		Render(strings.Join(bodyLines, "\n"))
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
