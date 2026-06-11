package inventory

import (
	"fmt"
	"strings"
)

// FormatToolsPrompt generates the "Available tools" system prompt section
// based on what tools are actually available in the Inventory.
//
// This replaces the hardcoded tool list in prompts.Executor().
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

// FormatEnvironmentPrompt generates the repo-wide Environment section: every
// available tool, across all detected languages. Used as the fallback when a task
// has no scope to project onto. Prefer FormatEnvironmentPromptForLanguages so a
// task only sees its own subproject's toolchain.
func FormatEnvironmentPrompt(inv *Inventory) string {
	return formatEnvironment(inv, nil)
}

// FormatEnvironmentPromptForLanguages scopes the Environment section to a set of
// languages — the per-task projection: a task that only touches web/ gets the JS
// toolchain, not the whole monorepo's five languages. An empty langs falls back to
// the full repo-wide view.
func FormatEnvironmentPromptForLanguages(inv *Inventory, langs []string) string {
	if len(langs) == 0 {
		return formatEnvironment(inv, nil)
	}
	filter := make(map[string]bool, len(langs))
	for _, l := range langs {
		filter[l] = true
	}
	return formatEnvironment(inv, filter)
}

// toolInScope reports whether a tool entry belongs in a language-filtered view:
// language-agnostic tools (search) are always in; otherwise at least one of its
// languages must be in the filter. A nil filter admits everything.
func toolInScope(e *ToolEntry, filter map[string]bool) bool {
	if filter == nil || len(e.Languages) == 0 {
		return true
	}
	for _, l := range e.Languages {
		if filter[l] {
			return true
		}
	}
	return false
}

// formatEnvironment renders the Environment section, optionally filtered to a set
// of languages. Deliberately omits, vs the old version: absolute paths (leak the
// home dir, useless to the model), install hints (tempt the model to install
// global tools as a side effect), and LSP servers (driven by the lsp_* first-class
// tools, NOT run_shell — listing them here mislead the model into shelling out).
func formatEnvironment(inv *Inventory, filter map[string]bool) string {
	if inv == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Environment\n\n")

	categories := []struct {
		cat   Category
		label string
	}{
		{CatInterpreter, "Languages / Compilers"},
		{CatPackageManager, "Package Managers"},
		{CatFormatter, "Formatters"},
		{CatLinter, "Linters"},
		{CatSearch, "Search Tools"},
	}

	hasContent := false
	for _, c := range categories {
		var lines []string
		for _, e := range inv.ByCategory(c.cat) {
			if !toolInScope(e, filter) {
				continue
			}
			line := "- " + e.Command
			if e.Version != "" {
				line += " " + e.Version
			}
			if len(e.Languages) > 0 {
				line += " — " + strings.Join(e.Languages, ", ")
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			continue
		}
		hasContent = true
		fmt.Fprintf(&sb, "**%s:**\n%s\n\n", c.label, strings.Join(lines, "\n"))
	}

	// Layer-4 long-tail fallback: what the catalog could NOT serve, so the model
	// drives it via run_shell with its own toolchain knowledge (keeps unconfigured
	// languages — nim, crystal, … — workable rather than invisible). Always shown
	// (unfiltered): it is precisely the case where no scoped tool exists.
	unserved := inv.UnservedLanguages()
	unknownExts := inv.UnknownExtensions()
	longTail := len(unserved) > 0 || len(unknownExts) > 0
	if longTail {
		sb.WriteString("**Detected, but no preconfigured tooling** (use run_shell with your own knowledge of the toolchain — install deps, build, test, format via shell):\n")
		if len(unserved) > 0 {
			fmt.Fprintf(&sb, "- languages: %s\n", strings.Join(unserved, ", "))
		}
		if len(unknownExts) > 0 {
			fmt.Fprintf(&sb, "- unrecognized source files: %s\n", strings.Join(unknownExts, ", "))
		}
		sb.WriteString("\n")
	}

	if !hasContent && !longTail {
		return ""
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
