package intent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

func TestSQLiteStorePersistsLatestIntent(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Save(model.IntentSpec{
		ID:              "intent-1",
		SessionID:       "session-1",
		Goal:            "ship feature",
		SuccessCriteria: []string{"tests pass", "artifact exists"},
		CreatedAt:       time.Now(),
	}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, ok := store.Latest("session-1")
	if !ok {
		t.Fatal("expected persisted intent")
	}
	if got.Goal != "ship feature" {
		t.Fatalf("unexpected goal: %q", got.Goal)
	}
	if len(got.SuccessCriteria) != 2 {
		t.Fatalf("unexpected success criteria: %+v", got.SuccessCriteria)
	}
}
