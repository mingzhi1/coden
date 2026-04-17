package intent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"

	_ "modernc.org/sqlite"
)

type Store interface {
	Save(intent model.IntentSpec) error
	Latest(sessionID string) (model.IntentSpec, bool)
	Close() error
}

type memoryStore struct {
	mu     sync.RWMutex
	latest map[string]model.IntentSpec
}

func NewStore() Store {
	return &memoryStore{
		latest: make(map[string]model.IntentSpec),
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

func (s *memoryStore) Save(intent model.IntentSpec) error {
	s.mu.Lock()
	s.latest[intent.SessionID] = cloneIntent(intent)
	s.mu.Unlock()
	return nil
}

func (s *memoryStore) Latest(sessionID string) (model.IntentSpec, bool) {
	s.mu.RLock()
	intent, ok := s.latest[sessionID]
	s.mu.RUnlock()
	return cloneIntent(intent), ok
}

func (s *memoryStore) Close() error {
	return nil
}

type sqliteStore struct {
	db *sql.DB
}

func (s *sqliteStore) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS intent_specs (
	session_id TEXT PRIMARY KEY,
	intent_id TEXT NOT NULL,
	goal TEXT NOT NULL,
	success_criteria_json TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create intent_specs table: %w", err)
	}
	return nil
}

func (s *sqliteStore) Save(intent model.IntentSpec) error {
	successCriteriaJSON, err := json.Marshal(intent.SuccessCriteria)
	if err != nil {
		return fmt.Errorf("marshal success criteria: %w", err)
	}

	_, err = s.db.Exec(`
INSERT INTO intent_specs (
	session_id,
	intent_id,
	goal,
	success_criteria_json,
	created_at_unix_nano
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
	intent_id=excluded.intent_id,
	goal=excluded.goal,
	success_criteria_json=excluded.success_criteria_json,
	created_at_unix_nano=excluded.created_at_unix_nano
`,
		intent.SessionID,
		intent.ID,
		intent.Goal,
		string(successCriteriaJSON),
		intent.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("upsert intent spec: %w", err)
	}
	return nil
}

func (s *sqliteStore) Latest(sessionID string) (model.IntentSpec, bool) {
	var (
		intent              model.IntentSpec
		successCriteriaJSON string
		createdAtUnixNano   int64
	)

	err := s.db.QueryRow(`
SELECT intent_id, goal, success_criteria_json, created_at_unix_nano
FROM intent_specs
WHERE session_id = ?
`,
		sessionID,
	).Scan(
		&intent.ID,
		&intent.Goal,
		&successCriteriaJSON,
		&createdAtUnixNano,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.IntentSpec{}, false
		}
		return model.IntentSpec{}, false
	}

	if err := json.Unmarshal([]byte(successCriteriaJSON), &intent.SuccessCriteria); err != nil {
		return model.IntentSpec{}, false
	}

	intent.SessionID = sessionID
	intent.CreatedAt = unixNanoToTime(createdAtUnixNano)
	return cloneIntent(intent), true
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

func cloneIntent(in model.IntentSpec) model.IntentSpec {
	in.SuccessCriteria = append([]string(nil), in.SuccessCriteria...)
	return in
}

func unixNanoToTime(unixNano int64) time.Time {
	if unixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano).UTC()
}
