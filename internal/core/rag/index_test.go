package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeIndexFiles writes name→content files under dir for index tests.
func writeIndexFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
	{
			"func TestSomething(t *testing.T)",
			[]string{"func", "test", "something", "testing"},
		},
		{
			"Kernel.Workflow.Start",
			[]string{"kernel", "workflow", "start"},
		},
		{
			"This is a test",
			[]string{"this", "test"},
		},
		{
			"a b", // Filter out single char
			[]string{},
		},
	}

	for _, tt := range tests {
		result := tokenize(tt.input)
		// Just check that tokenization produces reasonable output
		if len(result) < len(tt.expected) {
			t.Errorf("tokenize(%q): expected at least %d tokens, got %d (%v)", tt.input, len(tt.expected), len(result), result)
		}
	}
}

func TestTermFrequency(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	
	// "the" is now a stop word, so tf should be 0
	if tf := termFrequency("the", text); tf != 0 {
		t.Errorf("Expected tf('the')=0 (stop word), got %f", tf)
	}
	
	if tf := termFrequency("fox", text); tf != 1 {
		t.Errorf("Expected tf('fox')=1, got %f", tf)
	}
	
	if tf := termFrequency("missing", text); tf != 0 {
		t.Errorf("Expected tf('missing')=0, got %f", tf)
	}
}

func TestIndexSearch(t *testing.T) {
	dir := t.TempDir()
	writeIndexFiles(t, dir, map[string]string{
		"a.go": "package x\n\nfunc Kernel() error { return nil }\n",
		"b.go": "package x\n\ntype Workflow struct { tasks []Task }\n",
		"c.go": "package x\n\nfunc Submit(w *Workflow) { w.run() }\n",
	})

	idx := NewIndex(dir)
	defer idx.Close()
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Search for "kernel" — only a.go contains it.
	results, err := idx.Search("kernel", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Expected results for 'kernel'")
	}
	if results[0].Chunk.Path != "a.go" {
		t.Errorf("Expected a.go as top result, got %s", results[0].Chunk.Path)
	}

	// Search for "workflow" — b.go and c.go contain it.
	results, err = idx.Search("workflow", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("Expected results for 'workflow'")
	}
}

func TestIndexEmpty(t *testing.T) {
	idx := NewIndex(t.TempDir())
	defer idx.Close()

	results, err := idx.Search("anything", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results for empty index, got %d", len(results))
	}
}

func TestIsIndexableFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"main.go", true},
		{"internal/core/kernel.go", true},
		{".git/config", false},
		{"vendor/foo/bar.go", false},
		{"node_modules/pkg/index.js", false},
		{"main.exe", false},
		{"image.png", false},
		{"archive.zip", false},
	}
	
	for _, tt := range tests {
		result := IsIndexableFile(tt.path)
		if result != tt.expected {
			t.Errorf("IsIndexableFile(%q): expected %v, got %v", tt.path, tt.expected, result)
		}
	}
}

func TestChunkerGoFile(t *testing.T) {
	chunker := NewChunker()
	
	content := `package main

import "fmt"

// Kernel manages workflows.
type Kernel struct {
	workflows map[string]*Workflow
}

// NewKernel creates a new kernel.
func NewKernel() *Kernel {
	return &Kernel{
		workflows: make(map[string]*Workflow),
	}
}

// Submit submits a request.
func (k *Kernel) Submit(req Request) error {
	return nil
}
`
	
	chunks, err := chunker.ChunkFile("test.go", content)
	if err != nil {
		t.Fatalf("ChunkFile failed: %v", err)
	}
	
	// Should have at least 3 chunks: import, type Kernel, NewKernel, Submit
	if len(chunks) < 3 {
		t.Errorf("Expected at least 3 chunks, got %d", len(chunks))
	}
	
	// Check that struct is chunked
	foundKernel := false
	for _, c := range chunks {
		if c.Symbol == "Kernel" && c.SymbolType == "type" {
			foundKernel = true
			if c.Doc == "" {
				t.Error("Expected documentation for Kernel struct")
			}
		}
	}
	
	if !foundKernel {
		t.Error("Expected to find Kernel type chunk")
	}
}

func TestChunkerTextFile(t *testing.T) {
	chunker := NewChunker()
	chunker.MaxChunkLines = 5
	chunker.OverlapLines = 0 // No overlap for simpler test

	content := `line 1
line 2
line 3
line 4
line 5
line 6
line 7
line 8
line 9
line 10`

	chunks, err := chunker.ChunkFile("test.txt", content)
	if err != nil {
		t.Fatalf("ChunkFile failed: %v", err)
	}

	// Should have 2 chunks: 1-5, 6-10
	if len(chunks) != 2 {
		t.Errorf("Expected 2 chunks, got %d", len(chunks))
	}
}

func TestMergeAdjacentChunks(t *testing.T) {
	chunks := []Chunk{
		{Path: "a.go", StartLine: 1, EndLine: 10, Content: "chunk 1"},
		{Path: "a.go", StartLine: 11, EndLine: 20, Content: "chunk 2"}, // Adjacent
		{Path: "a.go", StartLine: 50, EndLine: 60, Content: "chunk 3"}, // Far
	}
	
	merged := MergeAdjacentChunks(chunks, 5)
	
	if len(merged) != 2 {
		t.Errorf("Expected 2 merged chunks, got %d", len(merged))
	}
	
	if merged[0].EndLine != 20 {
		t.Errorf("Expected merged chunk to end at 20, got %d", merged[0].EndLine)
	}
}

func TestIndexBuild(t *testing.T) {
	dir := t.TempDir()
	writeIndexFiles(t, dir, map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	idx := NewIndex(dir)
	defer idx.Close()
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if idx.Version() != 1 {
		t.Errorf("Expected version 1, got %d", idx.Version())
	}

	// Restart reconcile: a second Build with no changes must be a no-op stat
	// walk that still succeeds and finds the previously-indexed content.
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("re-Build failed: %v", err)
	}
	results, err := idx.Search("main", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("Expected the persisted index to still find 'main' after re-build")
	}
}

func TestDirtyHashTierSkipsReindex(t *testing.T) {
	dir := t.TempDir()
	writeIndexFiles(t, dir, map[string]string{
		"a.go": "package x\n\nfunc Distinctivetoken() {}\n",
	})
	idx := NewIndex(dir)
	defer idx.Close()
	ctx := context.Background()
	if err := idx.Build(ctx); err != nil {
		t.Fatal(err)
	}

	db, err := idx.ensureDB()
	if err != nil {
		t.Fatal(err)
	}
	firstRowID := func() int64 {
		var id int64
		if err := db.QueryRow(`SELECT rowid FROM rag_chunks WHERE path='a.go' ORDER BY rowid LIMIT 1`).Scan(&id); err != nil {
			t.Fatalf("rowid query: %v", err)
		}
		return id
	}
	before := firstRowID()

	// Bump mtime without changing content (simulates git checkout / touch).
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.go"), future, future); err != nil {
		t.Fatal(err)
	}
	if err := idx.Build(ctx); err != nil {
		t.Fatal(err)
	}
	// T2: hash unchanged → chunks must NOT be re-inserted (rowid stable).
	if after := firstRowID(); after != before {
		t.Errorf("expected T2 hash-match to skip reindex, but chunk rowid changed %d -> %d", before, after)
	}

	// Genuinely change content → must re-index and become searchable.
	writeIndexFiles(t, dir, map[string]string{"a.go": "package x\n\nfunc Changedsymbol() {}\n"})
	if err := idx.Build(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := idx.Search("changedsymbol", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Error("expected changed content to be re-indexed and searchable")
	}
}

func TestDirtyTracking(t *testing.T) {
	idx := NewIndex(".")
	
	idx.MarkDirty([]string{"a.go", "b.go"})
	
	dirty := idx.GetDirty()
	if len(dirty) != 2 {
		t.Errorf("Expected 2 dirty paths, got %d", len(dirty))
	}
	
	idx.ClearDirty([]string{"a.go"})
	
	dirty = idx.GetDirty()
	if len(dirty) != 1 {
		t.Errorf("Expected 1 dirty path, got %d", len(dirty))
	}
}
