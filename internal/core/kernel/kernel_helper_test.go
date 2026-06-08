package kernel

import "testing"

func TestRankInsightsRRF_QueryAware(t *testing.T) {
	items := []insightItem{
		{category: "finding", title: "RAG index uses FTS5", content: "bm25 ranking over chunks", confidence: 0.4},
		{category: "decision", title: "Use SQLite for state", content: "avoids ops complexity", confidence: 0.95},
		{category: "finding", title: "TUI event loop", content: "bubbletea update model", confidence: 0.9},
	}

	// Query about RAG should surface the low-confidence RAG insight first, even
	// though a different insight has higher confidence — proving relevance, not
	// just confidence, drives ordering.
	got := rankInsightsRRF(items, "how does the rag fts5 index work", 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].title != "RAG index uses FTS5" {
		t.Errorf("expected RAG insight ranked first for a RAG query, got %q", got[0].title)
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
