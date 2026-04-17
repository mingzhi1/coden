package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

func TestMemoryStoreSaveAndGet(t *testing.T) {
	t.Parallel()
	store := NewStore()
	now := time.Now()
	if err := store.Save(model.Session{ID: "s1", ProjectID: "p1", ProjectRoot: "D:\\projects\\p1", CreatedAt: now}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	got, ok := store.Get("s1")
	if !ok || got.ID != "s1" {
		t.Fatalf("unexpected session: %+v ok=%v", got, ok)
	}
	if got.ProjectID != "p1" || got.ProjectRoot != "D:\\projects\\p1" {
		t.Fatalf("unexpected session metadata: %+v", got)
	}
}

func TestSQLiteStorePersistsSessions(t *testing.T) {
	t.Parallel()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()
	if err := store.Save(model.Session{ID: "s1", ProjectID: "p1", ProjectRoot: "D:\\projects\\p1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	got, ok := store.Get("s1")
	if !ok {
		t.Fatal("expected persisted session")
	}
	if got.ProjectID != "p1" || got.ProjectRoot != "D:\\projects\\p1" {
		t.Fatalf("unexpected persisted session metadata: %+v", got)
	}
}
