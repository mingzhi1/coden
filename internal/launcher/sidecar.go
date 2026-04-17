package launcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/llm"
)

// Sidecar manages the coden-llm-server child process with auto-restart.
type Sidecar struct {
	cfg     SidecarConfig
	binPath string

	mu      sync.Mutex
	cmd     *exec.Cmd
	addr    string
	stopped bool        // true after Stop() — suppresses restart
	cancel  context.CancelFunc
}

// SidecarConfig holds parameters for launching the sidecar.
type SidecarConfig struct {
	Addr       string // listen address (default "127.0.0.1:7533")
	ConfigPath string // path to shared config.yaml (optional)
}

// StartSidecar launches coden-llm-server as a child process and waits
// until it responds to a ping. A background goroutine monitors the
// process and restarts it on crash (up to 3 times).
// If the server binary is not found, returns an error so the caller
// can fall back to embedded mode.
func StartSidecar(ctx context.Context, cfg SidecarConfig) (*Sidecar, error) {
	addr := cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:7533"
	}
	cfg.Addr = addr

	binName := "coden-llm-server"
	if runtime.GOOS == "windows" {
		binName = "coden-llm-server.exe"
	}

	binPath := findSidecarBinary(binName)
	if binPath == "" {
		return nil, fmt.Errorf("sidecar: %s not found in PATH or next to executable", binName)
	}

	monCtx, monCancel := context.WithCancel(ctx)
	s := &Sidecar{
		cfg:     cfg,
		binPath: binPath,
		addr:    addr,
		cancel:  monCancel,
	}

	if err := s.launch(ctx); err != nil {
		monCancel()
		return nil, err
	}

	// Background crash monitor.
	go s.monitor(monCtx)

	return s, nil
}

// launch starts the subprocess and waits for it to become healthy.
func (s *Sidecar) launch(ctx context.Context) error {
	args := []string{"--addr", s.cfg.Addr}
	if s.cfg.ConfigPath != "" {
		args = append(args, "--config", s.cfg.ConfigPath)
	}

	cmd := exec.CommandContext(ctx, s.binPath, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sidecar: start %s: %w", s.binPath, err)
	}

	slog.Info("[sidecar] launched llm-server",
		"pid", cmd.Process.Pid, "addr", s.addr, "bin", s.binPath)

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	tmp := &Sidecar{addr: s.addr}
	if err := tmp.waitReady(ctx); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("sidecar: server not ready: %w", err)
	}
	return nil
}

// monitor watches for process exit and restarts if it was unexpected.
func (s *Sidecar) monitor(ctx context.Context) {
	const maxRestarts = 3
	restarts := 0

	for {
		s.mu.Lock()
		cmd := s.cmd
		s.mu.Unlock()
		if cmd == nil {
			return
		}

		// Wait for process to exit.
		err := cmd.Wait()

		// Check if we should restart.
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		if ctx.Err() != nil {
			return // parent context canceled
		}

		restarts++
		if restarts > maxRestarts {
			slog.Error("[sidecar] max restarts exceeded, giving up",
				"restarts", restarts, "last_error", err)
			return
		}

		slog.Warn("[sidecar] process exited unexpectedly, restarting",
			"exit_error", err, "restart", restarts, "max", maxRestarts)

		// Brief delay before restart.
		delay := time.Duration(restarts) * 500 * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		if err := s.launch(ctx); err != nil {
			slog.Error("[sidecar] restart failed", "error", err)
			return
		}
	}
}

// Addr returns the address the sidecar is listening on.
func (s *Sidecar) Addr() string { return s.addr }

// Stop kills the sidecar process and prevents auto-restart.
func (s *Sidecar) Stop() error {
	s.mu.Lock()
	s.stopped = true
	cmd := s.cmd
	s.mu.Unlock()

	s.cancel() // stop the monitor goroutine

	if cmd == nil || cmd.Process == nil {
		return nil
	}
	slog.Info("[sidecar] stopping llm-server", "pid", cmd.Process.Pid)
	err := cmd.Process.Kill()
	_ = cmd.Wait()
	return err
}

// NewClient creates an LLMServerClient connected to this sidecar.
func (s *Sidecar) NewClient() *llm.LLMServerClient {
	return llm.NewLLMServerClient(s.addr)
}
