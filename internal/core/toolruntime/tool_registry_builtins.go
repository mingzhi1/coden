package toolruntime

// registerBuiltins populates the registry with all built-in tool metadata.
// Core tools (Deferred=false) are always injected into the system prompt.
// Deferred tools are only discovered via tool_search.
func (r *ToolRegistry) registerBuiltins() {
	// ── Core tools (always in system prompt) ──

	r.tools["write_file"] = ToolMeta{
		Name:        "write_file",
		Description: "Create or overwrite a file with the given content.",
		Parameters:  `{"path": "string (required)", "content": "string (required)"}`,
		Category:    "core",
		ReadOnly:    false,
		Concurrent:  false,
	}
	r.tools["edit_file"] = ToolMeta{
		Name:        "edit_file",
		Description: "Edit a file by replacing old_content with new_content (search-and-replace).",
		Parameters:  `{"path": "string", "old_content": "string", "new_content": "string"}`,
		Category:    "core",
		ReadOnly:    false,
		Concurrent:  false,
	}
	r.tools["read_file"] = ToolMeta{
		Name:        "read_file",
		Description: "Read the full content of a file.",
		Parameters:  `{"path": "string (required)"}`,
		Category:    "core",
		ReadOnly:    true,
		Concurrent:  true,
	}
	r.tools["list_dir"] = ToolMeta{
		Name:        "list_dir",
		Description: "List files and directories in the workspace or a subdirectory.",
		Parameters:  `{"dir": "string (optional, default '.')"}`,
		Category:    "core",
		ReadOnly:    true,
		Concurrent:  true,
	}
	r.tools["search"] = ToolMeta{
		Name:        "search",
		Description: "Search for text patterns in files using ripgrep.",
		Parameters:  `{"query": "string (required)", "path": "string (optional)", "is_regex": "bool"}`,
		Category:    "core",
		ReadOnly:    true,
		Concurrent:  true,
	}
	r.tools["run_shell"] = ToolMeta{
		Name:        "run_shell",
		Description: "Execute a shell command in the workspace directory.",
		Parameters:  `{"command": "string (required)", "timeout_sec": "int (optional, default 60)"}`,
		Category:    "core",
		ReadOnly:    false,
		Concurrent:  false,
	}

	// ── Meta tool (always in system prompt) ──

	r.tools["tool_search"] = ToolMeta{
		Name: "tool_search",
		Description: "Search for additional tools beyond the core set. " +
			"Use when you need LSP navigation, semantic search, or specialized analysis.",
		Parameters:  `{"query": "string (required) — describe what you want to do"}`,
		Category:    "meta",
		ReadOnly:    true,
		Concurrent:  true,
		SearchHints: []string{"find tools", "discover", "available tools"},
	}

	// ── Deferred tools (only discovered via tool_search) ──

	r.tools["grep_context"] = ToolMeta{
		Name:        "grep_context",
		Description: "Search for a pattern and return surrounding context lines.",
		Parameters:  `{"query": "string", "path": "string", "line": "int", "context_lines": "int"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "search",
		SearchHints: []string{"grep", "context", "surrounding lines", "nearby code"},
	}
	r.tools["lsp_symbols"] = ToolMeta{
		Name:        "lsp_symbols",
		Description: "List all symbols (functions, types, variables) in a file using LSP.",
		Parameters:  `{"path": "string (required)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"symbols", "functions", "classes", "types", "definitions", "outline"},
	}
	r.tools["lsp_definition"] = ToolMeta{
		Name:        "lsp_definition",
		Description: "Go to definition of a symbol at a specific line and column.",
		Parameters:  `{"path": "string", "line": "int", "column": "int"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"definition", "go to definition", "jump to", "navigate"},
	}
	r.tools["lsp_references"] = ToolMeta{
		Name:        "lsp_references",
		Description: "Find all references to a symbol at a specific line and column.",
		Parameters:  `{"path": "string", "line": "int", "column": "int"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"references", "usages", "who calls", "find usages", "callers"},
	}
	r.tools["lsp_didopen"] = ToolMeta{
		Name:        "lsp_didopen",
		Description: "Notify LSP that a file is open (triggers diagnostics).",
		Parameters:  `{"path": "string (required)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "lsp",
		SearchHints: []string{"diagnostics", "lint", "errors", "warnings", "open file"},
	}
	r.tools["rag_search"] = ToolMeta{
		Name:        "rag_search",
		Description: "Semantic search across the codebase using embeddings (RAG).",
		Parameters:  `{"query": "string (required)", "top_k": "int (optional, default 5)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "rag",
		SearchHints: []string{"semantic search", "similar code", "related", "embedding", "vector search"},
	}
	r.tools["web_fetch"] = ToolMeta{
		Name:        "web_fetch",
		Description: "Fetch content from a URL and return as text.",
		Parameters:  `{"query": "string (URL required)"}`,
		Deferred:    true,
		ReadOnly:    true,
		Concurrent:  true,
		Category:    "search",
		SearchHints: []string{"fetch", "url", "http", "web", "download", "api"},
	}
}
