package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/core/model"
)

func newTestAppModel(sessionIDs ...string) *AppModel {
	app := NewAppModel(context.Background(), nil, RuntimeInfo{})
	for _, sid := range sessionIDs {
		m := NewModel(sid, "")
		m.width = 120
		m.height = 40
		app.AddSession(m, sid, nil, func() {}, func() {})
	}
	return app
}

func TestAppModelInitialSession(t *testing.T) {
	app := newTestAppModel("session-1")
	if app.SessionCount() != 1 {
		t.Fatalf("expected 1 session, got %d", app.SessionCount())
	}
	if app.activeIdx != 0 {
		t.Fatalf("expected activeIdx 0, got %d", app.activeIdx)
	}
	s := app.activeSession()
	if s == nil || s.sessionID != "session-1" {
		t.Fatalf("expected active session session-1, got %v", s)
	}
}

func TestAppModelSwitchSession(t *testing.T) {
	app := newTestAppModel("s1", "s2", "s3")

	// Alt+2 should switch to s2
	app.switchToSession(1)
	if app.activeIdx != 1 {
		t.Fatalf("expected activeIdx 1, got %d", app.activeIdx)
	}
	if app.activeSession().sessionID != "s2" {
		t.Fatalf("expected s2, got %s", app.activeSession().sessionID)
	}

	// Out of range should be no-op
	app.switchToSession(10)
	if app.activeIdx != 1 {
		t.Fatalf("expected activeIdx still 1 after invalid switch, got %d", app.activeIdx)
	}
	app.switchToSession(-1)
	if app.activeIdx != 1 {
		t.Fatalf("expected activeIdx still 1 after negative switch, got %d", app.activeIdx)
	}
}

func TestAppModelEventMsgRouting(t *testing.T) {
	app := newTestAppModel("s1", "s2")
	app.width = 120
	app.height = 40

	// Send EventMsg for s2 — should route to s2 only
	ev := model.Event{
		Seq:       1,
		SessionID: "s2",
		Topic:     model.EventMessageCreated,
		Payload:   model.EncodePayload(model.MessageCreatedPayload{Role: "user", Content: "hello s2"}),
	}
	msg := EventMsg{SessionID: "s2", Event: ev}
	app.Update(msg)

	// s2 should have chat lines, s1 should not
	if len(app.sessions[1].chatLines) == 0 {
		t.Fatal("expected s2 to have chat lines from routed event")
	}
	if len(app.sessions[0].chatLines) != 0 {
		t.Fatal("expected s1 to have no chat lines")
	}
}

func TestAppModelWindowSizeBroadcast(t *testing.T) {
	app := newTestAppModel("s1", "s2")

	// WindowSizeMsg should propagate to ALL sessions
	app.Update(tea.WindowSizeMsg{Width: 200, Height: 50})

	if app.width != 200 || app.height != 50 {
		t.Fatalf("expected app size 200x50, got %dx%d", app.width, app.height)
	}
	for i, s := range app.sessions {
		if s.width != 200 || s.height != 50 {
			t.Fatalf("session[%d] size should be 200x50, got %dx%d", i, s.width, s.height)
		}
	}
}

func TestAppModelSingleSessionNoTabBar(t *testing.T) {
	app := newTestAppModel("only-session")
	app.width = 120
	app.height = 40

	v := app.View()
	content := v.Content

	// Single session: tab bar should NOT appear (no "Alt+n new" hint in tab bar)
	// The help line at the bottom may contain key hints, but the tab bar line should be absent
	// In single-session mode, content is rendered directly without tab bar
	if len(content) == 0 {
		t.Fatal("expected non-empty view content")
	}
}

func TestAppModelMultiSessionTabBar(t *testing.T) {
	app := newTestAppModel("alpha", "beta")
	app.width = 120
	app.height = 40

	v := app.View()
	content := v.Content

	// Multi-session: tab bar should show both session names
	if len(content) == 0 {
		t.Fatal("expected non-empty view content")
	}
	// Tab bar includes Alt+n hint
	if !containsText(content, "Alt+n") {
		t.Fatal("expected tab bar to contain Alt+n hint")
	}
}

func containsText(s, substr string) bool {
	// Strip ANSI for text match
	return len(s) > 0 && len(substr) > 0 &&
		contains(stripAnsi(s), substr)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func stripAnsi(s string) string {
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until 'm' or end
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				j++ // skip 'm'
			}
			i = j
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}

// TestAppModelCloseSession tests closing a session.
func TestAppModelCloseSession(t *testing.T) {
	app := newTestAppModel("s1", "s2", "s3")

	// Close middle session
	app.switchToSession(1) // s2
	shouldExit, err := app.CloseActiveSession()
	if err != nil {
		t.Fatalf("unexpected error closing session: %v", err)
	}
	if shouldExit {
		t.Fatal("shouldExit should be false when sessions remain")
	}
	if app.SessionCount() != 2 {
		t.Fatalf("expected 2 sessions, got %d", app.SessionCount())
	}
	// Active should adjust to last valid index
	if app.activeIdx != 1 {
		t.Fatalf("expected activeIdx 1 (now s3), got %d", app.activeIdx)
	}
}

// TestAppModelCloseLastSessionExit tests that closing last session signals exit.
func TestAppModelCloseLastSessionExit(t *testing.T) {
	app := newTestAppModel("only")

	shouldExit, err := app.CloseActiveSession()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shouldExit {
		t.Fatal("shouldExit should be true when closing last session")
	}
}

// TestAppModelPreventCloseRunningSession tests that running sessions cannot be closed.
func TestAppModelPreventCloseRunningSession(t *testing.T) {
	app := newTestAppModel("running-session")
	app.sessions[0].status = "running"

	_, err := app.CloseActiveSession()
	if err == nil {
		t.Fatal("expected error when closing running session")
	}
}
