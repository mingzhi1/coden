package toolruntime

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/mingzhi1/coden/internal/core/workspace"
)

func TestRunShellCapturesStdoutStderrAndExitCode(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available in PATH")
	}

	exec := NewLocalExecutor(workspace.New(t.TempDir()))
	command := "printf 'out'; printf 'err' 1>&2; exit 7"
	if runtime.GOOS == "windows" {
		command = "echo out<nul & echo err 1>&2 & exit /b 7"
	}

	result, err := exec.Execute(context.Background(), Request{
		Kind:    "run_shell",
		Command: command,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %+v", result)
	}
	if !strings.Contains(result.Output, "out") {
		t.Fatalf("expected stdout, got %+v", result)
	}
	if !strings.Contains(result.Stderr, "err") {
		t.Fatalf("expected stderr, got %+v", result)
	}
	if !strings.Contains(result.Summary, "code 7") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
}

func TestRunShellTimeout(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available in PATH")
	}

	exec := NewLocalExecutor(workspace.New(t.TempDir()))
	// Use sleep on Unix; on Windows use ping with enough iterations.
	sleepCmd := "sleep 10"
	if runtime.GOOS == "windows" {
		// ping -n 11 ~= 10s on Windows.
		sleepCmd = "ping -n 11 127.0.0.1"
	}

	result, err := exec.Execute(context.Background(), Request{
		Kind:       "run_shell",
		Command:    sleepCmd,
		TimeoutSec: 1,
	})
	if err != nil {
		t.Fatalf("Execute returned error (should swallow timeout as exitCode=-1): %v", err)
	}
	// On Windows the child process may not be killed immediately after context
	// cancel, but the exitCode and summary must still reflect the timeout.
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1 on timeout, got %d (summary: %s)", result.ExitCode, result.Summary)
	}
	if !strings.Contains(result.Summary, "timed out") {
		t.Fatalf("expected 'timed out' in summary, got %q", result.Summary)
	}
}

func TestTruncateOutput(t *testing.T) {
	t.Parallel()

	// Exactly at limit — no truncation.
	exact := strings.Repeat("a", maxShellOutputBytes)
	got := truncateOutput([]byte(exact), maxShellOutputBytes)
	if strings.Contains(got, "truncated") {
		t.Fatalf("should not truncate at limit, got: %q", got[:50])
	}

	// Over limit — should truncate.
	over := strings.Repeat("b", maxShellOutputBytes+100)
	got = truncateOutput([]byte(over), maxShellOutputBytes)
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation notice, got prefix %q", got[:50])
	}
	if len(got) > maxShellOutputBytes+200 {
		t.Fatalf("truncated output too long: %d", len(got))
	}
}

func TestWriteFileCapturesBeforeAfterAndDiff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ws := workspace.New(root)
	if _, err := ws.Write("artifacts/test.txt", []byte("line1\nold\n")); err != nil {
		t.Fatalf("seed Write failed: %v", err)
	}

	exec := NewLocalExecutor(ws)
	result, err := exec.Execute(context.Background(), Request{
		Kind:    "write_file",
		Path:    "artifacts/test.txt",
		Content: "line1\nnew\n",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Before != "line1\nold\n" {
		t.Fatalf("unexpected before: %q", result.Before)
	}
	if result.After != "line1\nnew\n" {
		t.Fatalf("unexpected after: %q", result.After)
	}
	for _, want := range []string{"--- artifacts/test.txt", "+++ artifacts/test.txt", "-old", "+new"} {
		if !strings.Contains(result.Diff, want) {
			t.Fatalf("expected diff to contain %q, got %q", want, result.Diff)
		}
	}
}

func TestEditFileUniqueMatch(t *testing.T) {
	t.Parallel()

	ws := workspace.New(t.TempDir())
	content := "func foo() {}\nfunc bar() {}\nfunc foo() {} // duplicate\n"
	if _, err := ws.Write("artifacts/dup.go", []byte(content)); err != nil {
		t.Fatalf("seed Write failed: %v", err)
	}

	exec := NewLocalExecutor(ws)

	// Multiple matches should error.
	_, err := exec.Execute(context.Background(), Request{
		Kind:       "edit_file",
		Path:       "artifacts/dup.go",
		OldContent: "func foo() {}",
		NewContent: "func replaced() {}",
	})
	if err == nil {
		t.Fatal("expected error for multiple matches, got nil")
	}
	if !strings.Contains(err.Error(), "2 matches found") {
		t.Fatalf("expected '2 matches found' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "First occurrence context") {
		t.Fatalf("expected context snippet in error, got: %v", err)
	}
}

func TestEditFileNotFound(t *testing.T) {
	t.Parallel()

	ws := workspace.New(t.TempDir())
	if _, err := ws.Write("artifacts/a.go", []byte("hello world\n")); err != nil {
		t.Fatalf("seed Write failed: %v", err)
	}

	exec := NewLocalExecutor(ws)
	_, err := exec.Execute(context.Background(), Request{
		Kind:       "edit_file",
		Path:       "artifacts/a.go",
		OldContent: "not present",
		NewContent: "replacement",
	})
	if err == nil {
		t.Fatal("expected error for missing old_content, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' in error, got: %v", err)
	}
}

// M12-01f: Integration test for the full spill lifecycle.
//
// Scenario: read a large file (>8000 chars) → verify spill happens
// → verify Preview is populated → verify re-read still works
// → verify CleanupSpillDir removes the spill directory.
func TestReadFileSpillIntegration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ws := workspace.New(root)

	// Create a file exceeding MaxResultChars (8000).
	bigContent := strings.Repeat("// this is line content for a Go file\n", 300) // ~11400 chars
	if _, err := ws.Write("artifacts/big.go", []byte(bigContent)); err != nil {
		t.Fatalf("seed Write failed: %v", err)
	}

	exec := NewLocalExecutor(ws)

	// Step 1: Read the large file — should trigger spill.
	result, err := exec.Execute(context.Background(), Request{
		Kind: "read_file",
		Path: "artifacts/big.go",
	})
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}

	// Verify spill happened.
	if result.SpilledPath == "" {
		t.Fatal("expected SpilledPath to be set for large file")
	}
	if result.Preview == "" {
		t.Fatal("expected Preview to be set when spilled")
	}

	// Verify Preview contains first N lines.
	previewLines := strings.Split(strings.TrimRight(result.Preview, "\n."), "\n")
	if len(previewLines) > spillPreviewLines+1 {
		t.Errorf("Preview has %d lines, want ≤ %d", len(previewLines), spillPreviewLines)
	}

	// Verify full content is still in Output (not truncated by spill).
	if result.Output != bigContent {
		t.Errorf("Output should contain full content, got %d bytes, want %d", len(result.Output), len(bigContent))
	}

	// Step 2: Re-read the same file — should still work and produce same spill path.
	result2, err := exec.Execute(context.Background(), Request{
		Kind: "read_file",
		Path: "artifacts/big.go",
	})
	if err != nil {
		t.Fatalf("second read_file failed: %v", err)
	}
	if result2.SpilledPath != result.SpilledPath {
		t.Errorf("content-addressed spill should produce same path: %s vs %s",
			result.SpilledPath, result2.SpilledPath)
	}

	// Step 3: Cleanup spill directory — should succeed.
	if err := CleanupSpillDir(root); err != nil {
		t.Fatalf("CleanupSpillDir failed: %v", err)
	}

	// Step 4: Verify spill file is gone.
	if _, statErr := os.Stat(result.SpilledPath); !os.IsNotExist(statErr) {
		t.Errorf("spill file should be removed after cleanup, stat err: %v", statErr)
	}
}

// TestReadFileNoSpillUnderThreshold verifies small files don't trigger spill.
func TestReadFileNoSpillUnderThreshold(t *testing.T) {
	t.Parallel()

	ws := workspace.New(t.TempDir())
	smallContent := "package main\n\nfunc main() {}\n"
	if _, err := ws.Write("artifacts/small.go", []byte(smallContent)); err != nil {
		t.Fatalf("seed Write failed: %v", err)
	}

	exec := NewLocalExecutor(ws)
	result, err := exec.Execute(context.Background(), Request{
		Kind: "read_file",
		Path: "artifacts/small.go",
	})
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}
	if result.SpilledPath != "" {
		t.Errorf("small file should not trigger spill, got SpilledPath=%s", result.SpilledPath)
	}
	if result.Preview != "" {
		t.Errorf("small file should not have Preview, got %q", result.Preview)
	}
	if result.Output != smallContent {
		t.Errorf("Output mismatch: got %q, want %q", result.Output, smallContent)
	}
}

