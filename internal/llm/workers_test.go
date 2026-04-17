package llm

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

func TestParseCodePlanReplySupportsStructuredFilesObject(t *testing.T) {
	reply := `{"files":[{"path":"src/main.go","content":"package main"},{"path":"README.md","content":"# demo"}]}`

	plan := parseCodePlanReply("wf-1", "intent-1", "demo goal", reply)
	calls := plan.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Request.Path != "src/main.go" || calls[1].Request.Path != "README.md" {
		t.Fatalf("unexpected tool call paths: %+v", calls)
	}
	if calls[0].Request.Kind != "write_file" || calls[1].Request.Kind != "write_file" {
		t.Fatalf("unexpected tool kinds: %+v", calls)
	}
}

func TestParseCodePlanReplySupportsStructuredToolCallsObject(t *testing.T) {
	reply := `{"tool_calls":[{"kind":"read_file","path":"README.md"},{"kind":"search","dir":"internal","query":"WorkflowID"},{"kind":"write_file","path":"artifacts/out.md","content":"# done"}]}`

	plan := parseCodePlanReply("wf-1", "intent-1", "demo goal", reply)
	calls := plan.Calls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(calls))
	}
	if calls[0].Request.Kind != "read_file" || calls[1].Request.Kind != "search" || calls[2].Request.Kind != "write_file" {
		t.Fatalf("unexpected tool call kinds: %+v", calls)
	}
	if calls[1].Request.Query != "WorkflowID" || calls[1].Request.Dir != "internal" {
		t.Fatalf("unexpected search request: %+v", calls[1].Request)
	}
}

func TestParseCodePlanReplySupportsStructuredFilesArray(t *testing.T) {
	reply := `[{"path":"a.txt","content":"A"},{"path":"b.txt","content":"B"}]`

	plan := parseCodePlanReply("wf-1", "intent-1", "demo goal", reply)
	calls := plan.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].ToolCallID != "tool-wf-1-write-file-1" || calls[1].ToolCallID != "tool-wf-1-write-file-2" {
		t.Fatalf("unexpected tool call ids: %+v", calls)
	}
}

func TestParseCodePlanReplyFallsBackToSingleArtifact(t *testing.T) {
	reply := "# Artifact\n\nHello"

	plan := parseCodePlanReply("wf-1", "intent-1", "demo goal", reply)
	calls := plan.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Request.Path != "artifacts/intent-1.md" {
		t.Fatalf("unexpected fallback path: %+v", calls[0].Request)
	}
	if calls[0].Request.Content != reply {
		t.Fatalf("unexpected fallback content: %q", calls[0].Request.Content)
	}
}

func TestRefineCodePlanWithContextPrependsReadForKnownSourceFile(t *testing.T) {
	ctx := model.WithWorkflowContext(context.Background(), model.WorkflowContext{
		FileTree: []string{"internal/app/main.go", "README.md"},
	})
	plan := workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{{
			ToolCallID: "tool-wf-1-write-1",
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    "internal/app/main.go",
				Content: "package app",
			},
		}},
	}

	refined := refineCodePlanWithContext(ctx, "wf-1", plan)
	calls := refined.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Request.Kind != "read_file" || calls[0].Request.Path != "internal/app/main.go" {
		t.Fatalf("unexpected discovery call: %+v", calls[0].Request)
	}
	if calls[1].Request.Kind != "write_file" {
		t.Fatalf("unexpected write call: %+v", calls[1].Request)
	}
}

func TestRefineCodePlanWithContextPrependsListDirForUnknownSourceFile(t *testing.T) {
	ctx := model.WithWorkflowContext(context.Background(), model.WorkflowContext{
		FileTree: []string{"README.md"},
	})
	plan := workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{{
			ToolCallID: "tool-wf-2-write-1",
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    "cmd/coden/main.go",
				Content: "package main",
			},
		}},
	}

	refined := refineCodePlanWithContext(ctx, "wf-2", plan)
	calls := refined.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Request.Kind != "list_dir" {
		t.Fatalf("unexpected discovery call: %+v", calls[0].Request)
	}
}

func TestRefineCodePlanWithContextSkipsArtifactsAndExistingDiscovery(t *testing.T) {
	ctx := model.WithWorkflowContext(context.Background(), model.WorkflowContext{
		FileTree: []string{"README.md"},
	})

	artifactPlan := workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{{
			ToolCallID: "tool-wf-3-write-1",
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    "artifacts/out.md",
				Content: "# done",
			},
		}},
	}
	if got := len(refineCodePlanWithContext(ctx, "wf-3", artifactPlan).Calls()); got != 1 {
		t.Fatalf("expected artifact plan to stay unchanged, got %d calls", got)
	}

	discoveryPlan := workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{
			{
				ToolCallID: "tool-wf-4-search-1",
				Request: toolruntime.Request{
					Kind:  "search",
					Dir:   "internal",
					Query: "Kernel",
				},
			},
			{
				ToolCallID: "tool-wf-4-write-1",
				Request: toolruntime.Request{
					Kind:    "write_file",
					Path:    "internal/core/kernel/kernel.go",
					Content: "package kernel",
				},
			},
		},
	}
	calls := refineCodePlanWithContext(ctx, "wf-4", discoveryPlan).Calls()
	if len(calls) != 2 || calls[0].Request.Kind != "search" {
		t.Fatalf("expected existing discovery plan to stay unchanged, got %+v", calls)
	}
}

func TestRefineCodePlanWithContextPrependsDiscoveryWhenSearchComesAfterWrite(t *testing.T) {
	ctx := model.WithWorkflowContext(context.Background(), model.WorkflowContext{
		FileTree: []string{"README.md"},
	})
	plan := workflow.CodePlan{
		ToolCalls: []workflow.ToolCall{
			{
				ToolCallID: "tool-wf-5-write-1",
				Request: toolruntime.Request{
					Kind:    "write_file",
					Path:    "internal/core/kernel/kernel.go",
					Content: "package kernel",
				},
			},
			{
				ToolCallID: "tool-wf-5-search-1",
				Request: toolruntime.Request{
					Kind:  "search",
					Dir:   "internal/core/kernel",
					Query: "Kernel",
				},
			},
		},
	}

	calls := refineCodePlanWithContext(ctx, "wf-5", plan).Calls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if calls[0].Request.Kind != "list_dir" {
		t.Fatalf("expected prepended discovery call, got %+v", calls[0].Request)
	}
}
