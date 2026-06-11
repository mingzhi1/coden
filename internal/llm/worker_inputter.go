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
			// Carry the assistant's previous reply so short follow-ups ("do it",
			// "go ahead", "yes") can be resolved against what was actually said —
			// e.g. a plan_only proposal that the user now wants executed.
			if resp := strings.TrimSpace(last.Response); resp != "" {
				const maxPrevResp = 600
				if len(resp) > maxPrevResp {
					resp = resp[:maxPrevResp] + "…"
				}
				prevIntentHint += fmt.Sprintf("\nPrevious assistant reply: %q", resp)
			}
		}
	}

	systemPrompt := prompts.Inputter(prevIntentHint)

	ctxInfo := inputterContext(ctx) // scoped: prev-turns + history + light profile (no FileTree bias)
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
		// Default to analyze (READ-ONLY) on parse failure, never code_gen: an
		// unparseable intent must NOT be routed to the file-WRITING pipeline. The
		// worst case for an uncertain request is read-only investigation — a misread
		// write-request degraded to analysis is merely re-asked; a misread
		// read-request that writes files is destructive. Worst case = analyze.
		parsed.Kind = model.IntentKindAnalyze
		parsed.SuccessCriteria = []string{"investigate and report"}
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
	// Validate kind against the single source of truth; default to other for
	// unknown values. Deriving from model.AllIntentKinds keeps this in lock-step
	// with the Dispatcher's routing table — no per-kind whitelist to drift.
	if !model.IsKnownIntentKind(parsed.Kind) {
		parsed.Kind = model.IntentKindOther
	}

	// Safety guard (代码裁决): a pure look-up / explain request must NEVER route to
	// the file-WRITING pipeline, even if the LLM classified it as a write-kind. The
	// prompt asks for this, but a Light-tier model often mis-picks code for "查 X 文档".
	// Downgrade to read-only — research when it's about external knowledge, else
	// analyze. This only ever downgrades TOWARD read-only, so a false positive costs
	// convenience (a code request answered as analysis, re-asked), never a wrongful
	// write. Worst case for an uncertain request is read-only.
	// Run the guard on the RAW prompt, not the normalized goal: the Inputter LLM
	// often rephrases "查一下 X 有哪些" into a goal that drops the look-up cues, so
	// the user's actual words are the faithful signal for read-only intent.
	parsed.Kind = safeIntentKind(parsed.Kind, prompt)

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

// --- intent safety guard helpers (deterministic look-up vs write detection) ---

func isWriteKind(k string) bool {
	switch k {
	case model.IntentKindCodeGen, model.IntentKindDebug, model.IntentKindRefactor, model.IntentKindConfig:
		return true
	}
	return false
}

// lookupCues are STRONG look-up phrasings (zh + en). Kept strict — bare "查"
// matches 查询/检查 in coding contexts, so only unambiguous phrases are listed.
var lookupCues = []string{
	"查一下", "查询一下", "查下", "有哪些", "怎么用", "如何使用", "如何用", "什么是", "是什么",
	"介绍一下", "说明一下", "解释一下", "怎么实现的", "是怎么", "了解一下", "查看文档",
	"look up", "what is", "what are", "how does", "how do i use", "explain ", "list the", "describe ", "docs for", "documentation for",
}

// writeVerbs are kept comprehensive so a real coding request (which the guard must
// NOT downgrade) is reliably detected and left alone.
var writeVerbs = []string{
	"实现", "写", "加", "添加", "改", "修改", "优化", "重构", "创建", "新建", "建一个", "修复", "生成",
	"删除", "重命名", "封装", "集成", "支持", "搭建", "迁移", "升级",
	"implement", "write", "add ", "create", "fix", "refactor", "optimize", "build ", "generate",
	"modify", "edit ", "delete", "rename", "wrap", "integrate", "migrate", "upgrade", "scaffold",
}

// externalCues mark a look-up as about EXTERNAL knowledge (→ research) rather than
// this codebase (→ analyze).
var externalCues = []string{
	"标准库", "三方", "第三方", "外部", "官方文档", "library", "package", "sdk", "api", "stdlib", "framework",
}

func containsAny(s string, cues []string) bool {
	s = strings.ToLower(s)
	for _, c := range cues {
		if strings.Contains(s, strings.ToLower(c)) {
			return true
		}
	}
	return false
}

func isLookupGoal(g string) bool              { return containsAny(g, lookupCues) }
func hasWriteVerb(g string) bool              { return containsAny(g, writeVerbs) }
func mentionsExternalKnowledge(g string) bool { return containsAny(g, externalCues) }

// safeIntentKind enforces the read-only-on-uncertainty invariant: a pure look-up /
// explain goal classified as a write-kind is downgraded to read-only (research for
// external knowledge, else analyze). It only ever downgrades toward read-only, so
// it can never turn a request into a wrongful write.
func safeIntentKind(kind, goal string) string {
	if isWriteKind(kind) && isLookupGoal(goal) && !hasWriteVerb(goal) {
		if mentionsExternalKnowledge(goal) {
			return model.IntentKindResearch
		}
		return model.IntentKindAnalyze
	}
	return kind
}

var _ workflow.Inputter = (*LLMInputter)(nil)

func (i *LLMInputter) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "llm-input", Role: workflow.RoleInput}
}
