package message

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
	Save(message model.Message) error
	// Delete removes a message by ID (L4-07 saga compensating action).
	Delete(messageID string)
	List(sessionID string, limit int) []model.Message
	Close() error
}

type memoryStore struct {
	mu       sync.RWMutex
	messages map[string][]model.Message
}

func NewStore() Store {
	return &memoryStore{
		messages: make(map[string][]model.Message),
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

func (s *memoryStore) Save(message model.Message) error {
	s.mu.Lock()
	s.messages[message.SessionID] = append(s.messages[message.SessionID], cloneMessage(message))
	s.mu.Unlock()
	return nil
}

func (s *memoryStore) List(sessionID string, limit int) []model.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.messages[sessionID]
	if len(items) == 0 {
		return nil
	}
	if limit > 0 && limit < len(items) {
		items = items[len(items)-limit:]
	}
	out := make([]model.Message, 0, len(items))
	for _, item := range items {
		out = append(out, cloneMessage(item))
	}
	return out
}

func (s *memoryStore) Delete(messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sid, msgs := range s.messages {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].ID == messageID {
				s.messages[sid] = append(msgs[:i], msgs[i+1:]...)
				return
			}
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
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS messages (
	session_id TEXT NOT NULL,
	message_id TEXT NOT NULL,
	role TEXT NOT NULL,
	content TEXT NOT NULL,
	created_at_unix_nano INTEGER NOT NULL,
	PRIMARY KEY (session_id, message_id)
)`)
	if err != nil {
		return fmt.Errorf("create messages table: %w", err)
	}
	return nil
}

func (s *sqliteStore) Save(message model.Message) error {
	_, err := s.db.Exec(`
INSERT INTO messages (
	session_id,
	message_id,
	role,
	content,
	created_at_unix_nano
) VALUES (?, ?, ?, ?, ?)
`,
		message.SessionID,
		message.ID,
		message.Role,
		message.Content,
		message.CreatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

func (s *sqliteStore) List(sessionID string, limit int) []model.Message {
	query := `
SELECT message_id, role, content, created_at_unix_nano
FROM messages
WHERE session_id = ?
ORDER BY created_at_unix_nano ASC
`
	args := []any{sessionID}
	if limit > 0 {
		query = `
SELECT message_id, role, content, created_at_unix_nano
FROM (
	SELECT message_id, role, content, created_at_unix_nano
	FROM messages
	WHERE session_id = ?
	ORDER BY created_at_unix_nano DESC
	LIMIT ?
)
ORDER BY created_at_unix_nano ASC
`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []model.Message
	for rows.Next() {
		var (
			msg               model.Message
			createdAtUnixNano int64
		)
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.Content, &createdAtUnixNano); err != nil {
			continue
		}
		msg.SessionID = sessionID
		msg.CreatedAt = unixNanoToTime(createdAtUnixNano)
		out = append(out, cloneMessage(msg))
	}
	return out
}

func (s *sqliteStore) Delete(messageID string) {
	s.db.Exec(`DELETE FROM messages WHERE message_id = ?`, messageID)
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

func cloneMessage(in model.Message) model.Message {
	return in
}

func unixNanoToTime(unixNano int64) time.Time {
	if unixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano).UTC()
}
