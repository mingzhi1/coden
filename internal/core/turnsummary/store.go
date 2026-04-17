package turnsummary

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

// Store persists and retrieves TurnSummary records.
type Store interface {
	Save(summary model.TurnSummary) error
	// Delete removes a turn summary by ID (L4-07 saga compensating action).
	Delete(id string)
	Get(turnID string) (model.TurnSummary, bool)
	ListBySession(sessionID string, limit int) []model.TurnSummary
	Close() error
}

// ── In-memory implementation ────────────────────────────────────────────────

type memoryStore struct {
	mu       sync.RWMutex
	byTurn   map[string]model.TurnSummary
	bySession map[string][]model.TurnSummary
}

func NewStore() Store {
	return &memoryStore{
		byTurn:    make(map[string]model.TurnSummary),
		bySession: make(map[string][]model.TurnSummary),
	}
}

func (s *memoryStore) Save(summary model.TurnSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := cloneSummary(summary)
	s.byTurn[summary.TurnID] = cloned
	s.bySession[summary.SessionID] = append(s.bySession[summary.SessionID], cloned)
	return nil
}

func (s *memoryStore) Get(turnID string) (model.TurnSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.byTurn[turnID]
	if !ok {
		return model.TurnSummary{}, false
	}
	return cloneSummary(item), true
}

func (s *memoryStore) ListBySession(sessionID string, limit int) []model.TurnSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.bySession[sessionID]
	if len(items) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	// Return most recent first.
	out := make([]model.TurnSummary, 0, limit)
	for i := len(items) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, cloneSummary(items[i]))
	}
	return out
}

func (s *memoryStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ts := range s.byTurn {
		if ts.ID == id {
			delete(s.byTurn, ts.TurnID)
			list := s.bySession[ts.SessionID]
			for i := len(list) - 1; i >= 0; i-- {
				if list[i].ID == id {
					s.bySession[ts.SessionID] = append(list[:i], list[i+1:]...)
					break
				}
			}
			return
		}
	}
}

func (s *memoryStore) Close() error { return nil }

// ── SQLite implementation ───────────────────────────────────────────────────

type sqliteStore struct {
	db *sql.DB
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

func (s *sqliteStore) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS turn_summaries (
	id TEXT NOT NULL,
	turn_id TEXT NOT NULL UNIQUE,
	session_id TEXT NOT NULL,
	intent_json TEXT NOT NULL,
	task_results_json TEXT NOT NULL,
	changed_files_json TEXT NOT NULL,
	checkpoint_json TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL,
	PRIMARY KEY (id)
)`)
	if err != nil {
		return fmt.Errorf("create turn_summaries table: %w", err)
	}
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_turn_summaries_session ON turn_summaries(session_id, created_at_unix_nano DESC)`)
	return nil
}

func (s *sqliteStore) Save(summary model.TurnSummary) error {
	intentJSON, err := json.Marshal(summary.Intent)
	if err != nil {
		return fmt.Errorf("marshal intent: %w", err)
	}
	taskResultsJSON, err := json.Marshal(summary.TaskResults)
	if err != nil {
		return fmt.Errorf("marshal task results: %w", err)
	}
	changedFilesJSON, err := json.Marshal(summary.ChangedFiles)
	if err != nil {
		return fmt.Errorf("marshal changed files: %w", err)
	}
	checkpointJSON, err := json.Marshal(summary.Checkpoint)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	_, err = s.db.Exec(`
INSERT INTO turn_summaries (
	id, turn_id, session_id,
	intent_json, task_results_json, changed_files_json, checkpoint_json,
	created_at_unix_nano
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(turn_id) DO UPDATE SET
	intent_json=excluded.intent_json,
	task_results_json=excluded.task_results_json,
	changed_files_json=excluded.changed_files_json,
	checkpoint_json=excluded.checkpoint_json,
	created_at_unix_nano=excluded.created_at_unix_nano
`,
		summary.ID,
		summary.TurnID,
		summary.SessionID,
		string(intentJSON),
		string(taskResultsJSON),
		string(changedFilesJSON),
		string(checkpointJSON),
		summary.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("upsert turn summary: %w", err)
	}
	return nil
}

func (s *sqliteStore) Get(turnID string) (model.TurnSummary, bool) {
	row := s.db.QueryRow(`
SELECT id, turn_id, session_id, intent_json, task_results_json, changed_files_json, checkpoint_json, created_at_unix_nano
FROM turn_summaries
WHERE turn_id = ?
`, turnID)
	return scanSummaryRow(row)
}

func (s *sqliteStore) ListBySession(sessionID string, limit int) []model.TurnSummary {
	query := `
SELECT id, turn_id, session_id, intent_json, task_results_json, changed_files_json, checkpoint_json, created_at_unix_nano
FROM turn_summaries
WHERE session_id = ?
ORDER BY created_at_unix_nano DESC
`
	args := []any{sessionID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []model.TurnSummary
	for rows.Next() {
		if summary, ok := scanSummaryFields(rows); ok {
			out = append(out, summary)
		}
	}
	return out
}

func (s *sqliteStore) Delete(id string) {
	s.db.Exec(`DELETE FROM turn_summaries WHERE id = ?`, id)
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSummaryRow(row rowScanner) (model.TurnSummary, bool) {
	return scanSummaryFields(row)
}

func scanSummaryFields(scanner rowScanner) (model.TurnSummary, bool) {
	var (
		summary           model.TurnSummary
		intentJSON        string
		taskResultsJSON   string
		changedFilesJSON  string
		checkpointJSON    string
		createdAtUnixNano int64
	)

	err := scanner.Scan(
		&summary.ID,
		&summary.TurnID,
		&summary.SessionID,
		&intentJSON,
		&taskResultsJSON,
		&changedFilesJSON,
		&checkpointJSON,
		&createdAtUnixNano,
	)
	if err != nil {
		return model.TurnSummary{}, false
	}
	if err := json.Unmarshal([]byte(intentJSON), &summary.Intent); err != nil {
		return model.TurnSummary{}, false
	}
	if err := json.Unmarshal([]byte(taskResultsJSON), &summary.TaskResults); err != nil {
		return model.TurnSummary{}, false
	}
	if err := json.Unmarshal([]byte(changedFilesJSON), &summary.ChangedFiles); err != nil {
		return model.TurnSummary{}, false
	}
	if err := json.Unmarshal([]byte(checkpointJSON), &summary.Checkpoint); err != nil {
		return model.TurnSummary{}, false
	}
	summary.CreatedAt = unixNanoToTime(createdAtUnixNano)
	return summary, true
}

func cloneSummary(in model.TurnSummary) model.TurnSummary {
	in.TaskResults = append([]model.TaskResult(nil), in.TaskResults...)
	in.ChangedFiles = append([]model.FileChange(nil), in.ChangedFiles...)
	in.Checkpoint.ArtifactPaths = append([]string(nil), in.Checkpoint.ArtifactPaths...)
	in.Checkpoint.Evidence = append([]string(nil), in.Checkpoint.Evidence...)
	return in
}

func unixNanoToTime(unixNano int64) time.Time {
	if unixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano).UTC()
}
