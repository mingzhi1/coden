package discovery

import (
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// DirtyTracker — basic state
// ---------------------------------------------------------------------------

func TestDirtyTracker_NewIsEmpty(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	if got := dt.DirtyPaths(); len(got) != 0 {
		t.Errorf("expected no dirty paths, got %v", got)
	}
	if got := dt.RAGDirtyPaths(); len(got) != 0 {
		t.Errorf("expected no RAG dirty paths, got %v", got)
	}
	if got := dt.ChangeLog(); len(got) != 0 {
		t.Errorf("expected empty change log, got %v", got)
	}
}

func TestDirtyTracker_MarkDirtyPopulatesAllSets(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.MarkDirty("pkg/foo.go", "write")

	if !dt.IsDirty("pkg/foo.go") {
		t.Error("expected IsDirty=true for marked path")
	}
	paths := dt.DirtyPaths()
	if len(paths) != 1 || paths[0] != "pkg/foo.go" {
		t.Errorf("unexpected DirtyPaths: %v", paths)
	}
	rag := dt.RAGDirtyPaths()
	if len(rag) != 1 || rag[0] != "pkg/foo.go" {
		t.Errorf("unexpected RAGDirtyPaths: %v", rag)
	}
	log := dt.ChangeLog()
	if len(log) != 1 || log[0].Path != "pkg/foo.go" || log[0].Op != "write" {
		t.Errorf("unexpected ChangeLog: %v", log)
	}
}

func TestDirtyTracker_MarkDirtyIgnoresEmptyPath(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.MarkDirty("", "write")
	dt.MarkDirty("   ", "create")
	if len(dt.DirtyPaths()) != 0 {
		t.Error("blank paths should be ignored")
	}
}

func TestDirtyTracker_IsDirtyFalseForUnknownPath(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.MarkDirty("a.go", "write")
	if dt.IsDirty("b.go") {
		t.Error("b.go should not be dirty")
	}
}

// ---------------------------------------------------------------------------
// ClearDirty
// ---------------------------------------------------------------------------

func TestDirtyTracker_ClearDirtyResetsFilesAndRAG(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.MarkDirty("a.go", "write")
	dt.MarkDirty("b.go", "create")
	dt.ClearDirty()

	if len(dt.DirtyPaths()) != 0 {
		t.Error("DirtyPaths should be empty after ClearDirty")
	}
	if len(dt.RAGDirtyPaths()) != 0 {
		t.Error("RAGDirtyPaths should be empty after ClearDirty")
	}
}

func TestDirtyTracker_ClearDirtyRetainsChangeLog(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.MarkDirty("a.go", "write")
	dt.ClearDirty()

	log := dt.ChangeLog()
	if len(log) != 1 || log[0].Path != "a.go" {
		t.Errorf("ChangeLog should survive ClearDirty, got %v", log)
	}
}

// ---------------------------------------------------------------------------
// SyncFrom
// ---------------------------------------------------------------------------

func TestDirtyTracker_SyncFromAddsNewPaths(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.SyncFrom([]string{"x.go", "y.go"})

	paths := dt.DirtyPaths()
	if len(paths) != 2 {
		t.Errorf("expected 2 dirty paths after SyncFrom, got %v", paths)
	}
	if !dt.IsDirty("x.go") || !dt.IsDirty("y.go") {
		t.Error("x.go and y.go should be dirty after SyncFrom")
	}
}

func TestDirtyTracker_SyncFromDoesNotOverwriteTimestamp(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.MarkDirty("a.go", "write")
	// Record the timestamp immediately after mark.
	firstTime := dt.ChangeLog()[0].Timestamp

	time.Sleep(2 * time.Millisecond)
	dt.SyncFrom([]string{"a.go"}) // should be a no-op for existing entry

	// Timestamp in changeLog should not have been updated.
	log := dt.ChangeLog()
	if len(log) != 1 {
		t.Fatalf("expected 1 change log entry, got %d", len(log))
	}
	if !log[0].Timestamp.Equal(firstTime) {
		t.Error("SyncFrom should not overwrite existing entry timestamp")
	}
}

func TestDirtyTracker_SyncFromIgnoresEmpty(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.SyncFrom([]string{"", "  "})
	if len(dt.DirtyPaths()) != 0 {
		t.Error("SyncFrom with blank paths should be no-op")
	}
}

// ---------------------------------------------------------------------------
// ChangeLog capping
// ---------------------------------------------------------------------------

func TestDirtyTracker_ChangeLogCappedAt100(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	for i := 0; i < 150; i++ {
		dt.MarkDirty("file.go", "write")
	}
	log := dt.ChangeLog()
	if len(log) > maxChangeLogEntries {
		t.Errorf("ChangeLog should be capped at %d, got %d", maxChangeLogEntries, len(log))
	}
}

// ---------------------------------------------------------------------------
// DirtyPaths sorted
// ---------------------------------------------------------------------------

func TestDirtyTracker_DirtyPathsSorted(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()
	dt.MarkDirty("z.go", "write")
	dt.MarkDirty("a.go", "write")
	dt.MarkDirty("m.go", "write")

	paths := dt.DirtyPaths()
	for i := 1; i < len(paths); i++ {
		if paths[i] < paths[i-1] {
			t.Errorf("DirtyPaths not sorted: %v", paths)
		}
	}
}

// ---------------------------------------------------------------------------
// MarkDirty → cache invalidation (G4)
// Tested sequentially (no t.Parallel) to avoid races with the global cache
// that is shared across all orchestrator tests in this package.
// ---------------------------------------------------------------------------

func TestDirtyTracker_MarkDirtyInvalidatesCache(t *testing.T) {
	// Not parallel — guards the global orchestratorCache from races with
	// other tests that use clearCache().
	clearCache()
	t.Cleanup(clearCache)

	// Seed an entry whose key contains "tracker_model.go".
	key := "/tracker-ws|tracker_model.go|identifier|grep||dirty="
	cachePut(key, nil)

	// Sanity: entry is present.
	if _, ok := cacheGet(key); !ok {
		t.Fatal("seeded cache entry should be present before MarkDirty")
	}

	// MarkDirty should evict all keys containing "tracker_model.go".
	dt := NewDirtyTracker()
	dt.MarkDirty("tracker_model.go", "write")

	orchestratorCache.mu.RLock()
	_, stillPresent := orchestratorCache.items[key]
	orchestratorCache.mu.RUnlock()

	if stillPresent {
		t.Error("cache entry containing the dirty path should have been evicted by MarkDirty")
	}
}

// ---------------------------------------------------------------------------
// Thread safety
// ---------------------------------------------------------------------------

func TestDirtyTracker_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	dt := NewDirtyTracker()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			path := "file_" + string(rune('a'+n%26)) + ".go"
			dt.MarkDirty(path, "write")
			_ = dt.IsDirty(path)
			_ = dt.DirtyPaths()
			_ = dt.RAGDirtyPaths()
			_ = dt.ChangeLog()
		}(i)
	}
	wg.Wait()
}
