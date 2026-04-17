package protocol

import (
	"encoding/json"

	"github.com/mingzhi1/coden/internal/core/model"
)

// WorkflowGetResult is the response for workflow.get; extends the persisted
// WorkflowRun record with a live Workers snapshot when the workflow is active.
type WorkflowGetResult struct {
	model.WorkflowRun
	Workers []model.WorkerState `json:"workers,omitempty"`
}

// WorkflowWorkersParams is the wire format for workflow.workers (R-06).
type WorkflowWorkersParams struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id"`
}

// WorkflowWorkersResult carries the live worker state snapshot (R-06).
type WorkflowWorkersResult struct {
	WorkflowID string              `json:"workflow_id"`
	Workers    []model.WorkerState `json:"workers"`
}


// WorkflowSubmitParams is the wire format for workflow.submit.
type WorkflowSubmitParams struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
}

// WorkflowSubmitResult reports the accepted workflow id for workflow.submit.
type WorkflowSubmitResult struct {
	Status     string `json:"status"`
	WorkflowID string `json:"workflow_id"`
}

// WorkflowCancelParams is the wire format for workflow.cancel.
type WorkflowCancelParams struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
}

// WorkflowCancelResult reports the outcome of workflow.cancel.
type WorkflowCancelResult struct {
	Status     string `json:"status"`
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
}

type WorkflowGetParams struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id"`
}

type WorkflowListParams struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type WorkflowObjectsParams struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id"`
}

type WorkflowObjectReadParams struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id"`
	ObjectID   string `json:"object_id"`
}

type WorkflowObjectReadResult struct {
	ObjectID string          `json:"object_id"`
	Payload  json.RawMessage `json:"payload"`
}

// CheckpointGetParams selects a single checkpoint.
type CheckpointGetParams struct {
	SessionID  string `json:"session_id"`
	WorkflowID string `json:"workflow_id,omitempty"`
}

// CheckpointListParams selects checkpoint history.
type CheckpointListParams struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

// WorkspaceChangesParams asks for current workspace change state.
type WorkspaceChangesParams struct {
	SessionID string `json:"session_id"`
}

type WorkspaceChange struct {
	WorkflowID string `json:"workflow_id"`
	Path       string `json:"path"`
	Operation  string `json:"operation"`
}

type WorkspaceChangesResult struct {
	SessionID string            `json:"session_id"`
	Changes   []WorkspaceChange `json:"changes,omitempty"`
}

// M11-05: Task management wire types.

// TaskSkipParams is the wire format for task.skip.
type TaskSkipParams struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id,omitempty"` // empty = skip next planned
}

// TaskSkipResult reports the outcome of task.skip.
type TaskSkipResult struct {
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}

// TaskUndoParams is the wire format for task.undo.
type TaskUndoParams struct {
	SessionID string `json:"session_id"`
}

// TaskUndoResult reports the outcome of task.undo.
type TaskUndoResult struct {
	Status string `json:"status"`
	Undone string `json:"undone"` // "skip:task-1" etc.
}

