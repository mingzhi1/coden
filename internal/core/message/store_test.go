package message

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

func TestStoreListsMessagesInOrder(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.Save(model.Message{ID: "m1", SessionID: "s1", Role: "user", Content: "hello", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Save first failed: %v", err)
	}
	if err := store.Save(model.Message{ID: "m2", SessionID: "s1", Role: "assistant", Content: "done", CreatedAt: time.Now().Add(time.Second)}); err != nil {
		t.Fatalf("Save second failed: %v", err)
	}

	got := store.List("s1", 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].ID != "m1" || got[1].ID != "m2" {
		t.Fatalf("unexpected ordering: %+v", got)
	}
}

func TestSQLiteStorePersistsMessages(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Save(model.Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      "user",
		Content:   "hello",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got := store.List("s1", 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].Content != "hello" || got[0].Role != "user" {
		t.Fatalf("unexpected message: %+v", got[0])
	}
}

func TestStoreLimitReturnsLatestMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		newFn func(t *testing.T) Store
	}{
		{
			name: "memory",
			newFn: func(t *testing.T) Store {
				t.Helper()
				return NewStore()
			},
		},
		{
			name: "sqlite",
			newFn: func(t *testing.T) Store {
				t.Helper()
				store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
				if err != nil {
					t.Fatalf("NewSQLiteStore failed: %v", err)
				}
				return store
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := tc.newFn(t)
			defer store.Close()

			base := time.Now()
			messages := []model.Message{
				{ID: "m1", SessionID: "s1", Role: "user", Content: "one", CreatedAt: base},
				{ID: "m2", SessionID: "s1", Role: "assistant", Content: "two", CreatedAt: base.Add(time.Second)},
				{ID: "m3", SessionID: "s1", Role: "user", Content: "three", CreatedAt: base.Add(2 * time.Second)},
			}
			for _, msg := range messages {
				if err := store.Save(msg); err != nil {
					t.Fatalf("Save(%s) failed: %v", msg.ID, err)
				}
			}

			got := store.List("s1", 2)
			if len(got) != 2 {
				t.Fatalf("expected 2 messages, got %d", len(got))
			}
			if got[0].ID != "m2" || got[1].ID != "m3" {
				t.Fatalf("expected latest two messages in chronological order, got %+v", got)
			}
		})
	}
}
