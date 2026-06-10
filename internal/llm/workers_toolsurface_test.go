package llm

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// TestNormalizePlanToolCalls_ExecutorSurface locks the catalog-driven gate:
// every kind the system advertises (built-ins, artifact tools, MCP tools) must
// survive normalization, because the prompt and tool_search both promise the
// model it can call them. Before the catalog refactor this was a hand-written
// whitelist that silently dropped read_artifact/list_artifacts (advertised in
// the core prompt), lsp_didopen and all mcp__* kinds (discoverable via
// tool_search) — the model called them and the calls vanished.
func TestNormalizePlanToolCalls_ExecutorSurface(t *testing.T) {
	t.Parallel()

	calls := []codePlanToolCall{
		{Kind: "mcp__github__create_issue", Content: `{"title":"bug","body":"details"}`},
		{Kind: "read_artifact", Path: "art-123"},
		{Kind: "list_artifacts", Query: "spill"},
		{Kind: "lsp_didopen", Path: "main.go"},
	}
	out := normalizePlanToolCalls("wf1", calls)
	if len(out) != 4 {
		t.Fatalf("expected all 4 advertised kinds to pass the gate, got %d: %+v", len(out), out)
	}
	if out[0].Request.Kind != "mcp__github__create_issue" || out[0].Request.Content == "" {
		t.Errorf("mcp call must pass with its JSON args intact, got %+v", out[0].Request)
	}
}

// TestNormalizePlanToolCalls_StillRejectsInvalid verifies the gate still drops
// hallucinated kinds and calls missing required fields — opening the surface
// must not mean accepting garbage.
func TestNormalizePlanToolCalls_StillRejectsInvalid(t *testing.T) {
	t.Parallel()

	calls := []codePlanToolCall{
		{Kind: "summon_demon", Content: "definitely not a tool"},
		{Kind: "read_artifact"},          // missing artifact ID
		{Kind: "lsp_didopen"},            // missing path
		{Kind: "write_file", Path: "ok"}, // missing content
	}
	if out := normalizePlanToolCalls("wf1", calls); len(out) != 0 {
		t.Errorf("invalid calls must be dropped, got %d: %+v", len(out), out)
	}
}

// TestSplitToolCalls_CatalogDriven locks the read/mutation partition to the
// catalog's ReadOnly flag: read-only kinds run in parallel with the full read
// budget and stay usable in read-only modes (analyzer, plan_only); mutations
// and MCP tools (side effects unknown) take the serial gated path. The old
// hardcoded list misclassified tool_search/web_fetch as mutations, so
// read-only mode refused them.
func TestSplitToolCalls_CatalogDriven(t *testing.T) {
	t.Parallel()

	mk := func(kind string) workflow.ToolCall {
		return workflow.ToolCall{Request: toolruntime.Request{Kind: kind}}
	}
	reads, mutations := splitToolCalls([]workflow.ToolCall{
		mk("read_file"), mk("tool_search"), mk("web_fetch"),
		mk("read_artifact"), mk("list_artifacts"), mk("rag_search"),
		mk("write_file"), mk("run_shell"), mk("mcp__jira__update_ticket"),
	})

	wantReads := map[string]bool{
		"read_file": true, "tool_search": true, "web_fetch": true,
		"read_artifact": true, "list_artifacts": true, "rag_search": true,
	}
	if len(reads) != len(wantReads) {
		t.Errorf("expected %d reads, got %d: %+v", len(wantReads), len(reads), reads)
	}
	for _, r := range reads {
		if !wantReads[r.Request.Kind] {
			t.Errorf("kind %q misclassified as read", r.Request.Kind)
		}
	}
	wantMut := map[string]bool{"write_file": true, "run_shell": true, "mcp__jira__update_ticket": true}
	if len(mutations) != len(wantMut) {
		t.Errorf("expected %d mutations, got %d: %+v", len(wantMut), len(mutations), mutations)
	}
	for _, m := range mutations {
		if !wantMut[m.Request.Kind] {
			t.Errorf("kind %q misclassified as mutation", m.Request.Kind)
		}
	}
}
