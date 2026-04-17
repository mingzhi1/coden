package turnsummary

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

func makeSummary(id, turnID, sessionID string, createdAt time.Time) model.TurnSummary {
	return model.TurnSummary{
		ID:        id,
		TurnID:    turnID,
		SessionID: sessionID,
		Intent: model.IntentSpec{
			ID:              "intent-" + turnID,
			SessionID:       sessionID,
			Goal:            "test goal for " + turnID,
			SuccessCriteria: []string{"criterion-1"},
		},
		TaskResults: []model.TaskResult{
			{TaskID: "t1", Title: "task one", Status: model.TaskStatusPassed, Attempts: 1, FilesWritten: []string{"a.go"}},
			{TaskID: "t2", Title: "task two", Status: model.TaskStatusFailed, Attempts: 2},
		},
		ChangedFiles: []model.FileChange{
			{Path: "a.go", Op: model.FileChangeCreated, LinesAdded: 10},
			{Path: "b.go", Op: model.FileChangeModified, LinesAdded: 5, LinesRemoved: 2},
		},
		Checkpoint: model.CheckpointResult{
			WorkflowID:    turnID,
			SessionID:     sessionID,
			Status:        "pass",
			ArtifactPaths: []string{"a.go"},
			Evidence:      []string{"compiles ok"},
		},
		CreatedAt: createdAt,
	}
}

func testStore(t *testing.T, store Store) {
	t.Helper()

	now := time.Now().UTC()
	first := makeSummary("ts-1", "wf-1", "session-1", now)
	second := makeSummary("ts-2", "wf-2", "session-1", now.Add(time.Second))
	other := makeSummary("ts-3", "wf-3", "session-2", now)

	for _, s := range []model.TurnSummary{first, second, other} {
		if err := store.Save(s); err != nil {
			t.Fatalf("Save(%s) failed: %v", s.ID, err)
		}
	}

	// Get by turn ID.
	got, ok := store.Get("wf-1")
	if !ok {
		t.Fatal("expected to find wf-1")
	}
	if got.ID != "ts-1" || got.Intent.Goal != first.Intent.Goal {
		t.Fatalf("unexpected summary for wf-1: %+v", got)
	}
	if len(got.TaskResults) != 2 || got.TaskResults[0].TaskID != "t1" {
		t.Fatalf("unexpected task results: %+v", got.TaskResults)
	}
	if len(got.ChangedFiles) != 2 || got.ChangedFiles[0].Path != "a.go" {
		t.Fatalf("unexpected changed files: %+v", got.ChangedFiles)
	}
	if got.Checkpoint.Status != "pass" {
		t.Fatalf("unexpected checkpoint status: %s", got.Checkpoint.Status)
	}

	// Get non-existent.
	_, ok = store.Get("wf-missing")
	if ok {
		t.Fatal("expected not found for missing turn")
	}

	// ListBySession returns most recent first.
	list := store.ListBySession("session-1", 10)
	if len(list) != 2 {
		t.Fatalf("expected 2 summaries for session-1, got %d", len(list))
	}
	if list[0].TurnID != "wf-2" || list[1].TurnID != "wf-1" {
		t.Fatalf("unexpected order: %s, %s", list[0].TurnID, list[1].TurnID)
	}

	// ListBySession with limit.
	list = store.ListBySession("session-1", 1)
	if len(list) != 1 || list[0].TurnID != "wf-2" {
		t.Fatalf("expected 1 most-recent summary, got %+v", list)
	}

	// ListBySession for other session.
	list = store.ListBySession("session-2", 10)
	if len(list) != 1 || list[0].TurnID != "wf-3" {
		t.Fatalf("expected 1 summary for session-2, got %+v", list)
	}

	// Empty session returns nil.
	list = store.ListBySession("no-session", 10)
	if list != nil {
		t.Fatalf("expected nil for empty session, got %+v", list)
	}
}

func TestMemoryStore(t *testing.T) {
	t.Parallel()
	testStore(t, NewStore())
}

func TestMemoryStoreReturnsCopies(t *testing.T) {
	t.Parallel()

	store := NewStore()
	s := makeSummary("ts-1", "wf-1", "session-1", time.Now())
	if err := store.Save(s); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, _ := store.Get("wf-1")
	got.TaskResults[0].Title = "mutated"
	got.ChangedFiles[0].Path = "mutated.go"
	got.Checkpoint.Evidence[0] = "mutated"

	again, _ := store.Get("wf-1")
	if again.TaskResults[0].Title == "mutated" || again.ChangedFiles[0].Path == "mutated.go" || again.Checkpoint.Evidence[0] == "mutated" {
		t.Fatal("store returned mutable backing slices")
	}
}

func TestSQLiteStore(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	testStore(t, store)
}

func TestSQLiteStoreUpsert(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	s := makeSummary("ts-1", "wf-1", "session-1", time.Now())
	if err := store.Save(s); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Update with same turn_id.
	s.Checkpoint.Status = "fail"
	if err := store.Save(s); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	got, ok := store.Get("wf-1")
	if !ok {
		t.Fatal("expected summary after upsert")
	}
	if got.Checkpoint.Status != "fail" {
		t.Fatalf("expected upserted status 'fail', got %q", got.Checkpoint.Status)
	}
}
