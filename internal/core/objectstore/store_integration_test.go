package objectstore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mingzhi1/coden/internal/core/objectstore"
)

// TestSQLiteStoreIntegration tests the full object store lifecycle with SQLite backend.
func TestSQLiteStoreIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := objectstore.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	// Test 1: Save multiple objects for same turn
	t.Run("save multiple objects", func(t *testing.T) {
		payload1 := map[string]any{
			"tool_call_id": "call-1",
			"tool":         "write_file",
			"status":       "written",
		}
		payload2 := map[string]any{
			"tool_call_id": "call-2",
			"tool":         "write_file",
			"status":       "written",
		}

		obj1, err := store.SaveModify("turn-1", "file1.go", dbPath, payload1)
		if err != nil {
			t.Fatalf("save obj1: %v", err)
		}
		obj2, err := store.SaveModify("turn-1", "file2.go", dbPath, payload2)
		if err != nil {
			t.Fatalf("save obj2: %v", err)
		}

		if obj1.Sequence != 1 {
			t.Errorf("obj1 sequence = %d, want 1", obj1.Sequence)
		}
		if obj2.Sequence != 2 {
			t.Errorf("obj2 sequence = %d, want 2", obj2.Sequence)
		}

		// List and verify
		list := store.ListByTurn("turn-1")
		if len(list) != 2 {
			t.Errorf("list len = %d, want 2", len(list))
		}
	})

	// Test 2: Same file path creates chain with PrevObjectID
	t.Run("object chain for same file", func(t *testing.T) {
		payload1 := map[string]any{"version": 1}
		payload2 := map[string]any{"version": 2}

		obj1, err := store.SaveModify("turn-2", "chain.go", dbPath, payload1)
		if err != nil {
			t.Fatalf("save obj1: %v", err)
		}
		obj2, err := store.SaveModify("turn-2", "chain.go", dbPath, payload2)
		if err != nil {
			t.Fatalf("save obj2: %v", err)
		}

		if obj1.PrevObjectID != "" {
			t.Errorf("first obj PrevObjectID = %q, want empty", obj1.PrevObjectID)
		}
		if obj2.PrevObjectID != obj1.ID {
			t.Errorf("second obj PrevObjectID = %q, want %q", obj2.PrevObjectID, obj1.ID)
		}
	})

	// Test 3: Read payload back
	t.Run("read payload", func(t *testing.T) {
		payload := map[string]any{
			"schema": "tool_audit.v1",
			"tool":   "write_file",
			"nested": map[string]any{"key": "value"},
		}

		obj, err := store.SaveModify("turn-3", "test.go", dbPath, payload)
		if err != nil {
			t.Fatalf("save: %v", err)
		}

		raw, err := store.ReadPayload(obj.ID)
		if err != nil {
			t.Fatalf("read payload: %v", err)
		}

		// Verify it's valid JSON
		if len(raw) == 0 {
			t.Error("payload is empty")
		}
	})

	// Test 4: Read non-existent object
	t.Run("read non-existent", func(t *testing.T) {
		_, err := store.ReadPayload("non-existent-id")
		if err == nil {
			t.Error("expected error for non-existent object")
		}
	})

	// Test 5: Empty turn returns empty list
	t.Run("empty turn", func(t *testing.T) {
		list := store.ListByTurn("never-used-turn")
		if len(list) != 0 {
			t.Errorf("expected empty list, got %d items", len(list))
		}
	})
}

// TestMemoryStore tests the in-memory store implementation.
func TestMemoryStore(t *testing.T) {
	store := objectstore.NewStore()
	defer store.Close()

	t.Run("basic operations", func(t *testing.T) {
		payload := map[string]any{"key": "value"}

		obj, err := store.SaveModify("turn-1", "test.go", "/tmp", payload)
		if err != nil {
			t.Fatalf("save: %v", err)
		}

		list := store.ListByTurn("turn-1")
		if len(list) != 1 {
			t.Errorf("list len = %d, want 1", len(list))
		}

		raw, err := store.ReadPayload(obj.ID)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(raw) == 0 {
			t.Error("payload is empty")
		}
	})
}

// TestStorePersistence verifies data survives store reopen.
func TestStorePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "persist.db")

	// Create and populate store
	store1, err := objectstore.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store1: %v", err)
	}

	payload := map[string]any{"data": "persistent"}
	obj, err := store1.SaveModify("turn-persist", "file.go", dbPath, payload)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	store1.Close()

	// Reopen and verify
	store2, err := objectstore.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store2: %v", err)
	}
	defer store2.Close()

	list := store2.ListByTurn("turn-persist")
	if len(list) != 1 {
		t.Fatalf("expected 1 object, got %d", len(list))
	}
	if list[0].ID != obj.ID {
		t.Errorf("object ID mismatch: got %q, want %q", list[0].ID, obj.ID)
	}

	raw, err := store2.ReadPayload(obj.ID)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if len(raw) == 0 {
		t.Error("payload is empty after reopen")
	}
}

// TestConcurrentAccess tests thread safety.
func TestConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "concurrent.db")

	store, err := objectstore.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	// Run concurrent saves
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			payload := map[string]any{"idx": idx}
			_, err := store.SaveModify("turn-concurrent", "file.go", dbPath, payload)
			if err != nil {
				t.Errorf("save %d: %v", idx, err)
			}
			done <- true
		}(i)
	}

	// Wait for all
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all objects saved
	list := store.ListByTurn("turn-concurrent")
	if len(list) != 10 {
		t.Errorf("expected 10 objects, got %d", len(list))
	}
}

// TestPayloadFileCreation verifies payload files are created on disk.
func TestPayloadFileCreation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "files.db")

	store, err := objectstore.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	payload := map[string]any{"test": "data"}
	obj, err := store.SaveModify("turn-file", "test.go", dbPath, payload)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(obj.StoragePath); os.IsNotExist(err) {
		t.Errorf("payload file not created at %s", obj.StoragePath)
	}
}
