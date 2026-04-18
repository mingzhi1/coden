package hook

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const defaultTimeout = 60 * time.Second

// execute runs a single hook command and returns the result.
func execute(ctx context.Context, cfg Config, hookCtx *Context) Result {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	slog.Info("[hook] running", "name", cfg.Name, "point", cfg.Point, "command", cfg.Command, "timeout", timeout)

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd", "/C", cfg.Command)
	} else {
		cmd = exec.CommandContext(execCtx, "sh", "-c", cfg.Command)
	}

	if hookCtx != nil && hookCtx.WorkspaceRoot != "" {
		cmd.Dir = hookCtx.WorkspaceRoot
	}

	// Build environment: inherit OS env + hook context + per-hook env
	cmd.Env = os.Environ()
	if hookCtx != nil {
		cmd.Env = append(cmd.Env, hookCtx.ToEnv()...)
	}
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	start := time.Now()
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()
	elapsed := time.Since(start)
	output := strings.TrimSpace(outBuf.String())

	result := Result{
		Name:     cfg.Name,
		Point:    cfg.Point,
		Verdict:  VerdictContinue,
		Output:   output,
		Duration: elapsed,
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Sprintf("hook %q timed out after %s", cfg.Name, timeout)
			result.Output = fmt.Sprintf("%s\n[timed out after %s]", output, timeout)
		} else {
			result.Error = err.Error()
		}
		if cfg.Blocking {
			result.Verdict = VerdictBlock
		}
		slog.Info("[hook] failed", "name", cfg.Name, "point", cfg.Point, "duration", elapsed, "error", err)
	} else {
		slog.Info("[hook] passed", "name", cfg.Name, "point", cfg.Point, "duration", elapsed)
	}

	return result
}
