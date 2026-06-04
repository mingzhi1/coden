package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
)

// Responder is the final pipeline stage (Step 1). It synthesizes the user-facing
// natural-language response from the workflow outcome, replacing the previous
// deterministic completion message. Every pipeline path closes with it.
type Responder interface {
	Respond(ctx context.Context, intent model.IntentSpec, tasks []model.Task, checkpoint model.CheckpointResult) (string, error)
}

// LocalResponder is the deterministic fallback used when no LLM responder is
// configured, or when the LLM call fails. It produces a terse, factual summary
// so the workflow always has a usable response.
type LocalResponder struct{}

func NewLocalResponder() *LocalResponder { return &LocalResponder{} }

func (r *LocalResponder) Respond(_ context.Context, intent model.IntentSpec, tasks []model.Task, cp model.CheckpointResult) (string, error) {
	if len(tasks) == 0 {
		// Question/chat: nothing was built — acknowledge the goal.
		if g := strings.TrimSpace(intent.Goal); g != "" {
			return g, nil
		}
		return "Done.", nil
	}
	done := 0
	for _, t := range tasks {
		if t.Status == model.TaskStatusPassed {
			done++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Completed %d/%d task(s) (checkpoint: %s).", done, len(tasks), cp.Status)
	if len(cp.ArtifactPaths) > 0 {
		fmt.Fprintf(&b, " Artifacts: %s.", strings.Join(cp.ArtifactPaths, ", "))
	}
	return b.String(), nil
}
