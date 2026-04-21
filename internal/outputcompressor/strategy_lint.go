package outputcompressor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// LintOutputStrategy groups linter diagnostics by rule/cop, producing a
// compact summary. Supports golangci-lint, eslint, ruff, and similar tools.
type LintOutputStrategy struct{}

func (s *LintOutputStrategy) Name() string { return "lint" }

func (s *LintOutputStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	cmd := strings.TrimSpace(command)
	for _, kw := range []string{
		"golangci-lint", "eslint",
		"ruff check", "ruff ",
		"clippy",
		"rubocop", "pylint", "flake8",
		"staticcheck", "revive",
	} {
		if strings.Contains(cmd, kw) {
			return true
		}
	}
	// Match "lint" only as a whole word boundary (preceded by space, /, or start).
	// Avoids false positives like "flint", "splinter".
	for _, prefix := range []string{" lint", "/lint", "\tlint"} {
		if strings.Contains(cmd, prefix) {
			return true
		}
	}
	return strings.HasPrefix(cmd, "lint")
}

// lintDiagRe matches common lint output formats:
//
//	file.go:10:3: message (linter)              — golangci-lint
//	file.ts:5:1  error  rule-name  message      — eslint
//	file.py:10:5: E401 message                  — ruff / flake8
var lintDiagRe = regexp.MustCompile(
	`^(.+?):(\d+):\d+:?\s+(.+)$`,
)

// golangciRuleRe extracts the linter name in parentheses at end of line.
// Example: "exported function Foo should have comment (golint)"
var golangciRuleRe = regexp.MustCompile(`\((\w[\w-]*)\)\s*$`)

// eslintRuleRe extracts eslint rule names.
// Example: "  error  no-unused-vars  'x' is defined..."
var eslintRuleRe = regexp.MustCompile(`\s+(error|warning)\s+([\w@/-]+)\s+`)

// ruffRuleRe extracts ruff/flake8/pycodestyle rule codes.
// Example: "file.py:10:5: E401 message"
var ruffRuleRe = regexp.MustCompile(`:\s+([A-Z]\d{3,4})\s`)

func (s *LintOutputStrategy) Compress(output string, budget int) string {
	lines := strings.Split(output, "\n")

	type diagnostic struct {
		file    string
		line    string
		message string
	}

	// rule → list of locations
	byRule := make(map[string][]string)
	totalIssues := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		m := lintDiagRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}

		file := m[1]
		lineNum := m[2]
		msg := m[3]
		loc := fmt.Sprintf("%s:%s", file, lineNum)

		// Try to extract rule name from various formats.
		rule := extractLintRule(trimmed, msg)
		byRule[rule] = append(byRule[rule], loc)
		totalIssues++
	}

	if totalIssues == 0 {
		return output
	}

	// Sort rules by issue count (descending).
	type ruleEntry struct {
		rule  string
		locs  []string
		count int
	}
	entries := make([]ruleEntry, 0, len(byRule))
	for rule, locs := range byRule {
		entries = append(entries, ruleEntry{rule: rule, locs: locs, count: len(locs)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].rule < entries[j].rule
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("lint: %d issues (%d rules)\n", totalIssues, len(entries)))

	for _, e := range entries {
		// Show up to 5 locations per rule, then summarize.
		if len(e.locs) <= 5 {
			sb.WriteString(fmt.Sprintf("  %s (%d): %s\n", e.rule, e.count, strings.Join(e.locs, ", ")))
		} else {
			shown := strings.Join(e.locs[:5], ", ")
			sb.WriteString(fmt.Sprintf("  %s (%d): %s, ... +%d more\n", e.rule, e.count, shown, e.count-5))
		}
	}

	result := sb.String()
	if len(result) > budget {
		return truncate(result, budget)
	}
	return result
}

func extractLintRule(fullLine, msg string) string {
	// golangci-lint: "(lintername)" at end of line
	if m := golangciRuleRe.FindStringSubmatch(fullLine); m != nil {
		return m[1]
	}
	// eslint: "error  rule-name  message"
	if m := eslintRuleRe.FindStringSubmatch(fullLine); m != nil {
		return m[2]
	}
	// ruff/flake8: "E401" code
	if m := ruffRuleRe.FindStringSubmatch(fullLine); m != nil {
		return m[1]
	}
	// Fallback: use the first few words of the message as a pseudo-rule.
	words := strings.Fields(msg)
	if len(words) > 3 {
		words = words[:3]
	}
	return strings.Join(words, " ")
}
