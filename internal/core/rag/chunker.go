// Package rag provides RAG (Retrieval-Augmented Generation) indexing.
package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Chunk represents a semantic chunk of code.
type Chunk struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Content    string `json:"content"`
	Hash       string `json:"hash"`
	Symbol     string `json:"symbol,omitempty"`      // Function/type name
	SymbolType string `json:"symbol_type,omitempty"` // "func", "type", "var", "const"
	Doc        string `json:"doc,omitempty"`         // Documentation comment
}

// Chunker splits files into semantic chunks.
type Chunker struct {
	// Config
	MaxChunkLines int
	OverlapLines  int
}

// NewChunker creates a new chunker with sensible defaults.
func NewChunker() *Chunker {
	return &Chunker{
		MaxChunkLines: 50,
		OverlapLines:  3,
	}
}

// ChunkFile chunks a single file.
// For Go files, uses AST-aware chunking; otherwise uses line-based chunking.
func (c *Chunker) ChunkFile(path string, content string) ([]Chunk, error) {
	if strings.HasSuffix(path, ".go") {
		return c.chunkGoFile(path, content)
	}
	return c.chunkTextFile(path, content)
}

// chunkGoFile uses Go AST to create semantic chunks at function/type boundaries.
func (c *Chunker) chunkGoFile(path string, content string) ([]Chunk, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		// Fall back to line-based chunking on parse error
		return c.chunkTextFile(path, content)
	}

	lines := strings.Split(content, "\n")
	var chunks []Chunk

	// Process declarations
	for _, decl := range f.Decls {
		chunk := c.declToChunk(fset, lines, decl, path)
		if chunk != nil {
			chunks = append(chunks, *chunk)
		}
	}

	if len(chunks) == 0 {
		// No declarations found, fall back to line-based
		return c.chunkTextFile(path, content)
	}

	return chunks, nil
}

// declToChunk converts an AST declaration to a chunk.
func (c *Chunker) declToChunk(fset *token.FileSet, lines []string, decl ast.Decl, path string) *Chunk {
	var start, end token.Pos
	var name, doc string
	var declType string

	switch d := decl.(type) {
	case *ast.FuncDecl:
		start = d.Pos()
		end = d.End()
		name = d.Name.Name
		declType = "func"
		if d.Doc != nil {
			doc = d.Doc.Text()
		}
	case *ast.GenDecl:
		start = d.Pos()
		end = d.End()
		declType = d.Tok.String() // "type", "var", "const", "import"
		if len(d.Specs) > 0 {
			switch s := d.Specs[0].(type) {
			case *ast.TypeSpec:
				name = s.Name.Name
			case *ast.ValueSpec:
				if len(s.Names) > 0 {
					name = s.Names[0].Name
				}
			}
		}
		if d.Doc != nil {
			doc = d.Doc.Text()
		}
	default:
		return nil
	}

	startLine := fset.Position(start).Line
	endLine := fset.Position(end).Line

	// Extract content
	contentLines := lines[startLine-1 : endLine] // Lines are 1-based in fset
	content := strings.Join(contentLines, "\n")

	return &Chunk{
		Path:       path,
		StartLine:  startLine,
		EndLine:    endLine,
		Content:    content,
		Hash:       hashContent(content),
		Symbol:     name,
		SymbolType: declType,
		Doc:        doc,
	}
}

// chunkTextFile creates chunks by fixed line count.
func (c *Chunker) chunkTextFile(path string, content string) ([]Chunk, error) {
	lines := strings.Split(content, "\n")
	var chunks []Chunk

	for start := 0; start < len(lines); start += c.MaxChunkLines - c.OverlapLines {
		end := start + c.MaxChunkLines
		if end > len(lines) {
			end = len(lines)
		}

		chunkLines := lines[start:end]
		chunkContent := strings.Join(chunkLines, "\n")

		chunk := Chunk{
			Path:      path,
			StartLine: start + 1, // 1-based
			EndLine:   end,
			Content:   chunkContent,
			Hash:      hashContent(chunkContent),
		}
		chunks = append(chunks, chunk)

		if end == len(lines) {
			break
		}
	}

	return chunks, nil
}

// ChunkDirectory chunks all files in a directory.
// Skips: .git, vendor, node_modules, binary files.
func (c *Chunker) ChunkDirectory(root string) ([]Chunk, error) {
	var allChunks []Chunk

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}

		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "vendor", "node_modules", ".hg", "__pycache__", ".idea", ".vscode":
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary / non-text extensions.
		if isBinaryChunkExt(filepath.Ext(name)) {
			return nil
		}

		// Skip files larger than 1 MB.
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > 1<<20 {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		chunks, chunkErr := c.ChunkFile(rel, string(data))
		if chunkErr != nil {
			return nil // skip files that fail to chunk
		}
		allChunks = append(allChunks, chunks...)
		return nil
	})
	if err != nil {
		return allChunks, err
	}
	return allChunks, nil
}

// isBinaryChunkExt returns true for file extensions that are typically binary.
func isBinaryChunkExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".a",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
		".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".mp3", ".mp4", ".avi", ".mov", ".wav", ".flac",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".wasm", ".pyc", ".class", ".jar", ".sqlite", ".db":
		return true
	}
	return false
}

// hashContent returns the SHA-256 hex digest of content.
func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// IsIndexableFile returns true if the file should be indexed.
func IsIndexableFile(path string) bool {
	// Skip common non-indexable paths
	skips := []string{
		".git/",
		"vendor/",
		"node_modules/",
		".idea/",
		".vscode/",
		"__pycache__/",
	}

	for _, skip := range skips {
		if strings.Contains(path, skip) {
			return false
		}
	}

	// Skip binary files by extension
	extensions := []string{
		".exe", ".dll", ".so", ".dylib",
		".bin", ".dat", ".db",
		".jpg", ".jpeg", ".png", ".gif", ".svg",
		".mp3", ".mp4", ".avi", ".mov",
		".zip", ".tar", ".gz", ".rar",
		".pdf", ".doc", ".docx",
	}

	lower := strings.ToLower(path)
	for _, ext := range extensions {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}

	return true
}

// MergeAdjacentChunks merges chunks that are very close together.
// Useful for reducing index size.
func MergeAdjacentChunks(chunks []Chunk, maxGap int) []Chunk {
	if len(chunks) <= 1 {
		return chunks
	}

	var merged []Chunk
	current := chunks[0]

	for i := 1; i < len(chunks); i++ {
		next := chunks[i]

		// Check if chunks are from same file and close together
		if current.Path == next.Path && next.StartLine-current.EndLine <= maxGap {
			// Merge
			current.EndLine = next.EndLine
			current.Content = current.Content + "\n" + next.Content
			current.Hash = hashContent(current.Content)
			if current.Symbol == "" {
				current.Symbol = next.Symbol
			}
		} else {
			merged = append(merged, current)
			current = next
		}
	}
	merged = append(merged, current)

	return merged
}
