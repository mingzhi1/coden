package retrieval

import (
	"strings"
	"testing"
)

func TestFormatForPrompt(t *testing.T) {
	hits := []RetrievalEvidence{
		{
			Source:      "grep",
			Path:        "internal/core/kernel.go",
			Line:        42,
			Snippet:     "func (k *Kernel) Submit(req Request)",
			Verified:    false,
			Explanation: "Text match for 'Submit'",
		},
		{
			Source:   "lsp",
			Path:     "internal/core/kernel.go",
			Line:     42,
			Column:   6,
			Symbol:   "Kernel.Submit",
			Snippet:  "func (k *Kernel) Submit(req Request) error",
			Verified: true,
		},
		{
			Source:   "rag",
			Path:     "docs/architecture.md",
			Line:     10,
			Snippet:  "Kernel manages workflow lifecycle",
			Score:    0.85,
			Verified: false,
		},
	}

	output := FormatForPrompt(hits)

	// Verify structure
	if !strings.Contains(output, "Found 3 result") {
		t.Error("Expected result count header")
	}
	if !strings.Contains(output, "[grep]") {
		t.Error("Expected grep badge")
	}
	if !strings.Contains(output, "[✓ lsp]") {
		t.Error("Expected verified LSP badge")
	}
	if !strings.Contains(output, "0.85") {
		t.Error("Expected RAG score")
	}
	if !strings.Contains(output, "Kernel.Submit") {
		t.Error("Expected symbol name")
	}
}

func TestFormatForPromptEmpty(t *testing.T) {
	output := FormatForPrompt([]RetrievalEvidence{})
	if !strings.Contains(output, "No results") {
		t.Error("Expected 'No results' message")
	}
}

func TestGrepHitsToEvidence(t *testing.T) {
	hits := []GrepHit{
		{Path: "a.go", Line: 1, Snippet: "func main()"},
		{Path: "b.go", Line: 5, Snippet: "type Foo struct"},
	}

	evidence := GrepHitsToEvidence(hits, "test query")

	if len(evidence) != 2 {
		t.Fatalf("Expected 2 evidence, got %d", len(evidence))
	}

	e := evidence[0]
	if e.Source != "grep" {
		t.Errorf("Expected source 'grep', got %s", e.Source)
	}
	if e.Verified {
		t.Error("Grep evidence should not be verified")
	}
	if !strings.Contains(e.Explanation, "test query") {
		t.Error("Expected query in explanation")
	}
}

func TestLSPHitsToEvidence(t *testing.T) {
	hits := []LSPHit{
		{Path: "a.go", Line: 10, Column: 5, Symbol: "Kernel", Kind: "struct"},
	}

	evidence := LSPHitsToEvidence(hits, "definition")

	if len(evidence) != 1 {
		t.Fatalf("Expected 1 evidence, got %d", len(evidence))
	}

	e := evidence[0]
	if e.Source != "lsp" {
		t.Errorf("Expected source 'lsp', got %s", e.Source)
	}
	if !e.Verified {
		t.Error("LSP evidence should be verified")
	}
	if e.Symbol != "Kernel" {
		t.Errorf("Expected symbol 'Kernel', got %s", e.Symbol)
	}
}

func TestRAGChunksToEvidence(t *testing.T) {
	chunks := []RAGChunk{
		{Path: "doc.md", StartLine: 1, EndLine: 10, Content: "Architecture overview", Score: 0.92},
	}

	evidence := RAGChunksToEvidence(chunks, "architecture")

	if len(evidence) != 1 {
		t.Fatalf("Expected 1 evidence, got %d", len(evidence))
	}

	e := evidence[0]
	if e.Source != "rag" {
		t.Errorf("Expected source 'rag', got %s", e.Source)
	}
	if e.Score != 0.92 {
		t.Errorf("Expected score 0.92, got %f", e.Score)
	}
	if e.Verified {
		t.Error("RAG evidence should not be verified")
	}
}
