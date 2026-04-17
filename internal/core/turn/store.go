package turn

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"

	_ "modernc.org/sqlite"
)

type Store interface {
	Save(turn model.Turn) error
	UpdateStatus(turnID, status string, updatedAt time.Time) error
	Get(turnID string) (model.Turn, bool)
	ListBySession(sessionID string, limit int) []model.Turn
	// ListRunning returns all turns with status=running, used on startup to
	// detect orphan turns left by a previous process crash (L4-08).
	ListRunning() []model.Turn
	Close() error
}

type memoryStore struct {
	mu    sync.RWMutex
	turns map[string]model.Turn
}

type sqliteStore struct {
	db *sql.DB
}

func NewStore() Store {
	return &memoryStore{turns: make(map[string]model.Turn)}
}

func NewSQLiteStore(path string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	store := &sqliteStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *memoryStore) Save(turn model.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns[turn.ID] = turn
	return nil
}

func (s *memoryStore) UpdateStatus(turnID, status string, updatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.turns[turnID]
	if !ok {
		return fmt.Errorf("turn not found")
	}
	item.Status = status
	item.UpdatedAt = updatedAt
	s.turns[turnID] = item
	return nil
}

func (s *memoryStore) Get(turnID string) (model.Turn, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.turns[turnID]
	return item, ok
}

func (s *memoryStore) ListBySession(sessionID string, limit int) []model.Turn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Turn, 0, len(s.turns))
	for _, item := range s.turns {
		if item.SessionID != sessionID {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *memoryStore) ListRunning() []model.Turn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.Turn
	for _, item := range s.turns {
		if item.Status == "running" {
			out = append(out, item)
		}
	}
	return out
}

func (s *memoryStore) Close() error { return nil }

func (s *sqliteStore) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS turns (
	turn_id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	workflow_id TEXT NOT NULL,
	prompt TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL,
	updated_at_unix_nano INTEGER NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create turns table: %w", err)
	}
	return nil
}

func (s *sqliteStore) Save(turn model.Turn) error {
	_, err := s.db.Exec(`
INSERT INTO turns (
	turn_id, session_id, workflow_id, prompt, status, created_at_unix_nano, updated_at_unix_nano
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(turn_id) DO UPDATE SET
	session_id=excluded.session_id,
	workflow_id=excluded.workflow_id,
	prompt=excluded.prompt,
	status=excluded.status,
	updated_at_unix_nano=excluded.updated_at_unix_nano
`, turn.ID, turn.SessionID, turn.WorkflowID, turn.Prompt, turn.Status, turn.CreatedAt.UnixNano(), turn.UpdatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("upsert turn: %w", err)
	}
	return nil
}

func (s *sqliteStore) UpdateStatus(turnID, status string, updatedAt time.Time) error {
	_, err := s.db.Exec(`
UPDATE turns
SET status = ?, updated_at_unix_nano = ?
WHERE turn_id = ?
`, status, updatedAt.UnixNano(), turnID)
	if err != nil {
		return fmt.Errorf("update turn status: %w", err)
	}
	return nil
}

func (s *sqliteStore) Get(turnID string) (model.Turn, bool) {
	var item model.Turn
	var createdAt, updatedAt int64
	err := s.db.QueryRow(`
SELECT turn_id, session_id, workflow_id, prompt, status, created_at_unix_nano, updated_at_unix_nano
FROM turns
WHERE turn_id = ?
`, turnID).Scan(&item.ID, &item.SessionID, &item.WorkflowID, &item.Prompt, &item.Status, &createdAt, &updatedAt)
	if err != nil {
		return model.Turn{}, false
	}
	item.CreatedAt = time.Unix(0, createdAt).UTC()
	item.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return item, true
}

func (s *sqliteStore) ListBySession(sessionID string, limit int) []model.Turn {
	query := `
SELECT turn_id, session_id, workflow_id, prompt, status, created_at_unix_nano, updated_at_unix_nano
FROM turns
WHERE session_id = ?
ORDER BY created_at_unix_nano DESC
	, turn_id DESC
`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(query+`LIMIT ?`, sessionID, limit)
	} else {
		rows, err = s.db.Query(query, sessionID)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []model.Turn
	for rows.Next() {
		var item model.Turn
		var createdAt, updatedAt int64
		if err := rows.Scan(&item.ID, &item.SessionID, &item.WorkflowID, &item.Prompt, &item.Status, &createdAt, &updatedAt); err != nil {
			continue
		}
		item.CreatedAt = time.Unix(0, createdAt).UTC()
		item.UpdatedAt = time.Unix(0, updatedAt).UTC()
		out = append(out, item)
	}
	return out
}

func (s *sqliteStore) ListRunning() []model.Turn {
	rows, err := s.db.Query(`
SELECT turn_id, session_id, workflow_id, prompt, status, created_at_unix_nano, updated_at_unix_nano
FROM turns
WHERE status = 'running'
ORDER BY created_at_unix_nano ASC
`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Turn
	for rows.Next() {
		var item model.Turn
		var createdAt, updatedAt int64
		if err := rows.Scan(&item.ID, &item.SessionID, &item.WorkflowID, &item.Prompt, &item.Status, &createdAt, &updatedAt); err != nil {
			continue
		}
		item.CreatedAt = time.Unix(0, createdAt).UTC()
		item.UpdatedAt = time.Unix(0, updatedAt).UTC()
		out = append(out, item)
	}
	return out
}

func (s *sqliteStore) Close() error { return s.db.Close() }
