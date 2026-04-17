package checkpoint

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

func TestStoreTracksLatestAndHistory(t *testing.T) {
	t.Parallel()

	store := NewStore()
	first := model.CheckpointResult{
		SessionID:     "session-1",
		WorkflowID:    "wf-1",
		Status:        "pass",
		ArtifactPaths: []string{"one.md"},
		Evidence:      []string{"one"},
		CreatedAt:     time.Now(),
	}
	second := model.CheckpointResult{
		SessionID:     "session-1",
		WorkflowID:    "wf-2",
		Status:        "pass",
		ArtifactPaths: []string{"two.md"},
		Evidence:      []string{"two"},
		CreatedAt:     time.Now().Add(time.Second),
	}

	if err := store.Save(first); err != nil {
		t.Fatalf("Save first failed: %v", err)
	}
	if err := store.Save(second); err != nil {
		t.Fatalf("Save second failed: %v", err)
	}

	latest, ok := store.Latest("session-1")
	if !ok {
		t.Fatal("expected latest checkpoint")
	}
	if latest.WorkflowID != "wf-2" {
		t.Fatalf("expected latest wf-2, got %q", latest.WorkflowID)
	}

	gotLatest, ok := store.Get("session-1", "")
	if !ok {
		t.Fatal("expected latest checkpoint via empty workflow id")
	}
	if gotLatest.WorkflowID != "wf-2" {
		t.Fatalf("expected Get(session, \"\") to return wf-2, got %q", gotLatest.WorkflowID)
	}

	got, ok := store.Get("session-1", "wf-1")
	if !ok {
		t.Fatal("expected workflow checkpoint")
	}
	if got.ArtifactPaths[0] != "one.md" {
		t.Fatalf("unexpected workflow artifact paths: %+v", got.ArtifactPaths)
	}

	list := store.List("session-1", 0)
	if len(list) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(list))
	}
	if list[0].WorkflowID != "wf-2" || list[1].WorkflowID != "wf-1" {
		t.Fatalf("unexpected list ordering: %+v", []string{list[0].WorkflowID, list[1].WorkflowID})
	}
}

func TestStoreReturnsCopies(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.Save(model.CheckpointResult{
		SessionID:     "session-1",
		WorkflowID:    "wf-1",
		ArtifactPaths: []string{"one.md"},
		Evidence:      []string{"one"},
	}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, ok := store.Get("session-1", "wf-1")
	if !ok {
		t.Fatal("expected checkpoint")
	}
	got.ArtifactPaths[0] = "mutated.md"
	got.Evidence[0] = "mutated"

	again, ok := store.Get("session-1", "wf-1")
	if !ok {
		t.Fatal("expected checkpoint again")
	}
	if again.ArtifactPaths[0] != "one.md" || again.Evidence[0] != "one" {
		t.Fatalf("store returned mutable backing slices: %+v", again)
	}
}

func TestSQLiteStorePersistsHistory(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Save(model.CheckpointResult{
		SessionID:     "session-1",
		WorkflowID:    "wf-1",
		Status:        "pass",
		ArtifactPaths: []string{"one.md"},
		Evidence:      []string{"one"},
		CreatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("Save first failed: %v", err)
	}
	if err := store.Save(model.CheckpointResult{
		SessionID:     "session-1",
		WorkflowID:    "wf-2",
		Status:        "pass",
		ArtifactPaths: []string{"two.md"},
		Evidence:      []string{"two"},
		CreatedAt:     time.Now().Add(time.Second),
	}); err != nil {
		t.Fatalf("Save second failed: %v", err)
	}

	got, ok := store.Get("session-1", "wf-1")
	if !ok {
		t.Fatal("expected persisted checkpoint")
	}
	if got.ArtifactPaths[0] != "one.md" {
		t.Fatalf("unexpected checkpoint artifact paths: %+v", got.ArtifactPaths)
	}

	gotLatest, ok := store.Get("session-1", "")
	if !ok {
		t.Fatal("expected latest checkpoint via empty workflow id")
	}
	if gotLatest.WorkflowID != "wf-2" {
		t.Fatalf("expected Get(session, \"\") to return wf-2, got %q", gotLatest.WorkflowID)
	}

	list := store.List("session-1", 1)
	if len(list) != 1 || list[0].WorkflowID != "wf-2" {
		t.Fatalf("unexpected checkpoint list: %+v", list)
	}
}
