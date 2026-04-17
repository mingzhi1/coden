package toolruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/artifact"
)

// ArtifactTool exposes read_artifact and list_artifacts to the LLM Coder.
// It allows the agent to retrieve previously saved tool results by ID,
// eliminating the need to re-execute expensive queries.
type ArtifactTool struct {
	mgr artifact.Manager
}

// NewArtifactTool creates an ArtifactTool backed by the given Manager.
func NewArtifactTool(mgr artifact.Manager) *ArtifactTool {
	return &ArtifactTool{mgr: mgr}
}

// Execute dispatches read_artifact and list_artifacts requests.
func (t *ArtifactTool) Execute(ctx context.Context, req Request) (Result, error) {
	switch req.Kind {
	case "read_artifact":
		return t.executeRead(ctx, req)
	case "list_artifacts":
		return t.executeList(ctx, req)
	default:
		return Result{}, fmt.Errorf("ArtifactTool: unsupported kind %q", req.Kind)
	}
}

// executeRead retrieves the content of a single artifact by ID.
//
// Request fields:
//   - Path: artifact ID (required)
func (t *ArtifactTool) executeRead(ctx context.Context, req Request) (Result, error) {
	id := strings.TrimSpace(req.Path)
	if id == "" {
		return Result{}, fmt.Errorf("read_artifact: artifact_id is required (use path field)")
	}

	meta, err := t.mgr.GetArtifact(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("read_artifact %s: %w", id, err)
	}

	content, err := t.mgr.GetArtifactContent(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("read_artifact content %s: %w", id, err)
	}

	summary := fmt.Sprintf("artifact %s (%s, %d bytes, tool=%s)", id, meta.Kind, meta.Size, meta.ToolKind)
	return Result{
		Summary: summary,
		Output:  string(content),
	}, nil
}

// executeList lists artifacts in the current workflow or session.
//
// Request fields:
//   - Query: optional filter — "workflow:<id>" or "session:<id>" (default: recent 20)
//   - Path:  optional — artifact kind filter (e.g. "tool_output", "diff")
func (t *ArtifactTool) executeList(ctx context.Context, req Request) (Result, error) {
	q := artifact.FindQuery{Limit: 20}

	// Parse query filter.
	filter := strings.TrimSpace(req.Query)
	if strings.HasPrefix(filter, "workflow:") {
		q.WorkflowID = strings.TrimPrefix(filter, "workflow:")
	} else if strings.HasPrefix(filter, "session:") {
		q.SessionID = strings.TrimPrefix(filter, "session:")
	} else {
		// Default: use workflow ID from context if available.
		if wfID := workflowIDFromContext(ctx); wfID != "" {
			q.WorkflowID = wfID
		}
	}

	// Optional kind filter via Path field.
	if req.Path != "" {
		q.Kinds = []artifact.ArtifactKind{artifact.ArtifactKind(req.Path)}
	}

	arts, err := t.mgr.FindArtifacts(ctx, q)
	if err != nil {
		return Result{}, fmt.Errorf("list_artifacts: %w", err)
	}

	if len(arts) == 0 {
		return Result{
			Summary: "no artifacts found",
			Output:  "No artifacts match the query.",
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d artifacts:\n\n", len(arts)))
	for _, a := range arts {
		line := fmt.Sprintf("- %s  kind=%s  size=%d  tool=%s",
			a.ID, a.Kind, a.Size, a.ToolKind)
		if a.Name != "" {
			line += fmt.Sprintf("  name=%s", a.Name)
		}
		if a.SourcePath != "" {
			line += fmt.Sprintf("  path=%s", a.SourcePath)
		}
		sb.WriteString(line + "\n")
	}

	return Result{
		Summary: fmt.Sprintf("listed %d artifacts", len(arts)),
		Output:  sb.String(),
	}, nil
}
