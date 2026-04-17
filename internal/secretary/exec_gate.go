package secretary

import (
	"fmt"
	"strings"
)

// AuthorizeToolCall checks whether a tool invocation is permitted.
// Returns nil if allowed, error with reason if denied.
//
// MVP: validates built-in tool kinds and MCP tools (prefix "mcp__").
func (s *Secretary) AuthorizeToolCall(sessionID, toolKind string) error {
	if isBuiltinTool(toolKind) {
		return nil
	}
	// MCP tools are authorized at registration time by the Manager.
	// At execution time, we just verify the prefix is known.
	if strings.HasPrefix(toolKind, "mcp__") {
		s.audit(sessionID, AuditEntry{
			Type:    "tool_auth",
			Allowed: true,
			Details: map[string]any{
				"tool": toolKind,
				"type": "mcp",
			},
		})
		return nil
	}

	// Unknown tool kind — deny
	return fmt.Errorf("secretary: unknown tool kind %q", toolKind)
}

// VisibleTools returns MCP tool definitions visible to the given worker.
// MVP: returns nil (no MCP tools registered yet).
// Phase 2: will return filtered MCP tool definitions.
func (s *Secretary) VisibleTools(target Target) []any {
	// Only Coder should see MCP tools.
	if target != TargetCoder {
		return nil
	}
	// Phase 2: return s.mcp.Tools() filtered by policy
	return nil
}

// isBuiltinTool returns true for known built-in tool kinds.
func isBuiltinTool(kind string) bool {
	switch kind {
	case "read_file", "write_file", "edit_file", "list_dir",
		"search", "grep_context", "run_shell",
		"lsp_symbols", "lsp_definition", "lsp_references", "lsp_didopen",
		"rag_search", "rag_index_build", "rag_index_update":
		return true
	}
	return false
}
