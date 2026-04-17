package skill

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Registry manages loaded skills with thread-safe access.
//
// The core design principle: skills are NOT injected uniformly.
// Each skill declares which workers it targets (coder, planner, acceptor, inputter),
// and its Source trust level imposes a hard ceiling on those targets.
//
// Usage:
//
//	reg := NewRegistry()
//	reg.LoadFromDir("~/.coden/skills", SourceUser)
//	reg.LoadFromDir(".coden/skills", SourceProject)
//	RegisterBuiltins(reg)
//
//	// In the Coder worker:
//	prompt := reg.FormatForWorker(TargetCoder, touchedPaths)
//
//	// In the Acceptor worker (only trusted skills visible):
//	prompt := reg.FormatForWorker(TargetAcceptor, touchedPaths)
type Registry struct {
	mu     sync.RWMutex
	skills map[string]*Skill // name -> skill (highest priority wins)
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
	}
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// LoadFromDirs scans multiple directories. Later directories have lower
// priority (an earlier directory's skill with the same name wins).
func (r *Registry) LoadFromDirs(dirs ...string) error {
	// Load in reverse order so higher-priority dirs overwrite lower ones.
	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]
		if dir == "" {
			continue
		}
		// Infer source from path heuristic; callers can use LoadFromDir
		// with an explicit source for precision.
		source := inferSource(dir)
		if err := r.LoadFromDir(dir, source); err != nil {
			slog.Warn("[skill] failed to load skills", "dir", dir, "error", err)
		}
	}
	return nil
}

// LoadFromDir loads all skills in a directory with the given source.
// Each subdirectory must contain a SKILL.md file.
func (r *Registry) LoadFromDir(dir string, source Source) error {
	skills, err := LoadSkillsFromDir(dir, source)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range skills {
		s.LoadedFrom = source
		s.Validate()
		r.skills[s.Frontmatter.Name] = s
		slog.Debug("[skill] loaded",
			"name", s.Frontmatter.Name,
			"source", string(source),
			"targets", s.EffectiveTargets(),
		)
	}
	return nil
}

// LoadRules loads a RULES.md file as an implicit skill with elevated targets.
// RULES.md is project-owned but treated as an explicit opt-in, so it is
// allowed to target both coder and planner (same as SourceProject ceiling).
func (r *Registry) LoadRules(path string, source Source) error {
	s, err := LoadRulesFile(path, source)
	if err != nil {
		return err
	}
	// RULES.md targets coder + planner by default (project convention guide).
	if len(s.Frontmatter.Targets) == 0 {
		s.Frontmatter.Targets = []Target{TargetCoder, TargetPlanner}
	}
	s.Validate()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Frontmatter.Name] = s
	return nil
}

// Register adds or replaces a skill. The skill is validated before insertion.
// Used for builtin and plugin skills.
func (r *Registry) Register(s *Skill) {
	s.Validate()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Frontmatter.Name] = s
}

// ---------------------------------------------------------------------------
// Querying
// ---------------------------------------------------------------------------

// Get returns a skill by name, or nil if not found.
func (r *Registry) Get(name string) *Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.skills[name]
}

// ListAll returns all registered skills (unfiltered).
func (r *Registry) ListAll() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	return out
}

// Count returns the total number of registered skills.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}

// ---------------------------------------------------------------------------
// Per-worker filtered querying (the core API)
// ---------------------------------------------------------------------------

// GetSkillsForWorker returns skills that:
//  1. Are allowed to inject into the given target (post-validation ceiling)
//  2. Match at least one of the touched file paths (or have no paths constraint)
//
// Results are sorted by priority (descending), then by name (ascending).
func (r *Registry) GetSkillsForWorker(target Target, touchedPaths []string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matched []*Skill
	for _, s := range r.skills {
		if !s.CanInjectInto(target) {
			continue
		}
		if !s.MatchesAnyPath(touchedPaths) {
			continue
		}
		matched = append(matched, s)
	}

	// Sort: higher priority first, then alphabetical for determinism.
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Frontmatter.Priority != matched[j].Frontmatter.Priority {
			return matched[i].Frontmatter.Priority > matched[j].Frontmatter.Priority
		}
		return matched[i].Frontmatter.Name < matched[j].Frontmatter.Name
	})

	return matched
}

// FormatForWorker renders the active skills for a specific worker target
// as a prompt section ready for injection into the LLM system prompt.
//
// Returns empty string if no skills are active for this worker.
//
// This is the ONLY correct way to get skill content for prompt injection.
// Do NOT use FormatForPrompt (which would inject everything everywhere).
//
// Each skill block includes a source label so the LLM (and audit logs)
// can see where the instruction came from:
//
//	### Skill: code-review [project]
//	_Enforce team code review standards_
//
//	When reviewing code, always check error handling...
func (r *Registry) FormatForWorker(target Target, touchedPaths []string) string {
	skills := r.GetSkillsForWorker(target, touchedPaths)
	if len(skills) == 0 {
		return ""
	}

	// Cap: inject at most 5 skills per worker to prevent prompt bloat.
	const maxSkillsPerWorker = 5
	if len(skills) > maxSkillsPerWorker {
		slog.Warn("[skill] too many active skills for worker, truncating",
			"target", string(target),
			"total", len(skills),
			"kept", maxSkillsPerWorker,
		)
		skills = skills[:maxSkillsPerWorker]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Active Skills (%s)\n\n", string(target)))

	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("### Skill: %s [%s]\n", s.Frontmatter.Name, s.SourceLabel()))
		if s.Frontmatter.Description != "" {
			sb.WriteString(fmt.Sprintf("_%s_\n\n", s.Frontmatter.Description))
		}
		sb.WriteString(strings.TrimSpace(s.Content))
		sb.WriteString("\n\n---\n\n")
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Deprecated: FormatForPrompt — use FormatForWorker instead
// ---------------------------------------------------------------------------

// FormatForPrompt renders ALL active skills regardless of target.
//
// Deprecated: This injects skills into every worker equally, violating the
// principle of least privilege. Use FormatForWorker(target, paths) instead.
// Kept only for backward compatibility during migration.
func (r *Registry) FormatForPrompt(touchedPaths []string) string {
	return r.FormatForWorker(TargetCoder, touchedPaths)
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

// Summary returns a human-readable summary of all registered skills,
// grouped by target. Useful for --debug output and tests.
func (r *Registry) Summary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.skills) == 0 {
		return "No skills loaded."
	}

	byTarget := make(map[Target][]*Skill)
	for _, s := range r.skills {
		for _, t := range s.EffectiveTargets() {
			byTarget[t] = append(byTarget[t], s)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Skills loaded: %d\n\n", len(r.skills)))

	for _, target := range AllTargets {
		skills := byTarget[target]
		if len(skills) == 0 {
			continue
		}
		sort.Slice(skills, func(i, j int) bool {
			return skills[i].Frontmatter.Name < skills[j].Frontmatter.Name
		})
		sb.WriteString(fmt.Sprintf("  [%s] (%d skills)\n", string(target), len(skills)))
		for _, s := range skills {
			sb.WriteString(fmt.Sprintf("    - %-20s  src=%-10s  priority=%d",
				s.Frontmatter.Name, string(s.LoadedFrom), s.Frontmatter.Priority))
			if len(s.Frontmatter.Paths) > 0 {
				sb.WriteString(fmt.Sprintf("  paths=%v", s.Frontmatter.Paths))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// inferSource guesses the Source from a directory path.
// This is a best-effort heuristic; callers should use LoadFromDir with
// an explicit source when precision matters.
func inferSource(dir string) Source {
	if strings.Contains(dir, ".coden/skills") || strings.Contains(dir, ".coden\\skills") {
		// Could be either user or project; check for home dir marker.
		home, _ := homeDir()
		if home != "" && strings.HasPrefix(dir, home) {
			return SourceUser
		}
		return SourceProject
	}
	return SourceProject
}

// homeDir returns the user's home directory or empty string.
func homeDir() (string, error) {
	// Avoid importing os at module level just for this;
	// the function is called rarely.
	import_os_UserHomeDir := userHomeDirFunc
	if import_os_UserHomeDir == nil {
		return "", nil
	}
	return import_os_UserHomeDir()
}

// userHomeDirFunc is injected to avoid a hard os dependency in this file.
// It is set by init() in loader.go or can be overridden in tests.
var userHomeDirFunc func() (string, error)
