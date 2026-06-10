package checkpoint

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
	Save(result model.CheckpointResult) error
	// Delete removes a checkpoint result (L4-07 saga compensating action).
	Delete(workflowID, sessionID string)
	Latest(sessionID string) (model.CheckpointResult, bool)
	Get(sessionID, workflowID string) (model.CheckpointResult, bool)
	List(sessionID string, limit int) []model.CheckpointResult
	// SaveBucket persists one incremental bucket-engine snapshot keyed by
	// (session, workflow, seq) — the per-sequence crash-resume anchor (§6/§10.1).
	SaveBucket(snap model.BucketSnapshot) error
	// LatestBucket returns the highest-seq snapshot for a workflow, for resume.
	LatestBucket(sessionID, workflowID string) (model.BucketSnapshot, bool)
	Close() error
}

type memoryStore struct {
	mu      sync.RWMutex
	latest  map[string]model.CheckpointResult
	history map[string][]model.CheckpointResult
	buckets map[string]model.BucketSnapshot // key: sessionID|workflowID → highest-seq snapshot
}

func NewStore() Store {
	return &memoryStore{
		latest:  make(map[string]model.CheckpointResult),
		history: make(map[string][]model.CheckpointResult),
		buckets: make(map[string]model.BucketSnapshot),
	}
}

func bucketKey(sessionID, workflowID string) string { return sessionID + "|" + workflowID }

func (s *memoryStore) SaveBucket(snap model.BucketSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := bucketKey(snap.SessionID, snap.WorkflowID)
	if prev, ok := s.buckets[k]; ok && prev.Seq > snap.Seq {
		return nil // never regress to an older seq
	}
	s.buckets[k] = cloneBucketSnapshot(snap)
	return nil
}

func (s *memoryStore) LatestBucket(sessionID, workflowID string) (model.BucketSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.buckets[bucketKey(sessionID, workflowID)]
	if !ok {
		return model.BucketSnapshot{}, false
	}
	return cloneBucketSnapshot(snap), true
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

func (s *memoryStore) Save(result model.CheckpointResult) error {
	s.mu.Lock()
	cloned := cloneCheckpointResult(result)
	s.latest[result.SessionID] = cloned
	s.history[result.SessionID] = append(s.history[result.SessionID], cloned)
	s.mu.Unlock()
	return nil
}

func (s *memoryStore) Latest(sessionID string) (model.CheckpointResult, bool) {
	s.mu.RLock()
	result, ok := s.latest[sessionID]
	s.mu.RUnlock()
	return cloneCheckpointResult(result), ok
}

func (s *memoryStore) Get(sessionID, workflowID string) (model.CheckpointResult, bool) {
	if workflowID == "" {
		return s.Latest(sessionID)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	results := s.history[sessionID]
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].WorkflowID == workflowID {
			return cloneCheckpointResult(results[i]), true
		}
	}

	return model.CheckpointResult{}, false
}

func (s *memoryStore) List(sessionID string, limit int) []model.CheckpointResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := s.history[sessionID]
	if len(results) == 0 {
		return nil
	}

	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}

	out := make([]model.CheckpointResult, 0, limit)
	for i := len(results) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, cloneCheckpointResult(results[i]))
	}
	return out
}

func (s *memoryStore) Delete(workflowID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.history[sessionID]
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].WorkflowID == workflowID {
			s.history[sessionID] = append(history[:i], history[i+1:]...)
			break
		}
	}
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
	// WAL avoids the exclusive-lock contention that caused SQLITE_BUSY when the
	// saga commit writes checkpoint + turn + message + insight to the shared
	// workspace sqlite file concurrently. (Persistent, file-level.)
	if _, err := s.db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("set journal_mode WAL: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS checkpoint_results (
	session_id TEXT NOT NULL,
	workflow_id TEXT NOT NULL,
	status TEXT NOT NULL,
	artifact_paths_json TEXT NOT NULL,
	evidence_json TEXT NOT NULL,
	fix_guidance TEXT NOT NULL DEFAULT '',
	created_at_unix_nano INTEGER NOT NULL,
	PRIMARY KEY (session_id, workflow_id)
)`)
	if err != nil {
		return fmt.Errorf("create checkpoint_results table: %w", err)
	}
	// Migration: add fix_guidance column to existing databases.
	s.db.Exec(`ALTER TABLE checkpoint_results ADD COLUMN fix_guidance TEXT NOT NULL DEFAULT ''`)

	// Incremental bucket-engine snapshots (§6/§10.1): many rows per workflow, keyed
	// by seq, for crash-resume / undo.
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS bucket_snapshots (
	session_id TEXT NOT NULL,
	workflow_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	snapshot_json TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL,
	PRIMARY KEY (session_id, workflow_id, seq)
)`); err != nil {
		return fmt.Errorf("create bucket_snapshots table: %w", err)
	}
	return nil
}

func (s *sqliteStore) Save(result model.CheckpointResult) error {
	artifactPathsJSON, err := json.Marshal(result.ArtifactPaths)
	if err != nil {
		return fmt.Errorf("marshal artifact paths: %w", err)
	}
	evidenceJSON, err := json.Marshal(result.Evidence)
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}

	_, err = s.db.Exec(`
INSERT INTO checkpoint_results (
	session_id,
	workflow_id,
	status,
	artifact_paths_json,
	evidence_json,
	fix_guidance,
	created_at_unix_nano
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, workflow_id) DO UPDATE SET
	status=excluded.status,
	artifact_paths_json=excluded.artifact_paths_json,
	evidence_json=excluded.evidence_json,
	fix_guidance=excluded.fix_guidance,
	created_at_unix_nano=excluded.created_at_unix_nano
`,
		result.SessionID,
		result.WorkflowID,
		result.Status,
		string(artifactPathsJSON),
		string(evidenceJSON),
		result.FixGuidance,
		result.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("upsert checkpoint result: %w", err)
	}
	return nil
}

func (s *sqliteStore) Latest(sessionID string) (model.CheckpointResult, bool) {
	results := s.List(sessionID, 1)
	if len(results) == 0 {
		return model.CheckpointResult{}, false
	}
	return results[0], true
}

func (s *sqliteStore) Get(sessionID, workflowID string) (model.CheckpointResult, bool) {
	if workflowID == "" {
		return s.Latest(sessionID)
	}

	row := s.db.QueryRow(`
SELECT workflow_id, status, artifact_paths_json, evidence_json, fix_guidance, created_at_unix_nano
FROM checkpoint_results
WHERE session_id = ? AND workflow_id = ?
`,
		sessionID,
		workflowID,
	)
	return scanCheckpointRow(sessionID, row)
}

func (s *sqliteStore) List(sessionID string, limit int) []model.CheckpointResult {
	query := `
SELECT workflow_id, status, artifact_paths_json, evidence_json, fix_guidance, created_at_unix_nano
FROM checkpoint_results
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

	var out []model.CheckpointResult
	for rows.Next() {
		result, ok := scanCheckpointFields(sessionID, rows)
		if ok {
			out = append(out, result)
		}
	}
	return out
}

func (s *sqliteStore) Delete(workflowID, sessionID string) {
	s.db.Exec(`DELETE FROM checkpoint_results WHERE workflow_id = ? AND session_id = ?`, workflowID, sessionID)
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

func (s *sqliteStore) SaveBucket(snap model.BucketSnapshot) error {
	blob, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal bucket snapshot: %w", err)
	}
	// One row per (session, workflow, seq); INSERT OR IGNORE so a replayed seq is
	// idempotent and never regresses the durable history.
	_, err = s.db.Exec(`
INSERT OR IGNORE INTO bucket_snapshots (session_id, workflow_id, seq, snapshot_json, created_at_unix_nano)
VALUES (?, ?, ?, ?, ?)`,
		snap.SessionID, snap.WorkflowID, snap.Seq, string(blob), snap.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("insert bucket snapshot: %w", err)
	}
	return nil
}

func (s *sqliteStore) LatestBucket(sessionID, workflowID string) (model.BucketSnapshot, bool) {
	var blob string
	err := s.db.QueryRow(`
SELECT snapshot_json FROM bucket_snapshots
WHERE session_id = ? AND workflow_id = ?
ORDER BY seq DESC LIMIT 1`, sessionID, workflowID).Scan(&blob)
	if err != nil {
		return model.BucketSnapshot{}, false
	}
	var snap model.BucketSnapshot
	if err := json.Unmarshal([]byte(blob), &snap); err != nil {
		return model.BucketSnapshot{}, false
	}
	return snap, true
}

func cloneBucketSnapshot(in model.BucketSnapshot) model.BucketSnapshot {
	out := in
	out.Bucket = append([]model.Task(nil), in.Bucket...)
	out.Completed = make(map[string]string, len(in.Completed))
	for k, v := range in.Completed {
		out.Completed[k] = v
	}
	out.Artifacts = make(map[string]model.Artifact, len(in.Artifacts))
	for k, v := range in.Artifacts {
		out.Artifacts[k] = v
	}
	return out
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCheckpointRow(sessionID string, row rowScanner) (model.CheckpointResult, bool) {
	return scanCheckpointFields(sessionID, row)
}

func scanCheckpointFields(sessionID string, scanner rowScanner) (model.CheckpointResult, bool) {
	var (
		result            model.CheckpointResult
		artifactPathsJSON string
		evidenceJSON      string
		createdAtUnixNano int64
	)

	err := scanner.Scan(
		&result.WorkflowID,
		&result.Status,
		&artifactPathsJSON,
		&evidenceJSON,
		&result.FixGuidance,
		&createdAtUnixNano,
	)
	if err != nil {
		return model.CheckpointResult{}, false
	}
	if err := json.Unmarshal([]byte(artifactPathsJSON), &result.ArtifactPaths); err != nil {
		return model.CheckpointResult{}, false
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &result.Evidence); err != nil {
		return model.CheckpointResult{}, false
	}

	result.SessionID = sessionID
	result.CreatedAt = unixNanoToTime(createdAtUnixNano)
	return cloneCheckpointResult(result), true
}

func cloneCheckpointResult(in model.CheckpointResult) model.CheckpointResult {
	in.ArtifactPaths = append([]string(nil), in.ArtifactPaths...)
	in.Evidence = append([]string(nil), in.Evidence...)
	return in
}

func unixNanoToTime(unixNano int64) time.Time {
	if unixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano).UTC()
}
