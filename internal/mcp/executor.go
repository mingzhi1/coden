package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// Executor adapts the MCP Manager to the toolruntime.Executor interface.
// It handles tool calls with the "mcp__" prefix by routing them to the
// appropriate MCP server.
type Executor struct {
	manager *Manager
}

// NewExecutor creates a toolruntime.Executor backed by MCP.
func NewExecutor(manager *Manager) *Executor {
	return &Executor{manager: manager}
}

// Execute implements toolruntime.Executor.
// It expects req.Kind to be "mcp__<serverName>__<toolName>".
func (e *Executor) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	if !strings.HasPrefix(req.Kind, "mcp__") {
		return toolruntime.Result{}, fmt.Errorf("mcp executor: unexpected kind %q (expected mcp__ prefix)", req.Kind)
	}

	// Build args map from the Request fields
	args := make(map[string]any)

	// If Content is valid JSON, parse it as the arguments
	if req.Content != "" {
		if err := json.Unmarshal([]byte(req.Content), &args); err != nil {
			// Not JSON — put raw content as "input"
			args["input"] = req.Content
		}
	}

	// Also include explicit fields if set
	if req.Query != "" {
		args["query"] = req.Query
	}
	if req.Path != "" {
		args["path"] = req.Path
	}

	output, err := e.manager.CallTool(ctx, req.Kind, args)
	if err != nil {
		return toolruntime.Result{
			Output:     err.Error(),
			ErrorClass: "mcp_error",
			ErrorHuman: fmt.Sprintf("MCP tool %s failed: %s", req.Kind, err.Error()),
		}, err
	}

	return toolruntime.Result{
		Summary: fmt.Sprintf("MCP tool %s executed", req.Kind),
		Output:  output,
	}, nil
}

// IsMCPTool returns true if the tool kind uses the MCP prefix convention.
func IsMCPTool(kind string) bool {
	return strings.HasPrefix(kind, "mcp__")
}
