package toolruntime

import "strings"

// builtinTools is the static metadata table for every built-in tool kind — the
// single source of truth for "which kinds exist" and "which are read-only".
// NewToolRegistry copies it; KnownKind/ReadOnlyKind query it directly. The LLM
// workers validate and classify model-emitted tool calls through those helpers,
// so adding a tool here is the ONLY step needed to make it callable end-to-end
// (prompt advertisement aside). Keeping three hand-maintained lists in sync
// (registry, normalize whitelist, read/write split) is exactly the drift this
// table replaces: it produced tools that were advertised but silently dropped
// (read_artifact), and discoverable but uncallable (lsp_didopen, mcp__*).
var builtinTools = map[string]ToolMeta{
	// ── Core tools (always in system prompt) ──
	"write_file": {
		Name:        "write_file",
		Description: "Create or overwrite a file with the given content.",
		Parameters:  `{"path": "string (required)", "content": "string (required)"}`,
		Category:    "core",
		ReadOnly:    false,
		Concurrent:  false,
	},
	"edit_file": {
		Name:        "edit_file",
		Description: "Edit a file by replacing old_content with new_content (search-and-replace).",
		Parameters:  `{"path": "string", "old_content": "string", "new_content": "string"}`,
		Category:    "core",
		ReadOnly:    false,
		Concurrent:  false,
	},
	"read_file": {
		Name:        "read_file",
		Description: "Read the full content of a file.",
		Parameters:  `{"path": "string (required)"}`,
		Category:    "core",
		ReadOnly:    true,
		Concurrent:  true,
	},
	"list_dir": {
		Name:        "list_dir",
		Description: "List files and directories in the workspace or a subdirectory.",
		Parameters:  `{"dir": "string (optional, default '.')"}`,
		Category:    "core",
		ReadOnly:    true,
		Concurrent:  true,
	},
	"search": {
		Name:        "search",
		Description: "Search for text patterns in files using ripgrep.",
		Parameters:  `{"query": "string (required)", "path": "string (optional)", "is_regex": "bool"}`,
		Category:    "core",
		ReadOnly:    true,
		Concurrent:  true,
	},
	"run_shell": {
		Name:        "run_shell",
		Description: "Execute a shell command in the workspace directory.",
		Parameters:  `{"command": "string (required)", "timeout_sec": "int (optional, default 60)"}`,
		Category:    "core",
		ReadOnly:    false,
		Concurrent:  false,
	},

	// ── Meta tools (always in system prompt) ──
	"tool_search": {
		Name: "tool_search",
		Description: "Search for additional tools beyond the core set. " +
			"Use when you need LSP navigation, semantic search, or specialized analysis.",
		Parameters:  `{"query": "string (required) — describe what you want to do"}`,
		Category:    "meta",
		ReadOnly:    true,
		Concurrent:  true,
		SearchHints: []string{"find tools", "discover", "available tools"},
	},
	"read_artifact": {
		Name:        "read_artifact",
		Description: "Read a previously saved tool result (artifact) by its artifact ID.",
		Parameters:  `{"path": "string (required) — artifact ID"}`,
		Category:    "meta",
		ReadOnly:    true,
		Concurrent:  true,
		SearchHints: []string{"artifact", "saved result", "spilled", "previous output"},
	},
	"list_artifacts": {
		Name:        "list_artifacts",
		Description: "List recent artifacts from the current workflow.",
		Parameters:  `{"query": "string (optional filter)", "path": "string (optional kind filter)"}`,
		Category:    "meta",
		ReadOnly:    true,
		Concurrent:  true,
		SearchHints: []string{"artifacts", "list results", "saved outputs"},
	},

	// ── Deferred tools (only discovered via tool_search) ──
	"grep_context": {
		Name:        "grep_context",
		Description: "Search for a pattern and return surrounding context lines.",
		Parameters:  `{"query": "string", "path": "string", "line": "int", "context_lines": "int"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "search",
		SearchHints: []string{"grep", "context", "surrounding lines", "nearby code"},
	},
	"lsp_symbols": {
		Name:        "lsp_symbols",
		Description: "List all symbols (functions, types, variables) in a file using LSP.",
		Parameters:  `{"path": "string (required)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"symbols", "functions", "classes", "types", "definitions", "outline"},
	},
	"lsp_definition": {
		Name:        "lsp_definition",
		Description: "Go to definition of a symbol at a specific line and column.",
		Parameters:  `{"path": "string", "line": "int", "column": "int"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"definition", "go to definition", "jump to", "navigate"},
	},
	"lsp_references": {
		Name:        "lsp_references",
		Description: "Find all references to a symbol at a specific line and column.",
		Parameters:  `{"path": "string", "line": "int", "column": "int"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"references", "usages", "who calls", "find usages", "callers"},
	},
	"lsp_didopen": {
		Name:        "lsp_didopen",
		Description: "Notify LSP that a file is open (triggers diagnostics).",
		Parameters:  `{"path": "string (required)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"diagnostics", "lint", "errors", "warnings", "open file"},
	},
	"rag_search": {
		Name:        "rag_search",
		Description: "Semantic search across the codebase using embeddings (RAG).",
		Parameters:  `{"query": "string (required)", "top_k": "int (optional, default 5)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "rag",
		SearchHints: []string{"semantic search", "similar code", "related", "embedding", "vector search"},
	},
	"web_fetch": {
		Name:        "web_fetch",
		Description: "Fetch content from a URL and return as text.",
		Parameters:  `{"query": "string (URL required)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "search",
		SearchHints: []string{"fetch", "url", "http", "web", "download", "api"},
	},
}

// IsMCPKind reports whether kind addresses an MCP tool ("mcp__<server>__<tool>").
// MCP tools are registered into ToolRegistry instances at startup, but their
// metadata is dynamic — the static helpers below treat them as known and
// non-read-only (side effects unknown, so they take the gated mutation path).
func IsMCPKind(kind string) bool {
	return strings.HasPrefix(kind, "mcp__")
}

// KnownKind reports whether kind is a callable tool: a built-in from the
// catalog or any MCP tool. Workers use it to validate model-emitted tool
// calls — anything else is a hallucinated kind and is dropped.
func KnownKind(kind string) bool {
	if IsMCPKind(kind) {
		return true
	}
	_, ok := builtinTools[kind]
	return ok
}

// ReadOnlyKind reports whether kind is a built-in with no side effects.
// Unknown kinds and MCP tools return false so callers route them through
// the mutation path (executed serially, refused in read-only modes).
func ReadOnlyKind(kind string) bool {
	m, ok := builtinTools[kind]
	return ok && m.ReadOnly
}
