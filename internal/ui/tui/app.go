package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"
)

// appMode represents the current mode of the AppModel.
type appMode int

const (
	appModeNormal        appMode = iota // Normal session interaction
	appModeNewSession                   // Entering new session name
	appModeSessionPicker                // Browsing session list
)

// AppModel is the top-level Bubble Tea model that manages multiple sessions,
// similar to tmux's server managing multiple sessions/windows.
type AppModel struct {
	sessions    []*SessionModel
	activeIdx   int
	mode        appMode
	appAlert    *alertState // app-level overlay (session picker, system errors)
	width       int
	height      int
	client      api.ClientAPI
	ctx         context.Context
	runtimeInfo RuntimeInfo
}

// SessionModel wraps a Model with lifecycle fields for multi-session management.
type SessionModel struct {
	*Model
	sessionID string
	cancel    context.CancelFunc
	eventCh   <-chan model.Event
	cleanup   func()
}

// NewAppModel creates an AppModel with an initial session.
func NewAppModel(ctx context.Context, client api.ClientAPI, info RuntimeInfo) *AppModel {
	return &AppModel{
		client:      client,
		ctx:         ctx,
		runtimeInfo: info,
	}
}

// AddSession creates a SessionModel and appends it to the session list.
// The caller must provide a fully constructed Model with event stream wired.
func (app *AppModel) AddSession(m *Model, sessionID string, eventCh <-chan model.Event, cleanup func(), cancel context.CancelFunc) {
	s := &SessionModel{
		Model:     m,
		sessionID: sessionID,
		cancel:    cancel,
		eventCh:   eventCh,
		cleanup:   cleanup,
	}
	app.sessions = append(app.sessions, s)
	app.activeIdx = len(app.sessions) - 1
}

// activeSession returns the currently active SessionModel, or nil.
func (app *AppModel) activeSession() *SessionModel {
	if app.activeIdx >= 0 && app.activeIdx < len(app.sessions) {
		return app.sessions[app.activeIdx]
	}
	return nil
}

// SessionCount returns the number of open sessions.
func (app *AppModel) SessionCount() int {
	return len(app.sessions)
}

// CreateNewSession creates a new session with a generated name and adds it to the app.
func (app *AppModel) CreateNewSession() tea.Cmd {
	// Generate session name: session-1, session-2, etc.
	baseName := "session"
	var sessionID string
	for i := 1; i <= 100; i++ {
		candidate := fmt.Sprintf("%s-%d", baseName, i)
		exists := false
		for _, s := range app.sessions {
			if s.sessionID == candidate {
				exists = true
				break
			}
		}
		if !exists {
			sessionID = candidate
			break
		}
	}
	if sessionID == "" {
		return func() tea.Msg {
			return ErrMsg{Err: fmt.Errorf("cannot generate unique session name")}
		}
	}

	return func() tea.Msg {
		_, err := app.client.CreateSession(app.ctx, sessionID)
		if err != nil {
			return ErrMsg{Err: fmt.Errorf("create session failed: %w", err)}
		}
		return NewSessionCreatedMsg{SessionID: sessionID}
	}
}

// AddSessionFromEvent creates a fully wired SessionModel for a newly created session.
func (app *AppModel) AddSessionFromEvent(msg NewSessionCreatedMsg) tea.Cmd {
	sessionID := msg.SessionID

	events, cleanup, err := app.client.Subscribe(app.ctx, sessionID)
	if err != nil {
		return func() tea.Msg {
			return ErrMsg{Err: fmt.Errorf("subscribe to new session failed: %w", err)}
		}
	}

	sessionCtx, cancel := context.WithCancel(app.ctx)

	// Build callbacks (similar to run.go)
	submitter := func(prompt string) tea.Cmd {
		return func() tea.Msg {
			workflowID, err := app.client.Submit(sessionCtx, sessionID, prompt)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return WorkflowAcceptedMsg{WorkflowID: workflowID}
		}
	}

	objectsLoader := func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			items, err := api.LoadWorkflowObjectDetails(sessionCtx, app.client, sessionID, workflowID)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return WorkflowObjectsLoadedMsg{WorkflowID: workflowID, Items: items}
		}
	}

	checkpointLoader := func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			result, err := app.client.GetCheckpoint(sessionCtx, sessionID, workflowID)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return CheckpointMsg{Result: result}
		}
	}

	canceler := func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			if err := app.client.CancelWorkflow(sessionCtx, sessionID, workflowID); err != nil {
				return WorkflowCancelFailedMsg{WorkflowID: workflowID, Err: err}
			}
			return WorkflowCancelRequestedMsg{WorkflowID: workflowID}
		}
	}

	workspaceReader := func(path string) tea.Cmd {
		return func() tea.Msg {
			data, err := app.client.WorkspaceRead(sessionCtx, sessionID, path)
			if err != nil {
				return WorkspaceReadLoadedMsg{Path: path, Err: err}
			}
			return WorkspaceReadLoadedMsg{Path: path, Content: string(data)}
		}
	}

	snapshotLoader := func() tea.Cmd {
		return func() tea.Msg {
			snapshot, err := api.LoadSessionSnapshot(sessionCtx, app.client, sessionID)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return SessionSnapshotLoadedMsg{Snapshot: snapshot}
		}
	}

	// Create Model for new session with all callbacks
	taskSkipper := func(taskID string) tea.Cmd {
		return func() tea.Msg {
			if err := app.client.SkipTask(sessionCtx, sessionID, taskID); err != nil {
				return TaskSkipResultMsg{TaskID: taskID, Err: err}
			}
			return TaskSkipResultMsg{TaskID: taskID}
		}
	}
	taskUndoer := func() tea.Cmd {
		return func() tea.Msg {
			restored, err := app.client.UndoTask(sessionCtx, sessionID)
			if err != nil {
				return TaskUndoResultMsg{Err: err}
			}
			return TaskUndoResultMsg{RestoredTaskID: restored}
		}
	}
	sessionRenamer := func(name string) tea.Cmd {
		return func() tea.Msg {
			_, err := app.client.RenameSession(sessionCtx, sessionID, name)
			if err != nil {
				return SessionRenameResultMsg{Err: err}
			}
			return SessionRenameResultMsg{Name: name}
		}
	}
	m := NewModel(sessionID, "").
		WithEventStream(events).
		WithSubmitter(submitter).
		WithWorkflowObjectsLoader(objectsLoader).
		WithSessionSnapshotLoader(snapshotLoader).
		WithCanceler(canceler).
		WithCheckpointLoader(checkpointLoader).
		WithWorkspaceReader(workspaceReader).
		WithTaskSkipper(taskSkipper).
		WithTaskUndoer(taskUndoer).
		WithSessionRenamer(sessionRenamer).
		WithRuntimeInfo(app.runtimeInfo)

	app.AddSession(m, sessionID, events, cleanup, cancel)

	// Initialise the new session (starts spinner, snapshot loader, and event stream).
	return m.Init()
}

// CloseActiveSession closes the currently active session.
// Returns true if the app should exit (last session closed).
func (app *AppModel) CloseActiveSession() (shouldExit bool, err error) {
	active := app.activeSession()
	if active == nil {
		return false, fmt.Errorf("no active session")
	}

	// Prevent closing if running
	if active.status == "running" {
		return false, fmt.Errorf("cannot close session while workflow is running")
	}

	// Cleanup session
	if active.cancel != nil {
		active.cancel()
	}
	if active.cleanup != nil {
		active.cleanup()
	}

	// Remove from sessions slice
	idx := app.activeIdx
	app.sessions = append(app.sessions[:idx], app.sessions[idx+1:]...)

	// Adjust active index
	if len(app.sessions) == 0 {
		return true, nil // Exit app
	}
	if app.activeIdx >= len(app.sessions) {
		app.activeIdx = len(app.sessions) - 1
	}
	return false, nil
}

// NewSessionCreatedMsg is sent when a new session is successfully created.
type NewSessionCreatedMsg struct {
	SessionID string
}
