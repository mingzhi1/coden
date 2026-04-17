package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
)

// SessionSnapshot aggregates the initial state for a session into one struct.
type SessionSnapshot struct {
	Messages         []model.Message
	LatestCheckpoint *model.CheckpointResult
	LatestRun        *model.WorkflowRun
	LatestIntent     *model.IntentSpec
	Changes          []model.WorkspaceChangedPayload
	LatestWorkflowID string
	ObjectDetails    []ObjectDetail
	LastEventSeq     uint64 // for R-01 zero-gap replay via SubscribeSince
}

// ObjectDetail holds the resolved details for a single tool invocation object.
type ObjectDetail struct {
	ToolCallID string
	Path       string
	Tool       string
	Status     string
	Summary    string
	Detail     string
	Preview    string
	ExitCode   int
}

// LoadSessionSnapshot fetches initial state for a session.
// It first tries the atomic session.snapshot RPC (R-02); if the client
// does not support it, it falls back to multiple individual calls.
func LoadSessionSnapshot(ctx context.Context, client ClientAPI, sessionID string) (SessionSnapshot, error) {
	// Try atomic snapshot first (R-02).
	kernelSnap, err := client.SessionSnapshot(ctx, sessionID, 50)
	if err == nil {
		// Map model.SessionSnapshot → api.SessionSnapshot
		snap := SessionSnapshot{
			Messages:         kernelSnap.Messages,
			LatestCheckpoint: kernelSnap.LatestCheckpoint,
			LatestRun:        kernelSnap.ActiveWorkflow,
			LatestIntent:     kernelSnap.LatestIntent,
			Changes:          kernelSnap.WorkspaceChanges,
			LastEventSeq:     kernelSnap.LastEventSeq,
		}
		if snap.LatestCheckpoint != nil {
			snap.LatestWorkflowID = snap.LatestCheckpoint.WorkflowID
		}
		if snap.LatestRun != nil && snap.LatestWorkflowID == "" {
			snap.LatestWorkflowID = snap.LatestRun.WorkflowID
		}
		if snap.LatestWorkflowID != "" {
			items, objErr := LoadWorkflowObjectDetails(ctx, client, sessionID, snap.LatestWorkflowID)
			if objErr != nil {
				return snap, objErr
			}
			snap.ObjectDetails = items
		}
		return snap, nil
	}

	// Fallback: multiple individual calls (for older server versions).
	return loadSessionSnapshotFallback(ctx, client, sessionID)
}

// loadSessionSnapshotFallback fetches initial state via multiple API calls.
// Used when the server does not support the atomic session.snapshot RPC.
func loadSessionSnapshotFallback(ctx context.Context, client ClientAPI, sessionID string) (SessionSnapshot, error) {
	snapshot := SessionSnapshot{}

	messages, err := client.ListMessages(ctx, sessionID, 50)
	if err != nil {
		return snapshot, fmt.Errorf("list messages: %w", err)
	}
	snapshot.Messages = messages

	checkpoints, err := client.ListCheckpoints(ctx, sessionID, 1)
	if err != nil {
		return snapshot, fmt.Errorf("list checkpoints: %w", err)
	}
	if len(checkpoints) > 0 {
		snapshot.LatestCheckpoint = &checkpoints[0]
		snapshot.LatestWorkflowID = checkpoints[0].WorkflowID
	}

	runs, err := client.ListWorkflowRuns(ctx, sessionID, 1)
	if err != nil {
		return snapshot, fmt.Errorf("list workflow runs: %w", err)
	}
	if len(runs) > 0 {
		snapshot.LatestRun = &runs[0]
		if snapshot.LatestWorkflowID == "" {
			snapshot.LatestWorkflowID = runs[0].WorkflowID
		}
	}

	intent, err := client.GetLatestIntent(ctx, sessionID)
	if err == nil && strings.TrimSpace(intent.Goal) != "" {
		snapshot.LatestIntent = &intent
	}

	changes, err := client.WorkspaceChanges(ctx, sessionID)
	if err != nil {
		return snapshot, fmt.Errorf("workspace changes: %w", err)
	}
	snapshot.Changes = changes

	if snapshot.LatestWorkflowID != "" {
		items, err := LoadWorkflowObjectDetails(ctx, client, sessionID, snapshot.LatestWorkflowID)
		if err != nil {
			return snapshot, err
		}
		snapshot.ObjectDetails = items
	}

	return snapshot, nil
}

// LoadWorkflowObjectDetails fetches and decodes tool invocation objects.
func LoadWorkflowObjectDetails(ctx context.Context, client ClientAPI, sessionID, workflowID string) ([]ObjectDetail, error) {
	objects, err := client.ListWorkflowRunObjects(ctx, sessionID, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list workflow objects: %w", err)
	}

	items := make([]ObjectDetail, 0, len(objects))
	for _, object := range objects {
		raw, readErr := client.ReadWorkflowRunObject(ctx, sessionID, workflowID, object.ID)
		if readErr != nil {
			continue
		}
		var payload struct {
			ToolCallID string `json:"tool_call_id"`
			Tool       string `json:"tool"`
			Status     string `json:"status"`
			Error      string `json:"error"`
			Request    struct {
				Path string `json:"path"`
			} `json:"request"`
			Response struct {
				Summary  string `json:"summary"`
				Output   string `json:"output"`
				Stderr   string `json:"stderr"`
				ExitCode int    `json:"exit_code"`
				Before   string `json:"before"`
				After    string `json:"after"`
				Diff     string `json:"diff"`
			} `json:"response"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}

		detail := payload.Response.Diff
		if detail == "" {
			detail = payload.Error
		}
		if detail == "" {
			detail = payload.Response.Stderr
		}
		preview := ""
		switch payload.Tool {
		case "write_file":
			preview = payload.Response.After
		case "read_file":
			preview = payload.Response.Output
		}
		items = append(items, ObjectDetail{
			ToolCallID: payload.ToolCallID,
			Path:       payload.Request.Path,
			Tool:       payload.Tool,
			Status:     payload.Status,
			Summary:    payload.Response.Summary,
			Detail:     detail,
			Preview:    preview,
			ExitCode:   payload.Response.ExitCode,
		})
	}
	return items, nil
}
