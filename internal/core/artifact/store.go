package artifact

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ─── Store interface ─────────────────────────────────────────────────

// Store is the low-level persistence layer for artifact metadata.
type Store interface {
	// Artifacts
	InsertArtifact(ctx context.Context, a *Artifact) error
	GetArtifact(ctx context.Context, id string) (*Artifact, error)
	ListByWorkflow(ctx context.Context, workflowID string, opts ListOptions) ([]Artifact, error)
	ListBySession(ctx context.Context, sessionID string, opts ListOptions) ([]Artifact, error)
	Find(ctx context.Context, q FindQuery) ([]Artifact, error)
	DeleteArtifact(ctx context.Context, id string) error
	DeleteByWorkflow(ctx context.Context, workflowID string) (int64, error)
	DeleteBySession(ctx context.Context, sessionID string) (int64, error)
	DeleteBefore(ctx context.Context, t time.Time) (int64, error)

	// Refs
	InsertRef(ctx context.Context, ref *ArtifactRef) error
	RefsFrom(ctx context.Context, artifactID string) ([]ArtifactRef, error)
	RefsTo(ctx context.Context, artifactID string) ([]ArtifactRef, error)

	// ToolCalls
	InsertToolCall(ctx context.Context, tc *ToolCall) error
	GetToolCall(ctx context.Context, id string) (*ToolCall, error)

	// Blobs (ref-count tracking)
	InsertBlob(ctx context.Context, blobID string, sizeBytes int64, storagePath string) error
	IncrBlobRef(ctx context.Context, blobID string) error
	DecrBlobRef(ctx context.Context, blobID string) error
	OrphanBlobIDs(ctx context.Context) ([]string, error)
	DeleteBlobRow(ctx context.Context, blobID string) error

	// Lifecycle helpers
	BlobIDsForWorkflow(ctx context.Context, workflowID string) ([]string, error)
	BlobIDsForSession(ctx context.Context, sessionID string) ([]string, error)
	BlobIDsBeforeTime(ctx context.Context, t time.Time) ([]string, error)
	DistinctWorkflowIDs(ctx context.Context) ([]string, error)
	WorkflowCreatedAt(ctx context.Context, workflowID string) (time.Time, error)
	CountArtifacts(ctx context.Context) (int64, error)

	Close() error
}

// ─── SQLite implementation ───────────────────────────────────────────

type sqliteStore struct {
	db *sql.DB
}

// OpenStore opens (or creates) a SQLite-backed artifact store.
func OpenStore(dbPath string) (Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open artifact store: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &sqliteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS artifacts (
		id TEXT PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		tool_call_id TEXT,
		kind TEXT NOT NULL,
		name TEXT,
		content_type TEXT,
		size_bytes INTEGER NOT NULL,
		content BLOB,
		blob_id TEXT,
		source_path TEXT,
		tool_kind TEXT,
		metadata_json TEXT,
		created_at_unix_nano INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_artifacts_workflow ON artifacts(workflow_id);
	CREATE INDEX IF NOT EXISTS idx_artifacts_session ON artifacts(session_id);
	CREATE INDEX IF NOT EXISTS idx_artifacts_tool_call ON artifacts(tool_call_id);
	CREATE INDEX IF NOT EXISTS idx_artifacts_kind ON artifacts(kind);
	CREATE INDEX IF NOT EXISTS idx_artifacts_tool_kind ON artifacts(tool_kind);
	CREATE INDEX IF NOT EXISTS idx_artifacts_created ON artifacts(created_at_unix_nano);

	CREATE TABLE IF NOT EXISTS artifact_refs (
		id TEXT PRIMARY KEY,
		from_artifact_id TEXT NOT NULL,
		to_artifact_id TEXT NOT NULL,
		reason TEXT,
		created_at_unix_nano INTEGER NOT NULL,
		UNIQUE(from_artifact_id, to_artifact_id)
	);
	CREATE INDEX IF NOT EXISTS idx_artifact_refs_from ON artifact_refs(from_artifact_id);
	CREATE INDEX IF NOT EXISTS idx_artifact_refs_to ON artifact_refs(to_artifact_id);

	CREATE TABLE IF NOT EXISTS tool_calls (
		id TEXT PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		tool_kind TEXT NOT NULL,
		request_json TEXT NOT NULL,
		status TEXT NOT NULL,
		duration_ms INTEGER,
		created_at_unix_nano INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_tool_calls_workflow ON tool_calls(workflow_id);

	CREATE TABLE IF NOT EXISTS blobs (
		id TEXT PRIMARY KEY,
		size_bytes INTEGER NOT NULL,
		ref_count INTEGER NOT NULL DEFAULT 1,
		storage_path TEXT NOT NULL,
		created_at_unix_nano INTEGER NOT NULL
	);
	`
	_, err := db.Exec(ddl)
	if err != nil {
		return fmt.Errorf("migrate artifact schema: %w", err)
	}
	return nil
}

// ─── Artifact CRUD ───────────────────────────────────────────────────

func (s *sqliteStore) InsertArtifact(ctx context.Context, a *Artifact) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifacts
			(id, workflow_id, session_id, tool_call_id, kind, name, content_type,
			 size_bytes, content, blob_id, source_path, tool_kind, metadata_json,
			 created_at_unix_nano)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.WorkflowID, a.SessionID, a.ToolCallID,
		string(a.Kind), a.Name, a.ContentType,
		a.Size, a.Content, a.BlobID,
		a.SourcePath, a.ToolKind, a.MetadataJSON(),
		a.CreatedAt.UnixNano(),
	)
	return err
}

func (s *sqliteStore) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workflow_id, session_id, tool_call_id, kind, name, content_type,
		       size_bytes, content, blob_id, source_path, tool_kind, metadata_json,
		       created_at_unix_nano
		FROM artifacts WHERE id = ?`, id)
	return scanArtifact(row)
}

func (s *sqliteStore) DeleteArtifact(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE id = ?`, id)
	return err
}

func (s *sqliteStore) DeleteByWorkflow(ctx context.Context, workflowID string) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE workflow_id = ?`, workflowID)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *sqliteStore) DeleteBySession(ctx context.Context, sessionID string) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE session_id = ?`, sessionID)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *sqliteStore) DeleteBefore(ctx context.Context, t time.Time) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE created_at_unix_nano < ?`, t.UnixNano())
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// ─── List / Find ─────────────────────────────────────────────────────

func (s *sqliteStore) ListByWorkflow(ctx context.Context, workflowID string, opts ListOptions) ([]Artifact, error) {
	q, args := buildListQuery("workflow_id = ?", workflowID, opts)
	return s.queryArtifacts(ctx, q, args...)
}

func (s *sqliteStore) ListBySession(ctx context.Context, sessionID string, opts ListOptions) ([]Artifact, error) {
	q, args := buildListQuery("session_id = ?", sessionID, opts)
	return s.queryArtifacts(ctx, q, args...)
}

func (s *sqliteStore) Find(ctx context.Context, fq FindQuery) ([]Artifact, error) {
	var clauses []string
	var args []any

	if fq.SessionID != "" {
		clauses = append(clauses, "session_id = ?")
		args = append(args, fq.SessionID)
	}
	if fq.WorkflowID != "" {
		clauses = append(clauses, "workflow_id = ?")
		args = append(args, fq.WorkflowID)
	}
	if len(fq.Kinds) > 0 {
		ph := placeholders(len(fq.Kinds))
		clauses = append(clauses, "kind IN ("+ph+")")
		for _, k := range fq.Kinds {
			args = append(args, string(k))
		}
	}
	if len(fq.ToolKinds) > 0 {
		ph := placeholders(len(fq.ToolKinds))
		clauses = append(clauses, "tool_kind IN ("+ph+")")
		for _, tk := range fq.ToolKinds {
			args = append(args, tk)
		}
	}
	if fq.NamePattern != "" {
		clauses = append(clauses, "name LIKE ?")
		args = append(args, fq.NamePattern)
	}
	if fq.MinSize > 0 {
		clauses = append(clauses, "size_bytes >= ?")
		args = append(args, fq.MinSize)
	}
	if fq.MaxSize > 0 {
		clauses = append(clauses, "size_bytes <= ?")
		args = append(args, fq.MaxSize)
	}
	if fq.CreatedAfter != nil {
		clauses = append(clauses, "created_at_unix_nano >= ?")
		args = append(args, fq.CreatedAfter.UnixNano())
	}
	if fq.CreatedBefore != nil {
		clauses = append(clauses, "created_at_unix_nano <= ?")
		args = append(args, fq.CreatedBefore.UnixNano())
	}

	where := "1=1"
	if len(clauses) > 0 {
		where = strings.Join(clauses, " AND ")
	}

	q := "SELECT id, workflow_id, session_id, tool_call_id, kind, name, content_type, " +
		"size_bytes, content, blob_id, source_path, tool_kind, metadata_json, " +
		"created_at_unix_nano FROM artifacts WHERE " + where +
		" ORDER BY created_at_unix_nano DESC"
	if fq.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", fq.Limit)
	}
	return s.queryArtifacts(ctx, q, args...)
}

// ─── Refs ────────────────────────────────────────────────────────────

func (s *sqliteStore) InsertRef(ctx context.Context, ref *ArtifactRef) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO artifact_refs
			(id, from_artifact_id, to_artifact_id, reason, created_at_unix_nano)
		VALUES (?,?,?,?,?)`,
		ref.ID, ref.FromArtifactID, ref.ToArtifactID, ref.Reason,
		ref.CreatedAt.UnixNano(),
	)
	return err
}

func (s *sqliteStore) RefsFrom(ctx context.Context, artifactID string) ([]ArtifactRef, error) {
	return s.queryRefs(ctx, `SELECT id, from_artifact_id, to_artifact_id, reason, created_at_unix_nano
		FROM artifact_refs WHERE from_artifact_id = ?`, artifactID)
}

func (s *sqliteStore) RefsTo(ctx context.Context, artifactID string) ([]ArtifactRef, error) {
	return s.queryRefs(ctx, `SELECT id, from_artifact_id, to_artifact_id, reason, created_at_unix_nano
		FROM artifact_refs WHERE to_artifact_id = ?`, artifactID)
}

// ─── ToolCalls ───────────────────────────────────────────────────────

func (s *sqliteStore) InsertToolCall(ctx context.Context, tc *ToolCall) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_calls
			(id, workflow_id, session_id, tool_kind, request_json, status, duration_ms, created_at_unix_nano)
		VALUES (?,?,?,?,?,?,?,?)`,
		tc.ID, tc.WorkflowID, tc.SessionID, tc.ToolKind,
		tc.RequestJSON, string(tc.Status), tc.DurationMs,
		tc.CreatedAt.UnixNano(),
	)
	return err
}

func (s *sqliteStore) GetToolCall(ctx context.Context, id string) (*ToolCall, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workflow_id, session_id, tool_kind, request_json, status, duration_ms, created_at_unix_nano
		FROM tool_calls WHERE id = ?`, id)
	var tc ToolCall
	var statusStr string
	var nano int64
	err := row.Scan(&tc.ID, &tc.WorkflowID, &tc.SessionID, &tc.ToolKind,
		&tc.RequestJSON, &statusStr, &tc.DurationMs, &nano)
	if err != nil {
		return nil, err
	}
	tc.Status = ToolCallStatus(statusStr)
	tc.CreatedAt = time.Unix(0, nano)
	return &tc, nil
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}

// ─── Helpers ─────────────────────────────────────────────────────────

func scanArtifact(row *sql.Row) (*Artifact, error) {
	var a Artifact
	var kindStr string
	var toolCallID, name, contentType, blobID, sourcePath, toolKind, metaJSON sql.NullString
	var content []byte
	var nano int64

	err := row.Scan(&a.ID, &a.WorkflowID, &a.SessionID, &toolCallID,
		&kindStr, &name, &contentType,
		&a.Size, &content, &blobID,
		&sourcePath, &toolKind, &metaJSON,
		&nano)
	if err != nil {
		return nil, err
	}
	a.Kind = ArtifactKind(kindStr)
	a.ToolCallID = toolCallID.String
	a.Name = name.String
	a.ContentType = contentType.String
	a.Content = content
	a.BlobID = blobID.String
	a.SourcePath = sourcePath.String
	a.ToolKind = toolKind.String
	a.CreatedAt = time.Unix(0, nano)
	a.ParseMetadataJSON(metaJSON.String)
	return &a, nil
}

func scanArtifactRow(rows *sql.Rows) (*Artifact, error) {
	var a Artifact
	var kindStr string
	var toolCallID, name, contentType, blobID, sourcePath, toolKind, metaJSON sql.NullString
	var content []byte
	var nano int64

	err := rows.Scan(&a.ID, &a.WorkflowID, &a.SessionID, &toolCallID,
		&kindStr, &name, &contentType,
		&a.Size, &content, &blobID,
		&sourcePath, &toolKind, &metaJSON,
		&nano)
	if err != nil {
		return nil, err
	}
	a.Kind = ArtifactKind(kindStr)
	a.ToolCallID = toolCallID.String
	a.Name = name.String
	a.ContentType = contentType.String
	a.Content = content
	a.BlobID = blobID.String
	a.SourcePath = sourcePath.String
	a.ToolKind = toolKind.String
	a.CreatedAt = time.Unix(0, nano)
	a.ParseMetadataJSON(metaJSON.String)
	return &a, nil
}

func (s *sqliteStore) queryArtifacts(ctx context.Context, query string, args ...any) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Artifact
	for rows.Next() {
		a, err := scanArtifactRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *sqliteStore) queryRefs(ctx context.Context, query string, args ...any) ([]ArtifactRef, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ArtifactRef
	for rows.Next() {
		var ref ArtifactRef
		var nano int64
		if err := rows.Scan(&ref.ID, &ref.FromArtifactID, &ref.ToArtifactID, &ref.Reason, &nano); err != nil {
			return nil, err
		}
		ref.CreatedAt = time.Unix(0, nano)
		out = append(out, ref)
	}
	return out, rows.Err()
}

func buildListQuery(whereClause string, whereArg any, opts ListOptions) (string, []any) {
	args := []any{whereArg}
	clauses := []string{whereClause}

	if len(opts.Kinds) > 0 {
		ph := placeholders(len(opts.Kinds))
		clauses = append(clauses, "kind IN ("+ph+")")
		for _, k := range opts.Kinds {
			args = append(args, string(k))
		}
	}
	if len(opts.ToolKinds) > 0 {
		ph := placeholders(len(opts.ToolKinds))
		clauses = append(clauses, "tool_kind IN ("+ph+")")
		for _, tk := range opts.ToolKinds {
			args = append(args, tk)
		}
	}

	where := strings.Join(clauses, " AND ")
	orderCol := "created_at_unix_nano"
	switch opts.OrderBy {
	case "size":
		orderCol = "size_bytes"
	case "name":
		orderCol = "name"
	}
	dir := "ASC"
	if opts.Descending {
		dir = "DESC"
	}

	q := "SELECT id, workflow_id, session_id, tool_call_id, kind, name, content_type, " +
		"size_bytes, content, blob_id, source_path, tool_kind, metadata_json, " +
		"created_at_unix_nano FROM artifacts WHERE " + where +
		" ORDER BY " + orderCol + " " + dir

	if opts.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	if opts.Offset > 0 {
		q += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}
	return q, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

// ─── Blob ref-count ──────────────────────────────────────────────────

func (s *sqliteStore) InsertBlob(ctx context.Context, blobID string, sizeBytes int64, storagePath string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO blobs (id, size_bytes, ref_count, storage_path, created_at_unix_nano)
		VALUES (?,?,1,?,?)`, blobID, sizeBytes, storagePath, time.Now().UnixNano())
	return err
}

func (s *sqliteStore) IncrBlobRef(ctx context.Context, blobID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE blobs SET ref_count = ref_count + 1 WHERE id = ?`, blobID)
	return err
}

func (s *sqliteStore) DecrBlobRef(ctx context.Context, blobID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE blobs SET ref_count = ref_count - 1 WHERE id = ?`, blobID)
	return err
}

func (s *sqliteStore) OrphanBlobIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM blobs WHERE ref_count <= 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *sqliteStore) DeleteBlobRow(ctx context.Context, blobID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM blobs WHERE id = ?`, blobID)
	return err
}

// ─── Lifecycle helpers ───────────────────────────────────────────────

func (s *sqliteStore) BlobIDsForWorkflow(ctx context.Context, workflowID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT blob_id FROM artifacts WHERE workflow_id = ? AND blob_id IS NOT NULL AND blob_id != ''`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *sqliteStore) BlobIDsForSession(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT blob_id FROM artifacts WHERE session_id = ? AND blob_id IS NOT NULL AND blob_id != ''`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *sqliteStore) BlobIDsBeforeTime(ctx context.Context, t time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT blob_id FROM artifacts WHERE created_at_unix_nano < ? AND blob_id IS NOT NULL AND blob_id != ''`, t.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *sqliteStore) DistinctWorkflowIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT workflow_id FROM artifacts GROUP BY workflow_id ORDER BY MIN(created_at_unix_nano) ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *sqliteStore) WorkflowCreatedAt(ctx context.Context, workflowID string) (time.Time, error) {
	var nano int64
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(created_at_unix_nano) FROM artifacts WHERE workflow_id = ?`, workflowID).Scan(&nano)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, nano), nil
}

func (s *sqliteStore) CountArtifacts(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts`).Scan(&n)
	return n, err
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
