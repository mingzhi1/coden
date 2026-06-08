package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMProfiler implements workflow.Profiler: a single LLM call that turns cheap
// project context (manifest + README head + file tree) into a prose overview and
// a code-style summary. It runs at most once per profile (cached until manifests
// change), so it uses the Light tier — this is summarization, not reasoning.
type LLMProfiler struct {
	chatter Chatter
}

func NewLLMProfiler(chatter Chatter) *LLMProfiler {
	return &LLMProfiler{chatter: chatter}
}

func (p *LLMProfiler) Profile(ctx context.Context, in workflow.ProfileInput) (workflow.ProfileResult, error) {
	if p.chatter == nil {
		return workflow.ProfileResult{}, fmt.Errorf("profiler: no chatter configured")
	}

	var b strings.Builder
	if len(in.Languages) > 0 {
		fmt.Fprintf(&b, "Detected languages: %s\n\n", strings.Join(in.Languages, ", "))
	}
	if s := strings.TrimSpace(in.GoMod); s != "" {
		fmt.Fprintf(&b, "## Primary manifest\n%s\n\n", s)
	}
	if s := strings.TrimSpace(in.Readme); s != "" {
		fmt.Fprintf(&b, "## README (head)\n%s\n\n", s)
	}
	if len(in.FileTree) > 0 {
		b.WriteString("## File tree (partial)\n")
		for _, f := range in.FileTree {
			b.WriteString(f)
			b.WriteString("\n")
		}
	}

	reply, err := RecoverableChat(ctx, p.chatter, RoleProfiler, []Message{
		{Role: "system", Content: prompts.Profiler()},
		{Role: "user", Content: b.String()},
	}, defaultRecoveryConfig())
	if err != nil {
		return workflow.ProfileResult{}, fmt.Errorf("profiler llm: %w", err)
	}

	var parsed struct {
		Overview string `json:"overview"`
		Style    string `json:"style"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &parsed); err != nil {
		// Non-JSON reply: treat the whole prose as the overview rather than failing.
		return workflow.ProfileResult{Overview: strings.TrimSpace(reply)}, nil
	}
	return workflow.ProfileResult{
		Overview: strings.TrimSpace(parsed.Overview),
		Style:    strings.TrimSpace(parsed.Style),
	}, nil
}

var _ workflow.Profiler = (*LLMProfiler)(nil)
