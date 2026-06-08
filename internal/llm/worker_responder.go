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

	// Anchor the reply on the user's ACTUAL words. intent.Goal and task titles are
	// normalized (often to English) by upstream roles, so without the raw message
	// the Responder has no Chinese/other-language text to match and defaults to
	// English — even when the user asked for another language. Include the latest
	// user message so "reply in the user's language" has something to match.
	if userMsg := lastUserMessage(model.WorkflowContextFrom(ctx).History); userMsg != "" {
		fmt.Fprintf(&b, "User's latest message (REPLY IN THE SAME LANGUAGE as this message): %q\n\n", userMsg)
	}
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
		// Split into completed vs not, so the Responder reports real progress
		// (and can propose next steps) instead of a flat "work done" list.
		var done, pending []model.Task
		for _, t := range tasks {
			if t.Status == model.TaskStatusPassed {
				done = append(done, t)
			} else {
				pending = append(pending, t)
			}
		}
		b.WriteString("\nCompleted:\n")
		if len(done) == 0 {
			b.WriteString("- (none)\n")
		}
		for _, t := range done {
			fmt.Fprintf(&b, "- %s\n", t.Title)
		}
		if len(pending) > 0 {
			b.WriteString("Not completed:\n")
			for _, t := range pending {
				fmt.Fprintf(&b, "- [%s] %s\n", t.Status, t.Title)
			}
		}
		fmt.Fprintf(&b, "Checkpoint: %s\n", cp.Status)
		if len(cp.ArtifactPaths) > 0 {
			fmt.Fprintf(&b, "Artifacts: %s\n", strings.Join(cp.ArtifactPaths, ", "))
		}
		if len(cp.Evidence) > 0 {
			fmt.Fprintf(&b, "Evidence: %s\n", strings.Join(cp.Evidence, "; "))
		}
		if cp.Status != "pass" || len(pending) > 0 {
			// Partial / failed: report progress and propose a concrete next step.
			b.WriteString("\nThe work is INCOMPLETE or FAILED. Tell the user: what is done, what is not, " +
				"why (use the evidence), and a concrete NEXT STEP to finish or fix it. Be specific and brief.\n")
		} else {
			b.WriteString("\nSummarize for the user what was accomplished, concisely.\n")
		}
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

// lastUserMessage returns the most recent user-role message content from the
// conversation history (history is oldest-first), or "" if none.
func lastUserMessage(history []model.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return strings.TrimSpace(history[i].Content)
		}
	}
	return ""
}
