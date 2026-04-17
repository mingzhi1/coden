package artifact

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Manager provides unified artifact lifecycle management.
type Manager interface {
	// SaveToolResult persists a tool execution result as one or more artifacts.
	SaveToolResult(ctx context.Context, workflowID, sessionID string, tc ToolCall, output, stderr, diff, spillContent string) (*SaveResult, error)

	// SaveContent stores arbitrary content as a named artifact.
	SaveContent(ctx context.Context, workflowID, sessionID, toolCallID string, kind ArtifactKind, name string, data []byte, meta map[string]string) (*Artifact, error)

	// GetArtifact retrieves artifact metadata (without large content).
	GetArtifact(ctx context.Context, id string) (*Artifact, error)

	// GetArtifactContent retrieves the full content of an artifact.
	GetArtifactContent(ctx context.Context, id string) ([]byte, error)

	// ListWorkflowArtifacts lists artifacts for a workflow.
	ListWorkflowArtifacts(ctx context.Context, workflowID string, opts ListOptions) ([]Artifact, error)

	// ListSessionArtifacts lists artifacts for a session.
	ListSessionArtifacts(ctx context.Context, sessionID string, opts ListOptions) ([]Artifact, error)

	// FindArtifacts searches artifacts by multi-field query.
	FindArtifacts(ctx context.Context, q FindQuery) ([]Artifact, error)

	// CreateReference creates a directed reference between two artifacts.
	CreateReference(ctx context.Context, fromID, toID, reason string) error

	// GetReferencedArtifacts returns artifacts referenced by the given artifact.
	GetReferencedArtifacts(ctx context.Context, artifactID string) ([]Artifact, error)

	// GetReferencingArtifacts returns artifacts that reference the given artifact.
	GetReferencingArtifacts(ctx context.Context, artifactID string) ([]Artifact, error)

	// CleanupWorkflow removes all artifacts for a workflow.
	CleanupWorkflow(ctx context.Context, workflowID string) error

	// CleanupSession removes all artifacts for a session.
	CleanupSession(ctx context.Context, sessionID string) error

	// CleanupBefore removes artifacts created before the given time.
	CleanupBefore(ctx context.Context, t time.Time, opts CleanupOptions) (CleanupResult, error)

	// RunGC collects orphan blobs whose ref_count has dropped to zero.
	RunGC(ctx context.Context) (CleanupResult, error)

	// RunAutoCleanup applies retention policies (keep N workflows, keep N days).
	RunAutoCleanup(ctx context.Context, retentionCount int, retentionDays int) (CleanupResult, error)

	// Close releases resources.
	Close() error
}

// ─── manager impl ────────────────────────────────────────────────────

type manager struct {
	store Store
	blobs *BlobStore
}

// NewManager creates a Manager backed by SQLite + filesystem blobs.
// dataDir is the base directory (e.g. <workspace>/.coden/artifacts).
func NewManager(dataDir string) (Manager, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("artifact manager mkdir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "artifacts.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, err
	}

	blobDir := filepath.Join(dataDir, "blobs")
	blobs, err := NewBlobStore(blobDir)
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	return &manager{store: store, blobs: blobs}, nil
}

// ─── SaveToolResult ──────────────────────────────────────────────────

func (m *manager) SaveToolResult(ctx context.Context, workflowID, sessionID string, tc ToolCall, output, stderr, diff, spillContent string) (*SaveResult, error) {
	// Persist tool call record.
	tc.WorkflowID = workflowID
	tc.SessionID = sessionID
	if tc.ID == "" {
		tc.ID = newID()
	}
	if tc.CreatedAt.IsZero() {
		tc.CreatedAt = time.Now()
	}
	if err := m.store.InsertToolCall(ctx, &tc); err != nil {
		slog.Warn("[artifact] failed to insert tool_call", "id", tc.ID, "error", err)
		// Non-fatal — continue saving artifacts.
	}

	sr := &SaveResult{ToolCallID: tc.ID}

	// Save stdout as tool_output artifact.
	if output != "" {
		a, err := m.saveData(ctx, workflowID, sessionID, tc.ID, KindToolOutput, tc.ToolKind+"_output", tc.ToolKind, []byte(output), nil)
		if err != nil {
			slog.Warn("[artifact] save tool_output failed", "error", err)
		} else {
			sr.Artifacts = append(sr.Artifacts, *a)
			if sr.Primary == nil {
				sr.Primary = a
			}
		}
	}

	// Save stderr as tool_error artifact.
	if stderr != "" {
		a, err := m.saveData(ctx, workflowID, sessionID, tc.ID, KindToolError, tc.ToolKind+"_stderr", tc.ToolKind, []byte(stderr), nil)
		if err != nil {
			slog.Warn("[artifact] save tool_error failed", "error", err)
		} else {
			sr.Artifacts = append(sr.Artifacts, *a)
		}
	}

	// Save diff artifact.
	if diff != "" {
		a, err := m.saveData(ctx, workflowID, sessionID, tc.ID, KindDiff, tc.ToolKind+"_diff", tc.ToolKind, []byte(diff), nil)
		if err != nil {
			slog.Warn("[artifact] save diff failed", "error", err)
		} else {
			sr.Artifacts = append(sr.Artifacts, *a)
		}
	}

	// Save spill content (replacing old spill mechanism).
	if spillContent != "" {
		a, err := m.saveData(ctx, workflowID, sessionID, tc.ID, KindSpill, tc.ToolKind+"_spill", tc.ToolKind, []byte(spillContent), nil)
		if err != nil {
			slog.Warn("[artifact] save spill failed", "error", err)
		} else {
			sr.Artifacts = append(sr.Artifacts, *a)
		}
	}

	return sr, nil
}

// ─── SaveContent ─────────────────────────────────────────────────────

func (m *manager) SaveContent(ctx context.Context, workflowID, sessionID, toolCallID string, kind ArtifactKind, name string, data []byte, meta map[string]string) (*Artifact, error) {
	return m.saveData(ctx, workflowID, sessionID, toolCallID, kind, name, "", data, meta)
}

// ─── Get ─────────────────────────────────────────────────────────────

func (m *manager) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	return m.store.GetArtifact(ctx, id)
}

func (m *manager) GetArtifactContent(ctx context.Context, id string) ([]byte, error) {
	a, err := m.store.GetArtifact(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get artifact %s: %w", id, err)
	}
	// Inline content.
	if len(a.Content) > 0 {
		return a.Content, nil
	}
	// Blob content.
	if a.BlobID != "" {
		return m.blobs.Get(a.BlobID)
	}
	return nil, fmt.Errorf("artifact %s has no content", id)
}

// ─── List / Find ─────────────────────────────────────────────────────

func (m *manager) ListWorkflowArtifacts(ctx context.Context, workflowID string, opts ListOptions) ([]Artifact, error) {
	return m.store.ListByWorkflow(ctx, workflowID, opts)
}

func (m *manager) ListSessionArtifacts(ctx context.Context, sessionID string, opts ListOptions) ([]Artifact, error) {
	return m.store.ListBySession(ctx, sessionID, opts)
}

func (m *manager) FindArtifacts(ctx context.Context, q FindQuery) ([]Artifact, error) {
	return m.store.Find(ctx, q)
}

// ─── References ──────────────────────────────────────────────────────

func (m *manager) CreateReference(ctx context.Context, fromID, toID, reason string) error {
	ref := &ArtifactRef{
		ID:             newID(),
		FromArtifactID: fromID,
		ToArtifactID:   toID,
		Reason:         reason,
		CreatedAt:      time.Now(),
	}
	return m.store.InsertRef(ctx, ref)
}

func (m *manager) GetReferencedArtifacts(ctx context.Context, artifactID string) ([]Artifact, error) {
	refs, err := m.store.RefsFrom(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	var out []Artifact
	for _, ref := range refs {
		a, err := m.store.GetArtifact(ctx, ref.ToArtifactID)
		if err != nil {
			continue
		}
		out = append(out, *a)
	}
	return out, nil
}

func (m *manager) GetReferencingArtifacts(ctx context.Context, artifactID string) ([]Artifact, error) {
	refs, err := m.store.RefsTo(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	var out []Artifact
	for _, ref := range refs {
		a, err := m.store.GetArtifact(ctx, ref.FromArtifactID)
		if err != nil {
			continue
		}
		out = append(out, *a)
	}
	return out, nil
}

// ─── Cleanup ─────────────────────────────────────────────────────────

func (m *manager) CleanupWorkflow(ctx context.Context, workflowID string) error {
	blobIDs, _ := m.store.BlobIDsForWorkflow(ctx, workflowID)
	_, err := m.store.DeleteByWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}
	m.decrBlobs(ctx, blobIDs)
	return nil
}

func (m *manager) CleanupSession(ctx context.Context, sessionID string) error {
	blobIDs, _ := m.store.BlobIDsForSession(ctx, sessionID)
	_, err := m.store.DeleteBySession(ctx, sessionID)
	if err != nil {
		return err
	}
	m.decrBlobs(ctx, blobIDs)
	return nil
}

func (m *manager) CleanupBefore(ctx context.Context, t time.Time, opts CleanupOptions) (CleanupResult, error) {
	if opts.DryRun {
		arts, err := m.store.Find(ctx, FindQuery{CreatedBefore: &t})
		if err != nil {
			return CleanupResult{}, err
		}
		return CleanupResult{ArtifactsRemoved: len(arts)}, nil
	}
	blobIDs, _ := m.store.BlobIDsBeforeTime(ctx, t)
	n, err := m.store.DeleteBefore(ctx, t)
	if err != nil {
		return CleanupResult{}, err
	}
	m.decrBlobs(ctx, blobIDs)
	return CleanupResult{ArtifactsRemoved: int(n)}, nil
}

func (m *manager) RunGC(ctx context.Context) (CleanupResult, error) {
	orphanIDs, err := m.store.OrphanBlobIDs(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("orphan blob query: %w", err)
	}
	var freed int64
	for _, id := range orphanIDs {
		_ = m.blobs.Delete(id)
		_ = m.store.DeleteBlobRow(ctx, id)
		freed++
	}
	return CleanupResult{BlobsRemoved: int(freed)}, nil
}

func (m *manager) RunAutoCleanup(ctx context.Context, retentionCount int, retentionDays int) (CleanupResult, error) {
	var total CleanupResult

	// Strategy 1: Keep only the most recent N workflows.
	if retentionCount > 0 {
		wfIDs, err := m.store.DistinctWorkflowIDs(ctx)
		if err != nil {
			return total, err
		}
		if len(wfIDs) > retentionCount {
			oldIDs := wfIDs[:len(wfIDs)-retentionCount]
			for _, wfID := range oldIDs {
				if err := m.CleanupWorkflow(ctx, wfID); err != nil {
					slog.Warn("[artifact] auto-cleanup workflow failed", "workflow", wfID, "error", err)
					continue
				}
				total.ArtifactsRemoved++ // approximate
			}
		}
	}

	// Strategy 2: Delete artifacts older than N days.
	if retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -retentionDays)
		res, err := m.CleanupBefore(ctx, cutoff, CleanupOptions{})
		if err != nil {
			return total, err
		}
		total.ArtifactsRemoved += res.ArtifactsRemoved
	}

	// Run GC to reclaim orphan blobs.
	gcRes, err := m.RunGC(ctx)
	if err != nil {
		slog.Warn("[artifact] GC failed", "error", err)
	} else {
		total.BlobsRemoved += gcRes.BlobsRemoved
	}

	return total, nil
}

func (m *manager) Close() error {
	return m.store.Close()
}

// ─── Internal helpers ────────────────────────────────────────────────

// saveData persists content either inline (small) or in the blob store (large).
func (m *manager) saveData(ctx context.Context, workflowID, sessionID, toolCallID string, kind ArtifactKind, name, toolKind string, data []byte, meta map[string]string) (*Artifact, error) {
	a := &Artifact{
		ID:         newID(),
		WorkflowID: workflowID,
		SessionID:  sessionID,
		ToolCallID: toolCallID,
		Kind:       kind,
		Name:       name,
		ToolKind:   toolKind,
		Size:       int64(len(data)),
		CreatedAt:  time.Now(),
		Metadata:   meta,
	}

	if len(data) <= InlineSizeThreshold {
		a.Content = data
	} else {
		blobID, existed, err := m.blobs.Put(data)
		if err != nil {
			return nil, fmt.Errorf("blob put: %w", err)
		}
		a.BlobID = blobID
		// Track blob ref-count.
		if existed {
			// Blob file already on disk → row already exists, just incr ref.
			_ = m.store.IncrBlobRef(ctx, blobID)
		} else {
			// New blob → insert row with ref_count=1.
			if err := m.store.InsertBlob(ctx, blobID, int64(len(data)), blobID); err != nil {
				slog.Warn("[artifact] insert blob row failed", "blob", blobID, "error", err)
			}
		}
	}

	if err := m.store.InsertArtifact(ctx, a); err != nil {
		return nil, fmt.Errorf("insert artifact: %w", err)
	}
	return a, nil
}

// decrBlobs decrements ref-counts for the given blob IDs (best-effort).
func (m *manager) decrBlobs(ctx context.Context, blobIDs []string) {
	for _, id := range blobIDs {
		if id == "" {
			continue
		}
		if err := m.store.DecrBlobRef(ctx, id); err != nil {
			slog.Warn("[artifact] decr blob ref failed", "blob", id, "error", err)
		}
	}
}

// newID returns a random 16-byte hex string.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
