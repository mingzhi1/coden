package toolruntime

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/core/rag"
	"github.com/mingzhi1/coden/internal/core/retrieval"
)

// RAGTool implements Executor for RAG operations.
type RAGTool struct {
	index *rag.Index
}

// NewRAGTool creates a new RAG tool that wraps the RAG index.
func NewRAGTool(index *rag.Index) *RAGTool {
	return &RAGTool{
		index: index,
	}
}

// Execute implements Executor.
func (t *RAGTool) Execute(ctx context.Context, req Request) (Result, error) {
	switch req.Kind {
	case "rag_search":
		return t.executeSearch(ctx, req)
	case "rag_index_build":
		return t.executeIndexBuild(ctx, req)
	case "rag_index_update":
		return t.executeIndexUpdate(ctx, req)
	default:
		return Result{}, fmt.Errorf("unsupported RAG tool kind: %s", req.Kind)
	}
}

func (t *RAGTool) executeSearch(ctx context.Context, req Request) (Result, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = strings.TrimSpace(req.Content)
	}
	if query == "" {
		return Result{}, fmt.Errorf("rag_search: query is required")
	}

	// Set default top_k
	topK := req.TopK
	config := t.index.Config()
	if topK <= 0 {
		topK = config.DefaultTopK
	}
	if topK > config.MaxTopK {
		topK = config.MaxTopK
	}

	// Perform BM25 search
	results, err := t.index.Search(query, topK)
	if err != nil {
		return Result{}, err
	}

	// Filter by minimum score
	var filteredResults []rag.ScoredChunk
	for _, r := range results {
		if r.Score >= config.MinScore {
			filteredResults = append(filteredResults, r)
		}
	}

	// Format results
	output := formatRAGResults(filteredResults)

	// Build structured evidence so callers can skip text re-parsing.
	evidence := make([]retrieval.RetrievalEvidence, 0, len(filteredResults))
	for _, r := range filteredResults {
		evidence = append(evidence, retrieval.RetrievalEvidence{
			Source:      "rag",
			Path:        r.Chunk.Path,
			Line:        r.Chunk.StartLine,
			Snippet:     truncateString(r.Chunk.Content, 200),
			Score:       r.Score,
			Verified:    false,
			Explanation: fmt.Sprintf("Semantic match for %q", query),
		})
	}

	return Result{
		Summary:        fmt.Sprintf("found %d relevant chunks for %q", len(filteredResults), query),
		Output:         output,
		StructuredData: evidence,
	}, nil
}

func (t *RAGTool) executeIndexBuild(ctx context.Context, req Request) (Result, error) {
	err := t.index.Build(ctx)
	if err != nil {
		return Result{}, err
	}

	chunks, terms := t.index.Stats()
	return Result{
		Summary: fmt.Sprintf("built RAG index with %d chunks, %d terms", chunks, terms),
	}, nil
}

func (t *RAGTool) executeIndexUpdate(ctx context.Context, req Request) (Result, error) {
	// Get dirty paths
	dirtyPaths := t.index.GetDirty()
	if len(dirtyPaths) == 0 {
		return Result{
			Summary: "no dirty paths to update",
		}, nil
	}

	err := t.index.IncrementalUpdate(dirtyPaths)
	if err != nil {
		return Result{}, err
	}
	t.index.ClearDirty(dirtyPaths)

	return Result{
		Summary: fmt.Sprintf("updated RAG index, current version: %d", t.index.Version()),
	}, nil
}

// formatRAGResults formats RAG search results as human-readable text.
func formatRAGResults(results []rag.ScoredChunk) string {
	if len(results) == 0 {
		return "No relevant chunks found."
	}

	var output strings.Builder
	for i, r := range results {
		output.WriteString(fmt.Sprintf("%d. %s:%d (score: %.3f)\n", i+1, r.Chunk.Path, r.Chunk.StartLine, r.Score))
		output.WriteString(fmt.Sprintf("   %s\n", truncateString(r.Chunk.Content, 200)))
		if i < len(results)-1 {
			output.WriteString("\n")
		}
	}
	return output.String()
}

// truncateString truncates a string to the specified length.
func truncateString(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// createRAGTool creates a RAG tool instance.
func createRAGTool(rootDir string, cfg *config.ToolsConfig) (*RAGTool, error) {
	if !cfg.RAG.Enabled {
		return nil, fmt.Errorf("RAG is disabled in configuration")
	}

	// Convert config.RAGConfig to rag.IndexConfig
	indexConfig := rag.IndexConfig{
		RootDir:       rootDir,
		MaxChunkLines: cfg.RAG.Indexing.ChunkSize,
		BM25K1:        cfg.RAG.Search.K1,
		BM25B:         cfg.RAG.Search.B,

		// Search parameters
		DefaultTopK: cfg.RAG.Search.DefaultTopK,
		MaxTopK:     cfg.RAG.Search.MaxTopK,
		MinScore:    cfg.RAG.Search.MinScore,

		// Indexing parameters
		MaxFileSize:         cfg.RAG.Indexing.MaxFileSize,
		ChunkOverlap:        cfg.RAG.Indexing.ChunkOverlap,
		IndexableExtensions: cfg.RAG.Indexing.IndexableExtensions,
		ExcludeDirs:         cfg.RAG.Indexing.ExcludeDirs,
		ExcludePatterns:     cfg.RAG.Indexing.ExcludePatterns,
	}

	index := rag.NewIndexWithConfig(indexConfig)

	// Auto-build if configured
	if cfg.RAG.Indexing.AutoBuild {
		go func() {
			if err := index.Build(context.Background()); err != nil {
				log.Printf("[rag] auto-build index failed: %v", err)
			}
		}()
	}

	return NewRAGTool(index), nil
}
