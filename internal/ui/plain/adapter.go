package plain

import (
	"fmt"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// Adapter keeps the current CLI output intentionally plain.
// When CodeN grows a real TUI, this layer should stay thin and borrow
// interaction patterns from crush without depending on crush code.
type Adapter struct{}

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) RenderEvent(event model.Event) string {
	return fmt.Sprintf("[%03d] %s %s", event.Seq, event.SessionID, event.Topic)
}

func (a *Adapter) RenderCheckpoint(result model.CheckpointResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\ncheckpoint: %s\n", result.Status)
	fmt.Fprintf(&b, "workflow: %s\n", result.WorkflowID)
	b.WriteString("artifacts:\n")
	for _, path := range result.ArtifactPaths {
		fmt.Fprintf(&b, " - %s\n", path)
	}
	b.WriteString("evidence:\n")
	for _, item := range result.Evidence {
		fmt.Fprintf(&b, " - %s\n", item)
	}

	return strings.TrimRight(b.String(), "\n")
}

func (a *Adapter) RenderCheckpointList(results []model.CheckpointResult) string {
	if len(results) == 0 {
		return "no checkpoints"
	}

	var b strings.Builder
	b.WriteString("checkpoints:\n")
	for _, result := range results {
		fmt.Fprintf(&b, " - %s  %s  artifacts=%d evidence=%d\n",
			result.WorkflowID,
			result.Status,
			len(result.ArtifactPaths),
			len(result.Evidence),
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (a *Adapter) RenderSessionList(sessions []model.Session) string {
	if len(sessions) == 0 {
		return "no sessions"
	}

	var b strings.Builder
	b.WriteString("sessions:\n")
	for _, session := range sessions {
		created := session.CreatedAt.UTC().Format(time.RFC3339)
		if session.CreatedAt.IsZero() {
			created = "unknown"
		}
		fmt.Fprintf(&b, " - %s  project=%s  root=%s  created=%s\n",
			session.ID,
			session.ProjectID,
			session.ProjectRoot,
			created,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}
