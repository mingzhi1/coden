package protocol

import "encoding/json"

// ToolDescribeResult advertises tool capabilities.
type ToolDescribeResult struct {
	Name           string   `json:"name"`
	Capability     string   `json:"capability"`
	Version        string   `json:"version,omitempty"`
	SupportsCancel bool     `json:"supports_cancel,omitempty"`
	TimeoutClass   string   `json:"timeout_class,omitempty"`
	Commands       []string `json:"commands,omitempty"`
}

// ToolExecParams carries one tool execution request from kernel to tool process.
type ToolExecParams struct {
	SessionID  string          `json:"session_id"`
	WorkflowID string          `json:"workflow_id"`
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Args       json.RawMessage `json:"args,omitempty"`
}

type ToolExecutionMeta struct {
	Tool   string `json:"tool,omitempty"`
	Status string `json:"status,omitempty"`
}

// ToolExecResult is the tool execution result returned to the kernel.
type ToolExecResult struct {
	ArtifactPath string             `json:"artifact_path,omitempty"`
	Summary      string             `json:"summary,omitempty"`
	Status       string             `json:"status"`
	Stdout       string             `json:"stdout,omitempty"`
	Stderr       string             `json:"stderr,omitempty"`
	ExitCode     int                `json:"exit_code,omitempty"`
	FilesTouched []string           `json:"files_touched,omitempty"`
	Metadata     *ToolExecutionMeta `json:"metadata,omitempty"`
}
