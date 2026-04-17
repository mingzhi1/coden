package objectstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteStorePersistsModifyObjects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mainDBPath := filepath.Join(root, "main.sqlite")
	workspaceDBPath := filepath.Join(root, "workspace.sqlite")

	store, err := NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	item, err := store.SaveModify("turn-1", "internal/core/kernel/kernel.go", mainDBPath, map[string]any{
		"op":      "write_file",
		"path":    "internal/core/kernel/kernel.go",
		"content": "hello",
	})
	if err != nil {
		t.Fatalf("SaveModify failed: %v", err)
	}
	if _, err := os.Stat(item.StoragePath); err != nil {
		t.Fatalf("expected object payload file: %v", err)
	}

	got := store.ListByTurn("turn-1")
	if len(got) != 1 {
		t.Fatalf("expected 1 object, got %d", len(got))
	}
	if got[0].FilePath != "internal/core/kernel/kernel.go" || got[0].Kind != "modify" {
		t.Fatalf("unexpected object: %+v", got[0])
	}
	if got[0].Sequence != 1 || got[0].PrevObjectID != "" {
		t.Fatalf("unexpected root object chain data: %+v", got[0])
	}
}

func TestSQLiteStoreChainsSameFileModificationsWithinTurn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mainDBPath := filepath.Join(root, "main.sqlite")
	workspaceDBPath := filepath.Join(root, "workspace.sqlite")

	store, err := NewSQLiteStore(workspaceDBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	first, err := store.SaveModify("turn-1", "a.txt", mainDBPath, map[string]any{"content": "one"})
	if err != nil {
		t.Fatalf("SaveModify first failed: %v", err)
	}
	second, err := store.SaveModify("turn-1", "b.txt", mainDBPath, map[string]any{"content": "two"})
	if err != nil {
		t.Fatalf("SaveModify second failed: %v", err)
	}
	third, err := store.SaveModify("turn-1", "a.txt", mainDBPath, map[string]any{"content": "three"})
	if err != nil {
		t.Fatalf("SaveModify third failed: %v", err)
	}

	if first.Sequence != 1 || first.PrevObjectID != "" {
		t.Fatalf("unexpected first object: %+v", first)
	}
	if second.Sequence != 2 || second.PrevObjectID != "" {
		t.Fatalf("unexpected second object: %+v", second)
	}
	if third.Sequence != 3 {
		t.Fatalf("expected third sequence 3, got %+v", third)
	}
	if third.PrevObjectID != first.ID {
		t.Fatalf("expected third prev_object_id %q, got %+v", first.ID, third)
	}
}
