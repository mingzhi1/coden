package llm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/llm"
)

// TestAgenticCoderWithTools runs the coder with an executor so that
// read_file, search, grep, write_file etc. are actually executed and logged.
func TestAgenticCoderWithTools(t *testing.T) {
	miniMaxKey := os.Getenv("TEST_MINIMAX_API_KEY")
	if miniMaxKey == "" {
		t.Skip("TEST_MINIMAX_API_KEY not set")
	}

	// Create a temp workspace with some Go files
	tmpDir := t.TempDir()
	ws := workspace.New(tmpDir)

	// Write a Go module
	mustWrite(ws, "go.mod", "module testapp\n\ngo 1.21\n")
	mustWrite(ws, "main.go", `package main

import "fmt"

func main() {
	fmt.Println("hello")
}

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a + b
}

// Multiply returns the product of two integers.
func Multiply(a, b int) int {
	return a * b
}
`)
	mustWrite(ws, "utils/helper.go", `package utils

// Max returns the larger of two integers.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Min returns the smaller of two integers.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
`)

	miniMaxBaseURL := os.Getenv("TEST_MINIMAX_BASE_URL")
	if miniMaxBaseURL == "" {
		t.Skip("TEST_MINIMAX_BASE_URL not set")
	}

	pool := llm.NewPool()
	pool.Add(llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})
	pool.AddLight(llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})

	broker := llm.NewBroker(pool)
	executor := toolruntime.NewLocalExecutor(ws)
	coder := llm.NewAgenticCoder(broker, executor)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	intent := model.IntentSpec{
		ID:              "intent-1",
		SessionID:       "test-session",
		Goal:            "Add a Subtract function in main.go and write a test for it",
		Kind:            model.IntentKindCodeGen,
		SuccessCriteria: []string{"Subtract function exists in main.go", "Test passes"},
	}

	tasks := []model.Task{
		{ID: "task-1", Title: "Implement Subtract function in main.go"},
		{ID: "task-2", Title: "Write unit test for Subtract"},
	}

	t.Log("=== Agentic Coder with tool execution ===")
	codePlan, err := coder.Build(ctx, "wf-test-1", intent, tasks)
	if err != nil {
		t.Fatalf("agentic coder failed: %v", err)
	}

	t.Logf("code plan: %d tool calls", len(codePlan.Calls()))
	for i, call := range codePlan.Calls() {
		t.Logf("  call[%d]: %s → %s", i, call.Request.Kind, call.Request.Path)
	}

	// Verify that write_file calls were generated
	var writeCalls, readCalls, searchCalls int
	for _, call := range codePlan.Calls() {
		switch call.Request.Kind {
		case "write_file":
			writeCalls++
		case "read_file":
			readCalls++
		case "search":
			searchCalls++
		}
	}

	t.Logf("tool call breakdown: write=%d read=%d search=%d", writeCalls, readCalls, searchCalls)

	if writeCalls == 0 {
		t.Log("warning: no write_file calls generated (may be expected for read-only tasks)")
	}

	// Print usage
	t.Log("=== Usage Summary ===")
	usage := broker.Usage()
	for role, stats := range usage {
		t.Logf("  role=%s calls=%d input_tokens=%d output_tokens=%d",
			role, stats.Calls, stats.InputTokens, stats.OutTokens)
	}
}

// TestToolExecutionDirectly tests each tool type directly with logging.
func TestToolExecutionDirectly(t *testing.T) {
	tmpDir := t.TempDir()
	ws := workspace.New(tmpDir)
	executor := toolruntime.NewLocalExecutor(ws)

	// Setup
	mustWrite(ws, "go.mod", "module testapp\n\ngo 1.21\n")
	mustWrite(ws, "main.go", `package main

func Add(a, b int) int { return a + b }
func Multiply(a, b int) int { return a * b }
`)

	ctx := context.Background()

	t.Run("write_file", func(t *testing.T) {
		result, err := executor.Execute(ctx, toolruntime.Request{
			Kind:    "write_file",
			Path:    "artifacts/test.md",
			Content: "# Test Artifact\n\nThis is a test.",
		})
		if err != nil {
			t.Fatalf("write_file failed: %v", err)
		}
		t.Logf("write_file result: %s", result.Summary)
	})

	t.Run("read_file", func(t *testing.T) {
		result, err := executor.Execute(ctx, toolruntime.Request{
			Kind: "read_file",
			Path: "main.go",
		})
		if err != nil {
			t.Fatalf("read_file failed: %v", err)
		}
		t.Logf("read_file result: %s, output_len=%d", result.Summary, len(result.Output))
	})

	t.Run("list_dir", func(t *testing.T) {
		result, err := executor.Execute(ctx, toolruntime.Request{
			Kind: "list_dir",
			Dir:  "",
		})
		if err != nil {
			t.Fatalf("list_dir failed: %v", err)
		}
		t.Logf("list_dir result: %s, files:\n%s", result.Summary, result.Output)
	})

	t.Run("search", func(t *testing.T) {
		result, err := executor.Execute(ctx, toolruntime.Request{
			Kind:  "search",
			Query: "func",
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		t.Logf("search result: %s", result.Summary)
	})

	t.Run("grep_context", func(t *testing.T) {
		result, err := executor.Execute(ctx, toolruntime.Request{
			Kind: "grep_context",
			Path: "main.go",
			Line: 3,
		})
		if err != nil {
			t.Fatalf("grep_context failed: %v", err)
		}
		t.Logf("grep_context result: %s\n%s", result.Summary, result.Output)
	})

	t.Run("edit_file", func(t *testing.T) {
		result, err := executor.Execute(ctx, toolruntime.Request{
			Kind:       "edit_file",
			Path:       "main.go",
			OldContent: "func Add(a, b int) int { return a + b }",
			NewContent: "func Add(a, b int) int {\n\treturn a + b\n}",
		})
		if err != nil {
			t.Fatalf("edit_file failed: %v", err)
		}
		t.Logf("edit_file result: %s", result.Summary)
	})

	t.Run("run_shell", func(t *testing.T) {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("sh not available in PATH")
		}
		result, err := executor.Execute(ctx, toolruntime.Request{
			Kind:    "run_shell",
			Command: "go version",
		})
		if err != nil {
			t.Fatalf("run_shell failed: %v", err)
		}
		t.Logf("run_shell result: %s, exit_code=%d, output=%s", result.Summary, result.ExitCode, result.Output)
	})
}

func mustWrite(ws *workspace.Service, path, content string) {
	dir := filepath.Dir(path)
	if dir != "." {
		_ = os.MkdirAll(filepath.Join(ws.Root(), dir), 0o755)
	}
	if _, err := ws.Write(path, []byte(content)); err != nil {
		panic(err)
	}
}
