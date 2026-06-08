package kernel

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/insight"
)

func TestRankInsightsFused_QueryAware(t *testing.T) {
	items := []insight.Insight{
		{Category: "finding", Title: "RAG index uses FTS5", Content: "bm25 ranking over chunks", Confidence: 0.4},
		{Category: "decision", Title: "Use SQLite for state", Content: "avoids ops complexity", Confidence: 0.95},
		{Category: "finding", Title: "TUI event loop", Content: "bubbletea update model", Confidence: 0.9},
	}

	// Lexical-only (no query vector): a RAG query surfaces the low-confidence RAG
	// insight first, proving relevance — not confidence — drives ordering.
	got := rankInsightsFused(items, "how does the rag fts5 index work", nil, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].Title != "RAG index uses FTS5" {
		t.Errorf("expected RAG insight ranked first for a RAG query, got %q", got[0].Title)
	}
}

func TestRankInsightsFused_Semantic(t *testing.T) {
	// Query embedding close to item[1]'s embedding should pull it to the top even
	// with no lexical overlap and lower confidence.
	items := []insight.Insight{
		{Title: "alpha", Content: "x", Confidence: 0.9, Embedding: []float32{1, 0, 0}},
		{Title: "beta", Content: "y", Confidence: 0.1, Embedding: []float32{0, 1, 0}},
	}
	got := rankInsightsFused(items, "zzz", []float32{0, 1, 0}, 1)
	if len(got) != 1 || got[0].Title != "beta" {
		t.Errorf("expected semantic match 'beta' first, got %+v", got)
	}
}

func TestLexOverlap(t *testing.T) {
	q := lexTerms("RAG FTS5 index")
	if n := lexOverlap(q, "the rag index uses fts5 bm25"); n < 2 {
		t.Errorf("expected >=2 term overlaps, got %d", n)
	}
	if n := lexOverlap(q, "completely unrelated text"); n != 0 {
		t.Errorf("expected 0 overlaps, got %d", n)
	}
}

func TestCosine(t *testing.T) {
	if s := cosine([]float32{1, 0}, []float32{1, 0}); s < 0.999 {
		t.Errorf("self-cosine should be ~1, got %f", s)
	}
	if s := cosine([]float32{1, 0}, []float32{0, 1}); s != 0 {
		t.Errorf("orthogonal cosine should be 0, got %f", s)
	}
}
