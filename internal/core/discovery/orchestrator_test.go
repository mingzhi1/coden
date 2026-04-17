package discovery

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/retrieval"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newMockRuntime(exec toolruntime.Executor) *toolruntime.Runtime {
	rt, _ := toolruntime.NewWithExecutor(exec)
	return rt
}

type mockExecutor struct {
	results map[string]toolruntime.Result
	errors  map[string]error
}

func (m *mockExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	if err, ok := m.errors[req.Kind]; ok {
		return toolruntime.Result{}, err
	}
	if res, ok := m.results[req.Kind]; ok {
		return res, nil
	}
	return toolruntime.Result{}, nil
}

func clearCache() {
	orchestratorCache.mu.Lock()
	orchestratorCache.items = make(map[string]cacheEntry)
	orchestratorCache.mu.Unlock()
}

// ---------------------------------------------------------------------------
// TestSearchReturnsGrepResults
// ---------------------------------------------------------------------------

func TestSearchReturnsGrepResults(t *testing.T) {
	t.Cleanup(clearCache)
	clearCache()

	exec := &mockExecutor{
		results: map[string]toolruntime.Result{
			"search": {
				Output: "src/main.go:10: func main() {\nsrc/util.go:25: func helper() {\n",
			},
		},
	}

	orch := NewToolOrchestrator(newMockRuntime(exec))
	hits, err := orch.Search(context.Background(), SearchParams{
		Query: "func",
		Kinds: []string{"grep"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}

	// First hit
	if hits[0].Source != "grep" {
		t.Errorf("hits[0].Source = %q, want %q", hits[0].Source, "grep")
	}
	if hits[0].Path != "src/main.go" {
		t.Errorf("hits[0].Path = %q, want %q", hits[0].Path, "src/main.go")
	}
	if hits[0].Line != 10 {
		t.Errorf("hits[0].Line = %d, want %d", hits[0].Line, 10)
	}
	if hits[0].Snippet != "func main() {" {
		t.Errorf("hits[0].Snippet = %q, want %q", hits[0].Snippet, "func main() {")
	}

	// Second hit
	if hits[1].Path != "src/util.go" {
		t.Errorf("hits[1].Path = %q, want %q", hits[1].Path, "src/util.go")
	}
	if hits[1].Line != 25 {
		t.Errorf("hits[1].Line = %d, want %d", hits[1].Line, 25)
	}
	if hits[1].Snippet != "func helper() {" {
		t.Errorf("hits[1].Snippet = %q, want %q", hits[1].Snippet, "func helper() {")
	}
}

// ---------------------------------------------------------------------------
// TestSearchWithStructuredRAGData
// ---------------------------------------------------------------------------

func TestSearchWithStructuredRAGData(t *testing.T) {
	t.Cleanup(clearCache)
	clearCache()

	structured := []retrieval.RetrievalEvidence{
		{
			Source:  "rag",
			Path:    "pkg/server.go",
			Line:    42,
			Snippet: "func StartServer() error {",
			Score:   0.95,
		},
		{
			Source:  "rag",
			Path:    "pkg/handler.go",
			Line:    10,
			Snippet: "func HandleRequest(w http.ResponseWriter, r *http.Request) {",
			Score:   0.88,
		},
	}

	exec := &mockExecutor{
		results: map[string]toolruntime.Result{
			"rag_search": {
				Output:         "1. pkg/server.go:42 (score: 0.95)\n   func StartServer() error {",
				StructuredData: structured,
			},
		},
	}

	orch := NewToolOrchestrator(newMockRuntime(exec))
	hits, err := orch.Search(context.Background(), SearchParams{
		Query: "start server",
		Kinds: []string{"rag"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (structured), got %d", len(hits))
	}

	// The orchestrator must prefer StructuredData over text parsing.
	if hits[0].Path != "pkg/server.go" {
		t.Errorf("hits[0].Path = %q, want %q", hits[0].Path, "pkg/server.go")
	}
	if hits[0].Score != 0.95 {
		t.Errorf("hits[0].Score = %f, want %f", hits[0].Score, 0.95)
	}
	if hits[1].Path != "pkg/handler.go" {
		t.Errorf("hits[1].Path = %q, want %q", hits[1].Path, "pkg/handler.go")
	}
	if hits[1].Score != 0.88 {
		t.Errorf("hits[1].Score = %f, want %f", hits[1].Score, 0.88)
	}
}

// ---------------------------------------------------------------------------
// TestSearchFallsBackToTextParsing
// ---------------------------------------------------------------------------

func TestSearchFallsBackToTextParsing(t *testing.T) {
	t.Cleanup(clearCache)
	clearCache()

	// Return RAG output with NO StructuredData — the orchestrator must fall
	// back to parseRAGOutput.
	exec := &mockExecutor{
		results: map[string]toolruntime.Result{
			"rag_search": {
				Output: "1. internal/db.go:100 (score: 0.82)\n   func OpenDB(dsn string) (*sql.DB, error) {\n2. internal/db.go:200 (score: 0.71)\n   func CloseDB(db *sql.DB) error {",
			},
		},
	}

	orch := NewToolOrchestrator(newMockRuntime(exec))
	hits, err := orch.Search(context.Background(), SearchParams{
		Query: "database connection",
		Kinds: []string{"rag"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (text-parsed), got %d", len(hits))
	}

	if hits[0].Source != "rag" {
		t.Errorf("hits[0].Source = %q, want %q", hits[0].Source, "rag")
	}
	if hits[0].Path != "internal/db.go" {
		t.Errorf("hits[0].Path = %q, want %q", hits[0].Path, "internal/db.go")
	}
	if hits[0].Line != 100 {
		t.Errorf("hits[0].Line = %d, want %d", hits[0].Line, 100)
	}
	if hits[0].Score != 0.82 {
		t.Errorf("hits[0].Score = %f, want %f", hits[0].Score, 0.82)
	}
	if hits[0].Snippet != "func OpenDB(dsn string) (*sql.DB, error) {" {
		t.Errorf("hits[0].Snippet = %q, want %q", hits[0].Snippet, "func OpenDB(dsn string) (*sql.DB, error) {")
	}

	if hits[1].Path != "internal/db.go" {
		t.Errorf("hits[1].Path = %q, want %q", hits[1].Path, "internal/db.go")
	}
	if hits[1].Line != 200 {
		t.Errorf("hits[1].Line = %d, want %d", hits[1].Line, 200)
	}
	if hits[1].Score != 0.71 {
		t.Errorf("hits[1].Score = %f, want %f", hits[1].Score, 0.71)
	}
}

// ---------------------------------------------------------------------------
// TestCacheTTLExpiry
// ---------------------------------------------------------------------------

func TestCacheTTLExpiry(t *testing.T) {
	t.Cleanup(clearCache)
	clearCache()

	key := "ttl-test-key"
	evidence := []retrieval.RetrievalEvidence{
		{Source: "grep", Path: "a.go", Line: 1, Snippet: "hello"},
	}

	// Insert an entry via cachePut.
	cachePut(key, evidence)

	// Immediately it should be found.
	hits, ok := cacheGet(key)
	if !ok {
		t.Fatal("expected cache hit immediately after put")
	}
	if len(hits) != 1 || hits[0].Path != "a.go" {
		t.Fatalf("unexpected cached value: %+v", hits)
	}

	// Manually backdate the entry so that it appears to have expired.
	orchestratorCache.mu.Lock()
	entry := orchestratorCache.items[key]
	entry.createdAt = time.Now().Add(-(cacheTTL + time.Second))
	orchestratorCache.items[key] = entry
	orchestratorCache.mu.Unlock()

	// Now cacheGet should treat it as a miss.
	_, ok = cacheGet(key)
	if ok {
		t.Fatal("expected cache miss after TTL expiry, but got a hit")
	}
}

// ---------------------------------------------------------------------------
// TestCacheEviction
// ---------------------------------------------------------------------------

func TestCacheEviction(t *testing.T) {
	t.Cleanup(clearCache)
	clearCache()

	evidence := []retrieval.RetrievalEvidence{
		{Source: "grep", Path: "x.go", Line: 1, Snippet: "x"},
	}

	// Fill the cache to exactly cacheMaxEntries.
	for i := 0; i < cacheMaxEntries; i++ {
		cachePut(fmt.Sprintf("evict-key-%d", i), evidence)
	}

	// Verify the cache is full.
	orchestratorCache.mu.RLock()
	sizeBeforeOverflow := len(orchestratorCache.items)
	orchestratorCache.mu.RUnlock()

	if sizeBeforeOverflow != cacheMaxEntries {
		t.Fatalf("expected cache size %d, got %d", cacheMaxEntries, sizeBeforeOverflow)
	}

	// Insert one more — this must trigger eviction inside cachePut.
	cachePut("evict-key-overflow", evidence)

	orchestratorCache.mu.RLock()
	sizeAfterOverflow := len(orchestratorCache.items)
	orchestratorCache.mu.RUnlock()

	// After eviction the cache should be strictly smaller than
	// cacheMaxEntries + 1 (i.e. eviction actually happened).
	if sizeAfterOverflow > cacheMaxEntries {
		t.Fatalf("cache should not exceed cacheMaxEntries after eviction; size = %d", sizeAfterOverflow)
	}

	// The newly-inserted key should still be present.
	if _, ok := cacheGet("evict-key-overflow"); !ok {
		t.Fatal("newly inserted key should survive eviction")
	}

	// At least some of the old keys should have been removed.
	evictedCount := 0
	for i := 0; i < cacheMaxEntries; i++ {
		if _, ok := cacheGet(fmt.Sprintf("evict-key-%d", i)); !ok {
			evictedCount++
		}
	}
	if evictedCount == 0 {
		t.Fatal("expected at least some old entries to be evicted")
	}
}

// ---------------------------------------------------------------------------
// TestDedupeEvidence
// ---------------------------------------------------------------------------

func TestDedupeEvidence(t *testing.T) {
	input := []retrieval.RetrievalEvidence{
		{Source: "grep", Path: "a.go", Line: 1, Snippet: "hello"},
		{Source: "grep", Path: "a.go", Line: 1, Snippet: "hello"}, // duplicate
		{Source: "grep", Path: "a.go", Line: 2, Snippet: "world"}, // different line
		{Source: "rag", Path: "a.go", Line: 1, Snippet: "hello"},  // different source
		{Source: "grep", Path: "b.go", Line: 1, Snippet: "hello"}, // different path
	}

	result := dedupeEvidence(input)

	if len(result) != 4 {
		t.Fatalf("expected 4 unique entries, got %d", len(result))
	}

	// Build a set of dedup keys to verify exact contents.
	keys := make(map[string]bool, len(result))
	for _, r := range result {
		key := r.Source + "|" + r.Path + "|" + strconv.Itoa(r.Line) + "|" + r.Symbol + "|" + r.Snippet
		keys[key] = true
	}

	expected := []string{
		"grep|a.go|1||hello",
		"grep|a.go|2||world",
		"rag|a.go|1||hello",
		"grep|b.go|1||hello",
	}
	for _, ek := range expected {
		if !keys[ek] {
			t.Errorf("expected key %q not found in deduped results", ek)
		}
	}
}

// ---------------------------------------------------------------------------
// TestDefaultKinds
// ---------------------------------------------------------------------------

func TestDefaultKinds(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		targetFiles []string
		mode        string
		want        []string
	}{
		{
			name: "identifier mode without targets",
			mode: "identifier",
			want: []string{"grep"},
		},
		{
			name:        "identifier mode with targets",
			mode:        "identifier",
			targetFiles: []string{"main.go"},
			want:        []string{"grep", "lsp"},
		},
		{
			name:        "symbol mode without targets",
			mode:        "symbol",
			targetFiles: nil,
			want:        []string{"grep"},
		},
		{
			name:        "symbol mode with targets",
			mode:        "symbol",
			targetFiles: []string{"main.go"},
			want:        []string{"grep", "lsp"},
		},
		{
			name: "semantic mode",
			mode: "semantic",
			want: []string{"rag", "grep"},
		},
		{
			name:        "unspecified mode with target files",
			query:       "something",
			targetFiles: []string{"file.go"},
			mode:        "",
			want:        []string{"grep", "lsp"},
		},
		{
			name:  "identifier-like query (no spaces, alphanumeric)",
			query: "myFunction",
			mode:  "",
			want:  []string{"grep"},
		},
		{
			name:  "natural language query (has spaces)",
			query: "how does the server start",
			mode:  "",
			want:  []string{"grep", "rag"},
		},
		{
			name:  "dotted identifier",
			query: "pkg.FuncName",
			mode:  "",
			want:  []string{"grep"},
		},
		{
			name:  "path-like identifier",
			query: "internal/core/discovery",
			mode:  "",
			want:  []string{"grep"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultKinds(tt.query, tt.targetFiles, tt.mode)
			if len(got) != len(tt.want) {
				t.Fatalf("defaultKinds(%q, %v, %q) = %v, want %v",
					tt.query, tt.targetFiles, tt.mode, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("defaultKinds(%q, %v, %q)[%d] = %q, want %q",
						tt.query, tt.targetFiles, tt.mode, i, got[i], tt.want[i])
				}
			}
		})
	}
}
