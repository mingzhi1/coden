// Package mcp implements the MCP (Model Context Protocol) client layer.
// It connects to external MCP servers over stdio, lists their tools,
// and exposes them as CodeN toolruntime.Executor implementations.
package mcp

// ServerConfig represents a single MCP server from .mcp.json.
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// MCPConfig represents the .mcp.json configuration file format.
// Compatible with Claude Code / Zed / VS Code MCP configuration.
type MCPConfig struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// ToolInfo describes an MCP tool exposed by a connected server.
type ToolInfo struct {
	ServerName  string         `json:"server_name"`
	ToolName    string         `json:"tool_name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Source      string         `json:"source"` // "user", "project", "plugin"
}

// Kind returns the CodeN tool kind for this MCP tool: "mcp__<server>__<tool>".
func (t ToolInfo) Kind() string {
	return "mcp__" + t.ServerName + "__" + t.ToolName
}
