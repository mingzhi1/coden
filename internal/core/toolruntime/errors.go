package toolruntime

import (
	"fmt"
	"strings"
)

// ErrorClass categorises a tool execution failure so that the LLM can
// distinguish environment-level problems (not worth retrying by changing code)
// from code-level problems (actionable by the coder).
type ErrorClass string

const (
	// ErrorClassEnvMissing — required binary / env-var not found on the host.
	ErrorClassEnvMissing ErrorClass = "env_missing"
	// ErrorClassPermission — OS denied access (file or network permission).
	ErrorClassPermission ErrorClass = "permission"
	// ErrorClassTimeout — command exceeded its allowed wall-clock budget.
	ErrorClassTimeout ErrorClass = "timeout"
	// ErrorClassCompileError — go build / go vet returned non-zero.
	ErrorClassCompileError ErrorClass = "compile_error"
	// ErrorClassTestFailure — go test returned non-zero (compile OK, tests fail).
	ErrorClassTestFailure ErrorClass = "test_failure"
	// ErrorClassRuntimeError — command ran and exited non-zero for other reasons.
	ErrorClassRuntimeError ErrorClass = "runtime_error"
	// ErrorClassUnknown — could not classify.
	ErrorClassUnknown ErrorClass = "unknown"
)

// ClassifiedError wraps a tool error with a machine-readable class and a
// human-readable description suitable for injection into LLM prompts.
type ClassifiedError struct {
	Class       ErrorClass
	HumanMsg    string // one-sentence summary, no raw paths
	RawStderr   string // full original stderr for reference
	ExitCode    int
}

func (e *ClassifiedError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Class, e.HumanMsg)
}

// ClassifyShellError inspects a run_shell / go-build result and returns a
// ClassifiedError. Returns nil when exitCode == 0.
func ClassifyShellError(command, stdout, stderr string, exitCode int, timedOut bool) *ClassifiedError {
	if exitCode == 0 && !timedOut {
		return nil
	}
	combined := strings.TrimSpace(stdout + "\n" + stderr)
	lower := strings.ToLower(combined)

	if timedOut {
		return &ClassifiedError{
			Class:     ErrorClassTimeout,
			HumanMsg:  fmt.Sprintf("Command timed out: %q. Consider splitting into smaller steps or increasing timeout.", truncateCmd(command)),
			RawStderr: stderr,
			ExitCode:  exitCode,
		}
	}

	// env_missing: binary not found
	if strings.Contains(lower, "exec:") && strings.Contains(lower, "not found") ||
		strings.Contains(lower, "command not found") ||
		strings.Contains(lower, "no such file or directory") && isExecutableCheck(stderr) {
		return &ClassifiedError{
			Class:     ErrorClassEnvMissing,
			HumanMsg:  fmt.Sprintf("Required tool not found on PATH. Ensure the build environment has the necessary binaries (command: %q).", truncateCmd(command)),
			RawStderr: stderr,
			ExitCode:  exitCode,
		}
	}

	// permission: access denied
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "access is denied") {
		return &ClassifiedError{
			Class:     ErrorClassPermission,
			HumanMsg:  "File or network permission denied. This is an environment issue, not a code issue.",
			RawStderr: stderr,
			ExitCode:  exitCode,
		}
	}

	// compile_error: patterns from Go, TypeScript, Rust, Python, Java, C/C++
	if isCompileError(lower) {
		return &ClassifiedError{
			Class:     ErrorClassCompileError,
			HumanMsg:  "Compilation failed. Fix the syntax or type errors shown above before re-running.",
			RawStderr: stderr,
			ExitCode:  exitCode,
		}
	}

	// test_failure: patterns from Go, pytest, Jest/Mocha, Rust, Java/JUnit
	if isTestFailure(lower) {
		return &ClassifiedError{
			Class:     ErrorClassTestFailure,
			HumanMsg:  "One or more tests failed. Review the test output and fix the implementation.",
			RawStderr: stderr,
			ExitCode:  exitCode,
		}
	}

	// default: runtime error
	return &ClassifiedError{
		Class:     ErrorClassRuntimeError,
		HumanMsg:  fmt.Sprintf("Command exited with code %d. Review stderr for details.", exitCode),
		RawStderr: stderr,
		ExitCode:  exitCode,
	}
}

func truncateCmd(cmd string) string {
	const maxLen = 60
	cmd = strings.TrimSpace(cmd)
	if len(cmd) > maxLen {
		return cmd[:maxLen] + "..."
	}
	return cmd
}

// isExecutableCheck returns true when the "no such file" message is about a
// missing executable rather than a missing data file.
func isExecutableCheck(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "exec") ||
		strings.Contains(lower, "executable") ||
		!strings.Contains(lower, "open")
}

// isCompileError returns true when the lowered combined output matches known
// compilation / type-check error patterns across supported languages.
func isCompileError(lower string) bool {
	patterns := []string{
		// Generic
		"build failed", "syntax error", "syntaxerror",

		// Go
		"undefined:", "cannot use", "imported and not used",
		"declared and not used", "too many arguments", "not enough arguments",

		// TypeScript / JavaScript
		"error ts", // tsc emits "error TS2304:" etc.
		"typeerror:", "referenceerror:",

		// Rust
		"error[e", // rustc emits "error[E0433]:" etc.

		// Python
		"indentationerror:", "modulenotfounderror:", "nameerror:",

		// Java / Kotlin
		"error: cannot find symbol", "error: incompatible types",

		// C / C++
		"fatal error:", "undefined reference",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// Language-specific source file indicators with line numbers (e.g. "foo.go:12:" or "foo.ts:5:10")
	srcExts := []string{".go:", ".ts:", ".tsx:", ".js:", ".jsx:", ".rs:", ".java:", ".kt:", ".c:", ".cpp:", ".cc:", ".cs:", ".swift:", ".zig:"}
	for _, ext := range srcExts {
		idx := strings.Index(lower, ext)
		if idx < 0 {
			continue
		}
		// Require a digit immediately after ".<ext>:" to avoid false positives
		// like "tests/test_main.py::test_add" matching ".py:".
		after := idx + len(ext)
		if after < len(lower) && lower[after] >= '0' && lower[after] <= '9' {
			return true
		}
	}
	return false
}

// isTestFailure returns true when the lowered combined output matches known
// test failure patterns across supported languages.
func isTestFailure(lower string) bool {
	patterns := []string{
		// Go
		"--- fail", "fail\t",

		// pytest
		"failed", "error in", // pytest summary: "1 failed"

		// Jest / Mocha / Node
		"tests failed", "test suites failed",

		// Rust (cargo test)
		"test result: failed",

		// Java / JUnit / Gradle
		"build failed", "there were failing tests",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// Go panic in test file
	if strings.Contains(lower, "panic:") && strings.Contains(lower, "_test.go") {
		return true
	}
	return false
}
