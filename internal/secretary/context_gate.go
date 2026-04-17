package secretary

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mingzhi1/coden/internal/skill"
)

// AssembleContext returns the ContextBlocks authorized for injection into
// the given Worker target, filtered by:
//  1. Trust matrix (source trust level >= target minimum)
//  2. Path activation (skill.Paths matches touchedPaths)
//  3. Content budget (truncation by source)
//  4. Count cap (max skills per worker)
//
// This is the ONLY correct way to get extension content for a Worker's prompt.
func (s *Secretary) AssembleContext(sessionID string, target Target, touchedPaths []string) []ContextBlock {
	if s.skills == nil {
		return nil
	}

	minTrust := MinTrustForTarget(target)
	allSkills := s.skills.ListAll()

	var candidates []*skill.Skill
	for _, sk := range allSkills {
		sourceName := string(sk.LoadedFrom)
		effectiveSource, effectiveTrust := s.policy.EffectiveTrustLevel(sk.Frontmatter.Name, sourceName)
		_ = effectiveSource

		// Check 1: Trust level sufficient for this target?
		if effectiveTrust < minTrust {
			s.audit(sessionID, AuditEntry{
				Type:    "skill_injection",
				Allowed: false,
				Reason:  fmt.Sprintf("source %s (trust %d) below minimum %d for %s", sourceName, effectiveTrust, minTrust, target),
				Details: map[string]any{
					"skill":       sk.Frontmatter.Name,
					"source":      sourceName,
					"trust_level": effectiveTrust,
					"target":      string(target),
					"target_min":  minTrust,
				},
			})
			continue
		}

		// Check 2: Skill declares this target? (or default = coder only)
		if !skillTargetsWorker(sk, target) {
			continue
		}

		// Check 3: Path activation
		if !sk.MatchesAnyPath(touchedPaths) {
			continue
		}

		candidates = append(candidates, sk)
	}

	// Sort by priority (descending), then name (ascending) for determinism.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Frontmatter.Priority != candidates[j].Frontmatter.Priority {
			return candidates[i].Frontmatter.Priority > candidates[j].Frontmatter.Priority
		}
		return candidates[i].Frontmatter.Name < candidates[j].Frontmatter.Name
	})

	// Cap count
	maxCount := s.policy.MaxSkillsPerWorker
	if maxCount <= 0 {
		maxCount = 5
	}
	if len(candidates) > maxCount {
		candidates = candidates[:maxCount]
	}

	// Build blocks with content truncation
	maxTotalBytes := s.policy.MaxSkillBytesPerWorker
	if maxTotalBytes <= 0 {
		maxTotalBytes = 20480
	}
	totalBytes := 0
	var blocks []ContextBlock
	for _, sk := range candidates {
		content := strings.TrimSpace(sk.Content)
		truncated := false

		// Per-source content limit
		sourceMax := s.policy.MaxContentBySource[string(sk.LoadedFrom)]
		if sourceMax > 0 && len(content) > sourceMax {
			content = content[:sourceMax] + "\n... (truncated)"
			truncated = true
		}

		// Total budget check
		if totalBytes+len(content) > maxTotalBytes {
			remaining := maxTotalBytes - totalBytes
			if remaining <= 100 {
				break // not enough space for anything meaningful
			}
			content = content[:remaining] + "\n... (truncated)"
			truncated = true
		}
		totalBytes += len(content)

		block := ContextBlock{
			Kind:      "skill",
			Name:      sk.Frontmatter.Name,
			Source:    string(sk.LoadedFrom),
			Content:   content,
			Truncated: truncated,
			Priority:  sk.Frontmatter.Priority,
		}
		blocks = append(blocks, block)

		s.audit(sessionID, AuditEntry{
			Type:    "skill_injection",
			Allowed: true,
			Details: map[string]any{
				"skill":     sk.Frontmatter.Name,
				"source":    string(sk.LoadedFrom),
				"target":    string(target),
				"bytes":     len(content),
				"truncated": truncated,
			},
		})
	}

	return blocks
}

// FormatContextBlocks renders ContextBlocks as a prompt section string.
func FormatContextBlocks(target Target, blocks []ContextBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Active Context (%s)\n\n", string(target)))
	for _, b := range blocks {
		sb.WriteString(fmt.Sprintf("### %s: %s [%s]\n", b.Kind, b.Name, b.Source))
		sb.WriteString(b.Content)
		sb.WriteString("\n\n---\n\n")
	}
	return sb.String()
}

// skillTargetsWorker checks if a skill declares the given target.
// If the skill has no Targets in frontmatter, defaults to coder only.
func skillTargetsWorker(sk *skill.Skill, target Target) bool {
	declared := sk.Frontmatter.Targets
	if len(declared) == 0 {
		// Default: coder only
		return target == TargetCoder
	}
	for _, t := range declared {
		if string(t) == string(target) {
			return true
		}
	}
	return false
}
