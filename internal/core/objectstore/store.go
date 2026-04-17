package objectstore

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/storagepath"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Store interface {
	SaveModify(turnID, filePath, mainDBPath string, payload map[string]any) (model.Object, error)
	// SaveSnapshot persists a pre-mutation file snapshot (kind=snapshot) to the
	// object store. beforeState maps workspace-relative paths to their original
	// bytes (nil means the file did not exist before the mutation).
	SaveSnapshot(turnID, mainDBPath string, beforeState map[string][]byte) ([]model.Object, error)
	ListByTurn(turnID string) []model.Object
	ReadPayload(objectID string) ([]byte, error)
	Close() error
}

type memoryStore struct {
	mu       sync.RWMutex
	objects   map[string][]model.Object
	payloads map[string][]byte
}

type sqliteStore struct {
	mu sync.Mutex
	db *sql.DB
}

func NewStore() Store {
	return &memoryStore{
		objects:  make(map[string][]model.Object),
		payloads: make(map[string][]byte),
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

func (s *memoryStore) SaveModify(turnID, filePath, mainDBPath string, payload map[string]any) (model.Object, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return model.Object{}, fmt.Errorf("marshal object payload: %w", err)
	}
	sum := sha256.Sum256(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	sequence := len(s.objects[turnID]) + 1
	prevObjectID := ""
	for i := len(s.objects[turnID]) - 1; i >= 0; i-- {
		if s.objects[turnID][i].FilePath == filePath {
			prevObjectID = s.objects[turnID][i].ID
			break
		}
	}
	item := model.Object{
		ID:           uuid.NewString(),
		TurnID:       turnID,
		Kind:         "modify",
		Sequence:     sequence,
		FilePath:     filePath,
		PrevObjectID: prevObjectID,
		StoragePath:  filepath.Join(storagepath.TurnObjectsDir(mainDBPath, turnID), fmt.Sprintf("object_%04d_%s.json", sequence, uuid.NewString())),
		ContentHash:  "sha256:" + hex.EncodeToString(sum[:]),
		CreatedAt:    time.Now().UTC(),
	}
	s.objects[turnID] = append(s.objects[turnID], item)
	s.payloads[item.ID] = append([]byte(nil), raw...)
	return item, nil
}

func (s *memoryStore) ListByTurn(turnID string) []model.Object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.objects[turnID]
	out := make([]model.Object, len(items))
	copy(out, items)
	return out
}

func (s *memoryStore) ReadPayload(objectID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, ok := s.payloads[objectID]
	if !ok {
		return nil, fmt.Errorf("object payload not found")
	}
	return append([]byte(nil), raw...), nil
}

func (s *memoryStore) SaveSnapshot(turnID, _ string, beforeState map[string][]byte) ([]model.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Object, 0, len(beforeState))
	for path, data := range beforeState {
		payload := map[string]any{
			"schema":    "snapshot.v1",
			"file_path": path,
			"content":   string(data),
		}
		if data == nil {
			payload["content"] = nil
			payload["not_exist"] = true
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return out, fmt.Errorf("marshal snapshot payload %s: %w", path, err)
		}
		sum := sha256.Sum256(raw)
		sequence := len(s.objects[turnID]) + 1
		item := model.Object{
			ID:          uuid.NewString(),
			TurnID:      turnID,
			Kind:        "snapshot",
			Sequence:    sequence,
			FilePath:    path,
			ContentHash: "sha256:" + hex.EncodeToString(sum[:]),
			CreatedAt:   time.Now().UTC(),
		}
		s.objects[turnID] = append(s.objects[turnID], item)
		s.payloads[item.ID] = append([]byte(nil), raw...)
		out = append(out, item)
	}
	return out, nil
}

func (s *memoryStore) Close() error { return nil }

func (s *sqliteStore) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS objects (
	object_id TEXT PRIMARY KEY,
	turn_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	sequence_no INTEGER NOT NULL,
	file_path TEXT NOT NULL,
	prev_object_id TEXT NOT NULL,
	storage_path TEXT NOT NULL,
	content_hash TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create objects table: %w", err)
	}
	return nil
}

func (s *sqliteStore) SaveModify(turnID, filePath, mainDBPath string, payload map[string]any) (model.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return model.Object{}, fmt.Errorf("marshal object payload: %w", err)
	}
	sum := sha256.Sum256(raw)
	tx, err := s.db.Begin()
	if err != nil {
		return model.Object{}, fmt.Errorf("begin object tx: %w", err)
	}
	defer tx.Rollback()

	var sequence int
	if err := tx.QueryRow(`
SELECT COALESCE(MAX(sequence_no), 0) + 1
FROM objects
WHERE turn_id = ?
`, turnID).Scan(&sequence); err != nil {
		return model.Object{}, fmt.Errorf("query next object sequence: %w", err)
	}

	prevObjectID := ""
	if err := tx.QueryRow(`
SELECT object_id
FROM objects
WHERE turn_id = ? AND file_path = ?
ORDER BY sequence_no DESC
LIMIT 1
`, turnID, filePath).Scan(&prevObjectID); err != nil && err != sql.ErrNoRows {
		return model.Object{}, fmt.Errorf("query previous object: %w", err)
	}

	objectID := uuid.NewString()
	storagePath := filepath.Join(storagepath.TurnObjectsDir(mainDBPath, turnID), fmt.Sprintf("object_%04d_%s.json", sequence, objectID))
	if err := os.MkdirAll(filepath.Dir(storagePath), 0o755); err != nil {
		return model.Object{}, fmt.Errorf("create object directory: %w", err)
	}
	if err := os.WriteFile(storagePath, raw, 0o644); err != nil {
		return model.Object{}, fmt.Errorf("write object payload: %w", err)
	}

	item := model.Object{
		ID:           objectID,
		TurnID:       turnID,
		Kind:         "modify",
		Sequence:     sequence,
		FilePath:     filePath,
		PrevObjectID: prevObjectID,
		StoragePath:  storagePath,
		ContentHash:  "sha256:" + hex.EncodeToString(sum[:]),
		CreatedAt:    time.Now().UTC(),
	}
	_, err = tx.Exec(`
INSERT INTO objects (
	object_id, turn_id, kind, sequence_no, file_path, prev_object_id, storage_path, content_hash, created_at_unix_nano
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, item.ID, item.TurnID, item.Kind, item.Sequence, item.FilePath, item.PrevObjectID, item.StoragePath, item.ContentHash, item.CreatedAt.UnixNano())
	if err != nil {
		return model.Object{}, fmt.Errorf("insert object: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Object{}, fmt.Errorf("commit object tx: %w", err)
	}
	return item, nil
}

func (s *sqliteStore) ListByTurn(turnID string) []model.Object {
	rows, err := s.db.Query(`
SELECT object_id, kind, sequence_no, file_path, prev_object_id, storage_path, content_hash, created_at_unix_nano
FROM objects
WHERE turn_id = ?
ORDER BY sequence_no ASC
`, turnID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Object
	for rows.Next() {
		var item model.Object
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.Kind, &item.Sequence, &item.FilePath, &item.PrevObjectID, &item.StoragePath, &item.ContentHash, &createdAt); err != nil {
			continue
		}
		item.TurnID = turnID
		item.CreatedAt = time.Unix(0, createdAt).UTC()
		out = append(out, item)
	}
	return out
}

func (s *sqliteStore) ReadPayload(objectID string) ([]byte, error) {
	var storagePath string
	err := s.db.QueryRow(`
SELECT storage_path
FROM objects
WHERE object_id = ?
`, objectID).Scan(&storagePath)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("object payload not found")
		}
		return nil, fmt.Errorf("query object payload path: %w", err)
	}
	raw, err := os.ReadFile(storagePath)
	if err != nil {
		return nil, fmt.Errorf("read object payload: %w", err)
	}
	return raw, nil
}

func (s *sqliteStore) SaveSnapshot(turnID, mainDBPath string, beforeState map[string][]byte) ([]model.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Object, 0, len(beforeState))
	for path, data := range beforeState {
		payload := map[string]any{
			"schema":    "snapshot.v1",
			"file_path": path,
			"content":   string(data),
		}
		if data == nil {
			payload["content"] = nil
			payload["not_exist"] = true
		}
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return out, fmt.Errorf("marshal snapshot payload %s: %w", path, err)
		}
		sum := sha256.Sum256(raw)

		tx, txErr := s.db.Begin()
		if txErr != nil {
			return out, fmt.Errorf("begin snapshot tx: %w", txErr)
		}
		defer tx.Rollback() //nolint:errcheck

		var sequence int
		if scanErr := tx.QueryRow(`SELECT COALESCE(MAX(sequence_no),0)+1 FROM objects WHERE turn_id=?`, turnID).Scan(&sequence); scanErr != nil {
			return out, fmt.Errorf("next snapshot sequence: %w", scanErr)
		}
		objectID := uuid.NewString()
		storagePath := filepath.Join(storagepath.TurnObjectsDir(mainDBPath, turnID), fmt.Sprintf("snap_%04d_%s.json", sequence, objectID))
		if mkErr := os.MkdirAll(filepath.Dir(storagePath), 0o755); mkErr != nil {
			return out, fmt.Errorf("create snapshot dir: %w", mkErr)
		}
		if wErr := os.WriteFile(storagePath, raw, 0o644); wErr != nil {
			return out, fmt.Errorf("write snapshot payload: %w", wErr)
		}
		item := model.Object{
			ID:          objectID,
			TurnID:      turnID,
			Kind:        "snapshot",
			Sequence:    sequence,
			FilePath:    path,
			StoragePath: storagePath,
			ContentHash: "sha256:" + hex.EncodeToString(sum[:]),
			CreatedAt:   time.Now().UTC(),
		}
		_, insErr := tx.Exec(`INSERT INTO objects (object_id,turn_id,kind,sequence_no,file_path,prev_object_id,storage_path,content_hash,created_at_unix_nano) VALUES (?,?,?,?,?,?,?,?,?)`,
			item.ID, item.TurnID, item.Kind, item.Sequence, item.FilePath, "", item.StoragePath, item.ContentHash, item.CreatedAt.UnixNano())
		if insErr != nil {
			return out, fmt.Errorf("insert snapshot object: %w", insErr)
		}
		if cErr := tx.Commit(); cErr != nil {
			return out, fmt.Errorf("commit snapshot tx: %w", cErr)
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }
