package toolruntime

import "testing"

// TestCatalogHelpers locks the semantics the LLM workers depend on for
// validating and classifying model-emitted tool calls.
func TestCatalogHelpers(t *testing.T) {
	t.Parallel()

	// Built-ins are known; MCP kinds are known by prefix; garbage is not.
	for _, kind := range []string{"read_file", "run_shell", "read_artifact", "list_artifacts", "lsp_didopen", "mcp__srv__tool"} {
		if !KnownKind(kind) {
			t.Errorf("KnownKind(%q) = false, want true", kind)
		}
	}
	if KnownKind("summon_demon") || KnownKind("") {
		t.Errorf("unknown kinds must not be known")
	}

	// ReadOnly: catalog flag for built-ins; conservative false for MCP/unknown.
	for _, kind := range []string{"read_file", "tool_search", "web_fetch", "read_artifact", "list_artifacts", "rag_search", "grep_context"} {
		if !ReadOnlyKind(kind) {
			t.Errorf("ReadOnlyKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"write_file", "edit_file", "run_shell", "mcp__srv__tool", "nonsense"} {
		if ReadOnlyKind(kind) {
			t.Errorf("ReadOnlyKind(%q) = true, want false", kind)
		}
	}
}

// TestRegistryServesCatalog verifies NewToolRegistry exposes every catalog
// entry — the registry (tool_search) and the static helpers (call validation)
// must never disagree about which built-ins exist.
func TestRegistryServesCatalog(t *testing.T) {
	t.Parallel()

	r := NewToolRegistry()
	for kind := range builtinTools {
		if _, ok := r.Get(kind); !ok {
			t.Errorf("registry is missing catalog kind %q", kind)
		}
	}
	if got := len(r.All()); got != len(builtinTools) {
		t.Errorf("registry has %d tools, catalog has %d", got, len(builtinTools))
	}
}
