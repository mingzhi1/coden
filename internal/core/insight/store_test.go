package insight_test

import (
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/insight"
)

func TestMemoryStore_SaveAndList(t *testing.T) {
	s := insight.NewStore()
	defer s.Close()

	ins := insight.Insight{
		ID:         "ins-1",
		SessionID:  "session-alpha",
		Category:   insight.CategoryDecision,
		Title:      "Use SQLite for persistence",
		Content:    "SQLite was chosen over Postgres for simplicity.",
		Tags:       []string{"storage", "architecture"},
		Confidence: 0.8,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.Save(ins); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	out := s.ListBySession("session-alpha", 0)
	if len(out) != 1 {
		t.Fatalf("expected 1 insight, got %d", len(out))
	}
	if out[0].Title != ins.Title {
		t.Errorf("unexpected title: %q", out[0].Title)
	}
}

func TestMemoryStore_TopKByTags(t *testing.T) {
	s := insight.NewStore()
	defer s.Close()

	now := time.Now().UTC()
	for _, item := range []insight.Insight{
		{ID: "i1", SessionID: "s1", Category: insight.CategoryDecision, Title: "Decision A", Content: "CA", Tags: []string{"storage", "kernel"}, Confidence: 0.9, CreatedAt: now, UpdatedAt: now},
		{ID: "i2", SessionID: "s1", Category: insight.CategoryFinding, Title: "Finding B", Content: "CB", Tags: []string{"api"}, Confidence: 0.7, CreatedAt: now, UpdatedAt: now},
		{ID: "i3", SessionID: "s1", Category: insight.CategoryDesign, Title: "Design C", Content: "CC", Tags: []string{"kernel"}, Confidence: 0.6, CreatedAt: now, UpdatedAt: now},
		{ID: "i4", SessionID: "other", Category: insight.CategoryDecision, Title: "Other session", Content: "CO", Tags: []string{"storage"}, Confidence: 0.95, CreatedAt: now, UpdatedAt: now},
	} {
		if err := s.Save(item); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	results := s.TopKByTags("s1", []string{"kernel"}, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 insights with kernel tag, got %d", len(results))
	}
	// Should be ordered by confidence desc: i1 (0.9) before i3 (0.6).
	if results[0].ID != "i1" || results[1].ID != "i3" {
		t.Errorf("unexpected order: %v", results)
	}
}

func TestMemoryStore_Supersede(t *testing.T) {
	s := insight.NewStore()
	defer s.Close()

	now := time.Now().UTC()
	old := insight.Insight{ID: "old", SessionID: "s1", Category: insight.CategoryDecision, Title: "Old", Content: "old content", Confidence: 0.5, CreatedAt: now, UpdatedAt: now}
	if err := s.Save(old); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	newIns := insight.Insight{ID: "new", SessionID: "s1", Category: insight.CategoryDecision, Title: "New", Content: "new content", Confidence: 0.8, CreatedAt: now, UpdatedAt: now}
	if err := s.Supersede("old", newIns); err != nil {
		t.Fatalf("Supersede failed: %v", err)
	}

	// TopK should not return superseded old insight.
	results := s.TopKByTags("s1", []string{"decision"}, 10)
	for _, r := range results {
		if r.ID == "old" && r.SupersededBy == "" {
			t.Errorf("superseded insight should be marked: %+v", r)
		}
	}
	all := s.ListBySession("s1", 0)
	found := false
	for _, r := range all {
		if r.ID == "old" && r.SupersededBy == "new" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected old insight to be marked superseded")
	}
}

func TestExtractInsights_ZeroLLMCost(t *testing.T) {
	text := `We reviewed the options and made a key decision.

## 结论
We will use SQLite for persistence. This avoids operational complexity.

## 对比
方案A: Use PostgreSQL (heavy)
方案B: Use SQLite (lightweight ✅)

设计原则: Keep it simple, avoid external dependencies.
`
	now := time.Now().UTC()
	ins := insight.ExtractInsights("turn-123", text, now)
	if len(ins) == 0 {
		t.Fatal("expected at least one extracted insight")
	}
	for _, i := range ins {
		if i.Category == "" {
			t.Errorf("unexpected empty category: %+v", i)
		}
		if i.Title == "" || i.Content == "" {
			t.Errorf("unexpected empty title/content: %+v", i)
		}
		if i.Confidence <= 0 {
			t.Errorf("expected positive confidence: %+v", i)
		}
	}
}

func TestExtractInsights_NoDuplicates(t *testing.T) {
	// Same trigger line repeated twice should produce only one insight.
	text := "## 结论\nWe chose approach X.\n## 结论\nWe chose approach X.\n"
	ins := insight.ExtractInsights("turn-dup", text, time.Now())
	count := 0
	for _, i := range ins {
		if i.Category == insight.CategoryDecision {
			count++
		}
	}
	if count > 1 {
		t.Errorf("expected at most 1 decision insight from duplicate lines, got %d", count)
	}
}
