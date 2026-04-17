// Package artifact provides unified storage, indexing, and referencing
// for tool execution results (artifacts). It replaces the ad-hoc spill
// mechanism with a structured, queryable, content-addressed store.
package artifact

import (
	"encoding/json"
	"time"
)

// ─── ArtifactKind ────────────────────────────────────────────────────

// ArtifactKind classifies what an artifact represents.
type ArtifactKind string

const (
	KindFile       ArtifactKind = "file"        // written/edited file content
	KindToolOutput ArtifactKind = "tool_output"  // tool stdout
	KindToolError  ArtifactKind = "tool_error"   // tool stderr
	KindDiff       ArtifactKind = "diff"         // edit_file unified diff
	KindSpill      ArtifactKind = "spill"        // spilled large result
	KindEvidence   ArtifactKind = "evidence"     // retrieval evidence
	KindWebContent ArtifactKind = "web_content"  // web_fetch page content
	KindSnapshot   ArtifactKind = "snapshot"     // file snapshot (before edit)
)

// ─── Artifact ────────────────────────────────────────────────────────

// InlineSizeThreshold: artifacts smaller than this are stored inline in
// the SQLite content column; larger ones go to the blob store.
const InlineSizeThreshold = 64 * 1024 // 64 KB

// Artifact represents a single product of a tool execution.
type Artifact struct {
	// Identity
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	SessionID  string `json:"session_id"`
	ToolCallID string `json:"tool_call_id,omitempty"`

	// Classification
	Kind        ArtifactKind `json:"kind"`
	Name        string       `json:"name,omitempty"`
	ContentType string       `json:"content_type,omitempty"` // MIME type
	Size        int64        `json:"size"`

	// Content storage (one of Content or BlobID is set)
	Content []byte `json:"-"`       // inline for small artifacts
	BlobID  string `json:"blob_id,omitempty"` // reference for large artifacts

	// Source information
	SourcePath string `json:"source_path,omitempty"` // file path, URL, etc.
	ToolKind   string `json:"tool_kind,omitempty"`   // producing tool kind

	// Timestamps
	CreatedAt time.Time `json:"created_at"`

	// Extensible metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

// MetadataJSON serialises the Metadata map for SQLite storage.
func (a *Artifact) MetadataJSON() string {
	if len(a.Metadata) == 0 {
		return ""
	}
	b, _ := json.Marshal(a.Metadata)
	return string(b)
}

// ParseMetadataJSON populates Metadata from a JSON string.
func (a *Artifact) ParseMetadataJSON(raw string) {
	if raw == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), &a.Metadata)
}

// ─── ArtifactRef ─────────────────────────────────────────────────────

// ArtifactRef represents a directed reference between two artifacts.
type ArtifactRef struct {
	ID             string    `json:"id"`
	FromArtifactID string    `json:"from_artifact_id"`
	ToArtifactID   string    `json:"to_artifact_id"`
	Reason         string    `json:"reason,omitempty"` // "reuse", "derive", "include"
	CreatedAt      time.Time `json:"created_at"`
}

// ─── ToolCall ────────────────────────────────────────────────────────

// ToolCallStatus describes the outcome of a tool invocation.
type ToolCallStatus string

const (
	StatusSuccess ToolCallStatus = "success"
	StatusError   ToolCallStatus = "error"
	StatusTimeout ToolCallStatus = "timeout"
)

// ToolCall records a single tool invocation in a workflow.
type ToolCall struct {
	ID         string         `json:"id"`
	WorkflowID string        `json:"workflow_id"`
	SessionID  string         `json:"session_id"`
	ToolKind   string         `json:"tool_kind"`
	RequestJSON string        `json:"request_json"`
	Status     ToolCallStatus `json:"status"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// ─── Query types ─────────────────────────────────────────────────────

// ListOptions controls pagination and filtering for list queries.
type ListOptions struct {
	Kinds      []ArtifactKind
	ToolKinds  []string
	Limit      int
	Offset     int
	OrderBy    string // "created_at" (default), "size", "name"
	Descending bool
}

// FindQuery is a multi-field search predicate.
type FindQuery struct {
	SessionID     string
	WorkflowID    string
	Kinds         []ArtifactKind
	ToolKinds     []string
	NamePattern   string // SQL LIKE pattern
	MinSize       int64
	MaxSize       int64
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	Limit         int
}

// ─── SaveResult ──────────────────────────────────────────────────────

// SaveResult is returned after persisting the results of a tool call.
type SaveResult struct {
	ToolCallID string     `json:"tool_call_id"`
	Artifacts  []Artifact `json:"artifacts"`
	Primary    *Artifact  `json:"primary,omitempty"`
}

// ─── CleanupOptions / CleanupResult ──────────────────────────────────

// CleanupOptions controls what gets cleaned up.
type CleanupOptions struct {
	DryRun       bool
	PreserveRefs bool // keep artifacts that are referenced by others
}

// CleanupResult summarises a cleanup operation.
type CleanupResult struct {
	ArtifactsRemoved int
	BlobsRemoved     int
	BytesFreed       int64
}
