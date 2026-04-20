package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Init initializes the AppModel by delegating to the active session's Init.
func (app *AppModel) Init() tea.Cmd {
	active := app.activeSession()
	if active == nil {
		return nil
	}
	return active.Init()
}

// Update routes messages: global keys → EventMsg by SessionID → active session.
func (app *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// App-level overlay intercepts key events first.
	if app.appAlert != nil {
		if kp, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case kp.Code == tea.KeyEsc:
				app.appAlert = nil
			case kp.Code == tea.KeyUp, kp.Text == "k":
				app.moveAppOverlayCursor(-1)
			case kp.Code == tea.KeyDown, kp.Text == "j":
				app.moveAppOverlayCursor(1)
			case kp.Code == tea.KeyEnter:
				if cmd := app.activateAppOverlayAction(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return app, tea.Batch(cmds...)
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		app.width = msg.Width
		app.height = msg.Height
		// Propagate to ALL sessions so they know the size
		for _, s := range app.sessions {
			updated, cmd := s.Model.Update(msg)
			s.Model = updated.(*Model)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return app, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		// Intercept global Alt+key shortcuts before delegating
		if handled, cmd := app.handleGlobalKey(msg); handled {
			return app, cmd
		}

	case tea.MouseWheelMsg:
		// Mouse wheel goes to active session only
		active := app.activeSession()
		if active != nil {
			updated, cmd := active.Model.Update(msg)
			active.Model = updated.(*Model)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return app, tea.Batch(cmds...)

	case EventMsg:
		// Route to the session that owns this event
		for _, s := range app.sessions {
			if s.sessionID == msg.SessionID {
				updated, cmd := s.Model.Update(msg)
				s.Model = updated.(*Model)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				break
			}
		}
		return app, tea.Batch(cmds...)

	case NewSessionCreatedMsg:
		// Add the new session to the app
		cmd := app.AddSessionFromEvent(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return app, tea.Batch(cmds...)

	case ErrMsg:
		// Route error to active session for display
		active := app.activeSession()
		if active != nil {
			updated, cmd := active.Model.Update(msg)
			active.Model = updated.(*Model)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return app, tea.Batch(cmds...)

	case OverlayRequestNewSessionMsg:
		cmd := app.CreateNewSession()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return app, tea.Batch(cmds...)

	case OverlayRequestCloseSessionMsg:
		shouldExit, err := app.CloseActiveSession()
		if err != nil {
			active := app.activeSession()
			if active != nil {
				cmds = append(cmds, func() tea.Msg { return ErrMsg{Err: err} })
			}
			return app, tea.Batch(cmds...)
		}
		if shouldExit {
			return app, tea.Quit
		}
		return app, tea.Batch(cmds...)
	}

	// Default: delegate to active session
	active := app.activeSession()
	if active != nil {
		updated, cmd := active.Model.Update(msg)
		active.Model = updated.(*Model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return app, tea.Batch(cmds...)
}

// handleGlobalKey intercepts Alt+ shortcuts for session management.
// Returns (handled, cmd): handled=true means the key was consumed and must NOT be delegated.
func (app *AppModel) handleGlobalKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	// Only intercept Alt+ combinations
	if msg.Mod&tea.ModAlt == 0 {
		return false, nil
	}

	switch {
	// Alt+1..9: switch to session N
	case msg.Code >= '1' && msg.Code <= '9':
		idx := int(msg.Code - '1')
		app.switchToSession(idx)
		return true, nil

	// Alt+[: previous session
	case msg.Code == '[':
		if app.activeIdx > 0 {
			app.switchToSession(app.activeIdx - 1)
		}
		return true, nil

	// Alt+]: next session
	case msg.Code == ']':
		if app.activeIdx < len(app.sessions)-1 {
			app.switchToSession(app.activeIdx + 1)
		}
		return true, nil

	// Alt+n: new session
	case msg.Code == 'n':
		return true, app.CreateNewSession()

	// Alt+w: close session
	case msg.Code == 'w':
		shouldExit, err := app.CloseActiveSession()
		if err != nil {
			// Show error in active session
			active := app.activeSession()
			if active != nil {
				return true, func() tea.Msg {
					return ErrMsg{Err: err}
				}
			}
			return true, nil
		}
		if shouldExit {
			return true, tea.Quit
		}
		return true, nil

	// Alt+s: session picker overlay
	case msg.Code == 's':
		app.appAlert = app.buildSessionPickerOverlay()
		return true, nil
	}

	return false, nil // not handled — will be delegated
}

// buildSessionPickerOverlay constructs the session list overlay for Alt+s.
func (app *AppModel) buildSessionPickerOverlay() *alertState {
	items := []overlayItem{
		{kind: "section", text: "SESSIONS"},
	}
	for i, s := range app.sessions {
		marker := "  "
		if i == app.activeIdx {
			marker = "▶ "
		}
		status := s.status
		if status == "" {
			status = "idle"
		}
		label := fmt.Sprintf("%s%d: %s  [%s]", marker, i+1, s.sessionID, status)
		items = append(items, overlayItem{
			kind:   "action",
			text:   label,
			action: fmt.Sprintf("switch_session:%d", i),
		})
	}
	items = append(items,
		overlayItem{kind: "spacer"},
		overlayItem{kind: "action", text: "[n] new session", action: "new_session"},
	)
	return &alertState{
		level:  "info",
		title:  "Sessions",
		items:  items,
		footer: "j/k move  enter select  esc dismiss",
	}
}

// moveAppOverlayCursor moves the cursor in the app-level overlay.
func (app *AppModel) moveAppOverlayCursor(delta int) {
	if app.appAlert == nil {
		return
	}
	var indices []int
	for i, item := range app.appAlert.items {
		if strings.TrimSpace(item.action) != "" {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return
	}
	app.appAlert.cursor = clamp(app.appAlert.cursor+delta, 0, len(indices)-1)
}

// activateAppOverlayAction dispatches the currently selected app overlay action.
func (app *AppModel) activateAppOverlayAction() tea.Cmd {
	if app.appAlert == nil {
		return nil
	}
	var indices []int
	for i, item := range app.appAlert.items {
		if strings.TrimSpace(item.action) != "" {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 || app.appAlert.cursor >= len(indices) {
		app.appAlert = nil
		return nil
	}
	action := app.appAlert.items[indices[app.appAlert.cursor]].action
	app.appAlert = nil

	if strings.HasPrefix(action, "switch_session:") {
		var idx int
		fmt.Sscanf(strings.TrimPrefix(action, "switch_session:"), "%d", &idx)
		app.switchToSession(idx)
		return nil
	}
	switch action {
	case "new_session":
		return app.CreateNewSession()
	}
	return nil
}

// switchToSession changes the active session index, clamped to valid range.
func (app *AppModel) switchToSession(idx int) {
	if idx < 0 || idx >= len(app.sessions) {
		return
	}
	app.activeIdx = idx
}
