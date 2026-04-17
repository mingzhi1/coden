// Package skill implements the Skill system — Markdown prompt injection
// with YAML frontmatter, scoped by worker target and trust level.
//
// Design principle: Skills are NOT equal-privilege. Each Skill declares
// which workflow workers it may inject into (target), and the system
// enforces hard restrictions based on the Skill's trust Source.
//
//	Builtin/User  → may target any worker
//	Project       → may target Coder + Planner (not Acceptor/Inputter)
//	Plugin        → may target Coder only (unless explicitly promoted)
//	MCP           → may target Coder only, and cannot list run_shell
package skill

import (
	"time"
)

// ---------------------------------------------------------------------------
// Source — where the skill was loaded from (determines trust ceiling)
// ---------------------------------------------------------------------------

// Source indicates where a skill was loaded from.
// Trust order (highest → lowest): Builtin > User > Project > Plugin > MCP
type Source string

const (
	SourceBuiltin Source = "builtin" // compiled into the binary
	SourceUser    Source = "user"    // ~/.coden/skills/
	SourceProject Source = "project" // <workspace>/.coden/skills/
	SourcePlugin  Source = "plugin"  // third-party plugin
	SourceMCP     Source = "mcp"     // MCP server prompts (lowest trust)
)

// TrustLevel returns a numeric trust level for comparison (higher = more trusted).
func (s Source) TrustLevel() int {
	switch s {
	case SourceBuiltin:
		return 100
	case SourceUser:
		return 80
	case SourceProject:
		return 60
	case SourcePlugin:
		return 40
	case SourceMCP:
		return 20
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// Target — which workflow worker a skill is allowed to inject into
// ---------------------------------------------------------------------------

// Target represents a workflow worker that a skill can inject into.
type Target string

const (
	TargetCoder    Target = "coder"    // code generation (most common)
	TargetPlanner  Target = "planner"  // task planning
	TargetAcceptor Target = "acceptor" // quality review (restricted)
	TargetInputter Target = "inputter" // intent parsing (restricted)
)

// AllTargets is the full set of injectable workers.
var AllTargets = []Target{TargetCoder, TargetPlanner, TargetAcceptor, TargetInputter}

// ---------------------------------------------------------------------------
// Trust ceiling — hard limits on what each Source may target
// ---------------------------------------------------------------------------

// maxTargets defines the maximum set of workers each Source is allowed to
// inject into. This is a HARD ceiling — even if the SKILL.md frontmatter
// lists a wider target set, it will be clamped to this.
//
// Rationale:
//   - Acceptor is the safety gate. Letting untrusted skills influence it
//     would allow bypassing code review ("always approve").
//   - Inputter rewrites user intent. Manipulation here is invisible to
//     the user and undermines the entire pipeline.
//   - Coder is the natural home for coding standards, style guides, etc.
//   - Planner benefits from architecture/convention guidance, but only
//     from sources the user explicitly trusts.
var maxTargets = map[Source]map[Target]bool{
	SourceBuiltin: {TargetCoder: true, TargetPlanner: true, TargetAcceptor: true, TargetInputter: true},
	SourceUser:    {TargetCoder: true, TargetPlanner: true, TargetAcceptor: true, TargetInputter: true},
	SourceProject: {TargetCoder: true, TargetPlanner: true},
	SourcePlugin:  {TargetCoder: true},
	SourceMCP:     {TargetCoder: true},
}

// SourceAllowsTarget returns true if the given Source trust level permits
// injection into the given Target worker. This is the hard ceiling check.
func SourceAllowsTarget(source Source, target Target) bool {
	allowed, ok := maxTargets[source]
	if !ok {
		return false
	}
	return allowed[target]
}

// ---------------------------------------------------------------------------
// Tool restrictions — what tools a skill is allowed to list
// ---------------------------------------------------------------------------

// deniedToolsBySource lists tool kinds that a Skill from a given Source
// is forbidden from including in its allowed_tools field.
var deniedToolsBySource = map[Source]map[string]bool{
	SourceMCP:    {"run_shell": true, "write_file": true, "edit_file": true},
	SourcePlugin: {"run_shell": true},
}

// SourceAllowsTool returns true if a Skill from the given Source is
// permitted to list the tool kind in its allowed_tools.
func SourceAllowsTool(source Source, tool string) bool {
	denied, ok := deniedToolsBySource[source]
	if !ok {
		return true // no restrictions for this source
	}
	return !denied[tool]
}

// ---------------------------------------------------------------------------
// Frontmatter — YAML metadata at the top of SKILL.md
// ---------------------------------------------------------------------------

// Frontmatter represents the YAML frontmatter of a SKILL.md file.
type Frontmatter struct {
	// Name is the display name and invocation key (/name).
	Name string `yaml:"name"`
	// Description is a one-line summary shown in skill listings.
	Description string `yaml:"description"`
	// WhenToUse tells the LLM when to automatically consider this skill.
	WhenToUse string `yaml:"when_to_use,omitempty"`

	// Targets declares which workers this skill injects into.
	// Defaults: ["coder"] if omitted.
	// Subject to clamping by Source trust ceiling (see maxTargets).
	Targets []Target `yaml:"targets,omitempty"`

	// AllowedTools lists additional tool kinds the skill recommends.
	// These are advisory — actual execution is gated by Kernel tool scope.
	// Subject to filtering by Source restrictions (see deniedToolsBySource).
	AllowedTools []string `yaml:"allowed_tools,omitempty"`

	// Paths contains gitignore-style patterns. When set, the skill only
	// activates when the user touches a matching file. Empty = always active.
	Paths []string `yaml:"paths,omitempty"`

	// UserInvocable controls whether users can trigger this skill via /name.
	// Default: true.
	UserInvocable *bool `yaml:"user_invocable,omitempty"`

	// Arguments names positional parameters for manual invocation.
	Arguments []string `yaml:"arguments,omitempty"`

	// Context can be "fork" to run in a sub-agent.
	Context string `yaml:"context,omitempty"`

	// Effort hints at the desired LLM reasoning depth: "low"/"medium"/"high".
	Effort string `yaml:"effort,omitempty"`

	// Priority controls ordering within the same target. Higher = earlier in prompt.
	// Default: 0. Builtin skills use 100.
	Priority int `yaml:"priority,omitempty"`
}

// ---------------------------------------------------------------------------
// Skill — the loaded, validated, ready-to-use skill
// ---------------------------------------------------------------------------

// Skill represents a loaded and validated skill instance.
type Skill struct {
	Frontmatter Frontmatter

	// Content is the Markdown body (everything after the frontmatter).
	Content string

	// SourcePath is the absolute filesystem path to the SKILL.md file.
	// Empty for builtin skills.
	SourcePath string

	// LoadedFrom indicates the trust source of this skill.
	LoadedFrom Source

	// LoadedAt records when this skill was loaded.
	LoadedAt time.Time

	// effectiveTargets is the post-validation set of targets, clamped
	// by Source trust ceiling. Populated by Validate().
	effectiveTargets map[Target]bool

	// effectiveTools is the post-validation set of allowed tools,
	// filtered by Source restrictions. Populated by Validate().
	effectiveTools []string
}

// Validate clamps the skill's targets and tools to what its Source
// trust level actually permits. Must be called after loading.
func (s *Skill) Validate() {
	// --- Resolve effective targets ---
	declared := s.Frontmatter.Targets
	if len(declared) == 0 {
		// Default: coder only (principle of least privilege).
		declared = []Target{TargetCoder}
	}

	s.effectiveTargets = make(map[Target]bool, len(declared))
	for _, t := range declared {
		if SourceAllowsTarget(s.LoadedFrom, t) {
			s.effectiveTargets[t] = true
		}
		// Silently drop targets that exceed the trust ceiling.
		// In a future version we could emit a warning event.
	}

	// --- Resolve effective allowed tools ---
	s.effectiveTools = make([]string, 0, len(s.Frontmatter.AllowedTools))
	for _, tool := range s.Frontmatter.AllowedTools {
		if SourceAllowsTool(s.LoadedFrom, tool) {
			s.effectiveTools = append(s.effectiveTools, tool)
		}
	}
}

// ---------------------------------------------------------------------------
// Accessors (always use these; they respect validation)
// ---------------------------------------------------------------------------

// CanInjectInto returns true if this skill is allowed to inject into the
// given worker target. Returns false if Validate() has not been called.
func (s *Skill) CanInjectInto(target Target) bool {
	if s.effectiveTargets == nil {
		return false // not validated
	}
	return s.effectiveTargets[target]
}

// EffectiveTargets returns the post-validation target set.
func (s *Skill) EffectiveTargets() []Target {
	out := make([]Target, 0, len(s.effectiveTargets))
	for t := range s.effectiveTargets {
		out = append(out, t)
	}
	return out
}

// EffectiveTools returns the post-validation allowed tools list.
func (s *Skill) EffectiveTools() []string {
	return append([]string(nil), s.effectiveTools...)
}

// IsUserInvocable returns whether the skill can be manually invoked via /name.
func (s *Skill) IsUserInvocable() bool {
	if s.Frontmatter.UserInvocable == nil {
		return true
	}
	return *s.Frontmatter.UserInvocable
}

// MatchesPath returns true if the skill's paths pattern matches the given file path.
// If no paths constraint is set, the skill is always active.
func (s *Skill) MatchesPath(path string) bool {
	if len(s.Frontmatter.Paths) == 0 {
		return true
	}
	for _, pattern := range s.Frontmatter.Paths {
		if matchGlob(pattern, path) {
			return true
		}
	}
	return false
}

// MatchesAnyPath returns true if the skill matches any of the given paths.
// Returns true if the skill has no paths constraint (always active).
func (s *Skill) MatchesAnyPath(paths []string) bool {
	if len(s.Frontmatter.Paths) == 0 {
		return true
	}
	for _, p := range paths {
		if s.MatchesPath(p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Summary helpers
// ---------------------------------------------------------------------------

// SourceLabel returns a human-readable label for display in prompts.
// This lets the LLM (and users) see where a skill came from.
func (s *Skill) SourceLabel() string {
	switch s.LoadedFrom {
	case SourceBuiltin:
		return "builtin"
	case SourceUser:
		return "user"
	case SourceProject:
		return "project"
	case SourcePlugin:
		return "plugin:" + s.SourcePath
	case SourceMCP:
		return "mcp:" + s.SourcePath
	default:
		return "unknown"
	}
}

// IsTrusted returns true if the skill comes from a trusted source
// (builtin or user). Untrusted skills get additional restrictions.
func (s *Skill) IsTrusted() bool {
	return s.LoadedFrom == SourceBuiltin || s.LoadedFrom == SourceUser
}
