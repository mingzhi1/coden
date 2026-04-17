package toolruntime

import (
	"testing"
)

// M12-02: ToolRegistry tests

func TestToolRegistry_ListCore(t *testing.T) {
	r := NewToolRegistry()
	core := r.ListCore()

	// Core tools: write_file, edit_file, read_file, list_dir, search, run_shell, tool_search
	if len(core) < 7 {
		t.Fatalf("expected at least 7 core tools, got %d", len(core))
	}

	// Verify core tools are not deferred.
	for _, tool := range core {
		if tool.Deferred {
			t.Errorf("core tool %q should not be deferred", tool.Name)
		}
	}

	// Verify tool_search is a core tool.
	found := false
	for _, tool := range core {
		if tool.Name == "tool_search" {
			found = true
			break
		}
	}
	if !found {
		t.Error("tool_search should be a core (non-deferred) tool")
	}
}

func TestToolRegistry_ListDeferred(t *testing.T) {
	r := NewToolRegistry()
	deferred := r.ListDeferred()

	// Deferred: grep_context, lsp_symbols, lsp_definition, lsp_references, lsp_didopen, rag_search, web_fetch
	if len(deferred) < 6 {
		t.Fatalf("expected at least 6 deferred tools, got %d", len(deferred))
	}

	for _, tool := range deferred {
		if !tool.Deferred {
			t.Errorf("deferred tool %q should be deferred", tool.Name)
		}
	}
}

func TestToolRegistry_SearchDeferred_LSP(t *testing.T) {
	r := NewToolRegistry()

	results := r.SearchDeferred("definition")
	if len(results) == 0 {
		t.Fatal("expected at least one result for 'definition'")
	}

	found := false
	for _, tool := range results {
		if tool.Name == "lsp_definition" {
			found = true
			break
		}
	}
	if !found {
		t.Error("search for 'definition' should find lsp_definition")
	}
}

func TestToolRegistry_SearchDeferred_Semantic(t *testing.T) {
	r := NewToolRegistry()

	results := r.SearchDeferred("semantic search")
	if len(results) == 0 {
		t.Fatal("expected at least one result for 'semantic search'")
	}

	found := false
	for _, tool := range results {
		if tool.Name == "rag_search" {
			found = true
			break
		}
	}
	if !found {
		t.Error("search for 'semantic search' should find rag_search")
	}
}

func TestToolRegistry_SearchDeferred_NoMatchForCoreTool(t *testing.T) {
	r := NewToolRegistry()

	results := r.SearchDeferred("write_file")
	for _, tool := range results {
		if tool.Name == "write_file" {
			t.Error("write_file is core and should not appear in SearchDeferred results")
		}
	}
}

func TestToolRegistry_SearchAll(t *testing.T) {
	r := NewToolRegistry()

	// Search for "file" should match both core (read_file, write_file) and
	// deferred tools.
	results := r.SearchAll("file")
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'file', got %d", len(results))
	}
}

func TestToolRegistry_Get(t *testing.T) {
	r := NewToolRegistry()

	tool, ok := r.Get("write_file")
	if !ok {
		t.Fatal("write_file should exist")
	}
	if tool.ReadOnly {
		t.Error("write_file should not be read-only")
	}

	_, ok = r.Get("nonexistent_tool")
	if ok {
		t.Error("nonexistent tool should not be found")
	}
}

func TestToolRegistry_Register(t *testing.T) {
	r := NewToolRegistry()

	r.Register(ToolMeta{
		Name:        "custom_mcp_tool",
		Description: "A custom MCP tool",
		Deferred:    true,
		SearchHints: []string{"custom", "mcp"},
	})

	tool, ok := r.Get("custom_mcp_tool")
	if !ok {
		t.Fatal("custom tool should exist after registration")
	}
	if !tool.Deferred {
		t.Error("custom tool should be deferred")
	}

	// Should also be findable via search.
	results := r.SearchDeferred("mcp")
	found := false
	for _, result := range results {
		if result.Name == "custom_mcp_tool" {
			found = true
			break
		}
	}
	if !found {
		t.Error("custom MCP tool should be found via SearchDeferred")
	}
}

func TestToolRegistry_ReadOnlyClassification(t *testing.T) {
	r := NewToolRegistry()

	readOnlyExpected := map[string]bool{
		"read_file":      true,
		"list_dir":       true,
		"search":         true,
		"write_file":     false,
		"edit_file":      false,
		"run_shell":      false,
		"lsp_symbols":    true,
		"lsp_definition": true,
		"lsp_references": true,
		"rag_search":     true,
	}

	for name, expectedReadOnly := range readOnlyExpected {
		tool, ok := r.Get(name)
		if !ok {
			t.Errorf("tool %q should exist", name)
			continue
		}
		if tool.ReadOnly != expectedReadOnly {
			t.Errorf("tool %q: ReadOnly=%v, want %v", name, tool.ReadOnly, expectedReadOnly)
		}
	}
}
