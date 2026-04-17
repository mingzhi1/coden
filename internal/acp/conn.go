package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// Conn wraps a single ACP server subprocess, communicating via
// newline-delimited JSON (ndJSON) over stdin/stdout. It handles the
// JSON-RPC multiplexing: responses go to pending channels, notifications
// go to the NotifyCh.
type Conn struct {
	Name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner

	mu     sync.Mutex
	nextID atomic.Int64
	closed bool

	pending   map[int64]chan json.RawMessage
	pendingMu sync.Mutex

	NotifyCh chan Notification
}

// Dial starts an ACP subprocess and performs the initialize handshake.
func Dial(ctx context.Context, cfg DialConfig) (*Conn, error) {
	parts := strings.Fields(cfg.Command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("acp(%s): empty command", cfg.Name)
	}
	cmdName := parts[0]
	cmdArgs := append(parts[1:], cfg.Args...)

	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	cmd.Stderr = os.Stderr
	if len(cfg.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp(%s): stdin pipe: %w", cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp(%s): stdout pipe: %w", cfg.Name, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp(%s): start: %w", cfg.Name, err)
	}

	conn := &Conn{
		Name:     cfg.Name,
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewScanner(stdout),
		pending:  make(map[int64]chan json.RawMessage),
		NotifyCh: make(chan Notification, 64),
	}
	go conn.readLoop()

	if err := conn.initialize(ctx, cfg.ClientName, cfg.ClientVersion); err != nil {
		conn.Close()
		return nil, fmt.Errorf("acp(%s): initialize: %w", cfg.Name, err)
	}

	slog.Info("[acp] connected", "name", cfg.Name, "pid", cmd.Process.Pid)
	return conn, nil
}

// DialConfig holds parameters for Dial.
type DialConfig struct {
	Name          string
	Command       string
	Args          []string
	Env           map[string]string
	ClientName    string // e.g. "coden" or "llm-server"
	ClientVersion string // e.g. "0.1.0"
}
