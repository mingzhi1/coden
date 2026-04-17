// Package lsp provides LSP client management for CodeN.
package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/core/retrieval"
)

// Manager manages LSP server lifecycle for a workspace.
type Manager struct {
	rootDir string
	lang    string
	cmdPath string
	args    []string // LSP server command arguments

	client     *Client
	process    *exec.Cmd
	procCancel context.CancelFunc

	// State
	ready   bool
	readyMu sync.RWMutex

	// Config
	startTimeout time.Duration
	callTimeout  time.Duration
	maxRestarts  int
	restartCount int
}

// ManagerConfig configures the LSP manager.
type ManagerConfig struct {
	RootDir      string
	Lang         string
	CmdPath      string
	StartTimeout time.Duration
	CallTimeout  time.Duration
	MaxRestarts  int
}

// DefaultManagerConfig returns sensible defaults for gopls.
func DefaultManagerConfig(rootDir string) ManagerConfig {
	return ManagerConfig{
		RootDir:      rootDir,
		Lang:         "go",
		CmdPath:      "gopls",
		StartTimeout: 30 * time.Second,
		CallTimeout:  30 * time.Second,
		MaxRestarts:  3,
	}
}

// NewManagerFromConfig creates a new LSP manager using configuration from tools.yaml.
func NewManagerFromConfig(rootDir string, lang string, lspConfig config.LSPConfig) (*Manager, error) {
	timeout, err := lspConfig.ParseTimeout()
	if err != nil {
		timeout = 30 * time.Second
	}

	// Build command path with platform suffix
	cmdPath := lspConfig.Command
	// On Windows, append .exe suffix if not already present and the bare
	// command is not found in PATH.
	if goruntime.GOOS == "windows" && !strings.HasSuffix(cmdPath, ".exe") {
		if _, err := exec.LookPath(cmdPath); err != nil {
			cmdPath += ".exe"
		}
	}

	return &Manager{
		rootDir:      rootDir,
		lang:         lang,
		cmdPath:      cmdPath,
		args:         lspConfig.Args,
		startTimeout: timeout,
		callTimeout:  timeout,
		maxRestarts:  lspConfig.MaxRestarts,
	}, nil
}

// NewManager creates a new LSP manager.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		rootDir:      cfg.RootDir,
		lang:         cfg.Lang,
		cmdPath:      cfg.CmdPath,
		args:         []string{"serve"},
		startTimeout: cfg.StartTimeout,
		callTimeout:  cfg.CallTimeout,
		maxRestarts:  cfg.MaxRestarts,
	}
}

// Start starts the LSP server and initializes the client.
func (m *Manager) Start(ctx context.Context) error {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()

	if m.ready {
		return nil // Already started
	}

	// Check if LSP command is available
	if _, err := exec.LookPath(m.cmdPath); err != nil {
		return fmt.Errorf("LSP command not found: %s", m.cmdPath)
	}

	// Use a long-lived cancellable context for the process lifetime.
	// Stop() will call procCancel to terminate it. Do NOT use the
	// caller's timeout context — exec.CommandContext kills the process
	// when its context is cancelled, and defer-cancel would fire on return.
	procCtx, procCancel := context.WithCancel(context.Background())

	m.process = exec.CommandContext(procCtx, m.cmdPath, m.args...)
	m.procCancel = procCancel

	stdin, err := m.process.StdinPipe()
	if err != nil {
		procCancel()
		return fmt.Errorf("LSP stdin pipe: %w", err)
	}

	stdout, err := m.process.StdoutPipe()
	if err != nil {
		procCancel()
		return fmt.Errorf("LSP stdout pipe: %w", err)
	}

	if err := m.process.Start(); err != nil {
		procCancel()
		return fmt.Errorf("LSP start: %w", err)
	}

	// Create client connected to the process
	conn := &stdioConn{
		stdin:   stdin,
		stdout:  stdout,
		process: m.process,
	}

	m.client = NewClient(conn)

	// Use a separate timeout context ONLY for the initialize handshake.
	initCtx, initCancel := context.WithTimeout(ctx, m.startTimeout)
	defer initCancel()

	_, err = m.client.Initialize(initCtx, m.rootDir)
	if err != nil {
		if m.process.Process != nil {
			m.process.Process.Kill()
		}
		procCancel()
		m.procCancel = nil
		return fmt.Errorf("LSP initialize: %w", err)
	}

	m.ready = true
	return nil
}

// Stop stops the LSP server gracefully.
func (m *Manager) Stop(ctx context.Context) error {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()

	if !m.ready || m.client == nil {
		return nil
	}

	// Shutdown sequence
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := m.client.Shutdown(ctx); err != nil {
		// Force kill on timeout
		if m.process != nil && m.process.Process != nil {
			m.process.Process.Kill()
		}
	}

	m.client.Exit()
	m.client.Close()

	// Cancel the process context so exec.CommandContext cleans up.
	if m.procCancel != nil {
		m.procCancel()
	}

	if m.process != nil {
		m.process.Wait()
	}

	m.ready = false
	m.client = nil
	m.process = nil
	m.procCancel = nil

	return nil
}

// IsReady returns true if LSP is ready for queries.
func (m *Manager) IsReady() bool {
	m.readyMu.RLock()
	defer m.readyMu.RUnlock()
	return m.ready
}

// Restart attempts to restart the LSP server.
func (m *Manager) Restart(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		// Log but continue
	}

	if m.restartCount >= m.maxRestarts {
		return fmt.Errorf("max restarts (%d) exceeded", m.maxRestarts)
	}
	m.restartCount++

	return m.Start(ctx)
}

// DocumentSymbol queries symbols in a document.
func (m *Manager) DocumentSymbol(ctx context.Context, path string) ([]DocumentSymbol, error) {
	if !m.IsReady() {
		return nil, fmt.Errorf("LSP not ready")
	}

	ctx, cancel := context.WithTimeout(ctx, m.callTimeout)
	defer cancel()

	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{
			URI: pathToURI(path),
		},
	}

	resp, err := m.client.Call(ctx, "textDocument/documentSymbol", params)
	if err != nil {
		return nil, err
	}

	var symbols []DocumentSymbol
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		return nil, err
	}

	return symbols, nil
}

// Definition queries the definition of a symbol.
func (m *Manager) Definition(ctx context.Context, path string, line, column int) ([]Location, error) {
	if !m.IsReady() {
		return nil, fmt.Errorf("LSP not ready")
	}

	ctx, cancel := context.WithTimeout(ctx, m.callTimeout)
	defer cancel()

	params := DefinitionParams{
		TextDocument: TextDocumentIdentifier{
			URI: pathToURI(path),
		},
		Position: Position{
			Line:      line - 1, // Convert to 0-based
			Character: column - 1,
		},
	}

	resp, err := m.client.Call(ctx, "textDocument/definition", params)
	if err != nil {
		return nil, err
	}

	// Definition can return Location or []Location
	var locations []Location
	if err := json.Unmarshal(resp.Result, &locations); err != nil {
		// Try single location
		var loc Location
		if err := json.Unmarshal(resp.Result, &loc); err != nil {
			return nil, err
		}
		locations = []Location{loc}
	}

	return locations, nil
}

// References queries references to a symbol.
func (m *Manager) References(ctx context.Context, path string, line, column int) ([]Location, error) {
	if !m.IsReady() {
		return nil, fmt.Errorf("LSP not ready")
	}

	ctx, cancel := context.WithTimeout(ctx, m.callTimeout)
	defer cancel()

	params := ReferencesParams{
		TextDocument: TextDocumentIdentifier{
			URI: pathToURI(path),
		},
		Position: Position{
			Line:      line - 1,
			Character: column - 1,
		},
		Context: ReferenceContext{
			IncludeDeclaration: true,
		},
	}

	resp, err := m.client.Call(ctx, "textDocument/references", params)
	if err != nil {
		return nil, err
	}

	var locations []Location
	if err := json.Unmarshal(resp.Result, &locations); err != nil {
		return nil, err
	}

	return locations, nil
}

// DidOpen notifies the server a document is open.
func (m *Manager) DidOpen(path, content string) error {
	if !m.IsReady() {
		return fmt.Errorf("LSP not ready")
	}

	params := DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        pathToURI(path),
			LanguageID: m.lang,
			Version:    1,
			Text:       content,
		},
	}

	return m.client.Notify("textDocument/didOpen", params)
}

// SymbolsToEvidence converts LSP symbols to RetrievalEvidence.
func SymbolsToEvidence(symbols []DocumentSymbol, path string) []retrieval.RetrievalEvidence {
	var evidence []retrieval.RetrievalEvidence
	for _, sym := range symbols {
		e := retrieval.RetrievalEvidence{
			Source:   "lsp",
			Path:     path,
			Line:     sym.Range.Start.Line + 1, // Convert to 1-based
			Symbol:   sym.Name,
			Snippet:  sym.Detail,
			Verified: true,
		}

		// Add kind as explanation
		e.Explanation = fmt.Sprintf("LSP symbol: %s (kind=%d)", sym.Name, sym.Kind)

		evidence = append(evidence, e)

		// Recurse into children
		if len(sym.Children) > 0 {
			childEvidence := SymbolsToEvidence(sym.Children, path)
			evidence = append(evidence, childEvidence...)
		}
	}
	return evidence
}

// LocationsToEvidence converts LSP locations to RetrievalEvidence.
func LocationsToEvidence(locations []Location, query string) []retrieval.RetrievalEvidence {
	evidence := make([]retrieval.RetrievalEvidence, 0, len(locations))
	for _, loc := range locations {
		e := retrieval.RetrievalEvidence{
			Source:   "lsp",
			Path:     uriToPath(loc.URI),
			Line:     loc.Range.Start.Line + 1,
			Column:   loc.Range.Start.Character + 1,
			Verified: true,
		}
		if query != "" {
			e.Explanation = query
		}
		evidence = append(evidence, e)
	}
	return evidence
}

// stdioConn wraps stdin/stdout pipes for net.Conn interface.
type stdioConn struct {
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	process *exec.Cmd
	closed  bool
	mu      sync.Mutex
}

func (c *stdioConn) Read(p []byte) (n int, err error) {
	return c.stdout.Read(p)
}

func (c *stdioConn) Write(p []byte) (n int, err error) {
	return c.stdin.Write(p)
}

func (c *stdioConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	c.stdin.Close()
	c.stdout.Close()

	if c.process != nil && c.process.Process != nil {
		c.process.Process.Kill()
	}
	return nil
}

func (c *stdioConn) LocalAddr() net.Addr {
	return &net.UnixAddr{Name: "local", Net: "stdio"}
}

func (c *stdioConn) RemoteAddr() net.Addr {
	return &net.UnixAddr{Name: "remote", Net: "stdio"}
}

func (c *stdioConn) SetDeadline(t time.Time) error      { return nil }
func (c *stdioConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(t time.Time) error { return nil }
