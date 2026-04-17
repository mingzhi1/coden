package turn

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

func TestSQLiteStorePersistsTurns(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	now := time.Now()
	item := model.Turn{
		ID:         "turn-1",
		SessionID:  "session-1",
		WorkflowID: "wf-1",
		Prompt:     "build feature",
		Status:     "running",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.Save(item); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if err := store.UpdateStatus("turn-1", "done", now.Add(time.Second)); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	got, ok := store.Get("turn-1")
	if !ok {
		t.Fatal("expected persisted turn")
	}
	if got.WorkflowID != "wf-1" || got.Status != "done" {
		t.Fatalf("unexpected turn: %+v", got)
	}
}

func TestSQLiteStoreListsSessionTurnsNewestFirst(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	now := time.Now()
	items := []model.Turn{
		{
			ID:         "turn-1",
			SessionID:  "session-1",
			WorkflowID: "wf-1",
			Prompt:     "first",
			Status:     "pass",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		{
			ID:         "turn-2",
			SessionID:  "session-1",
			WorkflowID: "wf-2",
			Prompt:     "second",
			Status:     "running",
			CreatedAt:  now.Add(time.Second),
			UpdatedAt:  now.Add(time.Second),
		},
		{
			ID:         "turn-3",
			SessionID:  "session-2",
			WorkflowID: "wf-3",
			Prompt:     "other",
			Status:     "pass",
			CreatedAt:  now.Add(2 * time.Second),
			UpdatedAt:  now.Add(2 * time.Second),
		},
	}
	for _, item := range items {
		if err := store.Save(item); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	got := store.ListBySession("session-1", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(got))
	}
	if got[0].ID != "turn-2" || got[1].ID != "turn-1" {
		t.Fatalf("unexpected turn order: %+v", got)
	}
}
