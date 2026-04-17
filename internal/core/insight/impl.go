package insight

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── in-memory implementation ──────────────────────────────────────────────────

type memoryStore struct {
	mu       sync.RWMutex
	insights map[string]Insight
}

// NewStore returns an in-memory insight store (no persistence).
func NewStore() Store {
	return &memoryStore{insights: make(map[string]Insight)}
}

func (s *memoryStore) Save(ins Insight) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.insights[ins.ID] = ins
	return nil
}

func (s *memoryStore) Supersede(oldID string, newIns Insight) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.insights[oldID]; ok {
		old.SupersededBy = newIns.ID
		old.UpdatedAt = time.Now().UTC()
		s.insights[old.ID] = old
	}
	s.insights[newIns.ID] = newIns
	return nil
}

func (s *memoryStore) ListBySession(sessionID string, limit int) []Insight {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Insight
	for _, ins := range s.insights {
		if ins.SessionID == sessionID {
			out = append(out, ins)
		}
	}
	sortInsights(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *memoryStore) TopKByTags(sessionID string, tags []string, k int) []Insight {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tagSet := toSet(tags)
	var out []Insight
	for _, ins := range s.insights {
		if ins.SessionID != sessionID || ins.SupersededBy != "" {
			continue
		}
		if hasOverlap(ins.Tags, tagSet) {
			out = append(out, ins)
		}
	}
	sortInsights(out)
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out
}

func (s *memoryStore) Close() error { return nil }

// ── SQLite implementation ─────────────────────────────────────────────────────

type sqliteStore struct {
	mu sync.Mutex
	db *sql.DB
}

func (s *sqliteStore) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS insights (
	insight_id        TEXT PRIMARY KEY,
	session_id        TEXT NOT NULL,
	category          TEXT NOT NULL,
	title             TEXT NOT NULL,
	content           TEXT NOT NULL,
	tags_json         TEXT NOT NULL DEFAULT '[]',
	confidence        REAL NOT NULL DEFAULT 0.5,
	superseded_by     TEXT NOT NULL DEFAULT '',
	created_at_ns     INTEGER NOT NULL,
	updated_at_ns     INTEGER NOT NULL
)`)
	return err
}

func (s *sqliteStore) Save(ins Insight) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tagsJSON, _ := json.Marshal(ins.Tags)
	_, err := s.db.Exec(`
INSERT INTO insights
  (insight_id,session_id,category,title,content,tags_json,confidence,superseded_by,created_at_ns,updated_at_ns)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(insight_id) DO UPDATE SET
  category=excluded.category, title=excluded.title, content=excluded.content,
  tags_json=excluded.tags_json, confidence=excluded.confidence,
  superseded_by=excluded.superseded_by, updated_at_ns=excluded.updated_at_ns
`,
		ins.ID, ins.SessionID, string(ins.Category), ins.Title, ins.Content,
		string(tagsJSON), ins.Confidence, ins.SupersededBy,
		ins.CreatedAt.UnixNano(), ins.UpdatedAt.UnixNano())
	return err
}

func (s *sqliteStore) Supersede(oldID string, newIns Insight) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().UnixNano()
	if _, err := s.db.Exec(`UPDATE insights SET superseded_by=?, updated_at_ns=? WHERE insight_id=?`,
		newIns.ID, now, oldID); err != nil {
		return err
	}
	tagsJSON, _ := json.Marshal(newIns.Tags)
	_, err := s.db.Exec(`
INSERT INTO insights
  (insight_id,session_id,category,title,content,tags_json,confidence,superseded_by,created_at_ns,updated_at_ns)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(insight_id) DO UPDATE SET
  category=excluded.category, title=excluded.title, content=excluded.content,
  tags_json=excluded.tags_json, confidence=excluded.confidence,
  superseded_by=excluded.superseded_by, updated_at_ns=excluded.updated_at_ns
`,
		newIns.ID, newIns.SessionID, string(newIns.Category), newIns.Title, newIns.Content,
		string(tagsJSON), newIns.Confidence, newIns.SupersededBy,
		newIns.CreatedAt.UnixNano(), newIns.UpdatedAt.UnixNano())
	return err
}

func (s *sqliteStore) ListBySession(sessionID string, limit int) []Insight {
	s.mu.Lock()
	defer s.mu.Unlock()
	query := `SELECT insight_id,session_id,category,title,content,tags_json,confidence,superseded_by,created_at_ns,updated_at_ns
FROM insights WHERE session_id=? ORDER BY updated_at_ns DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(query+" LIMIT ?", sessionID, limit)
	} else {
		rows, err = s.db.Query(query, sessionID)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanInsights(rows)
}

func (s *sqliteStore) TopKByTags(sessionID string, tags []string, k int) []Insight {
	all := s.ListBySession(sessionID, 0)
	tagSet := toSet(tags)
	var out []Insight
	for _, ins := range all {
		if ins.SupersededBy != "" {
			continue
		}
		if hasOverlap(ins.Tags, tagSet) {
			out = append(out, ins)
		}
	}
	sortInsights(out)
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// ── helpers ───────────────────────────────────────────────────────────────────

func scanInsights(rows *sql.Rows) []Insight {
	var out []Insight
	for rows.Next() {
		var ins Insight
		var cat, tagsJSON string
		var createdNS, updatedNS int64
		if err := rows.Scan(&ins.ID, &ins.SessionID, &cat, &ins.Title, &ins.Content,
			&tagsJSON, &ins.Confidence, &ins.SupersededBy, &createdNS, &updatedNS); err != nil {
			continue
		}
		ins.Category = Category(cat)
		_ = json.Unmarshal([]byte(tagsJSON), &ins.Tags)
		ins.CreatedAt = time.Unix(0, createdNS).UTC()
		ins.UpdatedAt = time.Unix(0, updatedNS).UTC()
		out = append(out, ins)
	}
	return out
}

func sortInsights(in []Insight) {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Confidence != in[j].Confidence {
			return in[i].Confidence > in[j].Confidence
		}
		return in[i].UpdatedAt.After(in[j].UpdatedAt)
	})
}

func toSet(tags []string) map[string]struct{} {
	s := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		s[strings.ToLower(t)] = struct{}{}
	}
	return s
}

func hasOverlap(tags []string, set map[string]struct{}) bool {
	for _, t := range tags {
		if _, ok := set[strings.ToLower(t)]; ok {
			return true
		}
	}
	return false
}
