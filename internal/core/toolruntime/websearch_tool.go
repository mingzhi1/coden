package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/retrieval"
)

// SearchResult is one hit returned by a SearchProvider.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// SearchProvider abstracts the web-search backend (Tavily / Exa / Brave / an MCP
// search server). Injected so the tool is testable without network and the
// backend is swappable — the analyze_research.md design calls for an
// agent-optimized search API rather than scraping HTML ourselves.
type SearchProvider interface {
	Search(ctx context.Context, query string, topK int) ([]SearchResult, error)
}

// WebSearchTool implements Executor for real-time web search (kind "web_search").
// It owns no search logic itself — it formats a SearchProvider's hits into the
// standard Result (human-readable Output + structured RetrievalEvidence) so search
// findings flow through the same artifact pipeline as every other tool.
type WebSearchTool struct {
	provider SearchProvider
}

func NewWebSearchTool(provider SearchProvider) *WebSearchTool {
	return &WebSearchTool{provider: provider}
}

// Execute implements Executor.
func (t *WebSearchTool) Execute(ctx context.Context, req Request) (Result, error) {
	if req.Kind != "web_search" {
		return Result{}, fmt.Errorf("unsupported web search tool kind: %s", req.Kind)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return Result{}, fmt.Errorf("web_search: query is required")
	}
	if t.provider == nil {
		return Result{}, fmt.Errorf("web_search: no search provider configured (set TAVILY_API_KEY or wire a SearchProvider)")
	}
	topK := req.TopK
	if topK <= 0 {
		topK = 5
	}

	results, err := t.provider.Search(ctx, query, topK)
	if err != nil {
		return Result{}, fmt.Errorf("web_search: %w", err)
	}

	var out strings.Builder
	evidence := make([]retrieval.RetrievalEvidence, 0, len(results))
	for i, r := range results {
		fmt.Fprintf(&out, "%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, strings.TrimSpace(r.Snippet))
		evidence = append(evidence, retrieval.RetrievalEvidence{
			Source:      "web_search",
			Path:        r.URL,
			Snippet:     truncateString(r.Snippet, 300),
			Verified:    false, // external content is untrusted (analyze_research.md §4 信任线)
			Explanation: r.Title,
		})
	}

	return Result{
		Summary:        fmt.Sprintf("web_search %q → %d result(s)", query, len(results)),
		Output:         out.String(),
		StructuredData: evidence,
	}, nil
}

// --- Tavily provider -------------------------------------------------------

// TavilyProvider is a SearchProvider backed by the Tavily search API
// (https://tavily.com), a search service designed for LLM agents: it returns
// ranked, content-extracted results rather than raw HTML.
type TavilyProvider struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

// NewTavilyProvider builds a Tavily-backed provider. apiKey must be non-empty.
func NewTavilyProvider(apiKey string) *TavilyProvider {
	return &TavilyProvider{
		apiKey:   apiKey,
		endpoint: "https://api.tavily.com/search",
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// newTavilyProviderFromEnv returns a Tavily provider when TAVILY_API_KEY is set,
// otherwise nil — so the tool is registered/known regardless, and only fails (with
// a clear message) when actually invoked without a key.
func newTavilyProviderFromEnv() SearchProvider {
	key := strings.TrimSpace(os.Getenv("TAVILY_API_KEY"))
	if key == "" {
		return nil
	}
	return NewTavilyProvider(key)
}

func (p *TavilyProvider) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	body, err := json.Marshal(map[string]any{
		"api_key":      p.apiKey,
		"query":        query,
		"max_results":  topK,
		"search_depth": "basic",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tavily request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create tavily request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tavily request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tavily HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode tavily response: %w", err)
	}

	out := make([]SearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}
