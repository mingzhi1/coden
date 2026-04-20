package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMCritic reviews task plans using an LLM and returns a structured critique.
// For structural anti-narcissism it should ideally use a DIFFERENT provider than
// the Planner; in practice the provider is selected by the caller at construction.
type LLMCritic struct {
	chatter Chatter
	msgBuffer
}

func NewLLMCritic(chatter Chatter) *LLMCritic {
	return &LLMCritic{chatter: chatter}
}

// Critique reviews tasks against the intent and returns issues + suggestions.
func (c *LLMCritic) Critique(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (model.CritiqueResult, error) {
	var taskList strings.Builder
	for _, t := range tasks {
		taskList.WriteString(fmt.Sprintf("- %s: %s", t.ID, t.Title))
		if len(t.Files) > 0 {
			taskList.WriteString(fmt.Sprintf(" (files: %s)", strings.Join(t.Files, ", ")))
		}
		if len(t.DependsOn) > 0 {
			taskList.WriteString(fmt.Sprintf(" [depends: %s]", strings.Join(t.DependsOn, ", ")))
		}
		if t.SuccessCmd != "" {
			taskList.WriteString(fmt.Sprintf(" [verify: %s]", t.SuccessCmd))
		}
		taskList.WriteString("\n")
	}

	var criteria strings.Builder
	for _, sc := range intent.SuccessCriteria {
		criteria.WriteString("- " + sc + "\n")
	}

	userMsg := fmt.Sprintf("## Goal\n%s\n\n## Success Criteria\n%s\n## Proposed Tasks\n%s",
		intent.Goal, criteria.String(), taskList.String())

	// Provide workspace file tree so the critic can validate file paths.
	if wc := model.WorkflowContextFrom(ctx); len(wc.FileTree) > 0 {
		const maxTreeFiles = 80
		tree := wc.FileTree
		if len(tree) > maxTreeFiles {
			tree = tree[:maxTreeFiles]
		}
		userMsg += fmt.Sprintf("\n## Workspace Files\n%s", strings.Join(tree, "\n"))
	}

	reply, err := RecoverableChat(ctx, c.chatter, RoleCritic, []Message{
		{Role: "system", Content: prompts.Critic()},
		{Role: "user", Content: userMsg},
	}, defaultRecoveryConfig())
	if err != nil {
		return model.CritiqueResult{}, fmt.Errorf("critic llm: %w", err)
	}

	var raw struct {
		Score       float64  `json:"score"`
		Approved    bool     `json:"approved"`
		Issues      []string `json:"issues"`
		Suggestions []string `json:"suggestions"`
		Summary     string   `json:"summary"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &raw); err != nil {
		// Fallback: if we can't parse, approve with a warning.
		c.push("warn", RoleCritic, "JSON parse failed, auto-approving plan")
		return model.CritiqueResult{Score: 0.8, Approved: true, Summary: "critic parse failed, proceeding"}, nil
	}

	return model.CritiqueResult{
		Score:       raw.Score,
		Approved:    raw.Approved,
		Issues:      raw.Issues,
		Suggestions: raw.Suggestions,
		Summary:     raw.Summary,
	}, nil
}

// Ensure LLMCritic satisfies the workflow.Critic interface.
var _ workflow.Critic = (*LLMCritic)(nil)
