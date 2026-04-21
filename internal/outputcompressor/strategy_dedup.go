package outputcompressor

import (
	"fmt"
	"strings"
)

// DuplicateLineStrategy collapses repeated lines into a single line with a
// count suffix. Activated when duplicate lines account for >30% of output.
type DuplicateLineStrategy struct{}

func (s *DuplicateLineStrategy) Name() string { return "duplicate_line" }

func (s *DuplicateLineStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	// Cheap check: need at least 10 lines to be worth deduplicating.
	// The expensive duplicate ratio check is deferred to Compress().
	return strings.Count(output, "\n") >= 10
}

func (s *DuplicateLineStrategy) Compress(output string, budget int) string {
	lines := strings.Split(output, "\n")

	// Check duplicate ratio — only compress if >30% of non-blank lines are dupes.
	seen := make(map[string]int, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			seen[trimmed]++
		}
	}
	nonBlank, totalDupes := 0, 0
	for _, c := range seen {
		nonBlank += c
		if c > 1 {
			totalDupes += c - 1
		}
	}
	if nonBlank == 0 || float64(totalDupes)/float64(nonBlank) <= 0.3 {
		return "" // not enough duplicates — let other strategies or truncation handle it
	}

	type entry struct {
		line  string
		count int
	}

	var result []entry
	var prev string
	var count int

	flush := func() {
		if prev == "" && count == 0 {
			return
		}
		result = append(result, entry{line: prev, count: count})
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == prev && trimmed != "" {
			count++
		} else {
			if count > 0 {
				flush()
			}
			prev = trimmed
			count = 1
		}
	}
	flush()

	var sb strings.Builder
	for _, e := range result {
		if e.count > 2 {
			sb.WriteString(fmt.Sprintf("%s (×%d)\n", e.line, e.count))
		} else {
			for i := 0; i < e.count; i++ {
				sb.WriteString(e.line)
				sb.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
