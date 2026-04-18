package discovery

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corelsp "github.com/mingzhi1/coden/internal/core/lsp"
	"github.com/mingzhi1/coden/internal/core/retrieval"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"golang.org/x/sync/errgroup"
)

// SearchParams describes a minimal retrieval request for the discovery layer.
type SearchParams struct {
	Query       string
	Kinds       []string
	TargetFiles []string
	Mode        string
	DirtyPaths  []string
	Workspace   string // used to scope the cache key per workspace
}

// RefineParams describes an incremental discovery request that starts from an
// existing query/result set and adds more precise hints.
type RefineParams struct {
	Query       string
	QueryID     string
	Hints       []string
	TargetFiles []string
	Mode        string
	DirtyPaths  []string
	Workspace   string // used to scope the cache key per workspace
}

// ValidateParams holds the inputs for Orchestrator.Validate.
type ValidateParams struct {
	Paths     []string // file paths to verify exist in the workspace
	Workspace string   // workspace root for path resolution
}

// GetContextParams holds the inputs for Orchestrator.GetContext.
type GetContextParams struct {
	Task      string   // task description or goal
	QueryID   string   // optional: base query ID to extend
	Budget    int      // approximate token budget; 0 means a default of 4000
	Workspace string
	DirtyPaths []string
}

// Orchestrator coordinates retrieval across the available tool layers.
type Orchestrator interface {
	Search(ctx context.Context, params SearchParams) ([]retrieval.RetrievalEvidence, error)
	Refine(ctx context.Context, params RefineParams) ([]retrieval.RetrievalEvidence, error)
	// Validate checks whether each path exists in the workspace.  The returned
	// map has one entry per non-empty input path.
	Validate(ctx context.Context, params ValidateParams) (map[string]bool, error)
	// GetContext returns retrieval evidence scoped to a single task, capped to
	// approximately params.Budget tokens worth of content.
	GetContext(ctx context.Context, params GetContextParams) ([]retrieval.RetrievalEvidence, error)
}

// ToolOrchestrator is the minimal implementation backed by toolruntime.
// The first version is grep-first, with optional rag/lsp enrichment.
// cacheStore is an isolated, thread-safe evidence cache.
// Each ToolOrchestrator owns one; tests get per-instance caches to avoid
// interference with the global default.
type cacheStore struct {
	mu    sync.RWMutex
	items map[string]cacheEntry
}

func newCacheStore() *cacheStore {
	return &cacheStore{items: make(map[string]cacheEntry)}
}

type ToolOrchestrator struct {
	tools *toolruntime.Runtime
	cache *cacheStore // nil → falls back to sharedCache
}

const (
	cacheMaxEntries = 256
	cacheTTL        = 5 * time.Minute
)

type cacheEntry struct {
	hits      []retrieval.RetrievalEvidence
	createdAt time.Time
}

// sharedCache is the process-wide default used by production orchestrators.
// Tests that call newCacheStore() get their own isolated instance.
var sharedCache = newCacheStore()

// orchestratorCache is kept as an alias for DirtyTracker.cacheInvalidateByPath
// which targets the shared cache.
var orchestratorCache = sharedCache

func NewToolOrchestrator(tools *toolruntime.Runtime) *ToolOrchestrator {
	return &ToolOrchestrator{tools: tools, cache: sharedCache}
}

// newToolOrchestratorWithCache creates an orchestrator backed by an isolated
// cacheStore; used in tests to prevent parallel tests from evicting each other's
// entries.
func newToolOrchestratorWithCache(tools *toolruntime.Runtime, c *cacheStore) *ToolOrchestrator {
	return &ToolOrchestrator{tools: tools, cache: c}
}

func (o *ToolOrchestrator) cacheGet(key string) ([]retrieval.RetrievalEvidence, bool) {
	c := o.cache
	if c == nil {
		c = sharedCache
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.createdAt) > cacheTTL {
		return nil, false
	}
	return cloneEvidence(entry.hits), true
}

func (o *ToolOrchestrator) cachePut(key string, hits []retrieval.RetrievalEvidence) {
	c := o.cache
	if c == nil {
		c = sharedCache
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= cacheMaxEntries {
		cacheEvictLocked(c)
	}
	c.items[key] = cacheEntry{hits: cloneEvidence(hits), createdAt: time.Now()}
}

func (o *ToolOrchestrator) Search(ctx context.Context, params SearchParams) ([]retrieval.RetrievalEvidence, error) {
	if o == nil || o.tools == nil {
		return nil, nil
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, nil
	}

	kinds := params.Kinds
	if len(kinds) == 0 {
		kinds = defaultKinds(query, params.TargetFiles, params.Mode)
	}

	cacheKey := buildCacheKey(params.Workspace, query, params.Mode, kinds, params.TargetFiles, params.DirtyPaths)
	if hits, ok := o.cacheGet(cacheKey); ok {
		return hits, nil
	}

	// G1: Run each retrieval layer concurrently.
	type kindResult struct {
		hits []retrieval.RetrievalEvidence
	}
	resultsCh := make(chan kindResult, len(kinds))

	eg, egCtx := errgroup.WithContext(ctx)
	for _, kind := range kinds {
		kind := kind // capture
		eg.Go(func() error {
			var hits []retrieval.RetrievalEvidence
			switch kind {
			case "grep":
				res, err := o.tools.Execute(egCtx, toolruntime.Request{Kind: "search", Query: query})
				if err == nil {
					hits = parseSearchOutput(res.Output, query)
				}
			case "rag":
				res, err := o.tools.Execute(egCtx, toolruntime.Request{Kind: "rag_search", Query: query, TopK: 5})
				if err == nil {
					if evidence, ok := res.StructuredData.([]retrieval.RetrievalEvidence); ok && len(evidence) > 0 {
						hits = evidence
					} else {
						hits = parseRAGOutput(res.Output, query)
					}
				}
			case "lsp":
				for _, path := range params.TargetFiles {
					path = strings.TrimSpace(path)
					if path == "" {
						continue
					}
					if mgr, ok := o.tools.LSPManager(path).(*corelsp.Manager); ok && mgr != nil {
						symbols, err := mgr.DocumentSymbol(egCtx, path)
						if err == nil {
							hits = append(hits, localSymbolsToEvidence(symbols, path)...)
							continue
						}
					}
					res, err := o.tools.Execute(egCtx, toolruntime.Request{Kind: "lsp_symbols", Path: path})
					if err == nil {
						hits = append(hits, retrieval.RetrievalEvidence{
							Source:      "lsp",
							Path:        path,
							Snippet:     strings.TrimSpace(res.Output),
							Verified:    true,
							Explanation: res.Summary,
						})
					}
				}
			}
			resultsCh <- kindResult{hits: hits}
			return nil
		})
	}
	// Wait for all goroutines; close channel when done.
	go func() {
		_ = eg.Wait()
		close(resultsCh)
	}()

	var out []retrieval.RetrievalEvidence
	for r := range resultsCh {
		out = append(out, r.hits...)
	}

	out = dedupeEvidence(out)
	o.cachePut(cacheKey, out)
	return out, nil
}

func (o *ToolOrchestrator) Refine(ctx context.Context, params RefineParams) ([]retrieval.RetrievalEvidence, error) {
	query := strings.TrimSpace(params.Query)
	if len(params.Hints) > 0 {
		joinedHints := strings.TrimSpace(strings.Join(params.Hints, " "))
		if joinedHints != "" {
			if query != "" {
				query += " "
			}
			query += joinedHints
		}
	}
	return o.Search(ctx, SearchParams{
		Query:       query,
		Kinds:       defaultKinds(query, params.TargetFiles, params.Mode),
		TargetFiles: params.TargetFiles,
		Mode:        params.Mode,
		DirtyPaths:  params.DirtyPaths,
		Workspace:   params.Workspace,
	})
}

// Validate checks whether each requested path exists.
// It uses the lsp_didopen tool to ask the LSP server (best-effort) and
// falls back to a grep probe when LSP is unavailable.
func (o *ToolOrchestrator) Validate(ctx context.Context, params ValidateParams) (map[string]bool, error) {
	result := make(map[string]bool, len(params.Paths))
	for _, path := range params.Paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		// Use read_file as a lightweight existence probe.
		res, err := o.tools.Execute(ctx, toolruntime.Request{Kind: "read_file", Path: path})
		if err == nil && strings.TrimSpace(res.Output) != "" {
			result[path] = true
		} else {
			result[path] = false
		}
	}
	return result, nil
}

// GetContext returns retrieval evidence for a specific task description,
// capped to approximately params.Budget tokens of content.
// It always runs grep; it skips heavier layers (rag/lsp) when the budget is tight.
func (o *ToolOrchestrator) GetContext(ctx context.Context, params GetContextParams) ([]retrieval.RetrievalEvidence, error) {
	if o == nil || o.tools == nil {
		return nil, nil
	}
	budget := params.Budget
	if budget <= 0 {
		budget = 4000
	}
	task := strings.TrimSpace(params.Task)
	if task == "" {
		return nil, nil
	}

	// Select retrieval kinds based on budget:
	//   tight (≤2000):  grep only
	//   medium (≤8000): grep + rag
	//   generous:       grep + rag + lsp
	var kinds []string
	switch {
	case budget <= 2000:
		kinds = []string{"grep"}
	case budget <= 8000:
		kinds = []string{"grep", "rag"}
	default:
		kinds = []string{"grep", "rag", "lsp"}
	}

	hits, err := o.Search(ctx, SearchParams{
		Query:      task,
		Kinds:      kinds,
		Mode:       "mixed",
		DirtyPaths: params.DirtyPaths,
		Workspace:  params.Workspace,
	})
	if err != nil {
		return nil, err
	}
	return capEvidenceByBudget(hits, budget), nil
}

// capEvidenceByBudget trims the evidence list so the total snippet length
// stays within the rough byte-to-token approximation of budget*4 bytes.
func capEvidenceByBudget(hits []retrieval.RetrievalEvidence, budget int) []retrieval.RetrievalEvidence {
	maxBytes := budget * 4 // rough 1 token ≈ 4 bytes
	used := 0
	out := make([]retrieval.RetrievalEvidence, 0, len(hits))
	for _, h := range hits {
		sz := len(h.Snippet)
		if used+sz > maxBytes {
			break
		}
		used += sz
		out = append(out, h)
	}
	return out
}

func defaultKinds(query string, targetFiles []string, mode string) []string {
	query = strings.TrimSpace(query)
	mode = strings.TrimSpace(strings.ToLower(mode))
	switch {
	case mode == "identifier" || mode == "symbol":
		if len(targetFiles) > 0 {
			return []string{"grep", "lsp"}
		}
		return []string{"grep"}
	case mode == "semantic":
		return []string{"rag", "grep"}
	case len(targetFiles) > 0:
		return []string{"grep", "lsp"}
	case looksLikeIdentifier(query):
		return []string{"grep"}
	default:
		return []string{"grep", "rag"}
	}
}

func looksLikeIdentifier(query string) bool {
	query = strings.TrimSpace(query)
	if query == "" || strings.Contains(query, " ") {
		return false
	}
	for _, r := range query {
		if !(r == '_' || r == '-' || r == '.' || r == '/' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
	}
	return true
}

func parseSearchOutput(output, query string) []retrieval.RetrievalEvidence {
	lines := strings.Split(output, "\n")
	out := make([]retrieval.RetrievalEvidence, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		lineNo, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		out = append(out, retrieval.RetrievalEvidence{
			Source:      "grep",
			Path:        strings.TrimSpace(parts[0]),
			Line:        lineNo,
			Snippet:     strings.TrimSpace(parts[2]),
			Explanation: "Text match for '" + query + "'",
		})
	}
	return out
}

var ragHeaderPattern = regexp.MustCompile(`^\d+\.\s+(.+?):(\d+)\s+\(score:\s+([0-9.]+)\)$`)

func parseRAGOutput(output, query string) []retrieval.RetrievalEvidence {
	output = strings.TrimSpace(output)
	if output == "" || output == "No relevant chunks found." {
		return nil
	}

	lines := strings.Split(output, "\n")
	out := make([]retrieval.RetrievalEvidence, 0)
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		m := ragHeaderPattern.FindStringSubmatch(line)
		if len(m) != 4 {
			continue
		}
		path := strings.TrimSpace(m[1])
		lineNo, _ := strconv.Atoi(strings.TrimSpace(m[2]))
		score, _ := strconv.ParseFloat(strings.TrimSpace(m[3]), 64)

		snippet := ""
		if i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if next != "" && !ragHeaderPattern.MatchString(next) {
				snippet = strings.TrimSpace(strings.TrimPrefix(next, "   "))
				i++
			}
		}

		out = append(out, retrieval.RetrievalEvidence{
			Source:      "rag",
			Path:        path,
			Line:        lineNo,
			Snippet:     snippet,
			Score:       score,
			Verified:    false,
			Explanation: "Semantic match for '" + query + "'",
		})
	}
	return out
}

func localSymbolsToEvidence(symbols []corelsp.DocumentSymbol, path string) []retrieval.RetrievalEvidence {
	out := make([]retrieval.RetrievalEvidence, 0)
	for _, sym := range symbols {
		out = append(out, retrieval.RetrievalEvidence{
			Source:      "lsp",
			Path:        path,
			Line:        sym.Range.Start.Line + 1,
			Symbol:      sym.Name,
			Snippet:     strings.TrimSpace(sym.Detail),
			Verified:    true,
			Explanation: "LSP symbol: " + sym.Name,
		})
		if len(sym.Children) > 0 {
			out = append(out, localSymbolsToEvidence(sym.Children, path)...)
		}
	}
	return out
}

func dedupeEvidence(in []retrieval.RetrievalEvidence) []retrieval.RetrievalEvidence {
	seen := make(map[string]bool, len(in))
	out := make([]retrieval.RetrievalEvidence, 0, len(in))
	for _, hit := range in {
		key := hit.Source + "|" + hit.Path + "|" + strconv.Itoa(hit.Line) + "|" + hit.Symbol + "|" + hit.Snippet
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, hit)
	}
	return out
}

func buildCacheKey(workspace, query, mode string, kinds, targetFiles, dirtyPaths []string) string {
	return strings.TrimSpace(workspace) + "|" + strings.TrimSpace(query) + "|" + strings.TrimSpace(strings.ToLower(mode)) + "|" + strings.Join(kinds, ",") + "|" + strings.Join(targetFiles, ",") + "|dirty=" + strings.Join(dirtyPaths, ",")
}

// cacheGet and cachePut are package-level helpers that operate on sharedCache.
// They are used by internal tests that directly verify cache mechanics.
func cacheGet(key string) ([]retrieval.RetrievalEvidence, bool) {
	sharedCache.mu.RLock()
	defer sharedCache.mu.RUnlock()
	entry, ok := sharedCache.items[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.createdAt) > cacheTTL {
		return nil, false
	}
	return cloneEvidence(entry.hits), true
}

func cachePut(key string, hits []retrieval.RetrievalEvidence) {
	sharedCache.mu.Lock()
	defer sharedCache.mu.Unlock()
	if len(sharedCache.items) >= cacheMaxEntries {
		cacheEvictLocked(sharedCache)
	}
	sharedCache.items[key] = cacheEntry{hits: cloneEvidence(hits), createdAt: time.Now()}
}

// cacheEvictLocked removes expired entries or the oldest half when full.
// Caller must hold c.mu write lock.
func cacheEvictLocked(c *cacheStore) {
	now := time.Now()
	for k, v := range c.items {
		if now.Sub(v.createdAt) > cacheTTL {
			delete(c.items, k)
		}
	}
	if len(c.items) >= cacheMaxEntries {
		type kv struct {
			key string
			ts  time.Time
		}
		entries := make([]kv, 0, len(c.items))
		for k, v := range c.items {
			entries = append(entries, kv{k, v.createdAt})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].ts.Before(entries[j].ts) })
		removeCount := len(entries) / 2
		for i := 0; i < removeCount; i++ {
			delete(c.items, entries[i].key)
		}
	}
}

// cacheInvalidateByPath evicts all entries in the shared production cache whose
// key contains path.  Called by DirtyTracker.MarkDirty.
func cacheInvalidateByPath(path string) {
	if path == "" {
		return
	}
	sharedCache.mu.Lock()
	defer sharedCache.mu.Unlock()
	for k := range sharedCache.items {
		if strings.Contains(k, path) {
			delete(sharedCache.items, k)
		}
	}
}

func cloneEvidence(in []retrieval.RetrievalEvidence) []retrieval.RetrievalEvidence {
	if len(in) == 0 {
		return nil
	}
	out := make([]retrieval.RetrievalEvidence, len(in))
	copy(out, in)
	return out
}
