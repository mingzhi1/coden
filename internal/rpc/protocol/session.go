package protocol

import (
	"github.com/mingzhi1/coden/internal/core/model"
)

// SessionSnapshotParams selects the session to snapshot.
type SessionSnapshotParams struct {
	SessionID    string `json:"session_id"`
	MessageLimit int    `json:"message_limit,omitempty"` // default 50
}

// SessionSnapshotResult is the atomic snapshot of a session's current state.
// LastEventSeq pairs with R-01 (since_seq replay): subscribe with
// since_seq=LastEventSeq immediately after receiving the snapshot to receive
// all events that occurred at or after the snapshot was taken.
type SessionSnapshotResult struct {
	SessionID        string                         `json:"session_id"`
	Messages         []Message                      `json:"messages"`
	ActiveWorkflow   *model.WorkflowRun             `json:"active_workflow,omitempty"`
	LatestCheckpoint *model.CheckpointResult        `json:"latest_checkpoint,omitempty"`
	LatestIntent     *model.IntentSpec              `json:"latest_intent,omitempty"`
	WorkspaceChanges []WorkspaceChange              `json:"workspace_changes"`
	LastEventSeq     uint64                         `json:"last_event_seq"`
}

// AckResult is the common "status only" response shape.
type AckResult struct {
	Status string `json:"status"`
}

// PingResult is the result of ping.
type PingResult struct {
	Status string `json:"status"`
}

type SessionCreateParams struct {
	SessionID string `json:"session_id,omitempty"`
}

type SessionListParams struct {
	Limit int `json:"limit,omitempty"`
}

type SessionListResult struct {
	Sessions []model.Session `json:"sessions,omitempty"`
}

// SessionAttachParams identifies a client attaching to a session.
type SessionAttachParams struct {
	SessionID  string `json:"session_id"`
	ClientName string `json:"client_name,omitempty"`
	View       string `json:"view,omitempty"`
}

// SessionAttachResult acknowledges a session attachment.
type SessionAttachResult struct {
	Status     string `json:"status"`
	SessionID  string `json:"session_id"`
	ClientName string `json:"client_name,omitempty"`
	View       string `json:"view,omitempty"`
}

// SessionDetachParams identifies a client detaching from a session.
type SessionDetachParams struct {
	SessionID  string `json:"session_id"`
	ClientName string `json:"client_name,omitempty"`
}

// SessionDetachResult acknowledges a session detachment.
type SessionDetachResult struct {
	Status     string `json:"status"`
	SessionID  string `json:"session_id"`
	ClientName string `json:"client_name,omitempty"`
}

// SessionRenameParams sets a human-readable label on a session (R-07).
type SessionRenameParams struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

// SessionRenameResult returns the updated session record.
type SessionRenameResult struct {
	Session model.Session `json:"session"`
}
