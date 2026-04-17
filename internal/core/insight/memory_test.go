package insight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteMemoryFile_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	err := WriteMemoryFile(dir, "s1", store)
	if err != nil {
		t.Fatalf("unexpected error on empty store: %v", err)
	}
	// No file should be written when there are no insights.
	_, statErr := os.Stat(filepath.Join(dir, ".coden", "MEMORY.md"))
	if !os.IsNotExist(statErr) {
		t.Error("expected no MEMORY.md for empty store")
	}
}

func TestWriteMemoryFile_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	_ = store.Save(Insight{
		ID:         "i1",
		SessionID:  "s1",
		Category:   CategoryDecision,
		Title:      "Use SQLite for storage",
		Content:    "Evaluated postgres and sqlite; sqlite wins for single-node MVP.",
		Confidence: 0.9,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	})

	err := WriteMemoryFile(dir, "s1", store)
	if err != nil {
		t.Fatalf("WriteMemoryFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".coden", "MEMORY.md"))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Use SQLite for storage") {
		t.Error("expected insight title in memory file")
	}
	if !strings.Contains(content, "## Decisions") {
		t.Error("expected Decisions heading")
	}
	if !strings.Contains(content, "# Session Memory") {
		t.Error("expected Session Memory heading")
	}
}

func TestWriteMemoryFile_SkipsSuperseded(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	_ = store.Save(Insight{
		ID:           "old",
		SessionID:    "s1",
		Category:     CategoryFinding,
		Title:        "Old finding",
		Content:      "This was replaced.",
		Confidence:   0.8,
		SupersededBy: "new",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	})
	_ = store.Save(Insight{
		ID:        "new",
		SessionID: "s1",
		Category:  CategoryFinding,
		Title:     "Updated finding",
		Content:   "The replacement.",
		Confidence: 0.95,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	err := WriteMemoryFile(dir, "s1", store)
	if err != nil {
		t.Fatalf("WriteMemoryFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".coden", "MEMORY.md"))
	content := string(data)
	if strings.Contains(content, "Old finding") {
		t.Error("superseded insight should not appear in memory file")
	}
	if !strings.Contains(content, "Updated finding") {
		t.Error("replacement insight should appear in memory file")
	}
}

func TestWriteMemoryFile_MultipleCategories(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	now := time.Now()
	for _, tc := range []struct {
		id, title string
		cat       Category
	}{
		{"d1", "Design choice", CategoryDesign},
		{"f1", "Key finding", CategoryFinding},
		{"c1", "A vs B comparison", CategoryComparison},
		{"de1", "Architecture decision", CategoryDecision},
	} {
		_ = store.Save(Insight{
			ID: tc.id, SessionID: "s1", Category: tc.cat,
			Title: tc.title, Content: "...", Confidence: 0.8,
			CreatedAt: now, UpdatedAt: now,
		})
	}

	err := WriteMemoryFile(dir, "s1", store)
	if err != nil {
		t.Fatalf("WriteMemoryFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".coden", "MEMORY.md"))
	content := string(data)

	for _, heading := range []string{"## Decisions", "## Designs", "## Findings", "## Comparisons"} {
		if !strings.Contains(content, heading) {
			t.Errorf("expected heading %q in memory file", heading)
		}
	}
}

func TestWriteMemoryFile_IdempotentOnRewrite(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	_ = store.Save(Insight{
		ID: "i1", SessionID: "s1", Category: CategoryDecision,
		Title: "Some decision", Content: "details", Confidence: 0.7,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// Write twice — second write should not error and produce consistent content.
	if err := WriteMemoryFile(dir, "s1", store); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteMemoryFile(dir, "s1", store); err != nil {
		t.Fatalf("second write: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".coden", "MEMORY.md"))
	if !strings.Contains(string(data), "Some decision") {
		t.Error("expected content after idempotent rewrite")
	}
}

func TestWriteMemoryFile_IsolatedBySession(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	_ = store.Save(Insight{
		ID: "a1", SessionID: "session-A", Category: CategoryDecision,
		Title: "Session A decision", Content: "...", Confidence: 0.8,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	_ = store.Save(Insight{
		ID: "b1", SessionID: "session-B", Category: CategoryDecision,
		Title: "Session B decision", Content: "...", Confidence: 0.8,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	// Write memory for session-A only.
	err := WriteMemoryFile(dir, "session-A", store)
	if err != nil {
		t.Fatalf("WriteMemoryFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".coden", "MEMORY.md"))
	content := string(data)
	if !strings.Contains(content, "Session A decision") {
		t.Error("expected session-A insight")
	}
	if strings.Contains(content, "Session B decision") {
		t.Error("session-B insight should not appear in session-A memory")
	}
}
