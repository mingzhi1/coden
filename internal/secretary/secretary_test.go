package secretary

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/skill"
)

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func makeSkill(name string, source skill.Source, targets []skill.Target, content string) *skill.Skill {
	return &skill.Skill{
		Frontmatter: skill.Frontmatter{
			Name:    name,
			Targets: targets,
		},
		Content:    content,
		LoadedFrom: source,
		LoadedAt:   time.Now(),
	}
}

// toSkillTargets converts secretary targets to skill targets for frontmatter.
func toSkillTargets(ts ...Target) []skill.Target {
	out := make([]skill.Target, len(ts))
	for i, t := range ts {
		out[i] = skill.Target(string(t))
	}
	return out
}

// allSkillTargets returns all four secretary targets converted to skill targets.
func allSkillTargets() []skill.Target {
	return toSkillTargets(TargetCoder, TargetPlanner, TargetAcceptor, TargetInputter)
}

// blockNames extracts the Name field from each ContextBlock.
func blockNames(blocks []ContextBlock) []string {
	names := make([]string, len(blocks))
	for i, b := range blocks {
		names[i] = b.Name
	}
	return names
}

// containsName checks whether any ContextBlock has the given Name.
func containsName(blocks []ContextBlock, name string) bool {
	for _, b := range blocks {
		if b.Name == name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 1. TestAssembleContext_TrustMatrix
//
// Trust matrix (secretary trust levels):
//
//	                Acceptor(70)  Inputter(70)  Planner(50)  Coder(10)
//	builtin (90)       ✅            ✅            ✅            ✅
//	user    (70)       ✅            ✅            ✅            ✅
//	project (50)       ❌            ❌            ✅            ✅
//	plugin  (30)       ❌            ❌            ❌            ✅
//	mcp     (10)       ❌            ❌            ❌            ✅
// ---------------------------------------------------------------------------

func TestAssembleContext_TrustMatrix(t *testing.T) {
	type tc struct {
		name    string
		source  skill.Source
		target  Target
		allowed bool
	}

	cases := []tc{
		// builtin (90) — passes every target threshold
		{"builtin→acceptor", skill.SourceBuiltin, TargetAcceptor, true},
		{"builtin→inputter", skill.SourceBuiltin, TargetInputter, true},
		{"builtin→planner", skill.SourceBuiltin, TargetPlanner, true},
		{"builtin→coder", skill.SourceBuiltin, TargetCoder, true},

		// user (70) — passes every target threshold
		{"user→acceptor", skill.SourceUser, TargetAcceptor, true},
		{"user→inputter", skill.SourceUser, TargetInputter, true},
		{"user→planner", skill.SourceUser, TargetPlanner, true},
		{"user→coder", skill.SourceUser, TargetCoder, true},

		// project (50) — blocked from acceptor/inputter (70)
		{"project→acceptor", skill.SourceProject, TargetAcceptor, false},
		{"project→inputter", skill.SourceProject, TargetInputter, false},
		{"project→planner", skill.SourceProject, TargetPlanner, true},
		{"project→coder", skill.SourceProject, TargetCoder, true},

		// plugin (30) — only passes coder (10)
		{"plugin→acceptor", skill.SourcePlugin, TargetAcceptor, false},
		{"plugin→inputter", skill.SourcePlugin, TargetInputter, false},
		{"plugin→planner", skill.SourcePlugin, TargetPlanner, false},
		{"plugin→coder", skill.SourcePlugin, TargetCoder, true},

		// mcp (10) — only passes coder (10)
		{"mcp→acceptor", skill.SourceMCP, TargetAcceptor, false},
		{"mcp→inputter", skill.SourceMCP, TargetInputter, false},
		{"mcp→planner", skill.SourceMCP, TargetPlanner, false},
		{"mcp→coder", skill.SourceMCP, TargetCoder, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := skill.NewRegistry()
			// Declare ALL targets in frontmatter so the skill is never
			// filtered by skillTargetsWorker; the only variable is the
			// trust ceiling.
			sk := makeSkill("trust-test", c.source, allSkillTargets(), "trust matrix content")
			reg.Register(sk)

			sec := New(reg, DefaultPolicy(), nil)
			blocks := sec.AssembleContext("sess", c.target, nil)

			found := containsName(blocks, "trust-test")
			if c.allowed && !found {
				t.Errorf("expected skill to be ALLOWED for %s→%s but was denied", c.source, c.target)
			}
			if !c.allowed && found {
				t.Errorf("expected skill to be DENIED for %s→%s but was allowed", c.source, c.target)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. TestAssembleContext_DefaultTargetIsCoder
// ---------------------------------------------------------------------------

func TestAssembleContext_DefaultTargetIsCoder(t *testing.T) {
	reg := skill.NewRegistry()
	// Empty Targets field → skillTargetsWorker defaults to coder only.
	sk := makeSkill("default-target", skill.SourceBuiltin, nil, "default body")
	reg.Register(sk)

	sec := New(reg, DefaultPolicy(), nil)

	// Must appear for Coder.
	blocks := sec.AssembleContext("sess", TargetCoder, nil)
	if !containsName(blocks, "default-target") {
		t.Error("skill with empty Targets should appear for Coder")
	}

	// Must NOT appear for Planner, Acceptor, or Inputter.
	for _, tgt := range []Target{TargetPlanner, TargetAcceptor, TargetInputter} {
		blocks = sec.AssembleContext("sess", tgt, nil)
		if containsName(blocks, "default-target") {
			t.Errorf("skill with empty Targets should NOT appear for %s", tgt)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. TestAssembleContext_PathFiltering
// ---------------------------------------------------------------------------

func TestAssembleContext_PathFiltering(t *testing.T) {
	reg := skill.NewRegistry()

	// Skill restricted to *.go files.
	restricted := makeSkill("go-only", skill.SourceBuiltin,
		toSkillTargets(TargetCoder), "go linting rules")
	restricted.Frontmatter.Paths = []string{"**/*.go"}
	reg.Register(restricted)

	// Skill with no path restriction → always active.
	unrestricted := makeSkill("always-on", skill.SourceBuiltin,
		toSkillTargets(TargetCoder), "universal rules")
	reg.Register(unrestricted)

	sec := New(reg, DefaultPolicy(), nil)

	// Only a .ts file is touched → restricted skill must NOT appear.
	blocks := sec.AssembleContext("sess", TargetCoder, []string{"main.ts"})

	if containsName(blocks, "go-only") {
		t.Error("go-only skill should NOT appear when touched files are [main.ts]")
	}
	if !containsName(blocks, "always-on") {
		t.Error("always-on skill should appear regardless of touched paths")
	}

	// Now touch a .go file → both should appear.
	blocks = sec.AssembleContext("sess", TargetCoder, []string{"main.go"})
	if !containsName(blocks, "go-only") {
		t.Error("go-only skill should appear when a .go file is touched")
	}
	if !containsName(blocks, "always-on") {
		t.Error("always-on skill should still appear when a .go file is touched")
	}
}

// ---------------------------------------------------------------------------
// 4. TestAssembleContext_ContentTruncation
// ---------------------------------------------------------------------------

func TestAssembleContext_ContentTruncation(t *testing.T) {
	reg := skill.NewRegistry()

	// 20 KB of content from an MCP source.
	bigContent := strings.Repeat("x", 20*1024)
	sk := makeSkill("big-mcp-skill", skill.SourceMCP,
		toSkillTargets(TargetCoder), bigContent)
	reg.Register(sk)

	policy := DefaultPolicy()
	policy.MaxContentBySource = map[string]int{
		"mcp": 2048,
	}
	sec := New(reg, policy, nil)

	blocks := sec.AssembleContext("sess", TargetCoder, nil)
	if len(blocks) == 0 {
		t.Fatal("expected at least one context block")
	}

	b := blocks[0]
	if !b.Truncated {
		t.Error("expected Truncated=true for 20 KB content with 2048 byte limit")
	}
	// After truncation: first 2048 bytes + "\n... (truncated)" (17 chars).
	maxExpected := 2048 + 50 // generous headroom for the suffix
	if len(b.Content) > maxExpected {
		t.Errorf("content length %d exceeds expected maximum ~%d after truncation",
			len(b.Content), maxExpected)
	}
}

// ---------------------------------------------------------------------------
// 5. TestAssembleContext_MaxSkillsPerWorker
// ---------------------------------------------------------------------------

func TestAssembleContext_MaxSkillsPerWorker(t *testing.T) {
	reg := skill.NewRegistry()

	for i := 0; i < 10; i++ {
		sk := makeSkill(
			fmt.Sprintf("skill-%02d", i),
			skill.SourceBuiltin,
			toSkillTargets(TargetCoder),
			fmt.Sprintf("content for skill %d", i),
		)
		reg.Register(sk)
	}

	policy := DefaultPolicy()
	policy.MaxSkillsPerWorker = 3
	sec := New(reg, policy, nil)

	blocks := sec.AssembleContext("sess", TargetCoder, nil)
	if len(blocks) != 3 {
		t.Errorf("expected exactly 3 blocks (MaxSkillsPerWorker=3), got %d: %v",
			len(blocks), blockNames(blocks))
	}
}

// ---------------------------------------------------------------------------
// 6. TestAssembleContext_SkillPromotion
// ---------------------------------------------------------------------------

func TestAssembleContext_SkillPromotion(t *testing.T) {
	// A project skill (trust 50) targeting acceptor (min 70).
	acceptorTargets := toSkillTargets(TargetAcceptor)

	t.Run("without_promotion", func(t *testing.T) {
		reg := skill.NewRegistry()
		sk := makeSkill("my-skill", skill.SourceProject, acceptorTargets, "project content")
		reg.Register(sk)

		sec := New(reg, DefaultPolicy(), nil)
		blocks := sec.AssembleContext("sess", TargetAcceptor, nil)

		if containsName(blocks, "my-skill") {
			t.Error("project skill (trust 50) should be DENIED for acceptor (min 70) without promotion")
		}
	})

	t.Run("with_promotion_to_user", func(t *testing.T) {
		reg := skill.NewRegistry()
		sk := makeSkill("my-skill", skill.SourceProject, acceptorTargets, "project content")
		reg.Register(sk)

		policy := DefaultPolicy()
		policy.SkillPromotions = map[string]string{"my-skill": "user"}
		sec := New(reg, policy, nil)
		blocks := sec.AssembleContext("sess", TargetAcceptor, nil)

		if !containsName(blocks, "my-skill") {
			t.Error("promoted project→user skill (trust 70) should be ALLOWED for acceptor (min 70)")
		}
	})
}

// ---------------------------------------------------------------------------
// 7. TestValidateTurnTransition_Legal
// ---------------------------------------------------------------------------

func TestValidateTurnTransition_Legal(t *testing.T) {
	sec := New(nil, DefaultPolicy(), nil)

	legal := []struct{ from, to string }{
		{"running", "pass"},
		{"running", "fail"},
		{"running", "canceled"},
		{"running", "failed"},
		{"running", "crashed"},
	}

	for _, tc := range legal {
		t.Run(tc.from+"→"+tc.to, func(t *testing.T) {
			if err := sec.ValidateTurnTransition("sess", "turn-1", tc.from, tc.to); err != nil {
				t.Errorf("expected legal transition %s→%s, got error: %v", tc.from, tc.to, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 8. TestValidateTurnTransition_Illegal
// ---------------------------------------------------------------------------

func TestValidateTurnTransition_Illegal(t *testing.T) {
	sec := New(nil, DefaultPolicy(), nil)

	illegal := []struct{ from, to string }{
		{"pass", "running"},
		{"failed", "running"},
		{"canceled", "pass"},
	}

	for _, tc := range illegal {
		t.Run(tc.from+"→"+tc.to, func(t *testing.T) {
			if err := sec.ValidateTurnTransition("sess", "turn-1", tc.from, tc.to); err == nil {
				t.Errorf("expected error for illegal transition %s→%s, got nil", tc.from, tc.to)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 9. TestValidateTaskTransition_Legal
// ---------------------------------------------------------------------------

func TestValidateTaskTransition_Legal(t *testing.T) {
	sec := New(nil, DefaultPolicy(), nil)

	legal := []struct{ from, to string }{
		{"planned", "coding"},
		{"coding", "passed"},
		{"coding", "failed"},
		{"failed", "retrying"},
		{"retrying", "coding"},
		{"planned", "skipped"},
		{"planned", "abandoned"},
	}

	for _, tc := range legal {
		t.Run(tc.from+"→"+tc.to, func(t *testing.T) {
			if err := sec.ValidateTaskTransition("sess", "task-1", tc.from, tc.to); err != nil {
				t.Errorf("expected legal transition %s→%s, got error: %v", tc.from, tc.to, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 10. TestValidateTaskTransition_Illegal
// ---------------------------------------------------------------------------

func TestValidateTaskTransition_Illegal(t *testing.T) {
	sec := New(nil, DefaultPolicy(), nil)

	illegal := []struct{ from, to string }{
		{"passed", "coding"},
		{"skipped", "coding"},
		{"abandoned", "coding"},
	}

	for _, tc := range illegal {
		t.Run(tc.from+"→"+tc.to, func(t *testing.T) {
			if err := sec.ValidateTaskTransition("sess", "task-1", tc.from, tc.to); err == nil {
				t.Errorf("expected error for illegal transition %s→%s, got nil", tc.from, tc.to)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 11. TestDecideFailureAction
// ---------------------------------------------------------------------------

func TestDecideFailureAction(t *testing.T) {
	cases := []struct {
		policyStr string
		expected  FailureAction
	}{
		{"stop", ActionStop},
		{"skip", ActionSkip},
		{"replan", ActionReplan},
		{"", ActionStop}, // default fallback
	}

	for _, tc := range cases {
		label := tc.policyStr
		if label == "" {
			label = "<empty>"
		}
		t.Run("policy="+label, func(t *testing.T) {
			policy := DefaultPolicy()
			policy.FailurePolicy = tc.policyStr
			sec := New(nil, policy, nil)

			got := sec.DecideFailureAction("sess", "task-1")
			if got != tc.expected {
				t.Errorf("expected %v (%d), got %v (%d)", tc.expected, tc.expected, got, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 12. TestAuthorizeToolCall_BuiltinTools
// ---------------------------------------------------------------------------

func TestAuthorizeToolCall_BuiltinTools(t *testing.T) {
	sec := New(nil, DefaultPolicy(), nil)

	// Known built-in tools must be allowed.
	for _, tool := range []string{"read_file", "write_file", "run_shell"} {
		t.Run("allow/"+tool, func(t *testing.T) {
			if err := sec.AuthorizeToolCall("sess", tool); err != nil {
				t.Errorf("expected built-in tool %q to be allowed, got: %v", tool, err)
			}
		})
	}

	// Unknown tool must be denied.
	t.Run("deny/foo", func(t *testing.T) {
		if err := sec.AuthorizeToolCall("sess", "foo"); err == nil {
			t.Error("expected error for unknown tool 'foo', got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// 13. TestFormatContextBlocks
// ---------------------------------------------------------------------------

func TestFormatContextBlocks(t *testing.T) {
	blocks := []ContextBlock{
		{
			Kind:     "skill",
			Name:     "style-guide",
			Source:   "user",
			Content:  "Use tabs not spaces.",
			Priority: 10,
		},
		{
			Kind:     "skill",
			Name:     "error-handling",
			Source:   "project",
			Content:  "Always wrap errors with fmt.Errorf.",
			Priority: 5,
		},
	}

	output := FormatContextBlocks(TargetCoder, blocks)

	if !strings.Contains(output, "## Active Context") {
		t.Error("output should contain '## Active Context' header")
	}
	if !strings.Contains(output, "style-guide") {
		t.Error("output should contain skill name 'style-guide'")
	}
	if !strings.Contains(output, "error-handling") {
		t.Error("output should contain skill name 'error-handling'")
	}
	if !strings.Contains(output, "Use tabs not spaces.") {
		t.Error("output should contain the content of the first block")
	}
	if !strings.Contains(output, "Always wrap errors") {
		t.Error("output should contain the content of the second block")
	}
}

// ---------------------------------------------------------------------------
// TestFormatContextBlocks_Empty — edge case: no blocks → empty string
// ---------------------------------------------------------------------------

func TestFormatContextBlocks_Empty(t *testing.T) {
	output := FormatContextBlocks(TargetCoder, nil)
	if output != "" {
		t.Errorf("expected empty string for nil blocks, got %q", output)
	}

	output = FormatContextBlocks(TargetCoder, []ContextBlock{})
	if output != "" {
		t.Errorf("expected empty string for empty blocks, got %q", output)
	}
}

// ---------------------------------------------------------------------------
// TestMinTrustForTarget — sanity-check the published thresholds
// ---------------------------------------------------------------------------

func TestMinTrustForTarget(t *testing.T) {
	cases := []struct {
		target   Target
		expected int
	}{
		{TargetCoder, 10},
		{TargetPlanner, 50},
		{TargetAcceptor, 70},
		{TargetInputter, 70},
		{Target("unknown"), 100},
	}

	for _, tc := range cases {
		t.Run(string(tc.target), func(t *testing.T) {
			got := MinTrustForTarget(tc.target)
			if got != tc.expected {
				t.Errorf("MinTrustForTarget(%q) = %d, want %d", tc.target, got, tc.expected)
			}
		})
	}
}
