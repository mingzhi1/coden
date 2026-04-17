package llm

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// TestMutationResultLine_WriteFile verifies the write_file feedback format.
func TestMutationResultLine_WriteFile(t *testing.T) {
	t.Parallel()

	call := workflow.ToolCall{
		ToolCallID: "tc-1",
		Request:    toolruntime.Request{Kind: "write_file", Path: "artifacts/out.md"},
	}
	result := toolruntime.Result{
		After:   "hello world\n",
		Summary: "wrote artifact to artifacts/out.md",
	}
	got := mutationResultLine(call, result)
	for _, want := range []string{"write_file", "artifacts/out.md", "written", "12 bytes"} {
		if !containsStr(got, want) {
			t.Errorf("mutationResultLine WriteFile: want %q in %q", want, got)
		}
	}
}

// TestMutationResultLine_EditFile verifies the edit_file feedback format.
func TestMutationResultLine_EditFile(t *testing.T) {
	t.Parallel()

	call := workflow.ToolCall{
		ToolCallID: "tc-2",
		Request:    toolruntime.Request{Kind: "edit_file", Path: "internal/foo.go"},
	}
	result := toolruntime.Result{
		Summary: "edited internal/foo.go (replaced 10 chars)",
	}
	got := mutationResultLine(call, result)
	for _, want := range []string{"edit_file", "internal/foo.go", "replaced"} {
		if !containsStr(got, want) {
			t.Errorf("mutationResultLine EditFile: want %q in %q", want, got)
		}
	}
}

// TestMutationResultLine_RunShell verifies the run_shell feedback format, including
// exit code and truncated stdout/stderr.
func TestMutationResultLine_RunShell(t *testing.T) {
	t.Parallel()

	call := workflow.ToolCall{
		ToolCallID: "tc-3",
		Request:    toolruntime.Request{Kind: "run_shell", Command: "go build ./..."},
	}
	result := toolruntime.Result{
		Output:   "build output here",
		Stderr:   "some warning",
		ExitCode: 0,
		Summary:  "executed shell command",
	}
	got := mutationResultLine(call, result)
	for _, want := range []string{"run_shell", "exit 0", "build output here", "stderr", "some warning"} {
		if !containsStr(got, want) {
			t.Errorf("mutationResultLine RunShell: want %q in %q", want, got)
		}
	}
}

// TestMutationResultLine_RunShellNonZeroExit verifies non-zero exit code is reported.
func TestMutationResultLine_RunShellNonZeroExit(t *testing.T) {
	t.Parallel()

	call := workflow.ToolCall{
		ToolCallID: "tc-4",
		Request:    toolruntime.Request{Kind: "run_shell", Command: "go test ./..."},
	}
	result := toolruntime.Result{
		Output:   "FAIL",
		ExitCode: 1,
	}
	got := mutationResultLine(call, result)
	if !containsStr(got, "exit 1") {
		t.Errorf("expected 'exit 1' in run_shell result, got %q", got)
	}
}

// TestSplitToolCalls verifies read vs mutation classification.
func TestSplitToolCallsClassification(t *testing.T) {
	t.Parallel()

	calls := []workflow.ToolCall{
		{Request: toolruntime.Request{Kind: "read_file", Path: "foo.go"}},
		{Request: toolruntime.Request{Kind: "search", Query: "Bar"}},
		{Request: toolruntime.Request{Kind: "list_dir", Dir: "internal"}},
		{Request: toolruntime.Request{Kind: "write_file", Path: "out.go"}},
		{Request: toolruntime.Request{Kind: "edit_file", Path: "out.go"}},
		{Request: toolruntime.Request{Kind: "run_shell", Command: "go build"}},
	}

	reads, mutations := splitToolCalls(calls)
	if len(reads) != 3 {
		t.Errorf("expected 3 reads, got %d", len(reads))
	}
	if len(mutations) != 3 {
		t.Errorf("expected 3 mutations, got %d", len(mutations))
	}
	for _, r := range reads {
		switch r.Request.Kind {
		case "read_file", "search", "list_dir":
		default:
			t.Errorf("unexpected kind in reads: %s", r.Request.Kind)
		}
	}
	for _, m := range mutations {
		switch m.Request.Kind {
		case "write_file", "edit_file", "run_shell":
		default:
			t.Errorf("unexpected kind in mutations: %s", m.Request.Kind)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
