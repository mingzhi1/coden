package outputcompressor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CompileErrorStrategy groups compile errors by file, producing a compact
// summary. Works for Go, Rust (cargo), TypeScript (tsc), and GCC/Clang.
type CompileErrorStrategy struct{}

func (s *CompileErrorStrategy) Name() string { return "compile_error" }

func (s *CompileErrorStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	cmd := strings.TrimSpace(command)
	// Match build/compile commands.
	for _, prefix := range []string{
		"go build", "go vet", "go install",
		"cargo build", "cargo check",
		"tsc", "npx tsc",
		"gcc", "g++", "clang", "make",
	} {
		if strings.HasPrefix(cmd, prefix) || strings.Contains(cmd, " "+prefix) {
			return true
		}
	}
	return false
}

// errorLineRe matches common compiler error formats:
//   file.go:10:3: message        (Go, GCC, Clang)
//   file.ts(10,3): error TS1234  (TypeScript)
//   error[E0308]: message        (Rust)
var errorLineRe = regexp.MustCompile(
	`^(.+?)[:\(](\d+)[,:\)]\d*[:\)]\s*(.+)$`,
)

// rustErrorRe matches Rust error lines: "error[E0308]: mismatched types"
var rustErrorRe = regexp.MustCompile(`^error(\[E\d+\])?: (.+)$`)

func (s *CompileErrorStrategy) Compress(output string, budget int) string {
	lines := strings.Split(output, "\n")

	type fileErrors struct {
		file   string
		errors []string // ":<line> <message>"
	}

	byFile := make(map[string]*fileErrors)
	totalErrors := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		m := errorLineRe.FindStringSubmatch(trimmed)
		if m == nil {
			// Try rust-style errors.
			if rustErrorRe.MatchString(trimmed) {
				fe, ok := byFile["(rust)"]
				if !ok {
					fe = &fileErrors{file: "(rust)"}
					byFile["(rust)"] = fe
				}
				fe.errors = append(fe.errors, trimmed)
				totalErrors++
			}
			continue
		}

		file := m[1]
		lineNum := m[2]
		msg := strings.TrimSpace(m[3])

		fe, ok := byFile[file]
		if !ok {
			fe = &fileErrors{file: file}
			byFile[file] = fe
		}
		fe.errors = append(fe.errors, fmt.Sprintf(":%s %s", lineNum, msg))
		totalErrors++
	}

	if totalErrors == 0 {
		// Can't parse structure — return as-is (caller will truncate).
		return output
	}

	// Sort files for deterministic output.
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("compile errors (%d in %d files):\n", totalErrors, len(files)))

	for _, file := range files {
		fe := byFile[file]
		sb.WriteString(fmt.Sprintf("  %s (%d errors):\n", fe.file, len(fe.errors)))
		for _, e := range fe.errors {
			sb.WriteString(fmt.Sprintf("    %s\n", e))
		}
	}

	result := sb.String()
	if len(result) > budget {
		return truncate(result, budget)
	}
	return result
}
