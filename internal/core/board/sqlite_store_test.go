package board

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := NewSQLiteStore(db)
	if err := store.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	return store
}

func newBoard(id string) *Board {
	now := time.Now().UTC().Truncate(time.Second)
	return &Board{
		ID:        id,
		Name:      "Test Board " + id,
		ProjectID: "proj-1",
		Columns:   DefaultColumns(),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func newCard(id, boardID string) *Card {
	now := time.Now().UTC().Truncate(time.Second)
	return &Card{
		ID:        id,
		BoardID:   boardID,
		ColumnID:  ColumnBacklog,
		Position:  0,
		Title:     "Task " + id,
		Priority:  PriorityMedium,
		Labels:    []string{"bug", "backend"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ---- Board CRUD ----

func TestBoard_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	b := newBoard("board-1")
	if err := s.CreateBoard(b); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetBoard("board-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != b.Name {
		t.Errorf("name: got %q want %q", got.Name, b.Name)
	}
	if len(got.Columns) != len(b.Columns) {
		t.Errorf("columns len: got %d want %d", len(got.Columns), len(b.Columns))
	}
}

func TestBoard_ListBoards(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"board-a", "board-b", "board-c"} {
		if err := s.CreateBoard(newBoard(id)); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	boards, err := s.ListBoards()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(boards) != 3 {
		t.Errorf("expected 3 boards, got %d", len(boards))
	}
}

func TestBoard_UpdateAndDelete(t *testing.T) {
	s := newTestStore(t)
	b := newBoard("board-upd")
	if err := s.CreateBoard(b); err != nil {
		t.Fatalf("create: %v", err)
	}
	b.Name = "Renamed Board"
	b.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateBoard(b); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.GetBoard("board-upd")
	if got.Name != "Renamed Board" {
		t.Errorf("name after update: got %q", got.Name)
	}
	if err := s.DeleteBoard("board-upd"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetBoard("board-upd"); err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestBoard_UpdateNotFound(t *testing.T) {
	s := newTestStore(t)
	b := newBoard("ghost")
	if err := s.UpdateBoard(b); err == nil {
		t.Error("expected error for missing board, got nil")
	}
}

// ---- Card CRUD ----

func TestCard_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateBoard(newBoard("board-1")); err != nil {
		t.Fatal(err)
	}
	c := newCard("card-1", "board-1")
	if err := s.CreateCard(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetCard("card-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != c.Title {
		t.Errorf("title: got %q want %q", got.Title, c.Title)
	}
	if len(got.Labels) != 2 {
		t.Errorf("labels: got %v", got.Labels)
	}
}

func TestCard_ListCardsByColumn(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateBoard(newBoard("b1")); err != nil {
		t.Fatal(err)
	}
	c1 := newCard("c1", "b1")
	c2 := newCard("c2", "b1")
	c3 := newCard("c3", "b1")
	c2.ColumnID = ColumnInProgress
	for _, c := range []*Card{c1, c2, c3} {
		if err := s.CreateCard(c); err != nil {
			t.Fatal(err)
		}
	}
	backlog, err := s.ListCardsByColumn("b1", ColumnBacklog)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlog) != 2 {
		t.Errorf("expected 2 backlog cards, got %d", len(backlog))
	}
	inprog, err := s.ListCardsByColumn("b1", ColumnInProgress)
	if err != nil {
		t.Fatal(err)
	}
	if len(inprog) != 1 {
		t.Errorf("expected 1 in-progress card, got %d", len(inprog))
	}
}

func TestCard_MoveCard(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateBoard(newBoard("b1")); err != nil {
		t.Fatal(err)
	}
	c := newCard("c1", "b1")
	if err := s.CreateCard(c); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveCard("c1", ColumnDone, 1); err != nil {
		t.Fatalf("move: %v", err)
	}
	got, _ := s.GetCard("c1")
	if got.ColumnID != ColumnDone {
		t.Errorf("column after move: got %q", got.ColumnID)
	}
}

func TestCard_DeleteCard(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateBoard(newBoard("b1")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateCard(newCard("c1", "b1")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCard("c1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetCard("c1"); err == nil {
		t.Error("expected error after delete")
	}
}

// ---- Link CRUD ----

func TestLink_AddAndGet(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateBoard(newBoard("b1")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c1", "c2"} {
		if err := s.CreateCard(newCard(id, "b1")); err != nil {
			t.Fatal(err)
		}
	}
	l := &Link{ID: "link-1", FromCard: "c1", ToCard: "c2", Type: LinkBlocks}
	if err := s.AddLink(l); err != nil {
		t.Fatalf("add link: %v", err)
	}
	links, err := s.GetLinks("c1")
	if err != nil {
		t.Fatalf("get links: %v", err)
	}
	if len(links) != 1 || links[0].Type != LinkBlocks {
		t.Errorf("unexpected links: %+v", links)
	}
}

func TestLink_Remove(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateBoard(newBoard("b1")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c1", "c2"} {
		if err := s.CreateCard(newCard(id, "b1")); err != nil {
			t.Fatal(err)
		}
	}
	l := &Link{ID: "link-1", FromCard: "c1", ToCard: "c2", Type: LinkRelatesTo}
	if err := s.AddLink(l); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLink("link-1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	links, _ := s.GetLinks("c1")
	if len(links) != 0 {
		t.Errorf("expected no links after remove, got %d", len(links))
	}
}

// ---- Helpers ----

func TestColumnForStatus(t *testing.T) {
	cases := []struct {
		status string
		col    string
	}{
		{"planned", ColumnBacklog},
		{"coding", ColumnInProgress},
		{"accepting", ColumnReview},
		{"passed", ColumnDone},
		{"failed", ColumnBlocked},
		{"retrying", ColumnInProgress},
		{"abandoned", ColumnDone},
		{"unknown", ColumnBacklog},
	}
	for _, tc := range cases {
		got := ColumnForStatus(tc.status)
		if got != tc.col {
			t.Errorf("ColumnForStatus(%q) = %q, want %q", tc.status, got, tc.col)
		}
	}
}

func TestIsBlocked(t *testing.T) {
	now := time.Now().UTC()
	blocker := &Card{ID: "blocker", ColumnID: ColumnBacklog, CreatedAt: now, UpdatedAt: now}
	done := &Card{ID: "done", ColumnID: ColumnDone, CreatedAt: now, UpdatedAt: now}
	dep := &Card{ID: "dep", DependsOn: []string{"blocker"}, ColumnID: ColumnBacklog, CreatedAt: now, UpdatedAt: now}
	free := &Card{ID: "free", DependsOn: []string{"done"}, ColumnID: ColumnBacklog, CreatedAt: now, UpdatedAt: now}

	index := map[string]*Card{"blocker": blocker, "done": done, "dep": dep, "free": free}

	if !dep.IsBlocked(index) {
		t.Error("dep should be blocked")
	}
	if free.IsBlocked(index) {
		t.Error("free should not be blocked")
	}
}
