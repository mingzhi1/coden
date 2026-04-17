package mux

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

type stubExecutor struct {
	name string
}

func (s stubExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	return toolruntime.Result{Summary: s.name + ":" + req.Kind}, nil
}

func TestExecutorRoutesByKind(t *testing.T) {
	exec := New(stubExecutor{name: "default"}, map[string]toolruntime.Executor{
		"read_file": stubExecutor{name: "read"},
		"run_shell": stubExecutor{name: "shell"},
	})

	result, err := exec.Execute(context.Background(), toolruntime.Request{Kind: "read_file"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Summary != "read:read_file" {
		t.Fatalf("unexpected read routing: %+v", result)
	}

	result, err = exec.Execute(context.Background(), toolruntime.Request{Kind: "write_file"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Summary != "default:write_file" {
		t.Fatalf("unexpected default routing: %+v", result)
	}
}
