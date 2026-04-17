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

	// compile_error: go build / go vet output patterns
	if strings.Contains(lower, "build failed") ||
		strings.Contains(lower, "syntax error") ||
		strings.Contains(lower, "undefined:") ||
		strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, "imported and not used") ||
		strings.Contains(lower, "declared and not used") ||
		strings.Contains(lower, "too many arguments") ||
		strings.Contains(lower, "not enough arguments") ||
		strings.Contains(lower, ".go:") {
		return &ClassifiedError{
			Class:     ErrorClassCompileError,
			HumanMsg:  "Compilation failed. Fix the syntax or type errors shown above before re-running.",
			RawStderr: stderr,
			ExitCode:  exitCode,
		}
	}

	// test_failure: go test patterns
	if strings.Contains(lower, "--- fail") ||
		strings.Contains(lower, "fail\t") ||
		strings.Contains(lower, "panic:") && strings.Contains(lower, "_test.go") {
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
