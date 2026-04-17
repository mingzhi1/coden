package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptypes "github.com/mark3labs/mcp-go/mcp"
)

// Manager manages connections to multiple MCP servers.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*mcpclient.Client // serverName → client
	tools   map[string]ToolInfo          // "mcp__server__tool" → info
	sources map[string]string            // serverName → "user"/"project"/"plugin"
}

// NewManager creates an MCP manager from configuration.
func NewManager(cfg MCPConfig, sources map[string]string) *Manager {
	m := &Manager{
		clients: make(map[string]*mcpclient.Client),
		tools:   make(map[string]ToolInfo),
		sources: sources,
	}
	return m
}

// ConnectAll starts all configured MCP servers and performs initialize + tools/list.
// Errors are logged but do not prevent other servers from starting.
// timeout per server: 30s.
func (m *Manager) ConnectAll(ctx context.Context, cfg MCPConfig) {
	for name, srv := range cfg.MCPServers {
		if err := m.connectOne(ctx, name, srv); err != nil {
			slog.Warn("[mcp] server connection failed",
				"server", name,
				"command", srv.Command,
				"error", err,
			)
		}
	}
}

func (m *Manager) connectOne(ctx context.Context, name string, srv ServerConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Expand environment variables
	env := ExpandEnv(srv.Env)
	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}

	// Create stdio client
	client, err := mcpclient.NewStdioMCPClient(srv.Command, envSlice, srv.Args...)
	if err != nil {
		return fmt.Errorf("create stdio client: %w", err)
	}

	// Initialize handshake
	initReq := mcptypes.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcptypes.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcptypes.Implementation{
		Name:    "coden",
		Version: "dev",
	}
	_, err = client.Initialize(ctx, initReq)
	if err != nil {
		client.Close()
		return fmt.Errorf("initialize: %w", err)
	}

	// List tools
	toolsResult, err := client.ListTools(ctx, mcptypes.ListToolsRequest{})
	if err != nil {
		slog.Warn("[mcp] tools/list failed, server connected but no tools", "server", name, "error", err)
		m.mu.Lock()
		m.clients[name] = client
		m.mu.Unlock()
		return nil
	}

	// Register tools
	source := m.sources[name]
	if source == "" {
		source = "project"
	}
	m.mu.Lock()
	m.clients[name] = client
	for _, tool := range toolsResult.Tools {
		desc := tool.Description
		if len(desc) > 2048 {
			desc = desc[:2048] + "..."
		}

		var schema map[string]any
		if tool.InputSchema.Properties != nil {
			schema = make(map[string]any)
			schema["type"] = tool.InputSchema.Type
			schema["properties"] = tool.InputSchema.Properties
			if tool.InputSchema.Required != nil {
				schema["required"] = tool.InputSchema.Required
			}
		}

		info := ToolInfo{
			ServerName:  name,
			ToolName:    tool.Name,
			Description: desc,
			InputSchema: schema,
			Source:      source,
		}
		m.tools[info.Kind()] = info
	}
	m.mu.Unlock()

	slog.Info("[mcp] server connected",
		"server", name,
		"tools", len(toolsResult.Tools),
		"source", source,
	)
	return nil
}

// Tools returns all registered MCP tools.
func (m *Manager) Tools() []ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ToolInfo, 0, len(m.tools))
	for _, t := range m.tools {
		out = append(out, t)
	}
	return out
}

// GetTool returns a specific tool by kind, or nil.
func (m *Manager) GetTool(kind string) *ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tools[kind]
	if !ok {
		return nil
	}
	return &t
}

// CallTool invokes a tool on the appropriate MCP server.
func (m *Manager) CallTool(ctx context.Context, kind string, args map[string]any) (string, error) {
	m.mu.RLock()
	info, ok := m.tools[kind]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("unknown MCP tool: %s", kind)
	}
	client, clientOk := m.clients[info.ServerName]
	m.mu.RUnlock()
	if !clientOk || client == nil {
		return "", fmt.Errorf("MCP server %q not connected", info.ServerName)
	}

	callReq := mcptypes.CallToolRequest{}
	callReq.Params.Name = info.ToolName
	callReq.Params.Arguments = args

	result, err := client.CallTool(ctx, callReq)
	if err != nil {
		return "", fmt.Errorf("MCP tool %s call failed: %w", kind, err)
	}

	// Extract text content from result
	var sb strings.Builder
	for _, block := range result.Content {
		if textContent, ok := block.(mcptypes.TextContent); ok {
			sb.WriteString(textContent.Text)
			sb.WriteString("\n")
		}
	}

	if result.IsError {
		return "", fmt.Errorf("MCP tool %s returned error: %s", kind, sb.String())
	}

	return strings.TrimSpace(sb.String()), nil
}

// Close shuts down all MCP server connections.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			slog.Warn("[mcp] close failed", "server", name, "error", err)
		}
	}
	m.clients = make(map[string]*mcpclient.Client)
	m.tools = make(map[string]ToolInfo)
	return nil
}

// ServerCount returns the number of connected servers.
func (m *Manager) ServerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// ToolCount returns the number of registered tools.
func (m *Manager) ToolCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tools)
}

// FormatToolParams returns a compact parameter spec string for a single MCP tool,
// suitable for display in tool_search results.
func FormatToolParams(t ToolInfo) string {
	if len(t.InputSchema) == 0 {
		return "content: <json args>"
	}
	b, err := json.Marshal(t.InputSchema)
	if err != nil {
		return "content: <json args>"
	}
	if len(b) > 200 {
		return "content: <json args> (see schema in prompt)"
	}
	return "content: " + string(b)
}

// FormatToolsForPrompt builds a pre-formatted markdown section describing all
// registered MCP tools so the Coder LLM knows how to invoke them.
// Returns "" when no tools are registered.
func FormatToolsForPrompt(tools []ToolInfo) string {
	if len(tools) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## MCP Tools\n\n")
	sb.WriteString("The following external tools are available via MCP (Model Context Protocol).\n")
	sb.WriteString("To call an MCP tool, emit a tool_call with `kind` set to the tool's kind string.\n")
	sb.WriteString("Pass arguments as a JSON object in the `content` field.\n\n")
	for _, t := range tools {
		sb.WriteString("### `")
		sb.WriteString(t.Kind())
		sb.WriteString("`\n")
		if t.Description != "" {
			sb.WriteString(t.Description)
			sb.WriteString("\n")
		}
		if len(t.InputSchema) > 0 {
			sb.WriteString("\n**Input schema:**\n```json\n")
			schemaJSON, err := json.Marshal(t.InputSchema)
			if err == nil {
				sb.Write(schemaJSON)
			}
			sb.WriteString("\n```\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
