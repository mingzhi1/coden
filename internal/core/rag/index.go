// Package rag provides RAG indexing and search.
package rag

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Index provides BM25-based semantic search over chunks.
type Index struct {
	rootDir string

	// In-memory index (for MVP)
	chunks   []Chunk
	chunkMap map[string][]int // path -> chunk indices

	// BM25 parameters
	k1 float64 // Term frequency saturation
	b  float64 // Length normalization

	// Term statistics
	termFreq  map[string]int   // Document frequency for each term
	termIndex map[string][]int // term -> chunk indices
	totalDocs int
	avgDocLen float64

	// State
	version   int
	lastBuilt time.Time
	dirty     map[string]bool // Paths needing re-indexing

	// Configuration
	config IndexConfig

	mu sync.RWMutex
}

// NewIndex creates a new RAG index with default configuration.
func NewIndex(rootDir string) *Index {
	return NewIndexWithConfig(DefaultIndexConfig(rootDir))
}

// NewIndexWithConfig creates a new RAG index with the given configuration.
func NewIndexWithConfig(config IndexConfig) *Index {
	return &Index{
		rootDir:   config.RootDir,
		chunks:    make([]Chunk, 0),
		chunkMap:  make(map[string][]int),
		termFreq:  make(map[string]int),
		termIndex: make(map[string][]int),
		k1:        config.BM25K1,
		b:         config.BM25B,
		dirty:     make(map[string]bool),
		config:    config,
	}
}

// Build performs a full index build by walking rootDir.
func (idx *Index) Build(ctx context.Context) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Reset index
	idx.chunks = idx.chunks[:0]
	idx.chunkMap = make(map[string][]int)
	idx.termFreq = make(map[string]int)
	idx.termIndex = make(map[string][]int)

	chunker := NewChunker()

	err := filepath.WalkDir(idx.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			base := d.Name()
			// Check if directory is in exclude list
			for _, excludeDir := range idx.config.ExcludeDirs {
				if base == excludeDir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, _ := filepath.Rel(idx.rootDir, path)
		rel = filepath.ToSlash(rel)
		if !idx.isIndexable(rel) {
			return nil
		}
		// Check file size limit
		if idx.config.MaxFileSize > 0 {
			info, infoErr := d.Info()
			if infoErr == nil && info.Size() > idx.config.MaxFileSize {
				return nil
			}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		chunks, chunkErr := chunker.ChunkFile(rel, string(data))
		if chunkErr != nil {
			return nil
		}
		for _, c := range chunks {
			idx.addChunk(c)
		}
		return nil
	})

	idx.version++
	idx.lastBuilt = time.Now()
	idx.recalculateStats()

	return err
}

// IncrementalUpdate updates the index for specific paths.
func (idx *Index) IncrementalUpdate(paths []string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	chunker := NewChunker()

	for _, relPath := range paths {
		// Remove old chunks for this path
		if indices, ok := idx.chunkMap[relPath]; ok {
			for _, i := range indices {
				if i < len(idx.chunks) {
					idx.chunks[i].Content = "" // soft delete
				}
			}
			delete(idx.chunkMap, relPath)
		}

		// Re-chunk the file if it still exists
		absPath := filepath.Join(idx.rootDir, filepath.FromSlash(relPath))
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue // file deleted, removal is enough
		}
		chunks, chunkErr := chunker.ChunkFile(relPath, string(data))
		if chunkErr != nil {
			continue
		}
		for _, c := range chunks {
			idx.addChunk(c)
		}
	}

	idx.version++
	idx.recalculateStats()

	return nil
}

// MarkDirty marks paths as needing re-indexing.
func (idx *Index) MarkDirty(paths []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, path := range paths {
		idx.dirty[path] = true
	}
}

// ClearDirty clears dirty status for paths.
func (idx *Index) ClearDirty(paths []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, path := range paths {
		delete(idx.dirty, path)
	}
}

// GetDirty returns dirty paths.
func (idx *Index) GetDirty() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	paths := make([]string, 0, len(idx.dirty))
	for path := range idx.dirty {
		paths = append(paths, path)
	}
	return paths
}

// Search performs BM25 search.
func (idx *Index) Search(query string, topK int) ([]ScoredChunk, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.chunks) == 0 {
		return nil, nil
	}

	// Tokenize query
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	// Score all chunks
	scores := make(map[int]float64)

	for _, term := range queryTerms {
		idf := idx.idf(term)

		// Find chunks containing this term
		chunkIndices, ok := idx.termIndex[term]
		if !ok {
			continue
		}

		for _, chunkIdx := range chunkIndices {
			if chunkIdx >= len(idx.chunks) || idx.chunks[chunkIdx].Content == "" {
				continue // Skip deleted
			}

			chunk := idx.chunks[chunkIdx]
			tf := termFrequency(term, chunk.Content)
			docLen := len(tokenize(chunk.Content))

			// BM25 formula
			score := idf * (tf * (idx.k1 + 1)) /
				(tf + idx.k1*(1-idx.b+idx.b*float64(docLen)/idx.avgDocLen))

			scores[chunkIdx] += score
		}
	}

	// Convert to scored chunks
	var results []ScoredChunk
	for chunkIdx, score := range scores {
		results = append(results, ScoredChunk{
			Chunk: idx.chunks[chunkIdx],
			Score: score,
		})
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Return top K
	if len(results) > topK {
		results = results[:topK]
	}

	return results, nil
}

// ScoredChunk is a chunk with a relevance score.
type ScoredChunk struct {
	Chunk Chunk
	Score float64
}

// Version returns the current index version.
func (idx *Index) Version() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.version
}

// Config returns the index configuration.
func (idx *Index) Config() IndexConfig {
	return idx.config
}

// LastBuilt returns when the index was last built.
func (idx *Index) LastBuilt() time.Time {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.lastBuilt
}

// Stats returns index statistics.
func (idx *Index) Stats() (chunks, terms int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.chunks), len(idx.termFreq)
}

// recalculateStats recalculates document statistics.
func (idx *Index) recalculateStats() {
	totalLen := 0
	validDocs := 0

	for _, chunk := range idx.chunks {
		if chunk.Content == "" {
			continue
		}
		validDocs++
		totalLen += len(tokenize(chunk.Content))
	}

	idx.totalDocs = validDocs
	if validDocs > 0 {
		idx.avgDocLen = float64(totalLen) / float64(validDocs)
	} else {
		idx.avgDocLen = 0
	}
}

// idf calculates inverse document frequency.
func (idx *Index) idf(term string) float64 {
	df, ok := idx.termFreq[term]
	if !ok {
		return 0
	}

	// IDF formula: log((N - n + 0.5) / (n + 0.5) + 1)
	n := float64(df)
	N := float64(idx.totalDocs)

	return math.Log((N-n+0.5)/(n+0.5) + 1)
}

// addChunk adds a chunk to the index.
func (idx *Index) addChunk(chunk Chunk) {
	chunkIdx := len(idx.chunks)
	idx.chunks = append(idx.chunks, chunk)

	// Update path -> indices mapping
	idx.chunkMap[chunk.Path] = append(idx.chunkMap[chunk.Path], chunkIdx)

	// Tokenize and update term index (deduplicated per chunk)
	terms := tokenize(chunk.Content)
	seen := make(map[string]bool)

	for _, term := range terms {
		if !seen[term] {
			seen[term] = true
			idx.termIndex[term] = append(idx.termIndex[term], chunkIdx)
			idx.termFreq[term]++
		}
	}
}

// isIndexable checks if a file should be indexed using config's extensions and
// exclude patterns. Falls back to IsIndexableFile when no extensions configured.
func (idx *Index) isIndexable(path string) bool {
	// Always exclude known binary paths
	if !IsIndexableFile(path) {
		return false
	}

	// Check exclude patterns
	lower := strings.ToLower(path)
	for _, pattern := range idx.config.ExcludePatterns {
		// Simple glob: "*.min.js" matches files ending with ".min.js"
		if strings.HasPrefix(pattern, "*") {
			suffix := strings.ToLower(pattern[1:])
			if strings.HasSuffix(lower, suffix) {
				return false
			}
		}
	}

	// If IndexableExtensions is configured, use allowlist
	if len(idx.config.IndexableExtensions) > 0 {
		ext := strings.ToLower(filepath.Ext(path))
		for _, allowed := range idx.config.IndexableExtensions {
			if ext == strings.ToLower(allowed) {
				return true
			}
		}
		return false
	}

	return true
}

// tokenize splits text into terms, including CamelCase splitting.
func tokenize(text string) []string {
	// Split CamelCase before lowercasing: "ExecuteRipgrep" → "Execute Ripgrep"
	text = splitCamelCase(text)
	text = strings.ToLower(text)

	// Replace common separators with spaces
	replacer := strings.NewReplacer(
		"_", " ",
		"-", " ",
		".", " ",
		"/", " ",
		"(", " ",
		")", " ",
		"{", " ",
		"}", " ",
		"[", " ",
		"]", " ",
		":", " ",
		";", " ",
		",", " ",
		"\"", " ",
		"'", " ",
		"`", " ",
		"*", " ",
		"&", " ",
		"=", " ",
		"<", " ",
		">", " ",
		"!", " ",
		"#", " ",
		"@", " ",
	)
	text = replacer.Replace(text)

	// Split and filter
	words := strings.Fields(text)
	var terms []string

	for _, w := range words {
		// Filter out very short words and common Go noise
		if len(w) >= 2 && !isStopWord(w) {
			terms = append(terms, w)
		}
	}

	return terms
}

// splitCamelCase inserts spaces at CamelCase boundaries.
// "ExecuteRipgrep" → "Execute Ripgrep", "BM25" → "BM 25"
func splitCamelCase(s string) string {
	var result []byte
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) ||
			(i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			result = append(result, ' ')
		}
		result = append(result, []byte(string(r))...)
	}
	return string(result)
}

// isStopWord filters out extremely common terms that add noise to BM25.
func isStopWord(w string) bool {
	switch w {
	case "if", "in", "is", "it", "or", "of", "to", "do", "no", "on",
		"an", "as", "at", "be", "by", "we", "so", "up",
		"the", "and", "for", "not", "but", "are", "was", "has", "had",
		"nil", "err", "var", "int":
		return true
	}
	return false
}

// termFrequency counts occurrences of term in text.
func termFrequency(term, text string) float64 {
	terms := tokenize(text)
	count := 0
	for _, t := range terms {
		if t == term {
			count++
		}
	}
	return float64(count)
}

// IndexConfig configures the RAG index.
type IndexConfig struct {
	RootDir       string
	MaxChunkLines int
	BM25K1        float64
	BM25B         float64

	// Search parameters
	DefaultTopK int
	MaxTopK     int
	MinScore    float64

	// Indexing parameters
	MaxFileSize         int64
	ChunkOverlap        int
	IndexableExtensions []string
	ExcludeDirs         []string
	ExcludePatterns     []string
}

// DefaultIndexConfig returns sensible defaults.
func DefaultIndexConfig(rootDir string) IndexConfig {
	return IndexConfig{
		RootDir:       rootDir,
		MaxChunkLines: 50,
		BM25K1:        1.5,
		BM25B:         0.75,

		// Search defaults
		DefaultTopK: 10,
		MaxTopK:     100,
		MinScore:    0.1,

		// Indexing defaults
		MaxFileSize:         1048576, // 1MB
		ChunkOverlap:        10,
		IndexableExtensions: []string{".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java"},
		ExcludeDirs:         []string{".git", "vendor", "node_modules"},
		ExcludePatterns:     []string{"*.min.js", "*.min.css", "*.map", "*.lock"},
	}
}
