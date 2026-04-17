package inventory

import (
	"fmt"
	"strings"
)

// FormatToolsPrompt generates the "Available tools" system prompt section
// based on what tools are actually available in the Inventory.
//
// This replaces the hardcoded tool list in prompts.Coder().
// Tools that depend on external services (LSP, RAG) are only included
// if the corresponding category has available entries.
//
// The tool list is derived from AllToolDefs() (SSOT).
func FormatToolsPrompt(inv *Inventory) string {
	if inv == nil {
		return defaultToolsPrompt() // fallback to full hardcoded list
	}

	defs := AllToolDefs()
	var sb strings.Builder
	sb.WriteString("Available tools and their required fields:\n")

	for _, def := range defs {
		// Gate non-always-on tools by inventory category.
		if !def.AlwaysOn {
			if def.Category != "" && !inv.HasCategory(def.Category) {
				continue
			}
		}

		// Build the JSON field spec.
		sb.WriteString(fmt.Sprintf("- %s: {\"kind\": \"%s\"", def.Kind, def.Kind))
		for field, desc := range def.Fields {
			sb.WriteString(fmt.Sprintf(", \"%s\": \"<%s>\"", field, desc))
		}
		sb.WriteString("}")

		// Add language note for LSP tools.
		if def.Category == CatLSP {
			lspLangs := inv.AvailableLanguages()
			if len(lspLangs) > 0 {
				sb.WriteString(fmt.Sprintf(" (available for: %s)", strings.Join(lspLangs, ", ")))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nAdditional tools are available but not listed here. Use tool_search to find them when you need:\n")
	sb.WriteString("- Code navigation (go-to-definition, find references, symbol lists)\n")
	sb.WriteString("- Semantic/embedding-based code search\n")
	sb.WriteString("- Surrounding context for a specific line\n")
	sb.WriteString("- Fetching content from URLs\n\n")

	return sb.String()
}

// FormatEnvironmentPrompt generates a user context section describing
// external tools available via run_shell. This helps the LLM make
// informed decisions about which shell commands to use.
func FormatEnvironmentPrompt(inv *Inventory) string {
	if inv == nil {
		return ""
	}

	available := inv.Available()
	if len(available) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Environment\n\n")

	// Group by category
	categories := []struct {
		cat   Category
		label string
	}{
		{CatInterpreter, "Languages / Compilers"},
		{CatPackageManager, "Package Managers"},
		{CatFormatter, "Formatters"},
		{CatLinter, "Linters"},
		{CatSearch, "Search Tools"},
		{CatLSP, "LSP Servers"},
	}

	hasContent := false
	for _, c := range categories {
		entries := inv.ByCategory(c.cat)
		if len(entries) == 0 {
			continue
		}
		hasContent = true
		sb.WriteString(fmt.Sprintf("**%s:**\n", c.label))
		for _, e := range entries {
			line := fmt.Sprintf("- %s", e.Command)
			if e.Version != "" {
				line += fmt.Sprintf(" %s", e.Version)
			}
			if e.Path != "" {
				line += fmt.Sprintf(" (%s)", e.Path)
			}
			if len(e.Languages) > 0 {
				line += fmt.Sprintf(" — %s", strings.Join(e.Languages, ", "))
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}

	if !hasContent {
		return ""
	}

	// Add missing tool hints
	unavailable := inv.Unavailable()
	if len(unavailable) > 0 {
		sb.WriteString("**Not installed (install hints):**\n")
		for _, e := range unavailable {
			if e.InstallHint != "" {
				sb.WriteString(fmt.Sprintf("- %s: `%s`\n", e.Name, e.InstallHint))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// defaultToolsPrompt returns the full hardcoded tool list as a fallback
// when no Inventory is available.
func defaultToolsPrompt() string {
	return `Available tools and their required fields:
- read_file: {"kind": "read_file", "path": "<workspace-relative path>"}
- search: {"kind": "search", "dir": "<directory>", "query": "<search text>", "is_regex": <bool, optional>}
- grep_context: {"kind": "grep_context", "path": "<file path>", "line": <number>, "context_lines": <number, optional>}
- list_dir: {"kind": "list_dir", "dir": "<directory>"}
- write_file: {"kind": "write_file", "path": "<file path>", "content": "<full file body>"}
- edit_file: {"kind": "edit_file", "path": "<file path>", "old_content": "<exact text to find>", "new_content": "<replacement text>"}
- run_shell: {"kind": "run_shell", "command": "<shell command>", "dir": "<optional working directory>", "timeout_sec": <optional integer, default 60>}
- lsp_symbols: {"kind": "lsp_symbols", "path": "<file path>"}
- lsp_definition: {"kind": "lsp_definition", "path": "<file path>", "line": <number>, "column": <number>}
- lsp_references: {"kind": "lsp_references", "path": "<file path>", "line": <number>, "column": <number>}
- rag_search: {"kind": "rag_search", "query": "<search query>", "top_k": <number, 1-10>}
`
}
