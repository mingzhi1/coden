package llm

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
)

// TestSafeIntentKind locks the read-only-on-uncertainty safety guard: a look-up
// request must never stay on a write-kind, and a real coding request must be left
// alone. The guard only ever downgrades TOWARD read-only.
func TestSafeIntentKind(t *testing.T) {
	cases := []struct {
		name string
		kind string
		goal string
		want string
	}{
		// look-up of EXTERNAL knowledge wrongly classified as code → research
		{"external lookup", model.IntentKindCodeGen, "查一下 Go 标准库 slices 包有哪些常用函数", model.IntentKindResearch},
		{"english external", model.IntentKindCodeGen, "what is the stripe charges api", model.IntentKindResearch},
		// look-up about THIS codebase wrongly classified as code → analyze
		{"internal lookup", model.IntentKindDebug, "这个项目的 RAG 怎么用的", model.IntentKindAnalyze},
		// real coding requests must be LEFT ALONE (write verb present)
		{"implement keeps code", model.IntentKindCodeGen, "实现一个 slices 工具函数", model.IntentKindCodeGen},
		{"optimize keeps code", model.IntentKindCodeGen, "优化查询性能", model.IntentKindCodeGen},
		{"add support keeps code", model.IntentKindCodeGen, "添加对 Postgres 的支持", model.IntentKindCodeGen},
		// non-write kinds are never touched
		{"analyze untouched", model.IntentKindAnalyze, "查一下有哪些函数", model.IntentKindAnalyze},
		{"question untouched", model.IntentKindQuestion, "什么是 RAG", model.IntentKindQuestion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeIntentKind(c.kind, c.goal); got != c.want {
				t.Errorf("safeIntentKind(%q, %q) = %q, want %q", c.kind, c.goal, got, c.want)
			}
		})
	}
}
