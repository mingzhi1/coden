package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"

	_ "modernc.org/sqlite"
)

type Store interface {
	Save(session model.Session) error
	Get(sessionID string) (model.Session, bool)
	List(limit int) []model.Session
	Rename(sessionID, name string) error
	Close() error
}

type memoryStore struct {
	mu       sync.RWMutex
	sessions map[string]model.Session
	order    []string
}

func NewStore() Store {
	return &memoryStore{
		sessions: make(map[string]model.Session),
	}
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

func (s *memoryStore) Save(session model.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[session.ID]; !exists {
		s.order = append(s.order, session.ID)
	}
	s.sessions[session.ID] = session
	return nil
}

func (s *memoryStore) Get(sessionID string) (model.Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	return session, ok
}

func (s *memoryStore) List(limit int) []model.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.order) == 0 {
		return nil
	}
	start := 0
	if limit > 0 && limit < len(s.order) {
		start = len(s.order) - limit
	}
	out := make([]model.Session, 0, len(s.order)-start)
	for _, id := range s.order[start:] {
		out = append(out, s.sessions[id])
	}
	return out
}

func (s *memoryStore) Rename(sessionID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	sess.Name = name
	s.sessions[sessionID] = sess
	return nil
}

func (s *memoryStore) Close() error { return nil }

type sqliteStore struct {
	db *sql.DB
}

func (s *sqliteStore) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
	session_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	project_root TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create sessions table: %w", err)
	}
	// Migration: add name column if it doesn't exist yet (R-07).
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN name TEXT NOT NULL DEFAULT ''`)
	return nil
}

func (s *sqliteStore) Save(session model.Session) error {
	_, err := s.db.Exec(`
INSERT INTO sessions (session_id, project_id, project_root, created_at_unix_nano, name)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
	project_id=excluded.project_id,
	project_root=excluded.project_root
`, session.ID, session.ProjectID, session.ProjectRoot, session.CreatedAt.UnixNano(), session.Name)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *sqliteStore) Rename(sessionID, name string) error {
	res, err := s.db.Exec(`UPDATE sessions SET name = ? WHERE session_id = ?`, name, sessionID)
	if err != nil {
		return fmt.Errorf("rename session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	return nil
}

func (s *sqliteStore) Get(sessionID string) (model.Session, bool) {
	var out model.Session
	var createdAt int64
	err := s.db.QueryRow(`
SELECT session_id, project_id, project_root, created_at_unix_nano, name
FROM sessions
WHERE session_id = ?
`, sessionID).Scan(&out.ID, &out.ProjectID, &out.ProjectRoot, &createdAt, &out.Name)
	if err != nil {
		return model.Session{}, false
	}
	out.CreatedAt = time.Unix(0, createdAt).UTC()
	return out, true
}

func (s *sqliteStore) List(limit int) []model.Session {
	query := `
SELECT session_id, project_id, project_root, created_at_unix_nano, name
FROM sessions
ORDER BY created_at_unix_nano ASC
`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		var item model.Session
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.ProjectRoot, &createdAt, &item.Name); err != nil {
			continue
		}
		item.CreatedAt = time.Unix(0, createdAt).UTC()
		out = append(out, item)
	}
	return out
}

func (s *sqliteStore) Close() error { return s.db.Close() }
