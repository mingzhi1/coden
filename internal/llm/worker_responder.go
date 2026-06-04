package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMResponder implements workflow.Responder using the Light-tier LLM. It runs
// as the final pipeline stage to produce the user-facing reply — answering a
// question/chat directly or summarizing what a code task accomplished.
type LLMResponder struct {
	chatter Chatter
	msgBuffer
}

func NewLLMResponder(chatter Chatter) *LLMResponder {
	return &LLMResponder{chatter: chatter}
}

// Respond builds a context block from the workflow outcome and asks the
// Responder role (Light tier) to produce the final user-facing text.
func (r *LLMResponder) Respond(ctx context.Context, intent model.IntentSpec, tasks []model.Task, cp model.CheckpointResult) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "User goal: %s\n", strings.TrimSpace(intent.Goal))

	if intent.Kind == model.IntentKindPlanOnly {
		b.WriteString("\nThis is a PLAN-ONLY request: a plan was produced and reviewed, but NO code was executed. Present the proposed plan to the user as ordered steps. Do not claim anything was implemented or changed.\n\nProposed plan:\n")
		for i, t := range tasks {
			fmt.Fprintf(&b, "%d. %s\n", i+1, t.Title)
		}
		b.WriteString("\nPresent these steps clearly and offer to execute them when the user is ready.\n")
	} else if len(tasks) == 0 {
		b.WriteString("\nNo code changes were required (greeting / question / chat). Answer the user directly.\n")
	} else {
		b.WriteString("\nWork done:\n")
		for _, t := range tasks {
			fmt.Fprintf(&b, "- [%s] %s\n", t.Status, t.Title)
		}
		fmt.Fprintf(&b, "Checkpoint: %s\n", cp.Status)
		if len(cp.ArtifactPaths) > 0 {
			fmt.Fprintf(&b, "Artifacts: %s\n", strings.Join(cp.ArtifactPaths, ", "))
		}
		if len(cp.Evidence) > 0 {
			fmt.Fprintf(&b, "Evidence: %s\n", strings.Join(cp.Evidence, "; "))
		}
		b.WriteString("\nSummarize for the user what was accomplished, concisely.\n")
	}

	reply, err := RecoverableChat(ctx, r.chatter, RoleResponder, []Message{
		{Role: "system", Content: prompts.Responder()},
		{Role: "user", Content: b.String()},
	}, defaultRecoveryConfig())
	if err != nil {
		return "", fmt.Errorf("responder llm: %w", err)
	}
	return strings.TrimSpace(reply), nil
}
