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

// TestBucketSnapshotRoundTrip verifies SaveBucket/LatestBucket persist a snapshot
// and return the highest-seq one, for both the memory and SQLite stores.
func TestBucketSnapshotRoundTrip(t *testing.T) {
	t.Parallel()
	stores := map[string]Store{"memory": NewStore()}
	sq, err := NewSQLiteStore(filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer sq.Close()
	stores["sqlite"] = sq

	for name, store := range stores {
		t.Run(name, func(t *testing.T) {
			mk := func(seq int, st string) model.BucketSnapshot {
				return model.BucketSnapshot{
					SessionID: "s1", WorkflowID: "wf1", Seq: seq,
					Bucket:    []model.Task{{ID: "t1", Status: st}},
					Completed: map[string]string{"t1": st},
					Artifacts: map[string]model.Artifact{"t1": {Path: "t1.go", Evidence: []string{"ok"}}},
					CreatedAt: time.Now(),
				}
			}
			if err := store.SaveBucket(mk(1, "coding")); err != nil {
				t.Fatalf("SaveBucket seq1: %v", err)
			}
			if err := store.SaveBucket(mk(2, "passed")); err != nil {
				t.Fatalf("SaveBucket seq2: %v", err)
			}
			// An older seq must not regress the latest.
			if err := store.SaveBucket(mk(1, "stale")); err != nil {
				t.Fatalf("SaveBucket stale: %v", err)
			}

			got, ok := store.LatestBucket("s1", "wf1")
			if !ok {
				t.Fatal("LatestBucket missing")
			}
			if got.Seq != 2 {
				t.Errorf("expected latest seq 2, got %d", got.Seq)
			}
			if got.Completed["t1"] != "passed" || got.Artifacts["t1"].Path != "t1.go" {
				t.Errorf("snapshot fields lost: %+v", got)
			}
			if _, ok := store.LatestBucket("s1", "other"); ok {
				t.Error("expected no snapshot for unknown workflow")
			}
		})
	}
}
