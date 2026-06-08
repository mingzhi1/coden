// Package rag provides RAG indexing and search backed by SQLite FTS5.
//
// The index is persisted on disk (<rootDir>/.coden/rag.sqlite) instead of being
// rebuilt in memory every process. This kills the previous "rebuild-from-scratch
// + background race" problem: a fresh process loads the existing index instantly
// and only re-indexes files that changed while it was down (see Build, which
// reconciles by comparing each file's mtime+size against the stored row).
//
// FTS5 provides BM25 ranking natively (bm25()), so the hand-rolled BM25 scoring
// is gone. Matching quality is preserved by storing a code-tokenized `body`
// column (camelCase/snake_case split via tokenize) alongside the raw content.
package rag

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (FTS5 enabled)
)

// Index provides BM25-based search over code chunks, persisted in SQLite FTS5.
type Index struct {
	config  IndexConfig
	rootDir string
	dbPath  string

	initOnce sync.Once
	initErr  error
	db       *sql.DB

	mu sync.Mutex // serializes writes (Build / IncrementalUpdate)

	dmu     sync.Mutex
	dirty   map[string]bool // paths pending re-index (set by checkpoint flow)
	version int
	built   time.Time
}

// NewIndex creates a new RAG index with default configuration.
func NewIndex(rootDir string) *Index {
	return NewIndexWithConfig(DefaultIndexConfig(rootDir))
}

// NewIndexWithConfig creates a new RAG index with the given configuration.
// The SQLite database is opened lazily on first use so this never fails.
//
// The database location is config.DBPath when set (callers point this at
// ~/.coden/workspace/ to keep the repo clean and co-locate it with the other
// per-workspace derived state); otherwise it falls back to the in-repo
// <rootDir>/.coden/rag.sqlite.
func NewIndexWithConfig(config IndexConfig) *Index {
	dbPath := strings.TrimSpace(config.DBPath)
	if dbPath == "" {
		dbPath = filepath.Join(config.RootDir, ".coden", "rag.sqlite")
	}
	return &Index{
		config:  config,
		rootDir: config.RootDir,
		dbPath:  dbPath,
		dirty:   make(map[string]bool),
	}
}

// ensureDB opens the SQLite database and creates the schema once.
func (idx *Index) ensureDB() (*sql.DB, error) {
	idx.initOnce.Do(func() {
		if err := os.MkdirAll(filepath.Dir(idx.dbPath), 0o755); err != nil {
			idx.initErr = fmt.Errorf("rag: create db dir: %w", err)
			return
		}
		dsn := idx.dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			idx.initErr = fmt.Errorf("rag: open db: %w", err)
			return
		}
		if err := initSchema(db); err != nil {
			db.Close()
			idx.initErr = fmt.Errorf("rag: init schema: %w", err)
			return
		}
		idx.db = db
	})
	return idx.db, idx.initErr
}

func initSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS rag_files (
			path  TEXT PRIMARY KEY,
			mtime INTEGER NOT NULL,
			size  INTEGER NOT NULL,
			hash  TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS rag_chunks USING fts5 (
			path UNINDEXED,
			start_line UNINDEXED,
			end_line UNINDEXED,
			raw UNINDEXED,
			body
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	// Migrate older databases that predate the hash column. A duplicate-column
	// error means it already exists, which is fine.
	if _, err := db.Exec(`ALTER TABLE rag_files ADD COLUMN hash TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	return nil
}

// Build reconciles the on-disk index with the current working tree: it indexes
// new files, re-indexes changed files (mtime or size differs from the stored
// row), and drops files that no longer exist. On a fresh database this indexes
// everything; on restart it is cheap (a stat walk that only touches changed
// files). This is what makes the persistent index correct across restarts.
func (idx *Index) Build(ctx context.Context) error {
	db, err := idx.ensureDB()
	if err != nil {
		return err
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Snapshot stored file states.
	type fstate struct {
		mtime int64
		size  int64
		hash  string
	}
	stored := make(map[string]fstate)
	rows, err := db.QueryContext(ctx, `SELECT path, mtime, size, hash FROM rag_files`)
	if err != nil {
		return fmt.Errorf("rag: load files: %w", err)
	}
	for rows.Next() {
		var p string
		var fs fstate
		if err := rows.Scan(&p, &fs.mtime, &fs.size, &fs.hash); err != nil {
			rows.Close()
			return err
		}
		stored[p] = fs
	}
	rows.Close()

	chunker := NewChunker()
	seen := make(map[string]bool)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	walkErr := filepath.WalkDir(idx.rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			base := d.Name()
			for _, ex := range idx.config.ExcludeDirs {
				if base == ex {
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
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if idx.config.MaxFileSize > 0 && info.Size() > idx.config.MaxFileSize {
			return nil
		}
		seen[rel] = true
		mtime, size := info.ModTime().UnixNano(), info.Size()
		s, ok := stored[rel]
		// T1: cheap stat check — mtime+size unchanged → skip without reading.
		if ok && s.mtime == mtime && s.size == size {
			return nil
		}
		// T1 says possibly changed; read once to compute the T2 hash.
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		h := hashContent(string(data))
		// T2: content identical (only mtime/size drifted, e.g. git checkout/touch)
		// → just refresh the stored mtime/size, skip the expensive re-chunk + write.
		if ok && s.hash == h {
			return upsertFileMeta(tx, rel, mtime, size, h)
		}
		// Content genuinely changed (or new file) → re-chunk and re-index.
		chunks, chunkErr := chunker.ChunkFile(rel, string(data))
		if chunkErr != nil {
			return nil
		}
		return reindexFile(tx, rel, chunks, mtime, size, h)
	})
	if walkErr != nil {
		return walkErr
	}

	// Drop files that disappeared.
	for p := range stored {
		if !seen[p] {
			if err := deleteFile(tx, p); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	idx.bump()
	return nil
}

// IncrementalUpdate re-indexes specific paths (used by the checkpoint flow for
// freshly modified files). Deleted files are removed from the index.
func (idx *Index) IncrementalUpdate(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	db, err := idx.ensureDB()
	if err != nil {
		return err
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	chunker := NewChunker()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, rel := range paths {
		rel = filepath.ToSlash(rel)
		abs := filepath.Join(idx.rootDir, filepath.FromSlash(rel))
		info, statErr := os.Stat(abs)
		if statErr != nil {
			if err := deleteFile(tx, rel); err != nil {
				return err
			}
			continue
		}
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		chunks, chunkErr := chunker.ChunkFile(rel, string(data))
		if chunkErr != nil {
			continue
		}
		if err := reindexFile(tx, rel, chunks, info.ModTime().UnixNano(), info.Size(), hashContent(string(data))); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	idx.bump()
	return nil
}

// reindexFile replaces all chunks for rel and upserts its file state, within tx.
func reindexFile(tx *sql.Tx, rel string, chunks []Chunk, mtime, size int64, hash string) error {
	if _, err := tx.Exec(`DELETE FROM rag_chunks WHERE path = ?`, rel); err != nil {
		return err
	}
	for _, c := range chunks {
		body := strings.Join(tokenize(c.Content), " ")
		if _, err := tx.Exec(
			`INSERT INTO rag_chunks(path, start_line, end_line, raw, body) VALUES (?,?,?,?,?)`,
			c.Path, c.StartLine, c.EndLine, c.Content, body,
		); err != nil {
			return err
		}
	}
	return upsertFileMeta(tx, rel, mtime, size, hash)
}

// upsertFileMeta records a file's mtime/size/hash without touching its chunks.
// Used by the T2 path when content is unchanged but mtime/size drifted.
func upsertFileMeta(tx *sql.Tx, rel string, mtime, size int64, hash string) error {
	_, err := tx.Exec(
		`INSERT INTO rag_files(path, mtime, size, hash) VALUES (?,?,?,?)
		 ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime, size=excluded.size, hash=excluded.hash`,
		rel, mtime, size, hash,
	)
	return err
}

func deleteFile(tx *sql.Tx, rel string) error {
	if _, err := tx.Exec(`DELETE FROM rag_chunks WHERE path = ?`, rel); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM rag_files WHERE path = ?`, rel)
	return err
}

// Search performs an FTS5 BM25 search. The returned Score is the negated bm25()
// value (FTS5 returns more-negative for better matches), so higher Score = more
// relevant, consistent with the previous in-memory implementation.
func (idx *Index) Search(query string, topK int) ([]ScoredChunk, error) {
	db, err := idx.ensureDB()
	if err != nil {
		return nil, err
	}
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil, nil
	}
	// Quote each token so FTS5 treats it as a literal (avoids operator/keyword
	// collisions like NEAR), and OR them for recall comparable to the old union.
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + t + `"`
	}
	match := strings.Join(quoted, " OR ")

	if topK <= 0 {
		topK = idx.config.DefaultTopK
	}

	rows, err := db.Query(
		`SELECT path, start_line, end_line, raw, bm25(rag_chunks)
		   FROM rag_chunks WHERE rag_chunks MATCH ?
		   ORDER BY bm25(rag_chunks) LIMIT ?`,
		match, topK,
	)
	if err != nil {
		return nil, fmt.Errorf("rag: search: %w", err)
	}
	defer rows.Close()

	var results []ScoredChunk
	for rows.Next() {
		var c Chunk
		var bm float64
		if err := rows.Scan(&c.Path, &c.StartLine, &c.EndLine, &c.Content, &bm); err != nil {
			return nil, err
		}
		results = append(results, ScoredChunk{Chunk: c, Score: -bm})
	}
	return results, rows.Err()
}

// ScoredChunk is a chunk with a relevance score (higher = more relevant).
type ScoredChunk struct {
	Chunk Chunk
	Score float64
}

// MarkDirty records paths that need re-indexing on the next IncrementalUpdate.
func (idx *Index) MarkDirty(paths []string) {
	idx.dmu.Lock()
	defer idx.dmu.Unlock()
	for _, p := range paths {
		idx.dirty[filepath.ToSlash(p)] = true
	}
}

// ClearDirty clears dirty status for paths.
func (idx *Index) ClearDirty(paths []string) {
	idx.dmu.Lock()
	defer idx.dmu.Unlock()
	for _, p := range paths {
		delete(idx.dirty, filepath.ToSlash(p))
	}
}

// GetDirty returns the current dirty paths.
func (idx *Index) GetDirty() []string {
	idx.dmu.Lock()
	defer idx.dmu.Unlock()
	paths := make([]string, 0, len(idx.dirty))
	for p := range idx.dirty {
		paths = append(paths, p)
	}
	return paths
}

func (idx *Index) bump() {
	idx.dmu.Lock()
	idx.version++
	idx.built = time.Now()
	idx.dmu.Unlock()
}

// Version returns the current index version (bumped on each build/update).
func (idx *Index) Version() int {
	idx.dmu.Lock()
	defer idx.dmu.Unlock()
	return idx.version
}

// LastBuilt returns when the index was last built or updated.
func (idx *Index) LastBuilt() time.Time {
	idx.dmu.Lock()
	defer idx.dmu.Unlock()
	return idx.built
}

// Config returns the index configuration.
func (idx *Index) Config() IndexConfig { return idx.config }

// Stats returns (chunks, files) currently indexed.
func (idx *Index) Stats() (chunks, files int) {
	db, err := idx.ensureDB()
	if err != nil {
		return 0, 0
	}
	_ = db.QueryRow(`SELECT count(*) FROM rag_chunks`).Scan(&chunks)
	_ = db.QueryRow(`SELECT count(*) FROM rag_files`).Scan(&files)
	return chunks, files
}

// Close releases the underlying database handle.
func (idx *Index) Close() error {
	if idx.db != nil {
		return idx.db.Close()
	}
	return nil
}

// isIndexable checks if a file should be indexed using config's extensions and
// exclude patterns. Falls back to IsIndexableFile when no extensions configured.
func (idx *Index) isIndexable(path string) bool {
	if !IsIndexableFile(path) {
		return false
	}
	lower := strings.ToLower(path)
	for _, pattern := range idx.config.ExcludePatterns {
		if strings.HasPrefix(pattern, "*") {
			if strings.HasSuffix(lower, strings.ToLower(pattern[1:])) {
				return false
			}
		}
	}
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

// tokenize splits text into terms, including CamelCase splitting. Used both to
// build the FTS5 `body` column and to tokenize search queries so identifiers
// like ExecuteRipgrep match a query for "ripgrep".
func tokenize(text string) []string {
	text = splitCamelCase(text)
	text = strings.ToLower(text)

	replacer := strings.NewReplacer(
		"_", " ", "-", " ", ".", " ", "/", " ", "(", " ", ")", " ",
		"{", " ", "}", " ", "[", " ", "]", " ", ":", " ", ";", " ",
		",", " ", "\"", " ", "'", " ", "`", " ", "*", " ", "&", " ",
		"=", " ", "<", " ", ">", " ", "!", " ", "#", " ", "@", " ",
	)
	text = replacer.Replace(text)

	words := strings.Fields(text)
	var terms []string
	for _, w := range words {
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
	RootDir string
	// DBPath is the SQLite file backing the index. When empty it defaults to
	// <RootDir>/.coden/rag.sqlite; callers set it to a ~/.coden/workspace/ path
	// to keep the index out of the repo working tree.
	DBPath        string
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

		DefaultTopK: 10,
		MaxTopK:     100,
		// FTS5 bm25() magnitudes are corpus-dependent and unrelated to the old
		// in-memory scale; MATCH + topK already bound relevance, so default to no
		// hard score floor.
		MinScore: 0,

		MaxFileSize:         1048576, // 1MB
		ChunkOverlap:        10,
		IndexableExtensions: []string{".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java"},
		ExcludeDirs:         []string{".git", "vendor", "node_modules"},
		ExcludePatterns:     []string{"*.min.js", "*.min.css", "*.map", "*.lock"},
	}
}
