package kernel

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/checkpoint"
	"github.com/mingzhi1/coden/internal/core/intent"
	"github.com/mingzhi1/coden/internal/core/message"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/objectstore"
	"github.com/mingzhi1/coden/internal/core/session"
	"github.com/mingzhi1/coden/internal/core/storagepath"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/turn"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/core/workspacestore"
)

// shellToolCoder returns a run_shell call.
type shellToolCoder struct{}

func (c shellToolCoder) Build(_ context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{{
			ToolCallID: "shell-call-" + workflowID,
			Request: toolruntime.Request{
				Kind:    "run_shell",
				Command: "echo hello",
			},
		}},
	}, nil
}

func (c shellToolCoder) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{
		{Kind: "info", Role: "coder", Content: "coder produced shell call"},
	}
}

func TestToolAuditPayloadUsesUnifiedSchema(t *testing.T) {
	t.Parallel()

	call := workflow.ToolCall{
		ToolCallID: "tool-1",
		Request: toolruntime.Request{
			Kind:    "run_shell",
			Command: "echo hello",
		},
	}
	result := toolruntime.Result{
		Summary:  "shell command exited with code 7",
		Output:   "out",
		Stderr:   "err",
		ExitCode: 7,
	}

	payload := toolAuditPayload(call, result, "failed", "shell_requires_explicit_approval", "boom")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded["schema"] != "tool_audit.v1" {
		t.Fatalf("unexpected schema: %+v", decoded)
	}
	request, ok := decoded["request"].(map[string]any)
	if !ok || request["command"] != "echo hello" {
		t.Fatalf("unexpected request payload: %+v", decoded)
	}
	response, ok := decoded["response"].(map[string]any)
	if !ok || response["stderr"] != "err" {
		t.Fatalf("unexpected response payload: %+v", decoded)
	}
	policy, ok := decoded["policy"].(map[string]any)
	if !ok || policy["rule"] != "shell_requires_explicit_approval" {
		t.Fatalf("unexpected policy payload: %+v", decoded)
	}
	if decoded["error"] != "boom" {
		t.Fatalf("unexpected error payload: %+v", decoded)
	}
}

func TestDeniedRunShellPersistsAuditObject(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace-project-shell")
	mainDBPath := filepath.Join(mainRoot, ".coden", "main.sqlite")
	k, err := NewPersistentWithWorkflowDependencies(
		workspaceRoot,
		mainDBPath,
		testInputter{},
		testPlanner{},
		shellToolCoder{},
		testExecutor{},
		testAcceptor{},
	)
	if err != nil {
		t.Fatalf("NewPersistentWithWorkflowDependencies failed: %v", err)
	}
	defer k.Close()

	workflowID, err := k.Submit(context.Background(), "session-1", "run shell")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	timeout := time.After(5 * time.Second)
	for {
		run, getErr := k.GetWorkflowRun(context.Background(), "session-1", workflowID)
		if getErr == nil && run.Status == "failed" {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-timeout:
			t.Fatalf("timed out waiting for failed workflow: %+v err=%v", run, getErr)
		}
	}

	objects, err := k.ListWorkflowRunObjects(context.Background(), "session-1", workflowID)
	if err != nil {
		t.Fatalf("ListWorkflowRunObjects failed: %v", err)
	}
	if len(objects) == 0 {
		t.Fatal("expected persisted audit object")
	}

	raw, err := k.ReadWorkflowRunObject(context.Background(), "session-1", workflowID, objects[0].ID)
	if err != nil {
		t.Fatalf("ReadWorkflowRunObject failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if payload["schema"] != "tool_audit.v1" || payload["status"] != "denied" {
		t.Fatalf("unexpected audit payload: %+v", payload)
	}
}

func TestWriteFileAuditPayloadContainsDiff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	call := workflow.ToolCall{
		ToolCallID: "tool-1",
		Request: toolruntime.Request{
			Kind:    "write_file",
			Path:    "artifacts/test.txt",
			Content: "line1\nnew\n",
		},
	}
	result := toolruntime.Result{
		ArtifactPath: filepath.Join(root, "artifacts", "test.txt"),
		Summary:      "wrote artifact",
		Before:       "line1\nold\n",
		After:        "line1\nnew\n",
		Diff:         "--- artifacts/test.txt\n+++ artifacts/test.txt\n line1\n-old\n+new",
	}

	payload := toolAuditPayload(call, result, "written", "", "")
	response, ok := payload["response"].(map[string]any)
	if !ok {
		t.Fatalf("missing response payload: %+v", payload)
	}
	for _, key := range []string{"before", "after", "diff"} {
		if _, exists := response[key]; !exists {
			t.Fatalf("expected response to contain %q: %+v", key, response)
		}
	}
}

func TestPersistentKernelSplitsMainAndWorkspaceSQLite(t *testing.T) {
	t.Parallel()

	mainRoot := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace-project-alpha")
	mainDBPath := filepath.Join(mainRoot, ".coden", "main.sqlite")
	k, err := NewPersistentWithWorkflowDependencies(
		workspaceRoot,
		mainDBPath,
		testInputter{},
		testPlanner{},
		testCoder{},
		testExecutor{},
		testAcceptor{},
	)
	if err != nil {
		t.Fatalf("NewPersistentWithWorkflowDependencies failed: %v", err)
	}

	sessionID := "project-alpha"
	wfID2, err := k.Submit(context.Background(), sessionID, "persist me")
	if err != nil {
		_ = k.Close()
		t.Fatalf("Submit failed: %v", err)
	}
	// Wait for checkpoint to be written before closing.
	evWait, cancelWait := k.Subscribe(sessionID)
	func() {
		timeout := time.After(5 * time.Second)
		for {
			select {
			case ev := <-evWait:
				if ev.Topic == "checkpoint.updated" {
					cancelWait()
					return
				}
			case <-timeout:
				cancelWait()
				t.Fatal("timed out")
			}
		}
	}()
	if err := k.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	workspaceRegistry, err := workspacestore.NewSQLiteStore(mainDBPath)
	if err != nil {
		t.Fatalf("open main workspace db failed: %v", err)
	}
	defer workspaceRegistry.Close()
	workspaceRef, ok := workspaceRegistry.GetByRoot(workspaceRoot)
	if !ok {
		t.Fatal("expected workspace metadata in main sqlite")
	}
	if workspaceRef.Root != workspaceRoot {
		t.Fatalf("unexpected workspace metadata: %+v", workspaceRef)
	}

	workspaceDBPath := storagepath.WorkspaceDBPath(mainDBPath, workspaceRef.ID)

	sessionStore, err := session.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("open workspace session db failed: %v", err)
	}
	defer sessionStore.Close()
	sessionRecord, ok := sessionStore.Get(sessionID)
	if !ok {
		t.Fatal("expected session metadata in workspace sqlite")
	}
	if sessionRecord.ProjectRoot != workspaceRoot {
		t.Fatalf("unexpected session metadata: %+v", sessionRecord)
	}

	intentStore, err := intent.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("open workspace intent db failed: %v", err)
	}
	defer intentStore.Close()
	gotIntent, ok := intentStore.Latest(sessionID)
	if !ok || gotIntent.Goal != "persist me" {
		t.Fatalf("unexpected project intent: %+v ok=%v", gotIntent, ok)
	}

	messageStore, err := message.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("open workspace message db failed: %v", err)
	}
	defer messageStore.Close()
	gotMessages := messageStore.List(sessionID, 0)
	if len(gotMessages) != 2 {
		t.Fatalf("expected 2 project messages, got %d", len(gotMessages))
	}

	checkpointStore, err := checkpoint.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("open workspace checkpoint db failed: %v", err)
	}
	defer checkpointStore.Close()
	gotCheckpoint, ok := checkpointStore.Get(sessionID, wfID2)
	if !ok || gotCheckpoint.WorkflowID != wfID2 {
		t.Fatalf("unexpected project checkpoint: %+v ok=%v", gotCheckpoint, ok)
	}

	turnStore, err := turn.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("open workspace turn db failed: %v", err)
	}
	defer turnStore.Close()
	gotTurn, ok := turnStore.Get(wfID2)
	if !ok || gotTurn.WorkflowID != wfID2 || gotTurn.Status != "pass" {
		t.Fatalf("unexpected turn: %+v ok=%v", gotTurn, ok)
	}

	objectStore, err := objectstore.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("open workspace object db failed: %v", err)
	}
	defer objectStore.Close()
	gotObjects := objectStore.ListByTurn(wfID2)
	// M8-09: SaveSnapshot now persists one snapshot object in addition to the
	// modify object, so we expect at least 1 modify and at most 1 snapshot.
	modifyCount := 0
	snapshotCount := 0
	for _, o := range gotObjects {
		switch o.Kind {
		case "modify":
			modifyCount++
		case "snapshot":
			snapshotCount++
		}
	}
	if modifyCount != 1 {
		t.Fatalf("expected 1 modify object, got %d (objects: %+v)", modifyCount, gotObjects)
	}
	if snapshotCount > 1 {
		t.Fatalf("expected at most 1 snapshot object, got %d (objects: %+v)", snapshotCount, gotObjects)
	}
}

func TestProjectMetadataForSessionUsesWorkspaceRootInMVP(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	k := New(workspaceRoot)

	projectID, projectRoot := k.projectMetadataForSession("session-123")
	if projectID != "session-123" {
		t.Fatalf("unexpected project id: %q", projectID)
	}
	if projectRoot != workspaceRoot {
		t.Fatalf("unexpected project root: %q", projectRoot)
	}
}
