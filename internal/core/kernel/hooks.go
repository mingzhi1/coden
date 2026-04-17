package kernel

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// HookConfig defines a single post-code hook from tools.yaml.
type HookConfig struct {
	Name     string        // hook identifier (e.g. "go_vet")
	Command  string        // shell command to execute
	Blocking bool          // if true, failure injects error into next Code round
	Timeout  time.Duration // max execution time; 0 means use defaultHookTimeout
}

// HookResult is the outcome of a single hook execution.
type HookResult struct {
	Name     string
	Command  string
	Passed   bool
	Output   string // combined stdout+stderr
	Duration time.Duration
	Error    error
}

// defaultHookTimeout is used when HookConfig.Timeout is zero.
const defaultHookTimeout = 60 * time.Second

// RunPostCodeHooks executes all configured post-code hooks in parallel.
// Returns results for all hooks, preserving the input order.
// Blocking hooks that fail are collected separately so the caller can
// inject their errors into the next Code round.
//
// If hooks is nil or empty, this is a no-op and returns nil.
func RunPostCodeHooks(ctx context.Context, workspaceRoot string, hooks []HookConfig) []HookResult {
	if len(hooks) == 0 {
		return nil
	}

	results := make([]HookResult, len(hooks))
	var wg sync.WaitGroup

	for i, h := range hooks {
		wg.Add(1)
		go func(idx int, hook HookConfig) {
			defer wg.Done()
			results[idx] = runSingleHook(ctx, workspaceRoot, hook)
		}(i, h)
	}

	wg.Wait()
	return results
}

// runSingleHook executes one hook command and returns its result.
func runSingleHook(ctx context.Context, workspaceRoot string, h HookConfig) HookResult {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}

	slog.Info("[hooks] running post-code hook", "name", h.Name, "command", h.Command, "timeout", timeout)

	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(hookCtx, "cmd", "/C", h.Command)
	} else {
		cmd = exec.CommandContext(hookCtx, "sh", "-c", h.Command)
	}

	if workspaceRoot != "" {
		cmd.Dir = workspaceRoot
	}

	start := time.Now()
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()
	elapsed := time.Since(start)
	output := strings.TrimSpace(outBuf.String())

	result := HookResult{
		Name:     h.Name,
		Command:  h.Command,
		Duration: elapsed,
		Output:   output,
	}

	if err != nil {
		if hookCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("hook %q timed out after %s", h.Name, timeout)
			result.Output = fmt.Sprintf("%s\n[timed out after %s]", output, timeout)
		} else {
			result.Error = err
		}
		result.Passed = false
		slog.Info("[hooks] post-code hook failed", "name", h.Name, "duration", elapsed, "error", err)
	} else {
		result.Passed = true
		slog.Info("[hooks] post-code hook passed", "name", h.Name, "duration", elapsed)
	}

	return result
}

// BlockingErrors extracts error messages from failed blocking hooks,
// formatted for injection into the LLM conversation as retry feedback.
//
// Returns an empty string when there are no blocking failures.
func BlockingErrors(results []HookResult) string {
	var sb strings.Builder
	first := true
	for _, r := range results {
		if r.Passed {
			continue
		}
		// Only surface blocking hooks as errors to the LLM.
		// Non-blocking failures are logged but not injected.
		if !isBlocking(r, results) {
			continue
		}
		if !first {
			sb.WriteString("\n\n")
		}
		first = false
		sb.WriteString(fmt.Sprintf("Post-code hook '%s' failed:\n", r.Name))
		if r.Output != "" {
			sb.WriteString(r.Output)
		} else if r.Error != nil {
			sb.WriteString(r.Error.Error())
		}
	}
	return sb.String()
}

// BlockingErrorsWithConfigs extracts error messages from failed blocking hooks
// using the original HookConfig slice for the blocking flag lookup.
// Returns an empty string when there are no blocking failures.
func BlockingErrorsWithConfigs(results []HookResult, hooks []HookConfig) string {
	blockingSet := make(map[string]bool, len(hooks))
	for _, h := range hooks {
		if h.Blocking {
			blockingSet[h.Name] = true
		}
	}

	var sb strings.Builder
	first := true
	for _, r := range results {
		if r.Passed {
			continue
		}
		if !blockingSet[r.Name] {
			continue
		}
		if !first {
			sb.WriteString("\n\n")
		}
		first = false
		sb.WriteString(fmt.Sprintf("Post-code hook '%s' failed:\n", r.Name))
		if r.Output != "" {
			sb.WriteString(r.Output)
		} else if r.Error != nil {
			sb.WriteString(r.Error.Error())
		}
	}
	return sb.String()
}

// HasBlockingFailures reports whether any blocking hook in the result set failed.
func HasBlockingFailures(results []HookResult, hooks []HookConfig) bool {
	blockingSet := make(map[string]bool, len(hooks))
	for _, h := range hooks {
		if h.Blocking {
			blockingSet[h.Name] = true
		}
	}
	for _, r := range results {
		if !r.Passed && blockingSet[r.Name] {
			return true
		}
	}
	return false
}

// isBlocking is a helper used by BlockingErrors (without configs).
// It falls back to checking all results — kept for backward compatibility
// with the simpler BlockingErrors signature that doesn't take configs.
// In practice, callers should prefer BlockingErrorsWithConfigs.
func isBlocking(_ HookResult, _ []HookResult) bool {
	// When called from BlockingErrors (no config available), we conservatively
	// treat all failed hooks as blocking.  The preferred path is
	// BlockingErrorsWithConfigs which uses the actual Blocking flag.
	return true
}
