package workspacestore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Store interface {
	EnsureByRoot(workspaceRoot string) (model.Workspace, error)
	GetByRoot(workspaceRoot string) (model.Workspace, bool)
	Close() error
}

type memoryStore struct {
	mu         sync.Mutex
	workspaces map[string]model.Workspace
}

type sqliteStore struct {
	db *sql.DB
}

func NewStore() Store {
	return &memoryStore{
		workspaces: make(map[string]model.Workspace),
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

func (s *memoryStore) EnsureByRoot(workspaceRoot string) (model.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if workspace, ok := s.workspaces[workspaceRoot]; ok {
		workspace.UpdatedAt = time.Now().UTC()
		s.workspaces[workspaceRoot] = workspace
		return workspace, nil
	}
	now := time.Now().UTC()
	workspace := model.Workspace{
		ID:        uuid.NewString(),
		Root:      workspaceRoot,
		Name:      filepath.Base(workspaceRoot),
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.workspaces[workspaceRoot] = workspace
	return workspace, nil
}

func (s *memoryStore) GetByRoot(workspaceRoot string) (model.Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, ok := s.workspaces[workspaceRoot]
	return workspace, ok
}

func (s *memoryStore) Close() error {
	return nil
}

func (s *sqliteStore) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS workspaces (
	workspace_id TEXT PRIMARY KEY,
	workspace_root TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL,
	updated_at_unix_nano INTEGER NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create workspaces table: %w", err)
	}
	return nil
}

func (s *sqliteStore) EnsureByRoot(workspaceRoot string) (model.Workspace, error) {
	if workspace, ok := s.GetByRoot(workspaceRoot); ok {
		now := time.Now().UTC()
		_, err := s.db.Exec(`
UPDATE workspaces
SET updated_at_unix_nano = ?
WHERE workspace_root = ?
`, now.UnixNano(), workspaceRoot)
		if err != nil {
			return model.Workspace{}, fmt.Errorf("update workspace timestamp: %w", err)
		}
		workspace.UpdatedAt = now
		return workspace, nil
	}

	now := time.Now().UTC()
	workspace := model.Workspace{
		ID:        uuid.NewString(),
		Root:      workspaceRoot,
		Name:      filepath.Base(workspaceRoot),
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.Exec(`
INSERT INTO workspaces (
	workspace_id,
	workspace_root,
	name,
	created_at_unix_nano,
	updated_at_unix_nano
) VALUES (?, ?, ?, ?, ?)
`, workspace.ID, workspace.Root, workspace.Name, workspace.CreatedAt.UnixNano(), workspace.UpdatedAt.UnixNano())
	if err != nil {
		return model.Workspace{}, fmt.Errorf("insert workspace: %w", err)
	}
	return workspace, nil
}

func (s *sqliteStore) GetByRoot(workspaceRoot string) (model.Workspace, bool) {
	var workspace model.Workspace
	var createdAt int64
	var updatedAt int64
	err := s.db.QueryRow(`
SELECT workspace_id, workspace_root, name, created_at_unix_nano, updated_at_unix_nano
FROM workspaces
WHERE workspace_root = ?
`, workspaceRoot).Scan(&workspace.ID, &workspace.Root, &workspace.Name, &createdAt, &updatedAt)
	if err != nil {
		return model.Workspace{}, false
	}
	workspace.CreatedAt = time.Unix(0, createdAt).UTC()
	workspace.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return workspace, true
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}
