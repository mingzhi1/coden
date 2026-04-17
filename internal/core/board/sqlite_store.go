package board

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using an SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) the SQLite database at path and enables WAL mode.
func OpenSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// NewSQLiteStore creates a new SQLiteStore wrapping the given *sql.DB.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Init creates all required tables and indexes.
func (s *SQLiteStore) Init() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS boards (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			project_id TEXT NOT NULL,
			columns    TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS cards (
			id           TEXT PRIMARY KEY,
			parent_id    TEXT NOT NULL DEFAULT '',
			board_id     TEXT NOT NULL,
			column_id    TEXT NOT NULL,
			position     INTEGER NOT NULL DEFAULT 0,
			title        TEXT NOT NULL,
			description  TEXT NOT NULL DEFAULT '',
			priority     INTEGER NOT NULL DEFAULT 2,
			labels       TEXT NOT NULL DEFAULT '[]',
			assignee_id  TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT '',
			depends_on   TEXT NOT NULL DEFAULT '[]',
			relates_to   TEXT NOT NULL DEFAULT '[]',
			supersedes   TEXT NOT NULL DEFAULT '[]',
			files        TEXT NOT NULL DEFAULT '[]',
			workflow_id  TEXT NOT NULL DEFAULT '',
			session_id   TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL,
			started_at   TEXT,
			completed_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS links (
			id        TEXT PRIMARY KEY,
			from_card TEXT NOT NULL,
			to_card   TEXT NOT NULL,
			type      TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agents (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			role         TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'idle',
			session_id   TEXT NOT NULL DEFAULT '',
			current_card TEXT NOT NULL DEFAULT '',
			provider     TEXT NOT NULL DEFAULT '',
			model        TEXT NOT NULL DEFAULT '',
			stats        TEXT NOT NULL DEFAULT '{}',
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cards_board_id    ON cards(board_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cards_column_id   ON cards(column_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cards_assignee_id ON cards(assignee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_links_from_card   ON links(from_card)`,
		`CREATE INDEX IF NOT EXISTS idx_links_to_card     ON links(to_card)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("board sqlite init: %w", err)
		}
	}
	return nil
}

// ---- helpers ----

const timeFmt = time.RFC3339Nano

func checkRowsAffected(res sql.Result, entity string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s not found", entity)
	}
	return nil
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func unmarshalStrings(data string) []string {
	var out []string
	if data == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(data), &out)
	return out
}

func formatTime(t time.Time) string { return t.Format(timeFmt) }
func parseTime(s string) time.Time  { t, _ := time.Parse(timeFmt, s); return t }

func formatOptTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(timeFmt), Valid: true}
}

func parseOptTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(timeFmt, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

// ---- Board CRUD ----

func (s *SQLiteStore) CreateBoard(b *Board) error {
	_, err := s.db.Exec(
		`INSERT INTO boards (id, name, project_id, columns, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		b.ID, b.Name, b.ProjectID,
		marshalJSON(b.Columns),
		formatTime(b.CreatedAt), formatTime(b.UpdatedAt),
	)
	return err
}

func (s *SQLiteStore) GetBoard(id string) (*Board, error) {
	row := s.db.QueryRow(
		`SELECT id, name, project_id, columns, created_at, updated_at FROM boards WHERE id = ?`, id,
	)
	return scanBoard(row)
}

func scanBoard(row *sql.Row) (*Board, error) {
	var b Board
	var colsJSON, createdAt, updatedAt string
	if err := row.Scan(&b.ID, &b.Name, &b.ProjectID, &colsJSON, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("board not found")
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(colsJSON), &b.Columns)
	b.CreatedAt = parseTime(createdAt)
	b.UpdatedAt = parseTime(updatedAt)
	return &b, nil
}

func (s *SQLiteStore) ListBoards() ([]*Board, error) {
	rows, err := s.db.Query(
		`SELECT id, name, project_id, columns, created_at, updated_at FROM boards ORDER BY created_at`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var boards []*Board
	for rows.Next() {
		var b Board
		var colsJSON, createdAt, updatedAt string
		if err := rows.Scan(&b.ID, &b.Name, &b.ProjectID, &colsJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(colsJSON), &b.Columns)
		b.CreatedAt = parseTime(createdAt)
		b.UpdatedAt = parseTime(updatedAt)
		boards = append(boards, &b)
	}
	return boards, rows.Err()
}

func (s *SQLiteStore) UpdateBoard(b *Board) error {
	res, err := s.db.Exec(
		`UPDATE boards SET name=?, project_id=?, columns=?, updated_at=? WHERE id=?`,
		b.Name, b.ProjectID, marshalJSON(b.Columns), formatTime(b.UpdatedAt), b.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(res, "board")
}

func (s *SQLiteStore) DeleteBoard(id string) error {
	res, err := s.db.Exec(`DELETE FROM boards WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res, "board")
}

// ---- Card CRUD ----

func (s *SQLiteStore) CreateCard(c *Card) error {
	_, err := s.db.Exec(
		`INSERT INTO cards (
			id, parent_id, board_id, column_id, position,
			title, description, priority, labels, assignee_id, status,
			depends_on, relates_to, supersedes, files,
			workflow_id, session_id,
			created_at, updated_at, started_at, completed_at
		) VALUES (?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?, ?,?, ?,?,?,?)`,
		c.ID, c.ParentID, c.BoardID, c.ColumnID, c.Position,
		c.Title, c.Description, c.Priority,
		marshalJSON(c.Labels), c.AssigneeID, c.Status,
		marshalJSON(c.DependsOn), marshalJSON(c.RelatesTo),
		marshalJSON(c.Supersedes), marshalJSON(c.Files),
		c.WorkflowID, c.SessionID,
		formatTime(c.CreatedAt), formatTime(c.UpdatedAt),
		formatOptTime(c.StartedAt), formatOptTime(c.CompletedAt),
	)
	return err
}

func (s *SQLiteStore) GetCard(id string) (*Card, error) {
	row := s.db.QueryRow(
		`SELECT id, parent_id, board_id, column_id, position,
			title, description, priority, labels, assignee_id, status,
			depends_on, relates_to, supersedes, files,
			workflow_id, session_id,
			created_at, updated_at, started_at, completed_at
		 FROM cards WHERE id = ?`, id,
	)
	return scanCard(row)
}

func scanCard(row *sql.Row) (*Card, error) {
	var c Card
	var labels, dependsOn, relatesTo, supersedes, files string
	var createdAt, updatedAt string
	var startedAt, completedAt sql.NullString

	err := row.Scan(
		&c.ID, &c.ParentID, &c.BoardID, &c.ColumnID, &c.Position,
		&c.Title, &c.Description, &c.Priority,
		&labels, &c.AssigneeID, &c.Status,
		&dependsOn, &relatesTo, &supersedes, &files,
		&c.WorkflowID, &c.SessionID,
		&createdAt, &updatedAt, &startedAt, &completedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("card not found")
		}
		return nil, err
	}
	c.Labels = unmarshalStrings(labels)
	c.DependsOn = unmarshalStrings(dependsOn)
	c.RelatesTo = unmarshalStrings(relatesTo)
	c.Supersedes = unmarshalStrings(supersedes)
	c.Files = unmarshalStrings(files)
	c.CreatedAt = parseTime(createdAt)
	c.UpdatedAt = parseTime(updatedAt)
	c.StartedAt = parseOptTime(startedAt)
	c.CompletedAt = parseOptTime(completedAt)
	return &c, nil
}

func scanCards(rows *sql.Rows) ([]*Card, error) {
	defer rows.Close()
	var cards []*Card
	for rows.Next() {
		var c Card
		var labels, dependsOn, relatesTo, supersedes, files string
		var createdAt, updatedAt string
		var startedAt, completedAt sql.NullString

		err := rows.Scan(
			&c.ID, &c.ParentID, &c.BoardID, &c.ColumnID, &c.Position,
			&c.Title, &c.Description, &c.Priority,
			&labels, &c.AssigneeID, &c.Status,
			&dependsOn, &relatesTo, &supersedes, &files,
			&c.WorkflowID, &c.SessionID,
			&createdAt, &updatedAt, &startedAt, &completedAt,
		)
		if err != nil {
			return nil, err
		}
		c.Labels = unmarshalStrings(labels)
		c.DependsOn = unmarshalStrings(dependsOn)
		c.RelatesTo = unmarshalStrings(relatesTo)
		c.Supersedes = unmarshalStrings(supersedes)
		c.Files = unmarshalStrings(files)
		c.CreatedAt = parseTime(createdAt)
		c.UpdatedAt = parseTime(updatedAt)
		c.StartedAt = parseOptTime(startedAt)
		c.CompletedAt = parseOptTime(completedAt)
		cards = append(cards, &c)
	}
	return cards, rows.Err()
}

const cardColumns = `id, parent_id, board_id, column_id, position,
	title, description, priority, labels, assignee_id, status,
	depends_on, relates_to, supersedes, files,
	workflow_id, session_id,
	created_at, updated_at, started_at, completed_at`

func (s *SQLiteStore) ListCards(boardID string) ([]*Card, error) {
	rows, err := s.db.Query(
		`SELECT `+cardColumns+` FROM cards WHERE board_id = ? ORDER BY column_id, position`, boardID,
	)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}

func (s *SQLiteStore) ListCardsByColumn(boardID, columnID string) ([]*Card, error) {
	rows, err := s.db.Query(
		`SELECT `+cardColumns+` FROM cards WHERE board_id = ? AND column_id = ? ORDER BY position`,
		boardID, columnID,
	)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}

func (s *SQLiteStore) ListCardsByAssignee(agentID string) ([]*Card, error) {
	rows, err := s.db.Query(
		`SELECT `+cardColumns+` FROM cards WHERE assignee_id = ? ORDER BY priority, created_at`,
		agentID,
	)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}

func (s *SQLiteStore) UpdateCard(c *Card) error {
	res, err := s.db.Exec(
		`UPDATE cards SET
			parent_id=?, board_id=?, column_id=?, position=?,
			title=?, description=?, priority=?,
			labels=?, assignee_id=?, status=?,
			depends_on=?, relates_to=?, supersedes=?, files=?,
			workflow_id=?, session_id=?,
			updated_at=?, started_at=?, completed_at=?
		 WHERE id=?`,
		c.ParentID, c.BoardID, c.ColumnID, c.Position,
		c.Title, c.Description, c.Priority,
		marshalJSON(c.Labels), c.AssigneeID, c.Status,
		marshalJSON(c.DependsOn), marshalJSON(c.RelatesTo),
		marshalJSON(c.Supersedes), marshalJSON(c.Files),
		c.WorkflowID, c.SessionID,
		formatTime(c.UpdatedAt),
		formatOptTime(c.StartedAt), formatOptTime(c.CompletedAt),
		c.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(res, "card")
}

func (s *SQLiteStore) MoveCard(cardID, toColumn string, position int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Shift existing cards in the target column at or after the target position.
	_, err = tx.Exec(
		`UPDATE cards SET position = position + 1
		 WHERE column_id = ? AND position >= ?`,
		toColumn, position,
	)
	if err != nil {
		return err
	}

	// Move the card.
	now := formatTime(time.Now())
	res, err := tx.Exec(
		`UPDATE cards SET column_id = ?, position = ?, updated_at = ? WHERE id = ?`,
		toColumn, position, now, cardID,
	)
	if err != nil {
		return err
	}
	if err := checkRowsAffected(res, "card"); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLiteStore) DeleteCard(id string) error {
	res, err := s.db.Exec(`DELETE FROM cards WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res, "card")
}

// ---- Graph Link CRUD ----

func (s *SQLiteStore) AddLink(l *Link) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO links (id, from_card, to_card, type) VALUES (?, ?, ?, ?)`,
		l.ID, l.FromCard, l.ToCard, string(l.Type),
	)
	return err
}

func (s *SQLiteStore) RemoveLink(id string) error {
	res, err := s.db.Exec(`DELETE FROM links WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res, "link")
}

func (s *SQLiteStore) GetLinks(cardID string) ([]*Link, error) {
	rows, err := s.db.Query(
		`SELECT id, from_card, to_card, type FROM links WHERE from_card = ? OR to_card = ?`,
		cardID, cardID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []*Link
	for rows.Next() {
		var l Link
		var linkType string
		if err := rows.Scan(&l.ID, &l.FromCard, &l.ToCard, &linkType); err != nil {
			return nil, err
		}
		l.Type = LinkType(linkType)
		links = append(links, &l)
	}
	return links, rows.Err()
}

// ---- Agent CRUD ----

func (s *SQLiteStore) CreateAgent(a *Agent) error {
	_, err := s.db.Exec(
		`INSERT INTO agents (id, name, role, status, session_id, current_card, provider, model, stats, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Role, string(a.Status), a.SessionID, a.CurrentCard,
		a.Provider, a.Model, marshalJSON(a.Stats),
		formatTime(a.CreatedAt), formatTime(a.UpdatedAt),
	)
	return err
}

func scanAgent(row *sql.Row) (*Agent, error) {
	var a Agent
	var status, statsJSON, createdAt, updatedAt string
	if err := row.Scan(
		&a.ID, &a.Name, &a.Role, &status, &a.SessionID, &a.CurrentCard,
		&a.Provider, &a.Model, &statsJSON, &createdAt, &updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found")
		}
		return nil, err
	}
	a.Status = AgentStatus(status)
	_ = json.Unmarshal([]byte(statsJSON), &a.Stats)
	a.CreatedAt = parseTime(createdAt)
	a.UpdatedAt = parseTime(updatedAt)
	return &a, nil
}

func (s *SQLiteStore) GetAgent(id string) (*Agent, error) {
	row := s.db.QueryRow(
		`SELECT id, name, role, status, session_id, current_card, provider, model, stats, created_at, updated_at
		 FROM agents WHERE id = ?`, id,
	)
	return scanAgent(row)
}

func (s *SQLiteStore) ListAgents() ([]*Agent, error) {
	rows, err := s.db.Query(
		`SELECT id, name, role, status, session_id, current_card, provider, model, stats, created_at, updated_at
		 FROM agents ORDER BY created_at`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []*Agent
	for rows.Next() {
		var a Agent
		var status, statsJSON, createdAt, updatedAt string
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Role, &status, &a.SessionID, &a.CurrentCard,
			&a.Provider, &a.Model, &statsJSON, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		a.Status = AgentStatus(status)
		_ = json.Unmarshal([]byte(statsJSON), &a.Stats)
		a.CreatedAt = parseTime(createdAt)
		a.UpdatedAt = parseTime(updatedAt)
		agents = append(agents, &a)
	}
	return agents, rows.Err()
}

func (s *SQLiteStore) UpdateAgent(a *Agent) error {
	res, err := s.db.Exec(
		`UPDATE agents SET name=?, role=?, status=?, session_id=?, current_card=?,
		 provider=?, model=?, stats=?, updated_at=?
		 WHERE id=?`,
		a.Name, a.Role, string(a.Status), a.SessionID, a.CurrentCard,
		a.Provider, a.Model, marshalJSON(a.Stats),
		formatTime(a.UpdatedAt), a.ID,
	)
	if err != nil {
		return err
	}
	return checkRowsAffected(res, "agent")
}

func (s *SQLiteStore) DeleteAgent(id string) error {
	res, err := s.db.Exec(`DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res, "agent")
}

