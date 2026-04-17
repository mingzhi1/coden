package protocol

import (
	"encoding/json"
)

// CancelParams is used by worker.cancel and tool.cancel.
type CancelParams struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id"`
	TaskID     string `json:"task_id,omitempty"`
	CallID     string `json:"call_id,omitempty"`
}

// WorkerDescribeResult advertises worker capabilities.
type WorkerDescribeResult struct {
	Name           string `json:"name"`
	Role           string `json:"role"`
	Version        string `json:"version,omitempty"`
	SupportsCancel bool   `json:"supports_cancel,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

// WorkerExecuteParams carries one unit of work from kernel to worker.
type WorkerExecuteParams struct {
	SessionID  string          `json:"session_id"`
	WorkflowID string          `json:"workflow_id"`
	TaskID     string          `json:"task_id"`
	Role       string          `json:"role"`
	Input      json.RawMessage `json:"input,omitempty"`
	Context    json.RawMessage `json:"context,omitempty"`
}

// WorkerMessage is a structured message emitted by a worker.
type WorkerMessage struct {
	Kind    string `json:"kind"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content"`
}

// TaskProposal is a worker-proposed task update.
type TaskProposal struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
}

// ToolCall is a worker-proposed tool invocation.
type ToolCall struct {
	ID       string          `json:"id"`
	ToolName string          `json:"tool_name"`
	Args     json.RawMessage `json:"args,omitempty"`
}

// ArtifactRef points at a worker-produced artifact.
type ArtifactRef struct {
	Path    string `json:"path"`
	Kind    string `json:"kind,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// IntentProposal is a worker-proposed normalized intent.
type IntentProposal struct {
	ID              string   `json:"id,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	Goal            string   `json:"goal"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
}

// CheckpointProposal is a worker-proposed checkpoint outcome.
type CheckpointProposal struct {
	Status        string   `json:"status"`
	ArtifactPaths []string `json:"artifact_paths,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
}

type WorkerExecutionMeta struct {
	Worker string `json:"worker,omitempty"`
	Role   string `json:"role,omitempty"`
}

// WorkerExecuteResult is the advisory result returned by a worker.
type WorkerExecuteResult struct {
	Status        string               `json:"status"`
	Messages      []WorkerMessage      `json:"messages,omitempty"`
	ProposedTasks []TaskProposal       `json:"proposed_tasks,omitempty"`
	ToolCalls     []ToolCall           `json:"tool_calls,omitempty"`
	Artifacts     []ArtifactRef        `json:"artifacts,omitempty"`
	Intent        *IntentProposal      `json:"intent,omitempty"`
	Checkpoint    *CheckpointProposal  `json:"checkpoint,omitempty"`
	Metadata      *WorkerExecutionMeta `json:"metadata,omitempty"`
}
