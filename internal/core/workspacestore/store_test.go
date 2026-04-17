package workspacestore

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStoreEnsureByRootPersistsWorkspace(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "main.sqlite")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	root := filepath.Join(t.TempDir(), "workspace-alpha")
	first, err := store.EnsureByRoot(root)
	if err != nil {
		t.Fatalf("EnsureByRoot failed: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected workspace id")
	}
	if first.Root != root {
		t.Fatalf("unexpected workspace root: %+v", first)
	}

	second, err := store.EnsureByRoot(root)
	if err != nil {
		t.Fatalf("EnsureByRoot second call failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected stable workspace id, got first=%q second=%q", first.ID, second.ID)
	}

	got, ok := store.GetByRoot(root)
	if !ok {
		t.Fatal("expected persisted workspace")
	}
	if got.ID != first.ID {
		t.Fatalf("unexpected persisted workspace: %+v", got)
	}
}
