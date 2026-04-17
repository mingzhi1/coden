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
)

// SearchParams describes a minimal retrieval request for the discovery layer.
type SearchParams struct {
	Query       string
	Kinds       []string
	TargetFiles []string
	Mode        string
	DirtyPaths  []string
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
}

// Orchestrator coordinates retrieval across the available tool layers.
type Orchestrator interface {
	Search(ctx context.Context, params SearchParams) ([]retrieval.RetrievalEvidence, error)
	Refine(ctx context.Context, params RefineParams) ([]retrieval.RetrievalEvidence, error)
}

// ToolOrchestrator is the minimal implementation backed by toolruntime.
// The first version is grep-first, with optional rag/lsp enrichment.
type ToolOrchestrator struct {
	tools *toolruntime.Runtime
}

const (
	cacheMaxEntries = 256
	cacheTTL        = 5 * time.Minute
)

type cacheEntry struct {
	hits      []retrieval.RetrievalEvidence
	createdAt time.Time
}

var orchestratorCache = struct {
	mu    sync.RWMutex
	items map[string]cacheEntry
}{items: make(map[string]cacheEntry)}

func NewToolOrchestrator(tools *toolruntime.Runtime) *ToolOrchestrator {
	return &ToolOrchestrator{tools: tools}
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

	cacheKey := buildCacheKey(query, params.Mode, kinds, params.TargetFiles, params.DirtyPaths)
	if hits, ok := cacheGet(cacheKey); ok {
		return hits, nil
	}

	var out []retrieval.RetrievalEvidence
	for _, kind := range kinds {
		switch kind {
		case "grep":
			res, err := o.tools.Execute(ctx, toolruntime.Request{Kind: "search", Query: query})
			if err != nil {
				continue
			}
			out = append(out, parseSearchOutput(res.Output, query)...)
		case "rag":
			res, err := o.tools.Execute(ctx, toolruntime.Request{Kind: "rag_search", Query: query, TopK: 5})
			if err != nil {
				continue
			}
			// Prefer structured data over text parsing when available.
			if evidence, ok := res.StructuredData.([]retrieval.RetrievalEvidence); ok && len(evidence) > 0 {
				out = append(out, evidence...)
			} else {
				out = append(out, parseRAGOutput(res.Output, query)...)
			}
		case "lsp":
			for _, path := range params.TargetFiles {
				path = strings.TrimSpace(path)
				if path == "" {
					continue
				}
				if mgr, ok := o.tools.LSPManager(path).(*corelsp.Manager); ok && mgr != nil {
					symbols, err := mgr.DocumentSymbol(ctx, path)
					if err == nil {
						lspEvidence := localSymbolsToEvidence(symbols, path)
						out = append(out, lspEvidence...)
						continue
					}
				}
				res, err := o.tools.Execute(ctx, toolruntime.Request{Kind: "lsp_symbols", Path: path})
				if err != nil {
					continue
				}
				out = append(out, retrieval.RetrievalEvidence{
					Source:      "lsp",
					Path:        path,
					Snippet:     strings.TrimSpace(res.Output),
					Verified:    true,
					Explanation: res.Summary,
				})
			}
		}
	}

	out = dedupeEvidence(out)
	cachePut(cacheKey, out)
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
	})
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

func buildCacheKey(query, mode string, kinds, targetFiles, dirtyPaths []string) string {
	return strings.TrimSpace(query) + "|" + strings.TrimSpace(strings.ToLower(mode)) + "|" + strings.Join(kinds, ",") + "|" + strings.Join(targetFiles, ",") + "|dirty=" + strings.Join(dirtyPaths, ",")
}

func cacheGet(key string) ([]retrieval.RetrievalEvidence, bool) {
	orchestratorCache.mu.RLock()
	defer orchestratorCache.mu.RUnlock()
	entry, ok := orchestratorCache.items[key]
	if !ok {
		return nil, false
	}
	// TTL check — expired entries are treated as cache misses.
	if time.Since(entry.createdAt) > cacheTTL {
		return nil, false
	}
	return cloneEvidence(entry.hits), true
}

func cachePut(key string, hits []retrieval.RetrievalEvidence) {
	orchestratorCache.mu.Lock()
	defer orchestratorCache.mu.Unlock()
	// Evict when the cache is full.
	if len(orchestratorCache.items) >= cacheMaxEntries {
		cacheEvictLocked()
	}
	orchestratorCache.items[key] = cacheEntry{hits: cloneEvidence(hits), createdAt: time.Now()}
}

// cacheEvictLocked removes expired entries or the oldest half when full.
// Caller must hold orchestratorCache.mu write lock.
func cacheEvictLocked() {
	now := time.Now()
	// First pass: remove expired entries.
	for k, v := range orchestratorCache.items {
		if now.Sub(v.createdAt) > cacheTTL {
			delete(orchestratorCache.items, k)
		}
	}
	// If still over limit, remove oldest half.
	if len(orchestratorCache.items) >= cacheMaxEntries {
		type kv struct {
			key string
			ts  time.Time
		}
		entries := make([]kv, 0, len(orchestratorCache.items))
		for k, v := range orchestratorCache.items {
			entries = append(entries, kv{k, v.createdAt})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].ts.Before(entries[j].ts) })
		removeCount := len(entries) / 2
		for i := 0; i < removeCount; i++ {
			delete(orchestratorCache.items, entries[i].key)
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
