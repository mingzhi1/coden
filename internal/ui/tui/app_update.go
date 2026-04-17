package tui

import (
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

	// Alt+s: session picker (TODO: Phase 2 — overlay list)
	}

	return false, nil // not handled — will be delegated
}

// switchToSession changes the active session index, clamped to valid range.
func (app *AppModel) switchToSession(idx int) {
	if idx < 0 || idx >= len(app.sessions) {
		return
	}
	app.activeIdx = idx
}
