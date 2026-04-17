package writefile

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func callToolExecRaw(t *testing.T, rwc io.ReadWriteCloser, params protocol.ToolExecParams) error {
	t.Helper()

	codec := transport.NewCodec(rwc)
	defer codec.Close()

	req, err := protocol.NewRequest(1, protocol.MethodToolExec, params)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if err := codec.WriteMessage(req); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	raw, err := codec.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Error == nil {
		return nil
	}
	return resp.Error
}

func TestRPCExecutorDescribeAndExecute(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(root)))
	go srv.ServeConn(ctx, serverRWC)

	exec := NewRPCExecutor(clientRWC)
	defer exec.Close()

	describe, err := exec.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}
	if describe.Name != "coden-tool-writefile" {
		t.Fatalf("unexpected tool name: %q", describe.Name)
	}
	if len(describe.Commands) != 7 {
		t.Fatalf("expected 7 commands, got %+v", describe.Commands)
	}

	result, err := exec.Execute(ctx, toolruntime.Request{
		Kind:    "write_file",
		Path:    "artifacts/test.md",
		Content: "hello from rpc tool",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.ArtifactPath == "" {
		t.Fatal("expected artifact path")
	}
	body, err := os.ReadFile(filepath.Join(root, "artifacts", "test.md"))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(body) != "hello from rpc tool" {
		t.Fatalf("unexpected file content: %q", string(body))
	}
}

func TestRPCExecutorReadFileReturnsOutput(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", "test.md"), []byte("hello from disk"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(root)))
	go srv.ServeConn(ctx, serverRWC)

	exec := NewRPCExecutor(clientRWC)
	defer exec.Close()

	result, err := exec.Execute(ctx, toolruntime.Request{
		Kind: "read_file",
		Path: "artifacts/test.md",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Output != "hello from disk" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if !strings.Contains(result.Summary, "encoding=utf-8") || !strings.Contains(result.Summary, "newlines=none") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
}

func TestRPCExecutorReadFileStripsBOMAndReportsCRLF(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("line1\r\nline2\r\n")...)
	if err := os.WriteFile(filepath.Join(root, "artifacts", "test.md"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(root)))
	go srv.ServeConn(ctx, serverRWC)

	exec := NewRPCExecutor(clientRWC)
	defer exec.Close()

	result, err := exec.Execute(ctx, toolruntime.Request{
		Kind: "read_file",
		Path: "artifacts/test.md",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.HasPrefix(result.Output, "\uFEFF") {
		t.Fatalf("expected BOM to be stripped from output, got %q", result.Output)
	}
	if result.Output != "line1\r\nline2\r\n" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	for _, want := range []string{"encoding=utf-8-bom", "newlines=crlf"} {
		if !strings.Contains(result.Summary, want) {
			t.Fatalf("expected summary to contain %q, got %q", want, result.Summary)
		}
	}
}

func TestRPCExecutorListDirReturnsOutput(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", "b.md"), []byte("b"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(root)))
	go srv.ServeConn(ctx, serverRWC)

	exec := NewRPCExecutor(clientRWC)
	defer exec.Close()

	result, err := exec.Execute(ctx, toolruntime.Request{
		Kind: "list_dir",
		Dir:  "artifacts",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	for _, want := range []string{"artifacts/a.md", "artifacts/b.md"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected listing to contain %q, got %q", want, result.Output)
		}
	}
}

func TestRPCExecutorSearchReturnsMatchingPaths(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", "a.md"), []byte("hello needle"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", "b.md"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(root)))
	go srv.ServeConn(ctx, serverRWC)

	exec := NewRPCExecutor(clientRWC)
	defer exec.Close()

	result, err := exec.Execute(ctx, toolruntime.Request{
		Kind:  "search",
		Dir:   "artifacts",
		Query: "needle",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result.Output, "artifacts/a.md") {
		t.Fatalf("expected search results to include matching file, got %q", result.Output)
	}
	if strings.Contains(result.Output, "artifacts/b.md") {
		t.Fatalf("expected search results to exclude non-matching file, got %q", result.Output)
	}
}

func TestScopedServerDescribeAndRejectUnsupportedTool(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	serverRWC, clientRWC := transport.Pipe()
	srv := NewScopedServer(
		"coden-tool-readfile",
		"filesystem-read",
		"short",
		[]string{"read_file", "list_dir", "search"},
		toolruntime.NewLocalExecutor(workspace.New(root)),
	)
	go srv.ServeConn(ctx, serverRWC)

	exec := NewRPCExecutor(clientRWC)
	defer exec.Close()

	describe, err := exec.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}
	if describe.Name != "coden-tool-readfile" {
		t.Fatalf("unexpected tool name: %q", describe.Name)
	}
	if len(describe.Commands) != 3 {
		t.Fatalf("expected 3 commands, got %+v", describe.Commands)
	}

	_, err = exec.Execute(ctx, toolruntime.Request{
		Kind:    "write_file",
		Path:    "artifacts/test.md",
		Content: "hello",
	})
	if err == nil {
		t.Fatal("expected unsupported tool error")
	}
	if !strings.Contains(err.Error(), "unsupported tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScopedServerRejectsMismatchedToolNameAndKind(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	serverRWC, clientRWC := transport.Pipe()
	srv := NewScopedServer(
		"coden-tool-readfile",
		"filesystem-read",
		"short",
		[]string{"read_file", "list_dir", "search"},
		toolruntime.NewLocalExecutor(workspace.New(root)),
	)
	go srv.ServeConn(ctx, serverRWC)

	args, err := protocol.MarshalRaw(toolruntime.Request{
		Kind:  "search",
		Dir:   "artifacts",
		Query: "hello",
	})
	if err != nil {
		t.Fatalf("MarshalRaw failed: %v", err)
	}

	err = callToolExecRaw(t, clientRWC, protocol.ToolExecParams{
		ToolCallID: "tool-1",
		ToolName:   "read_file",
		Args:       args,
	})
	if err == nil {
		t.Fatal("expected tool kind mismatch error")
	}
	if !strings.Contains(err.Error(), "tool kind mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRPCExecutorRunShellReturnsStructuredOutput(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available in PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(root)))
	go srv.ServeConn(ctx, serverRWC)

	exec := NewRPCExecutor(clientRWC)
	defer exec.Close()

	command := "printf 'out'; printf 'err' 1>&2; exit 7"
	if runtime.GOOS == "windows" {
		command = "echo out<nul & echo err 1>&2 & exit /b 7"
	}

	result, err := exec.Execute(ctx, toolruntime.Request{
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
		t.Fatalf("expected stdout in output, got %+v", result)
	}
	if !strings.Contains(result.Stderr, "err") {
		t.Fatalf("expected stderr, got %+v", result)
	}
}

func TestRPCExecutorCancelReportsNotSupported(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(root)))
	go srv.ServeConn(ctx, serverRWC)

	codec := transport.NewCodec(clientRWC)
	defer codec.Close()

	req, err := protocol.NewRequest(1, protocol.MethodToolCancel, protocol.CancelParams{
		SessionID:  "session-1",
		WorkflowID: "wf-1",
		CallID:     "tool-1",
	})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if err := codec.WriteMessage(req); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	raw, err := codec.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}

	var ack protocol.AckResult
	if err := json.Unmarshal(resp.Result, &ack); err != nil {
		t.Fatalf("unmarshal ack failed: %v", err)
	}
	if ack.Status != "not_supported" {
		t.Fatalf("unexpected ack: %+v", ack)
	}
}
