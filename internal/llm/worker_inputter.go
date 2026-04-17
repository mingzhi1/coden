package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMInputter uses an LLM to normalize the user prompt into an IntentSpec.
type LLMInputter struct {
	chatter Chatter
	msgBuffer
}

func NewLLMInputter(chatter Chatter) *LLMInputter {
	return &LLMInputter{chatter: chatter}
}

func (i *LLMInputter) Build(ctx context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	wc := model.WorkflowContextFrom(ctx)

	// M8-08: If there are previous turns, inform the LLM about the last intent
	// so it can distinguish "continue the previous work" from a new request.
	prevIntentHint := ""
	if len(wc.PreviousTurns) > 0 {
		last := wc.PreviousTurns[len(wc.PreviousTurns)-1]
		if last.Intent.Goal != "" {
			prevIntentHint = fmt.Sprintf("\n\nPrevious turn intent: %q (kind: %s, outcome: %s)",
				last.Intent.Goal, last.Intent.Kind, last.Checkpoint.Status)
		}
	}

	systemPrompt := prompts.Inputter(prevIntentHint)

	ctxInfo := contextSummary(ctx)
	userContent := prompt
	if ctxInfo != "" {
		userContent = ctxInfo + "\n## Current request\n" + prompt
	}

	reply, err := RecoverableChat(ctx, i.chatter, RoleInputter, []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}, defaultRecoveryConfig())
	if err != nil {
		return model.IntentSpec{}, fmt.Errorf("inputter llm: %w", err)
	}

	var parsed struct {
		Goal            string   `json:"goal"`
		Kind            string   `json:"kind"`
		SuccessCriteria []string `json:"success_criteria"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &parsed); err != nil {
		parsed.Goal = strings.TrimSpace(reply)
		// Default to code_gen so that parse failures still go through the full
		// Plan/Code/Accept pipeline rather than being misrouted as a question.
		parsed.Kind = model.IntentKindCodeGen
		parsed.SuccessCriteria = []string{"task is completed"}
	}
	if parsed.Goal == "" {
		parsed.Goal = strings.TrimSpace(prompt)
	}
	// Enforce goal length limit (200 chars)
	if len(parsed.Goal) > 200 {
		parsed.Goal = parsed.Goal[:200]
	}
	if len(parsed.SuccessCriteria) == 0 {
		parsed.SuccessCriteria = []string{"task is completed"}
	}
	// Enforce success_criteria count and length limits (2-4 items, 80 chars each)
	if len(parsed.SuccessCriteria) > 4 {
		parsed.SuccessCriteria = parsed.SuccessCriteria[:4]
	}
	for i := range parsed.SuccessCriteria {
		if len(parsed.SuccessCriteria[i]) > 80 {
			parsed.SuccessCriteria[i] = parsed.SuccessCriteria[i][:80]
		}
	}
	// Validate kind; default to other for unknown values.
	switch parsed.Kind {
	case model.IntentKindCodeGen, model.IntentKindDebug, model.IntentKindRefactor,
		model.IntentKindQuestion, model.IntentKindConfig,
		model.IntentKindChat, model.IntentKindAnalyze, model.IntentKindOther:
		// valid
	default:
		parsed.Kind = model.IntentKindOther
	}

	i.push("info", "input", fmt.Sprintf("intent parsed: [%s] %s", parsed.Kind, parsed.Goal))

	return model.IntentSpec{
		ID:              fmt.Sprintf("intent-%d", time.Now().UnixNano()),
		SessionID:       sessionID,
		Goal:            parsed.Goal,
		Kind:            parsed.Kind,
		SuccessCriteria: parsed.SuccessCriteria,
		CreatedAt:       time.Now(),
	}, nil
}

var _ workflow.Inputter = (*LLMInputter)(nil)

func (i *LLMInputter) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "llm-input", Role: workflow.RoleInput}
}
