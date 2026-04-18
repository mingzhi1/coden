package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/board"
	"github.com/mingzhi1/coden/internal/core/events"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/web"
)

// ─── fake board.Store ────────────────────────────────────────────────────────

type fakeStore struct {
	mu     sync.Mutex
	boards map[string]*board.Board
	cards  map[string]*board.Card
	links  map[string]*board.Link
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		boards: make(map[string]*board.Board),
		cards:  make(map[string]*board.Card),
		links:  make(map[string]*board.Link),
	}
}

func (s *fakeStore) CreateBoard(b *board.Board) error {
	s.mu.Lock(); defer s.mu.Unlock()
	cp := *b; s.boards[b.ID] = &cp; return nil
}
func (s *fakeStore) GetBoard(id string) (*board.Board, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	b, ok := s.boards[id]
	if !ok { return nil, errors.New("not found") }
	cp := *b; return &cp, nil
}
func (s *fakeStore) ListBoards() ([]*board.Board, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	out := make([]*board.Board, 0, len(s.boards))
	for _, b := range s.boards { cp := *b; out = append(out, &cp) }
	return out, nil
}
func (s *fakeStore) UpdateBoard(b *board.Board) error {
	s.mu.Lock(); defer s.mu.Unlock()
	cp := *b; s.boards[b.ID] = &cp; return nil
}
func (s *fakeStore) DeleteBoard(id string) error {
	s.mu.Lock(); defer s.mu.Unlock()
	delete(s.boards, id); return nil
}
func (s *fakeStore) CreateCard(c *board.Card) error {
	s.mu.Lock(); defer s.mu.Unlock()
	cp := *c; s.cards[c.ID] = &cp; return nil
}
func (s *fakeStore) GetCard(id string) (*board.Card, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	c, ok := s.cards[id]
	if !ok { return nil, errors.New("not found") }
	cp := *c; return &cp, nil
}
func (s *fakeStore) ListCards(boardID string) ([]*board.Card, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	out := make([]*board.Card, 0)
	for _, c := range s.cards {
		if c.BoardID == boardID { cp := *c; out = append(out, &cp) }
	}
	return out, nil
}
func (s *fakeStore) ListCardsByColumn(boardID, col string) ([]*board.Card, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	var out []*board.Card
	for _, c := range s.cards {
		if c.BoardID == boardID && c.ColumnID == col { cp := *c; out = append(out, &cp) }
	}
	return out, nil
}
func (s *fakeStore) ListCardsByAssignee(agentID string) ([]*board.Card, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	var out []*board.Card
	for _, c := range s.cards {
		if c.AssigneeID == agentID { cp := *c; out = append(out, &cp) }
	}
	return out, nil
}
func (s *fakeStore) UpdateCard(c *board.Card) error {
	s.mu.Lock(); defer s.mu.Unlock()
	cp := *c; s.cards[c.ID] = &cp; return nil
}
func (s *fakeStore) MoveCard(cardID, toColumn string, pos int) error {
	s.mu.Lock(); defer s.mu.Unlock()
	if c, ok := s.cards[cardID]; ok { c.ColumnID = toColumn; c.Position = pos }
	return nil
}
func (s *fakeStore) DeleteCard(id string) error {
	s.mu.Lock(); defer s.mu.Unlock()
	delete(s.cards, id); return nil
}
func (s *fakeStore) AddLink(l *board.Link) error {
	s.mu.Lock(); defer s.mu.Unlock()
	cp := *l; s.links[l.ID] = &cp; return nil
}
func (s *fakeStore) RemoveLink(id string) error {
	s.mu.Lock(); defer s.mu.Unlock()
	delete(s.links, id); return nil
}
func (s *fakeStore) GetLinks(cardID string) ([]*board.Link, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	var out []*board.Link
	for _, l := range s.links {
		if l.FromCard == cardID || l.ToCard == cardID { cp := *l; out = append(out, &cp) }
	}
	return out, nil
}
// ─── test server helpers ─────────────────────────────────────────────────────

// newTestServer creates an httptest.Server backed by the web.Server's routes.
func newTestServer(t *testing.T, store board.Store) *httptest.Server {
	t.Helper()
	srv := web.New(web.Config{
		Store:    store,
		EventBus: events.NewBus(),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// do performs a JSON request and returns the response body as a map.
func doRequest(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// ─── Board tests ─────────────────────────────────────────────────────────────

func TestHandleListBoards_Empty(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/boards", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var boards []*board.Board
	decodeJSON(t, resp, &boards)
	if len(boards) != 0 {
		t.Fatalf("expected empty list, got %d boards", len(boards))
	}
}

func TestHandleCreateBoard_Success(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards", map[string]string{
		"name": "My Board",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var b board.Board
	decodeJSON(t, resp, &b)
	if b.ID == "" {
		t.Error("expected board ID to be set")
	}
	if b.Name != "My Board" {
		t.Errorf("Name = %q, want %q", b.Name, "My Board")
	}
	if len(b.Columns) == 0 {
		t.Error("expected default columns to be set")
	}
}

func TestHandleCreateBoard_DefaultName(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards", map[string]string{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var b board.Board
	decodeJSON(t, resp, &b)
	if b.Name != "Default Board" {
		t.Errorf("Name = %q, want %q", b.Name, "Default Board")
	}
}

func TestHandleGetBoard_Success(t *testing.T) {
	store := newFakeStore()
	ts := newTestServer(t, store)

	// Create a board first.
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards", map[string]string{"name": "Test"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create board status = %d", resp.StatusCode)
	}
	var created board.Board
	decodeJSON(t, resp, &created)

	// Fetch it.
	resp = doRequest(t, http.MethodGet, ts.URL+"/api/boards/"+created.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status = %d, want 200", resp.StatusCode)
	}
	var result struct {
		board.Board
		Cards []*board.Card `json:"cards"`
	}
	decodeJSON(t, resp, &result)
	if result.ID != created.ID {
		t.Errorf("ID = %q, want %q", result.ID, created.ID)
	}
	// Cards may be nil or empty when the board has no cards — both are valid.
	if result.Name != "Test" {
		t.Errorf("Name = %q, want %q", result.Name, "Test")
	}
}

func TestHandleGetBoard_NotFound(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/boards/no-such-board", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleListBoards_AfterCreate(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	// Create two boards.
	for _, name := range []string{"Alpha", "Beta"} {
		resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards", map[string]string{"name": name})
		resp.Body.Close()
	}
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/boards", nil)
	var boards []*board.Board
	decodeJSON(t, resp, &boards)
	if len(boards) != 2 {
		t.Fatalf("expected 2 boards, got %d", len(boards))
	}
}

// ─── Card tests ──────────────────────────────────────────────────────────────

// createBoard is a test helper that creates a board and returns its ID.
func createBoard(t *testing.T, serverURL string) string {
	t.Helper()
	resp := doRequest(t, http.MethodPost, serverURL+"/api/boards", map[string]string{"name": "Test Board"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create board status = %d", resp.StatusCode)
	}
	var b board.Board
	decodeJSON(t, resp, &b)
	return b.ID
}

func TestHandleCreateCard_Success(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]any{
			"title":       "Fix login bug",
			"description": "Auth token expires too early",
			"priority":    1,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var c board.Card
	decodeJSON(t, resp, &c)
	if c.ID == "" {
		t.Error("expected card ID to be set")
	}
	if c.Title != "Fix login bug" {
		t.Errorf("Title = %q, want %q", c.Title, "Fix login bug")
	}
	if c.BoardID != boardID {
		t.Errorf("BoardID = %q, want %q", c.BoardID, boardID)
	}
	if c.ColumnID != board.ColumnBacklog {
		t.Errorf("ColumnID = %q, want %q", c.ColumnID, board.ColumnBacklog)
	}
}

func TestHandleCreateCard_MissingTitle(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"description": "no title"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleCreateCard_CustomColumn(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]any{"title": "Ready task", "column_id": board.ColumnReady})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var c board.Card
	decodeJSON(t, resp, &c)
	if c.ColumnID != board.ColumnReady {
		t.Errorf("ColumnID = %q, want %q", c.ColumnID, board.ColumnReady)
	}
}

func TestHandleGetCard_Success(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"title": "Hello card"})
	var created board.Card
	decodeJSON(t, resp, &created)

	resp = doRequest(t, http.MethodGet, ts.URL+"/api/boards/"+boardID+"/cards/"+created.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got board.Card
	decodeJSON(t, resp, &got)
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
}

func TestHandleGetCard_NotFound(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/boards/"+boardID+"/cards/no-such-card", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleUpdateCard_Success(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"title": "Original"})
	var created board.Card
	decodeJSON(t, resp, &created)

	resp = doRequest(t, http.MethodPatch, ts.URL+"/api/boards/"+boardID+"/cards/"+created.ID,
		map[string]any{"title": "Updated", "priority": 2})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated board.Card
	decodeJSON(t, resp, &updated)
	if updated.Title != "Updated" {
		t.Errorf("Title = %q, want %q", updated.Title, "Updated")
	}
	if updated.Priority != 2 {
		t.Errorf("Priority = %d, want 2", updated.Priority)
	}
}

func TestHandleDeleteCard_Success(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"title": "To be deleted"})
	var created board.Card
	decodeJSON(t, resp, &created)

	resp = doRequest(t, http.MethodDelete, ts.URL+"/api/boards/"+boardID+"/cards/"+created.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify it's gone.
	resp = doRequest(t, http.MethodGet, ts.URL+"/api/boards/"+boardID+"/cards/"+created.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete, status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleMoveCard_Success(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"title": "Moveable"})
	var created board.Card
	decodeJSON(t, resp, &created)

	resp = doRequest(t, http.MethodPost,
		ts.URL+"/api/boards/"+boardID+"/cards/"+created.ID+"/move",
		map[string]any{"column": board.ColumnInProgress, "position": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var moved board.Card
	decodeJSON(t, resp, &moved)
	if moved.ColumnID != board.ColumnInProgress {
		t.Errorf("ColumnID = %q, want %q", moved.ColumnID, board.ColumnInProgress)
	}
}

func TestHandleMoveCard_NotFound(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)
	resp := doRequest(t, http.MethodPost,
		ts.URL+"/api/boards/"+boardID+"/cards/no-such-card/move",
		map[string]any{"column": board.ColumnDone, "position": 0})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleListCards_Success(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)

	for _, title := range []string{"Card A", "Card B", "Card C"} {
		resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
			map[string]string{"title": title})
		resp.Body.Close()
	}

	resp := doRequest(t, http.MethodGet, ts.URL+"/api/boards/"+boardID+"/cards", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var cards []*board.Card
	decodeJSON(t, resp, &cards)
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards, got %d", len(cards))
	}
}

// ─── CORS tests ──────────────────────────────────────────────────────────────

func TestCORS_LocalhostOriginAllowed(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/boards", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204", resp.StatusCode)
	}
	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao == "" {
		t.Error("expected Access-Control-Allow-Origin header on localhost origin")
	}
}

func TestCORS_ExternalOriginBlocked(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/boards", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao != "" {
		t.Errorf("expected no ACAO header for external origin, got %q", acao)
	}
}

func TestCORS_127001Origin(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/boards", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao == "" {
		t.Error("expected ACAO header for 127.0.0.1 origin")
	}
}

// ─── error response format ───────────────────────────────────────────────────

func TestErrorResponseFormat(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	resp := doRequest(t, http.MethodGet, ts.URL+"/api/boards/not-exist", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var errResp map[string]string
	decodeJSON(t, resp, &errResp)
	if _, ok := errResp["error"]; !ok {
		t.Error(`expected JSON body with "error" key`)
	}
}

// ─── InvalidJSON tests ───────────────────────────────────────────────────────

func TestHandleCreateBoard_InvalidJSON(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/boards",
		strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleCreateCard_InvalidJSON(t *testing.T) {
	ts := newTestServer(t, newFakeStore())
	boardID := createBoard(t, ts.URL)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		strings.NewReader("{bad json}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// ─── fake SessionAPI ─────────────────────────────────────────────────────────

type fakeSessionAPI struct {
	mu       sync.Mutex
	sessions map[string]model.Session
	changes  map[string][]model.WorkspaceChangedPayload
	// submitted tracks session_id → prompt for Submit calls
	submitted map[string]string
}

func newFakeSessionAPI() *fakeSessionAPI {
	return &fakeSessionAPI{
		sessions:  make(map[string]model.Session),
		changes:   make(map[string][]model.WorkspaceChangedPayload),
		submitted: make(map[string]string),
	}
}

func (f *fakeSessionAPI) ListSessions(_ context.Context, limit int) ([]model.Session, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	out := make([]model.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeSessionAPI) CreateSession(_ context.Context, id string) (model.Session, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	sess := model.Session{ID: id, CreatedAt: time.Now().UTC()}
	f.sessions[id] = sess
	return sess, nil
}

func (f *fakeSessionAPI) RenameSession(_ context.Context, id, name string) (model.Session, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	sess, ok := f.sessions[id]
	if !ok {
		return model.Session{}, errors.New("session not found")
	}
	sess.Name = name
	f.sessions[id] = sess
	return sess, nil
}

func (f *fakeSessionAPI) WorkspaceChanges(_ context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	return f.changes[sessionID], nil
}

func (f *fakeSessionAPI) Submit(_ context.Context, sessionID, prompt string) (string, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	f.submitted[sessionID] = prompt
	return "wf-" + sessionID, nil
}

// newTestServerWithSessions creates a test server with a fakeSessionAPI.
func newTestServerWithSessions(t *testing.T, store board.Store, api *fakeSessionAPI) *httptest.Server {
	t.Helper()
	srv := web.New(web.Config{
		Store:     store,
		ClientAPI: api,
		EventBus:  events.NewBus(),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// ─── Session tests ───────────────────────────────────────────────────────────

func TestHandleListSessions_Empty(t *testing.T) {
	api := newFakeSessionAPI()
	ts := newTestServerWithSessions(t, newFakeStore(), api)

	resp := doRequest(t, http.MethodGet, ts.URL+"/api/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var sessions []map[string]string
	decodeJSON(t, resp, &sessions)
	if len(sessions) != 0 {
		t.Fatalf("expected empty list, got %d", len(sessions))
	}
}

func TestHandleCreateSession_Success(t *testing.T) {
	api := newFakeSessionAPI()
	ts := newTestServerWithSessions(t, newFakeStore(), api)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/sessions",
		map[string]string{"name": "Test Session"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var sess map[string]string
	decodeJSON(t, resp, &sess)
	if sess["id"] == "" {
		t.Error("expected session id to be set")
	}
	if sess["name"] != "Test Session" {
		t.Errorf("name = %q, want %q", sess["name"], "Test Session")
	}

	// Verify it appears in list.
	resp = doRequest(t, http.MethodGet, ts.URL+"/api/sessions", nil)
	var sessions []map[string]string
	decodeJSON(t, resp, &sessions)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
}

func TestHandleSessionChanges_Success(t *testing.T) {
	api := newFakeSessionAPI()
	api.changes["sess-1"] = []model.WorkspaceChangedPayload{
		{Path: "main.go", Operation: "modified"},
		{Path: "utils.go", Operation: "created"},
	}
	api.sessions["sess-1"] = model.Session{ID: "sess-1"}

	ts := newTestServerWithSessions(t, newFakeStore(), api)

	resp := doRequest(t, http.MethodGet, ts.URL+"/api/sessions/sess-1/changes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var changes []map[string]string
	decodeJSON(t, resp, &changes)
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}
	if changes[0]["path"] != "main.go" {
		t.Errorf("path = %q, want %q", changes[0]["path"], "main.go")
	}
}

func TestHandleSubmitCard_Success(t *testing.T) {
	api := newFakeSessionAPI()
	api.sessions["sess-1"] = model.Session{ID: "sess-1"}
	store := newFakeStore()
	ts := newTestServerWithSessions(t, store, api)

	boardID := createBoard(t, ts.URL)
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"title": "Task to submit"})
	var card board.Card
	decodeJSON(t, resp, &card)

	resp = doRequest(t, http.MethodPost, ts.URL+"/api/cards/"+card.ID+"/submit",
		map[string]string{"session_id": "sess-1"})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, body)
	}
	var result map[string]string
	decodeJSON(t, resp, &result)
	if result["session_id"] != "sess-1" {
		t.Errorf("session_id = %q, want %q", result["session_id"], "sess-1")
	}
	if result["workflow_id"] == "" {
		t.Error("expected workflow_id to be set")
	}

	// Verify card was updated in store.
	updated, _ := store.GetCard(card.ID)
	if updated.SessionID != "sess-1" {
		t.Errorf("card.SessionID = %q, want %q", updated.SessionID, "sess-1")
	}
	if updated.ColumnID != board.ColumnInProgress {
		t.Errorf("card.ColumnID = %q, want %q", updated.ColumnID, board.ColumnInProgress)
	}
}

func TestHandleSubmitCard_MissingSessionID(t *testing.T) {
	api := newFakeSessionAPI()
	store := newFakeStore()
	ts := newTestServerWithSessions(t, store, api)
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"title": "No session"})
	var card board.Card
	decodeJSON(t, resp, &card)

	resp = doRequest(t, http.MethodPost, ts.URL+"/api/cards/"+card.ID+"/submit",
		map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleSubmitCard_CardNotFound(t *testing.T) {
	api := newFakeSessionAPI()
	ts := newTestServerWithSessions(t, newFakeStore(), api)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/cards/no-such-card/submit",
		map[string]string{"session_id": "sess-1"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleGetBoard_Default(t *testing.T) {
	store := newFakeStore()
	ts := newTestServer(t, store)

	// Create a board first.
	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards", map[string]string{"name": "My Board"})
	resp.Body.Close()

	// Fetch via "default" alias.
	resp = doRequest(t, http.MethodGet, ts.URL+"/api/boards/default", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result struct {
		board.Board
		Cards []*board.Card `json:"cards"`
	}
	decodeJSON(t, resp, &result)
	if result.Name != "My Board" {
		t.Errorf("Name = %q, want %q", result.Name, "My Board")
	}
}

func TestHandleUpdateCard_SessionID(t *testing.T) {
	store := newFakeStore()
	ts := newTestServer(t, store)
	boardID := createBoard(t, ts.URL)

	resp := doRequest(t, http.MethodPost, ts.URL+"/api/boards/"+boardID+"/cards",
		map[string]string{"title": "Bind session"})
	var card board.Card
	decodeJSON(t, resp, &card)

	resp = doRequest(t, http.MethodPatch, ts.URL+"/api/boards/"+boardID+"/cards/"+card.ID,
		map[string]string{"session_id": "sess-42"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated board.Card
	decodeJSON(t, resp, &updated)
	if updated.SessionID != "sess-42" {
		t.Errorf("SessionID = %q, want %q", updated.SessionID, "sess-42")
	}
}
