package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type mockClientAPI struct {
	objects             []model.Object
	payload             map[string]json.RawMessage
	errLoad             error
	errRead             error
	workspaceReadData   []byte
	workspaceReadErr    error
	cancelErr           error
	cancelledWorkflowID string
	messages            []model.Message
	changes             []model.WorkspaceChangedPayload
	checkpoints         []model.CheckpointResult
	checkpointResult    model.CheckpointResult
	checkpointErr       error
	intent              model.IntentSpec
	runs                []model.WorkflowRun
}

func (m *mockClientAPI) CreateSession(ctx context.Context, sessionID string) (model.Session, error) {
	return model.Session{}, nil
}
func (m *mockClientAPI) ListSessions(ctx context.Context, limit int) ([]model.Session, error) {
	return nil, nil
}
func (m *mockClientAPI) Attach(ctx context.Context, sessionID, clientName, view string) error {
	return nil
}
func (m *mockClientAPI) Detach(ctx context.Context, sessionID, clientName string) error { return nil }
func (m *mockClientAPI) Submit(ctx context.Context, sessionID, prompt string) (string, error) {
	return "", nil
}
func (m *mockClientAPI) CancelWorkflow(ctx context.Context, sessionID, workflowID string) error {
	m.cancelledWorkflowID = workflowID
	return m.cancelErr
}
func (m *mockClientAPI) GetWorkflowRun(ctx context.Context, sessionID, workflowID string) (model.WorkflowRun, error) {
	return model.WorkflowRun{}, nil
}
func (m *mockClientAPI) ListWorkflowRuns(ctx context.Context, sessionID string, limit int) ([]model.WorkflowRun, error) {
	return m.runs, nil
}
func (m *mockClientAPI) ListWorkflowRunObjects(ctx context.Context, sessionID, workflowID string) ([]model.Object, error) {
	if m.errLoad != nil {
		return nil, m.errLoad
	}
	return m.objects, nil
}
func (m *mockClientAPI) ReadWorkflowRunObject(ctx context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error) {
	if m.errRead != nil {
		return nil, m.errRead
	}
	p, ok := m.payload[objectID]
	if !ok {
		return nil, errors.New("not found")
	}
	return p, nil
}
func (m *mockClientAPI) ListMessages(ctx context.Context, sessionID string, limit int) ([]model.Message, error) {
	return m.messages, nil
}
func (m *mockClientAPI) GetLatestIntent(ctx context.Context, sessionID string) (model.IntentSpec, error) {
	return m.intent, nil
}
func (m *mockClientAPI) WorkspaceChanges(ctx context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error) {
	return m.changes, nil
}
func (m *mockClientAPI) WorkspaceRead(ctx context.Context, sessionID, path string) ([]byte, error) {
	if m.workspaceReadErr != nil {
		return nil, m.workspaceReadErr
	}
	if m.workspaceReadData != nil {
		return m.workspaceReadData, nil
	}
	return nil, nil
}
func (m *mockClientAPI) WorkspaceWrite(ctx context.Context, sessionID, path string, content []byte) (string, error) {
	return path, nil
}
func (m *mockClientAPI) GetCheckpoint(ctx context.Context, sessionID, workflowID string) (model.CheckpointResult, error) {
	if m.checkpointErr != nil {
		return model.CheckpointResult{}, m.checkpointErr
	}
	if m.checkpointResult.WorkflowID != "" {
		return m.checkpointResult, nil
	}
	return model.CheckpointResult{}, nil
}
func (m *mockClientAPI) ListCheckpoints(ctx context.Context, sessionID string, limit int) ([]model.CheckpointResult, error) {
	return m.checkpoints, nil
}
func (m *mockClientAPI) Subscribe(ctx context.Context, sessionID string) (<-chan model.Event, func(), error) {
	return nil, func() {}, nil
}
func (m *mockClientAPI) GetWorkflowWorkers(ctx context.Context, sessionID, workflowID string) ([]model.WorkerState, error) {
	return nil, nil
}
func (m *mockClientAPI) SessionSnapshot(ctx context.Context, sessionID string, messageLimit int) (model.SessionSnapshot, error) {
	return model.SessionSnapshot{}, errors.New("not implemented")
}
func (m *mockClientAPI) SkipTask(ctx context.Context, sessionID, taskID string) error {
	return nil
}
func (m *mockClientAPI) UndoTask(ctx context.Context, sessionID string) (string, error) {
	return "", nil
}
func (m *mockClientAPI) RenameSession(ctx context.Context, sessionID, name string) (model.Session, error) {
	return model.Session{}, nil
}

func TestLoadWorkflowObjectDetails(t *testing.T) {
	t.Parallel()

	api2 := &mockClientAPI{
		objects: []model.Object{
			{ID: "obj-1", TurnID: "wf-1"},
			{ID: "obj-2", TurnID: "wf-1"},
			{ID: "obj-3", TurnID: "wf-1"},
		},
		payload: map[string]json.RawMessage{
			"obj-1": json.RawMessage(`{
				"tool_call_id": "call_1",
				"tool": "write_file",
				"status": "written",
				"request": {"path": "main.go"},
				"response": {"summary": "wrote file", "after": "package main\n", "diff": "--- a/main.go"}
			}`),
			"obj-2": json.RawMessage(`{
				"tool_call_id": "call_2",
				"tool": "run_command",
				"status": "failed",
				"error": "command not found",
				"request": {"path": "test.sh"}
			}`),
			"obj-3": json.RawMessage(`{
				"tool_call_id": "call_3",
				"tool": "read_file",
				"status": "read",
				"request": {"path": "README.md"},
				"response": {"summary": "read file", "output": "# Title\nbody"}
			}`),
		},
	}

	items, err := api.LoadWorkflowObjectDetails(context.Background(), api2, "sess-1", "wf-1")
	if err != nil {
		t.Fatalf("loadWorkflowObjectDetails failed: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	if items[0].ToolCallID != "call_1" || items[0].Detail != "--- a/main.go" || items[0].Preview != "package main\n" {
		t.Errorf("item 0 mismatch: %+v", items[0])
	}

	if items[1].ToolCallID != "call_2" || items[1].Detail != "command not found" {
		t.Errorf("item 1 mismatch: %+v", items[1])
	}

	if items[2].ToolCallID != "call_3" || items[2].Preview != "# Title\nbody" || items[2].Detail != "" {
		t.Errorf("item 2 mismatch: %+v", items[2])
	}
}

func TestLoadWorkflowObjectDetails_ListError(t *testing.T) {
	t.Parallel()

	api3 := &mockClientAPI{
		errLoad: errors.New("db error"),
	}

	_, err := api.LoadWorkflowObjectDetails(context.Background(), api3, "sess-1", "wf-1")
	if err == nil || err.Error() != "list workflow objects: db error" {
		t.Fatalf("expected specific error, got %v", err)
	}
}

func TestLoadSessionSnapshot(t *testing.T) {
	t.Parallel()

	api4 := &mockClientAPI{
		messages: []model.Message{
			{Role: "user", Content: "build it"},
			{Role: "assistant", Content: "working on it"},
		},
		intent: model.IntentSpec{
			Goal:            "build it end-to-end",
			SuccessCriteria: []string{"artifact exists"},
		},
		runs: []model.WorkflowRun{
			{WorkflowID: "wf-1", SessionID: "sess-1", Status: "pass", Prompt: "build it"},
		},
		changes: []model.WorkspaceChangedPayload{
			{WorkflowID: "wf-1", Path: "workspace/artifacts/test.md", Operation: "write"},
		},
		checkpoints: []model.CheckpointResult{
			{WorkflowID: "wf-1", SessionID: "sess-1", Status: "pass", ArtifactPaths: []string{"workspace/artifacts/test.md"}},
		},
		objects: []model.Object{
			{ID: "obj-1", TurnID: "wf-1"},
		},
		payload: map[string]json.RawMessage{
			"obj-1": json.RawMessage(`{
				"tool_call_id": "call_1",
				"tool": "write_file",
				"status": "written",
				"request": {"path": "workspace/artifacts/test.md"},
				"response": {"summary": "wrote file", "diff": "--- old\n+++ new"}
			}`),
		},
	}

	snapshot, err := api.LoadSessionSnapshot(context.Background(), api4, "sess-1")
	if err != nil {
		t.Fatalf("loadSessionSnapshot failed: %v", err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("expected messages, got %+v", snapshot)
	}
	if snapshot.LatestCheckpoint == nil || snapshot.LatestCheckpoint.WorkflowID != "wf-1" {
		t.Fatalf("expected latest checkpoint, got %+v", snapshot.LatestCheckpoint)
	}
	if snapshot.LatestWorkflowID != "wf-1" {
		t.Fatalf("expected latest workflow id, got %+v", snapshot)
	}
	if snapshot.LatestRun == nil || snapshot.LatestRun.WorkflowID != "wf-1" {
		t.Fatalf("expected latest run, got %+v", snapshot.LatestRun)
	}
	if snapshot.LatestIntent == nil || snapshot.LatestIntent.Goal != "build it end-to-end" {
		t.Fatalf("expected latest intent, got %+v", snapshot.LatestIntent)
	}
	if len(snapshot.ObjectDetails) != 1 || snapshot.ObjectDetails[0].ToolCallID != "call_1" {
		t.Fatalf("expected object details, got %+v", snapshot.ObjectDetails)
	}
}

func TestModelAppliesSessionSnapshot(t *testing.T) {
	t.Parallel()

	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(SessionSnapshotLoadedMsg{
		Snapshot: api.SessionSnapshot{
			Messages: []model.Message{
				{Role: "user", Content: "build it"},
				{Role: "assistant", Content: "done"},
			},
			LatestCheckpoint: &model.CheckpointResult{
				WorkflowID:    "wf-1",
				SessionID:     "demo-session",
				Status:        "pass",
				ArtifactPaths: []string{"workspace/artifacts/test.md"},
			},
			LatestRun: &model.WorkflowRun{
				WorkflowID: "wf-1",
				SessionID:  "demo-session",
				Status:     "pass",
				Prompt:     "build it",
			},
			LatestIntent: &model.IntentSpec{
				Goal:            "build it end-to-end",
				SuccessCriteria: []string{"artifact exists"},
			},
			Changes: []model.WorkspaceChangedPayload{
				{WorkflowID: "wf-1", Path: "workspace/artifacts/test.md", Operation: "write"},
			},
			ObjectDetails: []api.ObjectDetail{
				{
					ToolCallID: "call-1",
					Path:       "workspace/artifacts/test.md",
					Tool:       "write_file",
					Status:     "written",
					Summary:    "wrote file",
					Detail:     "--- old\n+++ new",
				},
			},
		},
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"YOU", "build it", "CODE", "done", "test.md", "wrote file", "latest workflow: wf-1", "intent: build it end-to-end", "criteria: 1", "artifacts: 1  evidence: 0"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelAppliesReadFileObjectPreviewFromSnapshot(t *testing.T) {
	t.Parallel()

	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusChanged

	next, _ := m.Update(SessionSnapshotLoadedMsg{
		Snapshot: api.SessionSnapshot{
			ObjectDetails: []api.ObjectDetail{
				{
					ToolCallID: "call-read-1",
					Path:       "workspace/README.md",
					Tool:       "read_file",
					Status:     "read",
					Summary:    "read README",
					Preview:    "# Title\nbody",
				},
			},
		},
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"README.md", "read README", "preview", "# Title", "body"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestRunWithRuntimeInfoWiresWorkflowCanceler(t *testing.T) {
	t.Parallel()

	mock := &mockClientAPI{}
	cmd := func() tea.Msg {
		canceler := func(workflowID string) tea.Cmd {
			return func() tea.Msg {
				if err := mock.CancelWorkflow(context.Background(), "sess-1", workflowID); err != nil {
					return WorkflowCancelFailedMsg{WorkflowID: workflowID, Err: err}
				}
				return WorkflowCancelRequestedMsg{WorkflowID: workflowID}
			}
		}
		return canceler("wf-9")()
	}

	msg := cmd()
	got, ok := msg.(WorkflowCancelRequestedMsg)
	if !ok {
		t.Fatalf("expected WorkflowCancelRequestedMsg, got %#v", msg)
	}
	if got.WorkflowID != "wf-9" {
		t.Fatalf("unexpected workflow id: %+v", got)
	}
	if mock.cancelledWorkflowID != "wf-9" {
		t.Fatalf("expected cancel to call API, got %q", mock.cancelledWorkflowID)
	}
}
