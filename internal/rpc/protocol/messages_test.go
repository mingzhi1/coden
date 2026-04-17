package protocol

import (
	"encoding/json"
	"testing"
)

func TestNewRequestWithWorkflowSubmitParams(t *testing.T) {
	req, err := NewRequest(7, MethodWorkflowSubmit, WorkflowSubmitParams{
		SessionID: "s-1",
		Prompt:    "ship it",
	})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	if req.Method != MethodWorkflowSubmit {
		t.Fatalf("expected %q, got %q", MethodWorkflowSubmit, req.Method)
	}

	var params WorkflowSubmitParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params failed: %v", err)
	}

	if params.SessionID != "s-1" || params.Prompt != "ship it" {
		t.Fatalf("unexpected params: %+v", params)
	}
}

func TestWorkerExecuteParamsRoundTrip(t *testing.T) {
	input := json.RawMessage(`{"goal":"plan next patch"}`)
	context := json.RawMessage(`{"changed_files":["a.go","b.go"]}`)
	want := WorkerExecuteParams{
		SessionID:  "session-1",
		WorkflowID: "wf-1",
		TaskID:     "task-plan",
		Role:       "planner",
		Input:      input,
		Context:    context,
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got WorkerExecuteParams
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if got.SessionID != want.SessionID || got.WorkflowID != want.WorkflowID || got.TaskID != want.TaskID || got.Role != want.Role {
		t.Fatalf("unexpected identity fields: %+v", got)
	}
	if string(got.Input) != string(want.Input) {
		t.Fatalf("unexpected input: %s", string(got.Input))
	}
	if string(got.Context) != string(want.Context) {
		t.Fatalf("unexpected context: %s", string(got.Context))
	}
}

func TestToolExecResultRoundTrip(t *testing.T) {
	want := ToolExecResult{
		ArtifactPath: "workspace/internal/foo.go",
		Summary:      "wrote artifact",
		Status:       "ok",
		Stdout:       "done",
		Stderr:       "",
		ExitCode:     0,
		FilesTouched: []string{"internal/foo.go", "README.md"},
		Metadata: &ToolExecutionMeta{
			Tool:   "shell",
			Status: "written",
		},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got ToolExecResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if got.Status != want.Status || got.Stdout != want.Stdout || got.ExitCode != want.ExitCode {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.ArtifactPath != want.ArtifactPath || got.Summary != want.Summary {
		t.Fatalf("unexpected artifact fields: %+v", got)
	}
	if len(got.FilesTouched) != 2 || got.FilesTouched[0] != "internal/foo.go" {
		t.Fatalf("unexpected files_touched: %+v", got.FilesTouched)
	}
	if got.Metadata == nil || got.Metadata.Tool != "shell" {
		t.Fatalf("unexpected metadata: %+v", got.Metadata)
	}
}

func TestWorkerExecuteResultCheckpointRoundTrip(t *testing.T) {
	want := WorkerExecuteResult{
		Status: "ok",
		Checkpoint: &CheckpointProposal{
			Status:        "pass",
			ArtifactPaths: []string{"artifacts/intent-1.md"},
			Evidence:      []string{"artifact exists"},
		},
		Metadata: &WorkerExecutionMeta{
			Role: "acceptor",
		},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got WorkerExecuteResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if got.Checkpoint == nil {
		t.Fatal("expected checkpoint payload")
	}
	if got.Checkpoint.Status != "pass" {
		t.Fatalf("unexpected checkpoint status: %+v", got.Checkpoint)
	}
	if len(got.Checkpoint.ArtifactPaths) != 1 || got.Checkpoint.ArtifactPaths[0] != "artifacts/intent-1.md" {
		t.Fatalf("unexpected checkpoint artifact paths: %+v", got.Checkpoint.ArtifactPaths)
	}
	if got.Metadata == nil || got.Metadata.Role != "acceptor" {
		t.Fatalf("unexpected metadata: %+v", got.Metadata)
	}
}

func TestWorkerExecuteResultIntentRoundTrip(t *testing.T) {
	want := WorkerExecuteResult{
		Status: "ok",
		Intent: &IntentProposal{
			ID:              "intent-1",
			SessionID:       "session-1",
			Goal:            "normalized goal",
			SuccessCriteria: []string{"artifact exists"},
		},
		Metadata: &WorkerExecutionMeta{
			Role: "input",
		},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got WorkerExecuteResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if got.Intent == nil {
		t.Fatal("expected intent payload")
	}
	if got.Intent.Goal != "normalized goal" || got.Intent.ID != "intent-1" {
		t.Fatalf("unexpected intent payload: %+v", got.Intent)
	}
	if got.Metadata == nil || got.Metadata.Role != "input" {
		t.Fatalf("unexpected metadata: %+v", got.Metadata)
	}
}
