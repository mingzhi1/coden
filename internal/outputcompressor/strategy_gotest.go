package outputcompressor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// GoTestStrategy compresses `go test` NDJSON output to a compact summary.
// When all tests pass, it produces a one-line "ok" summary.
// When tests fail, it shows the summary plus failure details.
type GoTestStrategy struct{}

func (s *GoTestStrategy) Name() string { return "go_test" }

func (s *GoTestStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	// Match "go test" commands.
	cmd := strings.TrimSpace(command)
	return strings.HasPrefix(cmd, "go test") ||
		strings.Contains(cmd, " go test")
}

// goTestEvent represents a single JSON event from `go test -json`.
type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func (s *GoTestStrategy) Compress(output string, budget int) string {
	lines := strings.Split(output, "\n")

	// Try NDJSON parsing first (go test -json or go test with verbose).
	events := parseGoTestEvents(lines)
	if len(events) > 0 {
		return compressGoTestNDJSON(events, budget)
	}

	// Fallback: parse plain text go test output.
	return compressGoTestText(lines, budget)
}

func parseGoTestEvents(lines []string) []goTestEvent {
	var events []goTestEvent
	jsonCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line[0] != '{' {
			continue
		}
		var ev goTestEvent
		if err := json.Unmarshal([]byte(line), &ev); err == nil && ev.Action != "" {
			events = append(events, ev)
			jsonCount++
		}
	}
	// Only treat as NDJSON if we found a reasonable number of JSON lines.
	if jsonCount < 3 {
		return nil
	}
	return events
}

func compressGoTestNDJSON(events []goTestEvent, budget int) string {
	type pkgResult struct {
		passed   int
		failed   int
		skipped  int
		elapsed  float64
		failures map[string][]string // test name → output lines
	}

	pkgs := make(map[string]*pkgResult)
	getPkg := func(pkg string) *pkgResult {
		if p, ok := pkgs[pkg]; ok {
			return p
		}
		p := &pkgResult{failures: make(map[string][]string)}
		pkgs[pkg] = p
		return p
	}

	for _, ev := range events {
		p := getPkg(ev.Package)
		switch ev.Action {
		case "pass":
			if ev.Test != "" {
				p.passed++
			} else {
				p.elapsed = ev.Elapsed
			}
		case "fail":
			if ev.Test != "" {
				p.failed++
			} else {
				p.elapsed = ev.Elapsed
			}
		case "skip":
			if ev.Test != "" {
				p.skipped++
			}
		case "output":
			if ev.Test != "" && p.failures != nil {
				// Collect output for all tests; we'll filter to failures later.
				p.failures[ev.Test] = append(p.failures[ev.Test], ev.Output)
			}
		}
	}

	// Identify actually failed tests: only keep output for tests that failed.
	failedTests := make(map[string]map[string]bool) // pkg → set of failed test names
	for _, ev := range events {
		if ev.Action == "fail" && ev.Test != "" {
			if failedTests[ev.Package] == nil {
				failedTests[ev.Package] = make(map[string]bool)
			}
			failedTests[ev.Package][ev.Test] = true
		}
	}

	// Build summary.
	var sb strings.Builder
	totalPassed, totalFailed, totalSkipped := 0, 0, 0

	// Sort packages for deterministic output.
	pkgNames := make([]string, 0, len(pkgs))
	for name := range pkgs {
		pkgNames = append(pkgNames, name)
	}
	sort.Strings(pkgNames)

	for _, name := range pkgNames {
		p := pkgs[name]
		totalPassed += p.passed
		totalFailed += p.failed
		totalSkipped += p.skipped
	}

	// Header line.
	if totalFailed == 0 {
		sb.WriteString(fmt.Sprintf("ok: %d passed", totalPassed))
		if totalSkipped > 0 {
			sb.WriteString(fmt.Sprintf(", %d skipped", totalSkipped))
		}
		sb.WriteString(fmt.Sprintf(" (%d packages)\n", len(pkgs)))
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("FAIL: %d failed, %d passed", totalFailed, totalPassed))
	if totalSkipped > 0 {
		sb.WriteString(fmt.Sprintf(", %d skipped", totalSkipped))
	}
	sb.WriteString(fmt.Sprintf(" (%d packages)\n", len(pkgs)))

	// Failure details.
	for _, name := range pkgNames {
		ft := failedTests[name]
		if len(ft) == 0 {
			continue
		}
		p := pkgs[name]
		sb.WriteString(fmt.Sprintf("\nFAIL %s:\n", name))
		for testName := range ft {
			sb.WriteString(fmt.Sprintf("  --- FAIL: %s\n", testName))
			// Include output lines for this test (trimmed).
			if outputs, ok := p.failures[testName]; ok {
				for _, line := range outputs {
					trimmed := strings.TrimRight(line, "\n")
					if trimmed == "" {
						continue
					}
					// Skip the test header/footer lines go test adds.
					if strings.HasPrefix(trimmed, "=== RUN") || strings.HasPrefix(trimmed, "--- FAIL") || strings.HasPrefix(trimmed, "--- PASS") {
						continue
					}
					sb.WriteString(fmt.Sprintf("      %s\n", trimmed))
				}
			}
		}
	}

	result := sb.String()
	if len(result) > budget {
		return truncate(result, budget)
	}
	return result
}

func compressGoTestText(lines []string, budget int) string {
	// Parse plain text go test output.
	// Look for summary lines: "ok  pkg  0.123s", "FAIL  pkg  0.456s"
	var (
		summaryLines  []string
		failureBlocks []string
		inFailure     bool
		currentBlock  strings.Builder
		passed, failed int
	)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "ok  ") || strings.HasPrefix(trimmed, "ok\t") {
			passed++
			summaryLines = append(summaryLines, trimmed)
			if inFailure {
				failureBlocks = append(failureBlocks, currentBlock.String())
				currentBlock.Reset()
				inFailure = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "FAIL\t") || strings.HasPrefix(trimmed, "FAIL ") {
			failed++
			summaryLines = append(summaryLines, trimmed)
			if inFailure {
				failureBlocks = append(failureBlocks, currentBlock.String())
				currentBlock.Reset()
				inFailure = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			if inFailure {
				failureBlocks = append(failureBlocks, currentBlock.String())
				currentBlock.Reset()
			}
			inFailure = true
			currentBlock.WriteString(line + "\n")
			continue
		}
		if inFailure {
			currentBlock.WriteString(line + "\n")
		}
	}
	if inFailure {
		failureBlocks = append(failureBlocks, currentBlock.String())
	}

	var sb strings.Builder
	if failed == 0 && passed > 0 {
		sb.WriteString(fmt.Sprintf("ok: %d packages passed\n", passed))
		return sb.String()
	}
	if failed > 0 {
		sb.WriteString(fmt.Sprintf("FAIL: %d failed, %d passed\n", failed, passed))
		for _, block := range failureBlocks {
			sb.WriteString("\n")
			sb.WriteString(block)
		}
		result := sb.String()
		if len(result) > budget {
			return truncate(result, budget)
		}
		return result
	}

	// Can't parse structure — return original (will be truncated by caller).
	return strings.Join(lines, "\n")
}
