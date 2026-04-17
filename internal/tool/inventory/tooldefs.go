package inventory

// ToolDef is the single source of truth for an LLM-facing tool description.
// prompt.go and FormatToolsPrompt use these to build the dynamic system prompt.
type ToolDef struct {
	Kind        string            // tool kind key, e.g. "read_file"
	Description string            // one-line human description
	Fields      map[string]string // field name → description (required fields)
	Category    Category          // which inventory category gates this tool
	AlwaysOn    bool              // true = always listed regardless of inventory
}

// AllToolDefs returns the canonical list of tool definitions.
// This is the SSOT — both the hardcoded prompt fallback and the dynamic
// FormatToolsPrompt path should ultimately derive from this list.
func AllToolDefs() []ToolDef {
	return []ToolDef{
		// ── Always-on core tools ──────────────────────────────────
		{
			Kind:        "read_file",
			Description: "Read a workspace file",
			Fields:      map[string]string{"path": "workspace-relative path"},
			AlwaysOn:    true,
		},
		{
			Kind:        "search",
			Description: "Search for text patterns in the workspace",
			Fields: map[string]string{
				"dir":      "directory to search",
				"query":    "search text",
				"is_regex": "bool, optional",
			},
			AlwaysOn: true,
		},
		{
			Kind:        "grep_context",
			Description: "Read lines around a specific line in a file",
			Fields: map[string]string{
				"path":          "file path",
				"line":          "line number",
				"context_lines": "number, optional",
			},
			AlwaysOn: true,
		},
		{
			Kind:        "list_dir",
			Description: "List directory contents",
			Fields:      map[string]string{"dir": "directory to list"},
			AlwaysOn:    true,
		},
		{
			Kind:        "write_file",
			Description: "Create a new file with full content",
			Fields: map[string]string{
				"path":    "file path",
				"content": "full file body",
			},
			AlwaysOn: true,
		},
		{
			Kind:        "edit_file",
			Description: "Replace text in an existing file",
			Fields: map[string]string{
				"path":        "file path",
				"old_content": "exact text to find",
				"new_content": "replacement text",
			},
			AlwaysOn: true,
		},
		{
			Kind:        "run_shell",
			Description: "Execute a shell command",
			Fields: map[string]string{
				"command":     "shell command",
				"dir":         "optional working directory",
				"timeout_sec": "optional integer, default 60",
			},
			AlwaysOn: true,
		},
		{
			Kind:        "tool_search",
			Description: "Discover additional tools (LSP, semantic search, context grep, web fetch, etc.)",
			Fields:      map[string]string{"query": "describe what you want to do"},
			AlwaysOn:    true,
		},

		// ── LSP-gated tools ───────────────────────────────────────
		{
			Kind:        "lsp_symbols",
			Description: "List symbols in a file via LSP",
			Fields:      map[string]string{"path": "file path"},
			Category:    CatLSP,
		},
		{
			Kind:        "lsp_definition",
			Description: "Go to definition via LSP",
			Fields: map[string]string{
				"path":   "file path",
				"line":   "line number",
				"column": "column number",
			},
			Category: CatLSP,
		},
		{
			Kind:        "lsp_references",
			Description: "Find references via LSP",
			Fields: map[string]string{
				"path":   "file path",
				"line":   "line number",
				"column": "column number",
			},
			Category: CatLSP,
		},

		// ── Always-on subsystem tools ─────────────────────────────
		{
			Kind:        "rag_search",
			Description: "Semantic search over indexed codebase",
			Fields: map[string]string{
				"query": "search query",
				"top_k": "number, 1-10",
			},
			AlwaysOn: true,
		},

		// ── Artifact tools (M13) ─────────────────────────────────
		{
			Kind:        "read_artifact",
			Description: "Read a previously saved tool result by artifact ID",
			Fields:      map[string]string{"path": "artifact ID"},
			AlwaysOn:    true,
		},
		{
			Kind:        "list_artifacts",
			Description: "List recent artifacts from the current workflow",
			Fields: map[string]string{
				"query": "optional filter: workflow:<id> or session:<id>",
				"path":  "optional kind filter: tool_output, diff, spill, etc.",
			},
			AlwaysOn: true,
		},
	}
}
