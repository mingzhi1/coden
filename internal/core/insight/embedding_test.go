package insight

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestEmbeddingRoundTrip verifies the embedding vector survives a save→load
// cycle through the SQLite store (the storage half of semantic memory).
func TestEmbeddingRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "ins.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	vec := []float32{0.1, -0.2, 0.3, 0.42, -0.9}
	in := Insight{
		ID: "i1", SessionID: "s1", Category: CategoryFinding,
		Title: "t", Content: "c", Confidence: 0.7,
		Embedding: vec, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.Save(in); err != nil {
		t.Fatal(err)
	}
	got := store.ListBySession("s1", 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0].Embedding, vec) {
		t.Errorf("embedding round-trip mismatch: got %v want %v", got[0].Embedding, vec)
	}
}
