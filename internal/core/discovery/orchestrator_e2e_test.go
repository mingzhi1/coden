package discovery

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/retrieval"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// ---------------------------------------------------------------------------
// concurrentMockExecutor: records parallel call counts
// ---------------------------------------------------------------------------

// concurrentMockExecutor records peak concurrency and total calls per kind.
type concurrentMockExecutor struct {
	mu          sync.Mutex
	results     map[string]toolruntime.Result
	errors      map[string]error
	callsPerKind map[string]int
	delay       map[string]time.Duration // artificial delay per kind

	activeMu   sync.Mutex
	active     int32
	peakActive int32
}

func newConcurrentMock(results map[string]toolruntime.Result) *concurrentMockExecutor {
	return &concurrentMockExecutor{
		results:      results,
		callsPerKind: make(map[string]int),
		delay:        make(map[string]time.Duration),
	}
}

func (m *concurrentMockExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	// Track concurrency.
	cur := atomic.AddInt32(&m.active, 1)
	for {
		peak := atomic.LoadInt32(&m.peakActive)
		if cur <= peak || atomic.CompareAndSwapInt32(&m.peakActive, peak, cur) {
			break
		}
	}

	m.mu.Lock()
	m.callsPerKind[req.Kind]++
	delay := m.delay[req.Kind]
	m.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	atomic.AddInt32(&m.active, -1)

	if m.errors != nil {
		if err, ok := m.errors[req.Kind]; ok {
			return toolruntime.Result{}, err
		}
	}
	if res, ok := m.results[req.Kind]; ok {
		return res, nil
	}
	return toolruntime.Result{}, nil
}

// ---------------------------------------------------------------------------
// G1: parallel retrieval
// ---------------------------------------------------------------------------

func TestG1_SearchRunsKindsInParallel(t *testing.T) {
	t.Parallel()

	// All three layers get a 20ms delay so that, if serial, total ≥ 60ms;
	// if parallel, total ≈ 20ms.
	delay := 20 * time.Millisecond
	exec := newConcurrentMock(map[string]toolruntime.Result{
		"search":     {Output: "a.go:1: hello"},
		"rag_search": {Output: ""},
	})
	exec.delay["search"] = delay
	exec.delay["rag_search"] = delay

	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))
	start := time.Now()
	_, err := orch.Search(context.Background(), SearchParams{
		Query: "hello",
		Kinds: []string{"grep", "rag"},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// If parallel, elapsed should be close to one delay, not two.
	if elapsed > 3*delay {
		t.Errorf("Search took %v, expected < %v — looks serial, not parallel", elapsed, 3*delay)
	}
}

func TestG1_SearchAllKindsExecuted(t *testing.T) {
	t.Parallel()

	structured := []retrieval.RetrievalEvidence{{Source: "rag", Path: "r.go", Line: 1, Snippet: "rag"}}
	exec := newConcurrentMock(map[string]toolruntime.Result{
		"search":     {Output: "g.go:1: grep hit"},
		"rag_search": {StructuredData: structured},
	})

	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))
	hits, err := orch.Search(context.Background(), SearchParams{
		Query: "something",
		Kinds: []string{"grep", "rag"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect evidence from both grep and rag.
	sources := make(map[string]bool)
	for _, h := range hits {
		sources[h.Source] = true
	}
	if !sources["grep"] {
		t.Error("expected grep results")
	}
	if !sources["rag"] {
		t.Error("expected rag results")
	}
}

func TestG1_SearchLayerErrorDoesNotAbort(t *testing.T) {
	t.Parallel()

	exec := &mockExecutor{
		results: map[string]toolruntime.Result{
			"search": {Output: "ok.go:5: func OK() {}"},
		},
		errors: map[string]error{
			"rag_search": context.DeadlineExceeded,
		},
	}

	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))
	hits, err := orch.Search(context.Background(), SearchParams{
		Query: "OK",
		Kinds: []string{"grep", "rag"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) == 0 {
		t.Error("expected at least grep hits even when rag layer errors")
	}
	for _, h := range hits {
		if h.Source != "grep" {
			t.Errorf("unexpected source %q from erroring layer", h.Source)
		}
	}
}

// ---------------------------------------------------------------------------
// G2: cache workspace isolation
// ---------------------------------------------------------------------------

func TestG2_CacheIsolatedByWorkspace(t *testing.T) {
	t.Parallel()

	var callCount int32
	exec := &mockExecutor{
		results: map[string]toolruntime.Result{
			"search": {Output: "main.go:1: func main() {}"},
		},
	}

	// Override Execute to count calls.
	countingExec := &countingSearchExecutor{inner: exec, count: &callCount}
	orch, _ := newIsolatedOrchestrator(newMockRuntime(countingExec))

	ctx := context.Background()

	// First call: workspace A.
	_, err := orch.Search(ctx, SearchParams{
		Query:     "main",
		Kinds:     []string{"grep"},
		Workspace: "/ws/a",
	})
	if err != nil {
		t.Fatalf("ws-a search failed: %v", err)
	}

	// Second call: same query, workspace B — must NOT hit cache.
	_, err = orch.Search(ctx, SearchParams{
		Query:     "main",
		Kinds:     []string{"grep"},
		Workspace: "/ws/b",
	})
	if err != nil {
		t.Fatalf("ws-b search failed: %v", err)
	}

	// Third call: workspace A again — MUST hit cache (no extra executor call).
	_, err = orch.Search(ctx, SearchParams{
		Query:     "main",
		Kinds:     []string{"grep"},
		Workspace: "/ws/a",
	})
	if err != nil {
		t.Fatalf("ws-a second search failed: %v", err)
	}

	got := atomic.LoadInt32(&callCount)
	if got != 2 {
		t.Errorf("expected 2 executor calls (ws-a miss + ws-b miss), got %d", got)
	}
}

// countingSearchExecutor wraps an executor and increments a counter on every search call.
type countingSearchExecutor struct {
	inner toolruntime.Executor
	count *int32
}

func (e *countingSearchExecutor) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	if req.Kind == "search" {
		atomic.AddInt32(e.count, 1)
	}
	return e.inner.Execute(ctx, req)
}

// ---------------------------------------------------------------------------
// G3: Validate
// ---------------------------------------------------------------------------

func TestG3_ValidateExistingPath(t *testing.T) {
	t.Parallel()

	exec := &mockExecutor{
		results: map[string]toolruntime.Result{
			"read_file": {Output: "package main\nfunc main() {}"},
		},
	}
	orch := NewToolOrchestrator(newMockRuntime(exec))

	result, err := orch.Validate(context.Background(), ValidateParams{
		Paths: []string{"main.go"},
	})
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if !result["main.go"] {
		t.Error("expected main.go to be valid (exists)")
	}
}

func TestG3_ValidateMissingPath(t *testing.T) {
	t.Parallel()

	exec := &mockExecutor{
		// read_file returns empty output for missing files.
		results: map[string]toolruntime.Result{
			"read_file": {Output: ""},
		},
	}
	orch := NewToolOrchestrator(newMockRuntime(exec))

	result, err := orch.Validate(context.Background(), ValidateParams{
		Paths: []string{"nonexistent.go"},
	})
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if result["nonexistent.go"] {
		t.Error("expected nonexistent.go to be invalid (missing)")
	}
}

func TestG3_ValidateEmptyPathsIgnored(t *testing.T) {
	t.Parallel()

	exec := &mockExecutor{}
	orch := NewToolOrchestrator(newMockRuntime(exec))

	result, err := orch.Validate(context.Background(), ValidateParams{
		Paths: []string{"", "  ", ""},
	})
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result map for blank paths, got %v", result)
	}
}

func TestG3_ValidateMultiplePaths(t *testing.T) {
	t.Parallel()

	var muCalls sync.Mutex
	readCalls := map[string]int{}

	exec := &funcExecutor{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		if req.Kind == "read_file" {
			muCalls.Lock()
			readCalls[req.Path]++
			muCalls.Unlock()
			if req.Path == "exists.go" {
				return toolruntime.Result{Output: "content"}, nil
			}
		}
		return toolruntime.Result{Output: ""}, nil
	}}
	orch := NewToolOrchestrator(newMockRuntime(exec))

	result, err := orch.Validate(context.Background(), ValidateParams{
		Paths: []string{"exists.go", "missing.go", "also_missing.go"},
	})
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if !result["exists.go"] {
		t.Error("exists.go should be valid")
	}
	if result["missing.go"] {
		t.Error("missing.go should be invalid")
	}
	if result["also_missing.go"] {
		t.Error("also_missing.go should be invalid")
	}
	if len(result) != 3 {
		t.Errorf("expected 3 results, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// G3: GetContext
// ---------------------------------------------------------------------------

func TestG3_GetContextTightBudgetOnlyGrep(t *testing.T) {
	t.Parallel()

	exec := newConcurrentMock(map[string]toolruntime.Result{
		"search":     {Output: "auth.go:10: func Auth() {}"},
		"rag_search": {Output: "1. auth.go:10 (score: 0.9)\n   func Auth() {}"},
	})
	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))

	_, err := orch.GetContext(context.Background(), GetContextParams{
		Task:   "implement auth",
		Budget: 500, // tight → grep only
	})
	if err != nil {
		t.Fatalf("GetContext failed: %v", err)
	}

	exec.mu.Lock()
	grepCalls := exec.callsPerKind["search"]
	ragCalls := exec.callsPerKind["rag_search"]
	exec.mu.Unlock()

	if grepCalls == 0 {
		t.Error("expected grep to be called with tight budget")
	}
	if ragCalls > 0 {
		t.Error("expected RAG to be skipped with tight budget")
	}
}

func TestG3_GetContextMediumBudgetGrepAndRag(t *testing.T) {
	t.Parallel()

	structured := []retrieval.RetrievalEvidence{{Source: "rag", Path: "r.go", Line: 1, Snippet: "rag result"}}
	exec := newConcurrentMock(map[string]toolruntime.Result{
		"search":     {Output: "g.go:1: grep result"},
		"rag_search": {StructuredData: structured},
	})
	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))

	hits, err := orch.GetContext(context.Background(), GetContextParams{
		Task:   "find server code",
		Budget: 4000, // medium → grep + rag
	})
	if err != nil {
		t.Fatalf("GetContext failed: %v", err)
	}

	exec.mu.Lock()
	grepCalls := exec.callsPerKind["search"]
	ragCalls := exec.callsPerKind["rag_search"]
	exec.mu.Unlock()

	if grepCalls == 0 {
		t.Error("expected grep to be called with medium budget")
	}
	if ragCalls == 0 {
		t.Error("expected RAG to be called with medium budget")
	}

	sources := map[string]bool{}
	for _, h := range hits {
		sources[h.Source] = true
	}
	if !sources["grep"] {
		t.Error("expected grep evidence in results")
	}
	if !sources["rag"] {
		t.Error("expected rag evidence in results")
	}
}

func TestG3_GetContextDefaultBudgetUsedWhenZero(t *testing.T) {
	t.Parallel()

	exec := newConcurrentMock(map[string]toolruntime.Result{
		"search": {Output: "a.go:1: result"},
	})
	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))

	// Budget 0 → should use default (4000), which triggers grep+rag.
	_, err := orch.GetContext(context.Background(), GetContextParams{
		Task:   "find something",
		Budget: 0,
	})
	if err != nil {
		t.Fatalf("GetContext with Budget=0 failed: %v", err)
	}

	exec.mu.Lock()
	grepCalls := exec.callsPerKind["search"]
	exec.mu.Unlock()

	if grepCalls == 0 {
		t.Error("expected grep to run when Budget=0 (falls back to default 4000)")
	}
}

func TestG3_GetContextEmptyTaskReturnsNil(t *testing.T) {
	t.Parallel()

	exec := &mockExecutor{}
	orch := NewToolOrchestrator(newMockRuntime(exec))

	hits, err := orch.GetContext(context.Background(), GetContextParams{
		Task:   "   ",
		Budget: 4000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected nil hits for empty task, got %v", hits)
	}
}

func TestG3_GetContextBudgetCapsResults(t *testing.T) {
	t.Parallel()

	// Build large snippets so budget capping is exercised.
	bigSnippet := strings.Repeat("x", 500)
	grepOutput := ""
	for i := 0; i < 20; i++ {
		grepOutput += "file.go:" + string(rune('0'+i)) + ": " + bigSnippet + "\n"
	}

	exec := &mockExecutor{
		results: map[string]toolruntime.Result{
			"search": {Output: grepOutput},
		},
	}
	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))

	// Budget of 1000 tokens × 4 bytes = 4000 bytes cap.
	hits, err := orch.GetContext(context.Background(), GetContextParams{
		Task:   "search for large content",
		Budget: 1000,
	})
	if err != nil {
		t.Fatalf("GetContext failed: %v", err)
	}

	totalBytes := 0
	for _, h := range hits {
		totalBytes += len(h.Snippet)
	}
	maxBytes := 1000 * 4
	if totalBytes > maxBytes {
		t.Errorf("total snippet bytes %d exceeded budget cap %d", totalBytes, maxBytes)
	}
}

// ---------------------------------------------------------------------------
// Refine: hints are appended to query
// ---------------------------------------------------------------------------

func TestRefineAppendsHintsToQuery(t *testing.T) {
	t.Parallel()

	var capturedQuery string
	exec := &funcExecutor{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		if req.Kind == "search" {
			capturedQuery = req.Query
		}
		return toolruntime.Result{Output: "a.go:1: result"}, nil
	}}
	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))

	_, err := orch.Refine(context.Background(), RefineParams{
		Query:     "original query",
		Hints:     []string{"extra hint", "another hint"},
		Workspace: "/ws",
	})
	if err != nil {
		t.Fatalf("Refine failed: %v", err)
	}

	if !strings.Contains(capturedQuery, "original query") {
		t.Errorf("refined query %q should contain original query", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "extra hint") {
		t.Errorf("refined query %q should contain hints", capturedQuery)
	}
}

func TestRefineWithNoHintsUsesOriginalQuery(t *testing.T) {
	t.Parallel()

	var capturedQuery string
	exec := &funcExecutor{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		if req.Kind == "search" {
			capturedQuery = req.Query
		}
		return toolruntime.Result{Output: ""}, nil
	}}
	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))

	_, err := orch.Refine(context.Background(), RefineParams{
		Query: "base query",
		Hints: nil,
	})
	if err != nil {
		t.Fatalf("Refine failed: %v", err)
	}

	if capturedQuery != "base query" {
		t.Errorf("expected query %q, got %q", "base query", capturedQuery)
	}
}

// ---------------------------------------------------------------------------
// Cache: hit returns same data without re-executing
// ---------------------------------------------------------------------------

func TestCacheHitSkipsExecutor(t *testing.T) {
	t.Parallel()

	var calls int32
	exec := &funcExecutor{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		if req.Kind == "search" {
			atomic.AddInt32(&calls, 1)
		}
		return toolruntime.Result{Output: "a.go:1: hit"}, nil
	}}
	orch, _ := newIsolatedOrchestrator(newMockRuntime(exec))

	params := SearchParams{
		Query:     "cache-test-query",
		Kinds:     []string{"grep"},
		Workspace: "/ws/cache",
	}
	// First call: cache miss.
	hits1, err := orch.Search(context.Background(), params)
	if err != nil {
		t.Fatalf("first search failed: %v", err)
	}
	// Second call: should cache-hit.
	hits2, err := orch.Search(context.Background(), params)
	if err != nil {
		t.Fatalf("second search failed: %v", err)
	}

	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 executor call, got %d", calls)
	}
	if len(hits1) != len(hits2) {
		t.Errorf("cached result mismatch: first=%d second=%d", len(hits1), len(hits2))
	}
}

func TestCacheMissAfterTTLExpiry(t *testing.T) {
	t.Parallel()

	var calls int32
	exec := &funcExecutor{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		if req.Kind == "search" {
			atomic.AddInt32(&calls, 1)
		}
		return toolruntime.Result{Output: "x.go:1: item"}, nil
	}}
	orch, c := newIsolatedOrchestrator(newMockRuntime(exec))

	params := SearchParams{
		Query:     "ttl-miss-query",
		Kinds:     []string{"grep"},
		Workspace: "/ws/ttl",
	}
	_, _ = orch.Search(context.Background(), params)

	// Expire the entry directly in the isolated cacheStore.
	cacheKey := buildCacheKey(params.Workspace, params.Query, params.Mode, params.Kinds, params.TargetFiles, params.DirtyPaths)
	c.mu.Lock()
	if e, ok := c.items[cacheKey]; ok {
		e.createdAt = time.Now().Add(-(cacheTTL + time.Second))
		c.items[cacheKey] = e
	}
	c.mu.Unlock()

	_, _ = orch.Search(context.Background(), params)

	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 executor calls after TTL expiry, got %d", calls)
	}
}

// ---------------------------------------------------------------------------
// Nil orchestrator safety
// ---------------------------------------------------------------------------

func TestNilOrchestratorSafeSearch(t *testing.T) {
	t.Parallel()
	var o *ToolOrchestrator
	hits, err := o.Search(context.Background(), SearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error from nil orchestrator: %v", err)
	}
	if hits != nil {
		t.Errorf("expected nil hits from nil orchestrator")
	}
}

func TestNilOrchestratorSafeGetContext(t *testing.T) {
	t.Parallel()
	var o *ToolOrchestrator
	hits, err := o.GetContext(context.Background(), GetContextParams{Task: "test", Budget: 1000})
	if err != nil {
		t.Fatalf("unexpected error from nil orchestrator: %v", err)
	}
	if hits != nil {
		t.Errorf("expected nil from nil orchestrator")
	}
}

// ---------------------------------------------------------------------------
// BuildCacheKey workspace prefix isolation
// ---------------------------------------------------------------------------

func TestBuildCacheKeyWorkspacePrefix(t *testing.T) {
	t.Parallel()
	key1 := buildCacheKey("/ws/a", "query", "grep", []string{"grep"}, nil, nil)
	key2 := buildCacheKey("/ws/b", "query", "grep", []string{"grep"}, nil, nil)
	key3 := buildCacheKey("/ws/a", "query", "grep", []string{"grep"}, nil, nil)

	if key1 == key2 {
		t.Error("different workspaces must produce different cache keys")
	}
	if key1 != key3 {
		t.Error("same workspace and params must produce identical cache key")
	}
}

// ---------------------------------------------------------------------------
// funcExecutor: general-purpose executor backed by a function
// ---------------------------------------------------------------------------

type funcExecutor struct {
	fn func(context.Context, toolruntime.Request) (toolruntime.Result, error)
}

func (f *funcExecutor) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	return f.fn(ctx, req)
}
