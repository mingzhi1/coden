package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// RPCExecutor implements toolruntime.Executor over a JSON-RPC connection to a
// tool process. The kernel substitutes this for a LocalExecutor once that tool
// is split into its own process.
type RPCExecutor struct {
	client   *Client
	toolName string
}

// NewRPCExecutor creates an RPCExecutor that routes Execute calls through c.
// toolName identifies the tool process (e.g. "shell", "git", "lsp").
func NewRPCExecutor(c *Client, toolName string) *RPCExecutor {
	return &RPCExecutor{client: c, toolName: toolName}
}

// Execute implements toolruntime.Executor.
func (e *RPCExecutor) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	args, err := json.Marshal(req)
	if err != nil {
		return toolruntime.Result{}, fmt.Errorf("rpc executor: marshal request: %w", err)
	}

	params := protocol.ToolExecParams{
		ToolName: e.toolName,
		Args:     json.RawMessage(args),
	}

	result, err := e.client.ExecuteTool(ctx, params)
	if err != nil {
		return toolruntime.Result{}, fmt.Errorf("rpc executor: %w", err)
	}

	return protoResultToToolResult(result), nil
}

func protoResultToToolResult(r protocol.ToolExecResult) toolruntime.Result {
	return toolruntime.Result{
		ArtifactPath: r.ArtifactPath,
		Summary:      r.Summary,
		Output:       r.Stdout,
		Stderr:       r.Stderr,
		ExitCode:     r.ExitCode,
	}
}
