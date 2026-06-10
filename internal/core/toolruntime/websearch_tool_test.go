package toolruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/mingzhi1/coden/internal/core/retrieval"
)

type fakeSearchProvider struct {
	results []SearchResult
	gotTopK int
}

func (f *fakeSearchProvider) Search(_ context.Context, _ string, topK int) ([]SearchResult, error) {
	f.gotTopK = topK
	return f.results, nil
}

func TestWebSearch_FormatsResults(t *testing.T) {
	t.Parallel()
	prov := &fakeSearchProvider{results: []SearchResult{
		{Title: "Stripe API", URL: "https://stripe.com/docs", Snippet: "Charges API reference"},
		{Title: "Stripe Go", URL: "https://github.com/stripe/stripe-go", Snippet: "Go SDK"},
	}}
	tool := NewWebSearchTool(prov)

	res, err := tool.Execute(context.Background(), Request{Kind: "web_search", Query: "stripe go sdk"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if prov.gotTopK != 5 {
		t.Errorf("expected default top_k 5, got %d", prov.gotTopK)
	}
	if !strings.Contains(res.Output, "Stripe API") || !strings.Contains(res.Output, "https://stripe.com/docs") {
		t.Errorf("output missing result content: %q", res.Output)
	}
	ev, ok := res.StructuredData.([]retrieval.RetrievalEvidence)
	if !ok || len(ev) != 2 {
		t.Fatalf("expected 2 structured evidence, got %#v", res.StructuredData)
	}
	// External content is untrusted (信任线).
	if ev[0].Verified {
		t.Error("web_search evidence must be marked unverified (untrusted external source)")
	}
	if ev[0].Source != "web_search" {
		t.Errorf("expected source web_search, got %q", ev[0].Source)
	}
}

func TestWebSearch_RespectsTopK(t *testing.T) {
	t.Parallel()
	prov := &fakeSearchProvider{}
	tool := NewWebSearchTool(prov)
	_, err := tool.Execute(context.Background(), Request{Kind: "web_search", Query: "x", TopK: 3})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if prov.gotTopK != 3 {
		t.Errorf("expected top_k 3 passed through, got %d", prov.gotTopK)
	}
}

func TestWebSearch_EmptyQuery(t *testing.T) {
	t.Parallel()
	tool := NewWebSearchTool(&fakeSearchProvider{})
	if _, err := tool.Execute(context.Background(), Request{Kind: "web_search", Query: "  "}); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestWebSearch_NoProvider(t *testing.T) {
	t.Parallel()
	tool := NewWebSearchTool(nil)
	_, err := tool.Execute(context.Background(), Request{Kind: "web_search", Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "no search provider") {
		t.Fatalf("expected no-provider error, got %v", err)
	}
}

func TestWebSearch_CatalogRegistered(t *testing.T) {
	t.Parallel()
	if !KnownKind("web_search") {
		t.Error("web_search not in catalog (KnownKind false)")
	}
	if !ReadOnlyKind("web_search") {
		t.Error("web_search should be read-only")
	}
}
