package subprocess

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// processExitTimeout is how long Close waits for the child process to exit
// after cancellation before giving up.
const processExitTimeout = 10 * time.Second

type Spec struct {
	ModuleRoot string
	BinaryName string
	GoPackage  string
	Args       []string
}

// Start starts a child process over stdio.
// It prefers a sibling executable next to the current binary and falls back to go run.
func Start(ctx context.Context, spec Spec) (io.ReadWriteCloser, error) {
	if spec.BinaryName == "" {
		return nil, fmt.Errorf("binary name is required")
	}
	if execPath, ok := currentExecutable(); ok {
		if siblingPath, ok := findSiblingExecutable(execPath, spec.BinaryName); ok {
			return startCommand(ctx, "", siblingPath, spec.Args...)
		}
	}
	if spec.GoPackage == "" {
		return nil, fmt.Errorf("go package is required when sibling executable is unavailable")
	}
	return StartGoRun(ctx, spec.ModuleRoot, spec.GoPackage, spec.Args...)
}

// StartGoRun starts a Go package as a child process over stdio.
func StartGoRun(ctx context.Context, workdir, pkg string, args ...string) (io.ReadWriteCloser, error) {
	if workdir == "" {
		return nil, fmt.Errorf("workdir is required")
	}
	return startCommand(ctx, workdir, "go", append([]string{"run", pkg}, args...)...)
}

func startCommand(ctx context.Context, workdir, command string, args ...string) (io.ReadWriteCloser, error) {
	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, command, args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	return &processStream{
		stdin:  stdin,
		stdout: stdout,
		cmd:    cmd,
		cancel: cancel,
	}, nil
}

func currentExecutable() (string, bool) {
	path, err := os.Executable()
	if err != nil || path == "" {
		return "", false
	}
	return path, true
}

func findSiblingExecutable(execPath, binaryName string) (string, bool) {
	siblingPath := filepath.Join(filepath.Dir(execPath), executableName(binaryName))
	info, err := os.Stat(siblingPath)
	if err != nil || info.IsDir() {
		return "", false
	}
	return siblingPath, true
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

type processStream struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
	cancel context.CancelFunc
	once   sync.Once
}

func (p *processStream) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

func (p *processStream) Write(b []byte) (int, error) {
	return p.stdin.Write(b)
}

func (p *processStream) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		p.cancel()

		// Wait for the child process with a timeout to avoid blocking forever
		// if the process is stuck.
		done := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			// exited cleanly
		case <-time.After(processExitTimeout):
			log.Printf("rpc/subprocess: process %d did not exit within %v after cancel", p.cmd.Process.Pid, processExitTimeout)
		}

		_ = p.stdout.Close()
	})
	return nil
}
