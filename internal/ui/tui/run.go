package tui

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/api"
	clog "github.com/mingzhi1/coden/internal/log"
)

func Run(ctx context.Context, client api.ClientAPI, sessionID, prompt string) error {
	return RunWithRuntimeInfo(ctx, client, sessionID, prompt, RuntimeInfo{})
}

func RunWithRuntimeInfo(ctx context.Context, client api.ClientAPI, sessionID, prompt string, info RuntimeInfo) error {
	// Redirect log output to a file so it doesn't corrupt the TUI.
	restoreLog := redirectLogToFile()
	defer restoreLog()

	events, cleanup, err := client.Subscribe(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}

	sessionCtx, rawCancel := context.WithCancel(ctx)

	// Wrap cancel+cleanup in sync.Once so they are safe to call multiple times
	// (e.g. once by CloseActiveSession via Alt+W, once at the bottom of Run).
	var cleanupOnce sync.Once
	sessionCancel := func() {
		cleanupOnce.Do(func() {
			rawCancel()
			cleanup()
		})
	}

	submitter := func(prompt string) tea.Cmd {
		return func() tea.Msg {
			workflowID, err := client.Submit(sessionCtx, sessionID, prompt)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return WorkflowAcceptedMsg{WorkflowID: workflowID}
		}
	}

	objectsLoader := func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			items, err := api.LoadWorkflowObjectDetails(sessionCtx, client, sessionID, workflowID)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return WorkflowObjectsLoadedMsg{WorkflowID: workflowID, Items: items}
		}
	}

	checkpointLoader := func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			result, err := client.GetCheckpoint(sessionCtx, sessionID, workflowID)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return CheckpointMsg{Result: result}
		}
	}

	canceler := func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			if err := client.CancelWorkflow(sessionCtx, sessionID, workflowID); err != nil {
				return WorkflowCancelFailedMsg{WorkflowID: workflowID, Err: err}
			}
			return WorkflowCancelRequestedMsg{WorkflowID: workflowID}
		}
	}

	workspaceReader := func(path string) tea.Cmd {
		return func() tea.Msg {
			data, err := client.WorkspaceRead(sessionCtx, sessionID, path)
			if err != nil {
				return WorkspaceReadLoadedMsg{Path: path, Err: err}
			}
			return WorkspaceReadLoadedMsg{Path: path, Content: string(data)}
		}
	}

	snapshotLoader := func() tea.Cmd {
		return func() tea.Msg {
			snapshot, err := api.LoadSessionSnapshot(sessionCtx, client, sessionID)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return SessionSnapshotLoadedMsg{Snapshot: snapshot}
		}
	}

	taskSkipper := func(taskID string) tea.Cmd {
		return func() tea.Msg {
			if err := client.SkipTask(sessionCtx, sessionID, taskID); err != nil {
				return TaskSkipResultMsg{TaskID: taskID, Err: err}
			}
			return TaskSkipResultMsg{TaskID: taskID}
		}
	}

	taskUndoer := func() tea.Cmd {
		return func() tea.Msg {
			restored, err := client.UndoTask(sessionCtx, sessionID)
			if err != nil {
				return TaskUndoResultMsg{Err: err}
			}
			return TaskUndoResultMsg{RestoredTaskID: restored}
		}
	}

	sessionRenamer := func(name string) tea.Cmd {
		return func() tea.Msg {
			_, err := client.RenameSession(sessionCtx, sessionID, name)
			if err != nil {
				return SessionRenameResultMsg{Err: err}
			}
			return SessionRenameResultMsg{Name: name}
		}
	}

	m := NewModel(sessionID, prompt).
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
		WithRuntimeInfo(info)

	// Wrap in AppModel for multi-session support
	app := NewAppModel(ctx, client, info)
	app.AddSession(m, sessionID, events, sessionCancel, sessionCancel)

	_, err = tea.NewProgram(app, tea.WithContext(ctx)).Run()

	// Cleanup: cancel session context and unsubscribe (sync.Once ensures single execution)
	sessionCancel()

	return err
}

// redirectLogToFile ensures log output does not pollute the TUI.
// If the structured logger is already initialised (log.Setup was called),
// it is a no-op — logs are already going to a file.
// Otherwise it redirects both the stdlib log package and slog.Default to a
// fallback file so the terminal is not corrupted.
func redirectLogToFile() func() {
	// Fast path: structured logger already configured by main.
	if clog.Initialized() {
		return func() {}
	}

	// Derive fallback log directory from env.
	dir, _ := os.UserHomeDir()
	if dir == "" {
		if h := os.Getenv("USERPROFILE"); h != "" {
			dir = h
		} else {
			dir = "."
		}
	}
	dir = filepath.Join(dir, ".coden")
	_ = os.MkdirAll(dir, 0o755)

	f, err := os.OpenFile(filepath.Join(dir, "coden.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}
	}

	// Redirect stdlib log.
	origLogWriter := log.Writer()
	log.SetOutput(f)

	// Redirect slog.Default — the primary logger used by the codebase.
	origSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	return func() {
		slog.SetDefault(origSlog)
		log.SetOutput(origLogWriter)
		_ = f.Close()
	}
}
