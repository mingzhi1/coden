package outputcompressor

import (
	"regexp"
	"strings"
)

// ProgressBarStrategy strips ANSI escape sequences and progress bar lines
// that waste tokens without conveying useful information to the LLM.
type ProgressBarStrategy struct{}

func (s *ProgressBarStrategy) Name() string { return "progress_bar" }

// ansiRe matches ANSI escape sequences (color codes, cursor movement, etc.)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// progressPatterns matches common progress bar patterns.
var progressPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*\[?[=>#\-]{3,}`),            // [=====>   ] or ###---
	regexp.MustCompile(`^\s*\d{1,3}(\.\d+)?%`),          // 45.2% ...
	regexp.MustCompile(`^\s*\(\d+/\d+\)`),               // (3/10) ...
	regexp.MustCompile(`(?i)^\s*(downloading|uploading|extracting|installing|resolving)\b.*\d+%`), // Downloading... 45%
	regexp.MustCompile(`^\s*⠋|⠙|⠹|⠸|⠼|⠴|⠦|⠧|⠇|⠏`),     // spinner characters
}

func (s *ProgressBarStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	if ansiRe.MatchString(output) || strings.Contains(output, "\r") {
		return true
	}
	for _, line := range strings.Split(output, "\n") {
		for _, re := range progressPatterns {
			if re.MatchString(line) {
				return true
			}
		}
	}
	return false
}

func (s *ProgressBarStrategy) Compress(output string, budget int) string {
	// Strip all ANSI escape sequences first.
	cleaned := ansiRe.ReplaceAllString(output, "")

	// Remove progress bar lines.
	lines := strings.Split(cleaned, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if isProgressLine(line) {
			continue
		}
		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}

func isProgressLine(line string) bool {
	for _, re := range progressPatterns {
		if re.MatchString(line) {
			return true
		}
	}
	// Also remove lines that are just carriage returns (overwritten progress).
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.Contains(line, "\r") && !strings.HasSuffix(strings.TrimSpace(line), "\n") {
		// Lines with carriage returns are typically progress overwrites.
		return true
	}
	return false
}
