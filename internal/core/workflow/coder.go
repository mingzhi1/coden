package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// Coder is the code-generation boundary used by the workflow engine.
// It returns a tool request; the kernel still owns tool execution.
type Coder interface {
	Build(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (CodePlan, error)
}

// CodePlan is the coder output consumed by the kernel.
type CodePlan struct {
	ToolCalls  []ToolCall
	ToolCallID string
	Request    toolruntime.Request

	// M11-03: AppendTasks holds new tasks the Coder requests to be appended
	// to the TaskQueue after executing the current task. The kernel enforces
	// a per-source cap (maxAppendPerCoder) to prevent runaway LLM loops.
	AppendTasks []model.Task `json:"append_tasks,omitempty"`
}

type ToolCall struct {
	ToolCallID string
	Request    toolruntime.Request

	// Executed is true when the agentic coder already ran this mutation
	// in its loop and fed the result back to the LLM. The kernel should
	// skip re-execution but still perform bookkeeping (events, auditing,
	// workspace change recording).
	Executed   bool
	ExecResult toolruntime.Result // valid only when Executed is true
}

func (p CodePlan) Calls() []ToolCall {
	if len(p.ToolCalls) > 0 {
		return append([]ToolCall(nil), p.ToolCalls...)
	}
	if p.ToolCallID == "" && p.Request.Kind == "" {
		return nil
	}
	return []ToolCall{{
		ToolCallID: p.ToolCallID,
		Request:    p.Request,
	}}
}

// LocalCoder provides the built-in fallback code worker.
type LocalCoder struct{}

func NewLocalCoder() *LocalCoder {
	return &LocalCoder{}
}

func (c *LocalCoder) Build(_ context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (CodePlan, error) {
	var taskLines strings.Builder
	if len(tasks) == 0 {
		taskLines.WriteString("- no tasks proposed\n")
	} else {
		for _, task := range tasks {
			title := strings.TrimSpace(task.Title)
			if title == "" {
				title = strings.TrimSpace(task.ID)
			}
			if title == "" {
				title = "unnamed task"
			}
			taskLines.WriteString("- ")
			taskLines.WriteString(title)
			taskLines.WriteString("\n")
		}
	}

	content := fmt.Sprintf(
		"# CodeN Artifact\n\nGoal: %s\n\nTasks:\n%s",
		intent.Goal,
		taskLines.String(),
	)

	return CodePlan{
		ToolCalls: []ToolCall{{
			ToolCallID: fmt.Sprintf("tool-%s-write-file", workflowID),
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    filepathForIntent(intent),
				Content: content,
			},
		}},
		ToolCallID: fmt.Sprintf("tool-%s-write-file", workflowID),
		Request: toolruntime.Request{
			Kind:    "write_file",
			Path:    filepathForIntent(intent),
			Content: content,
		},
	}, nil
}

func (c *LocalCoder) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "local-coder", Role: RoleCoder}
}
