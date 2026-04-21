package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/core/board"
	"github.com/mingzhi1/coden/internal/core/events"
	"github.com/mingzhi1/coden/internal/core/kernel"
	"github.com/mingzhi1/coden/internal/hook"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/storagepath"
	"github.com/mingzhi1/coden/internal/launcher"
	"github.com/mingzhi1/coden/internal/llm"
	rpcclient "github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/server"
	"github.com/mingzhi1/coden/internal/rpc/transport"
	"github.com/mingzhi1/coden/internal/tool/inventory"
	"github.com/mingzhi1/coden/internal/ui/plain"
	"github.com/mingzhi1/coden/internal/ui/tui"
	"github.com/mingzhi1/coden/internal/web"

	"github.com/charmbracelet/x/term"
	"gopkg.in/yaml.v3"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// plainIdleTimeout is how long we wait without receiving any event before
// considering the workflow stuck. Resets on every event, so long-running
// workflows that keep producing events never hit this.
var plainIdleTimeout = 2 * time.Minute

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("coden", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		renderUsage(os.Stdout, fs)
	}

	showVersion := fs.Bool("version", false, "print version and exit")
	prompt := fs.String("prompt", "", "user prompt")
	sessionID := fs.String("session", "demo-session", "session identifier")
	listSessions := fs.Bool("sessions", false, "list known sessions")
	newSession := fs.Bool("new-session", false, "create a new session before running")
	workspaceRoot := fs.String("workspace", filepath.Join(".", "workspace"), "workspace root")
	stateDBPath := fs.String("state-db", "", "main sqlite database path for global metadata (default: ~/.coden/main.sqlite)")
	plainMode := fs.Bool("plain", false, "force plain output")
	listCheckpoints := fs.Bool("checkpoints", false, "list persisted checkpoints for the session")
	checkpointWorkflow := fs.String("checkpoint-workflow", "", "read a persisted checkpoint by workflow id")
	checkpointLimit := fs.Int("checkpoint-limit", 20, "maximum checkpoints to list")
	inputMode := fs.String("input", "", "input launcher: process, loopback, or llm (default: auto-detect)")
	plannerMode := fs.String("planner", "", "planner launcher: process, loopback, or llm (default: auto-detect)")
	coderMode := fs.String("coder", "", "coder launcher: process, loopback, or llm (default: auto-detect)")
	acceptorMode := fs.String("acceptor", "", "acceptor launcher: process, loopback, or llm (default: auto-detect)")
	executorMode := fs.String("executor", "", "executor launcher: process or loopback (default: process)")
	allowShell := fs.Bool("allow-shell", false, "allow run_shell tool execution for this kernel process")
	serve := fs.String("serve", "", "start kernel as RPC server on this TCP address (e.g. 127.0.0.1:7100)")
	connect := fs.String("connect", "", "attach to a running kernel server at this TCP address")
	cliModel := fs.String("model", "", "override primary LLM model name")
	cliProvider := fs.String("provider", "", "override primary LLM provider (openai, anthropic, deepseek)")
	showConfig := fs.Bool("show-config", false, "print effective merged config and exit")
	configStatus := fs.Bool("config-status", false, "show config file locations and migration status")
	configMigrate := fs.Bool("config-migrate", false, "migrate legacy tools.yaml to .coden/config.yaml")
	toolsList := fs.Bool("tools-list", false, "list discovered tools and their status")
	toolsCheck := fs.Bool("tools-check", false, "force re-probe all tools (ignores cache)")
	noServer := fs.Bool("no-server", false, "force embedded LLM mode (skip llm-server sidecar)")
	webAddr := fs.String("web", "", "start Kanban web UI on this address (e.g. 127.0.0.1:7200)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.Usage()
			return nil
		}
		return err
	}

	if *showVersion {
		fmt.Printf("coden %s\n", Version)
		os.Exit(0)
	}

	if *configStatus {
		s := config.CheckStatus(*workspaceRoot)
		mark := func(ok bool) string {
			if ok {
				return "✓"
			}
			return "✗"
		}
		fmt.Println("Config status:")
		if s.UserConfig != "" {
			fmt.Printf("  [%s] %s (user)\n", mark(s.UserExists), s.UserConfig)
		}
		if s.WorkspaceConfig != "" {
			fmt.Printf("  [%s] %s (workspace)\n", mark(s.WorkspaceExists), s.WorkspaceConfig)
		}
		if s.LegacyConfig != "" {
			label := "legacy"
			if s.NeedsMigration {
				label = "legacy — needs migration"
			}
			fmt.Printf("  [%s] %s (%s)\n", mark(s.LegacyExists), s.LegacyConfig, label)
		}
		os.Exit(0)
	}

	if *configMigrate {
		res, err := config.MigrateToolsYaml(*workspaceRoot)
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		if !res.Migrated {
			fmt.Println("Nothing to migrate (no tools.yaml found)")
		} else {
			fmt.Printf("✓ Migrated %s → %s\n", res.SourcePath, res.DestPath)
			if res.BackupPath != "" {
				fmt.Printf("✓ Backed up → %s\n", res.BackupPath)
			}
		}
		os.Exit(0)
	}

	if *toolsList || *toolsCheck {
		return runToolsCommand(*workspaceRoot, *toolsCheck)
	}

	if *showConfig {
		loader := config.NewLoader(*workspaceRoot)
		fmt.Println("Config search paths:")
		for i, p := range loader.SearchPaths() {
			exists := "not found"
			if _, statErr := os.Stat(p); statErr == nil {
				exists = "found"
			}
			fmt.Printf("  %d. %s [%s]\n", i+1, p, exists)
		}
		fmt.Println()
		cfg, err := loader.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		out, marshalErr := yaml.Marshal(cfg)
		if marshalErr != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling config: %v\n", marshalErr)
			os.Exit(1)
		}
		fmt.Println("Effective merged configuration:")
		fmt.Print(string(out))
		os.Exit(0)
	}

	// Support positional args as prompt: `coden fix the bug in calc.go`
	if *prompt == "" && fs.NArg() > 0 {
		*prompt = strings.Join(fs.Args(), " ")
	}

	resolvedStateDBPath := *stateDBPath
	if resolvedStateDBPath == "" {
		resolvedStateDBPath = defaultStateDBPath(*workspaceRoot)
	}

	opts := dependencyOptions(*workspaceRoot, *inputMode, *plannerMode, *coderMode, *acceptorMode, *executorMode)
	opts.AllowShell = *allowShell

	var registry launcher.Registry
	if *cliProvider != "" || *cliModel != "" {
		registry = launcher.DefaultWithOverride(*cliProvider, *cliModel)
	} else {
		// Check config for llm-server mode.
		llmCfg := loadLLMServerConfig(*workspaceRoot)
		if llmCfg.Enabled && !*noServer {
			addr := llmCfg.Addr
			if addr == "" {
				addr = "127.0.0.1:7533"
			}

			// Try to launch the sidecar subprocess.
			configPath := findConfigPath(*workspaceRoot)
			sidecar, sidecarErr := launcher.StartSidecar(
				context.Background(),
				launcher.SidecarConfig{Addr: addr, ConfigPath: configPath},
			)
			if sidecarErr != nil {
				slog.Warn("[main] sidecar launch failed, falling back to embedded mode",
					"error", sidecarErr)
				registry = launcher.Default()
			} else {
				// Sidecar is running — wire up server mode.
				defer sidecar.Stop()
				registry = launcher.DefaultWithServer(addr)
				// Server handles provider detection — force LLM workers on.
				opts.Input = "llm"
				opts.Planner = "llm"
				opts.Coder = "llm"
				opts.Acceptor = "llm"
				opts.Agentic = true
			}
		} else {
			registry = launcher.Default()
		}
	}

	if *serve != "" {
		return runServe(*serve, *webAddr, *workspaceRoot, resolvedStateDBPath, opts, registry)
	}
	query := checkpointQuery{
		List:       *listCheckpoints,
		WorkflowID: *checkpointWorkflow,
		Limit:      *checkpointLimit,
	}
	sessionQuery := sessionQuery{
		List: *listSessions,
	}
	if *connect != "" {
		return runRemoteConnect(*connect, *sessionID, *prompt, *plainMode, query, sessionQuery, *newSession)
	}

	ctx := context.Background()
	client, cleanup, err := newLocalRPCClient(ctx, *workspaceRoot, resolvedStateDBPath, opts, registry)
	if err != nil {
		return fmt.Errorf("local rpc startup failed: %w", err)
	}
	defer cleanup()

	broker := registry.Broker()
	chatter := registry.Chatter()

	// Fast-fail: detect missing LLM configuration early.
	// In server mode (broker == nil), we trust the server is running.
	if broker != nil && needsLLM(opts) && !broker.IsConfigured() {
		return fmt.Errorf(
			"LLM is required but not configured.\n"+
				"\tSet one of: OPENAI_API_KEY, ANTHROPIC_API_KEY, DEEPSEEK_API_KEY, MINIMAX_API_KEY, or GITHUB_COPILOT_TOKEN\n"+
				"\tCurrent pool status: %s",
			broker.Summary(),
		)
	}

	info := tui.RuntimeInfo{
		Mode:            "local",
		AllowShellKnown: true,
		AllowShell:      *allowShell,
		ConfigSource:    "env/cli",
	}
	if broker != nil {
		info.Model = broker.Model()
		info.Provider = broker.Provider()
		info.LightModel = broker.LightModel()
		info.PoolSummary = broker.Summary()
	} else if chatter != nil {
		info.Provider = "llm-server"
		info.PoolSummary = "llm-server (remote)"
		info.ConfigSource = "llm-server"
	}
	if err := runClientSession(ctx, client, *sessionID, *prompt, *plainMode, query, sessionQuery, *newSession, info); err != nil {
		return err
	}
	return nil
}

func renderUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: coden [options] [prompt...]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Positional arguments are joined as the prompt:")
	fmt.Fprintln(w, "  coden fix the bug in calc.go")
	fmt.Fprintln(w, "  coden --prompt \"fix the bug in calc.go\"   # equivalent")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	original := fs.Output()
	fs.SetOutput(w)
	defer fs.SetOutput(original)
	fs.PrintDefaults()
}

// runServe starts the kernel as a headless RPC server.
// webAddr is optional: if non-empty, also starts the Kanban web UI on that address.
func runServe(addr, webAddr, workspaceRoot, stateDBPath string, opts launcher.Options, reg launcher.Registry) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	k, cleanup, err := newKernel(ctx, workspaceRoot, stateDBPath, opts, reg)
	if err != nil {
		return fmt.Errorf("kernel startup failed: %w", err)
	}
	defer cleanup()
	srv := server.New(k)

	// Optionally start the Kanban web UI alongside the kernel.
	if webAddr != "" {
		boardDBPath := storagepath.BoardDBPath(stateDBPath)
		if boardSrv, startErr := startWebServer(ctx, webAddr, boardDBPath, k.Events()); startErr != nil {
			slog.Warn("[web] Kanban UI failed to start", "error", startErr)
		} else {
			_ = boardSrv // server runs in background goroutine until ctx is cancelled
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "CodeN kernel serving on %s\n", ln.Addr())

	const keepalivePeriod = 30 * time.Second
	return serveListener(ctx, ln, keepalivePeriod, func(rwc io.ReadWriteCloser) {
		srv.ServeConn(ctx, rwc)
	})
}

func serveListener(ctx context.Context, ln net.Listener, keepalivePeriod time.Duration, serve func(io.ReadWriteCloser)) error {
	var wg sync.WaitGroup
	listenerClosed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ln.Close()
		case <-listenerClosed:
		}
	}()
	defer close(listenerClosed)

	for {
		// R-08: enable TCP keepalive so dead peers are detected within keepalivePeriod.
		rwc, err := transport.AcceptKeepalive(ln, keepalivePeriod)
		if err != nil {
			if ctx.Err() != nil && isClosedListenerError(err) {
				wg.Wait()
				return nil
			}
			return fmt.Errorf("accept failed: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			serve(rwc)
		}()
	}
}

func isClosedListenerError(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}

// startWebServer opens the board SQLite database and starts the Kanban web server
// in a background goroutine. The server runs until ctx is cancelled.
func startWebServer(ctx context.Context, addr, boardDBPath string, bus *events.Bus) (*web.Server, error) {
	if err := os.MkdirAll(filepath.Dir(boardDBPath), 0o755); err != nil {
		return nil, fmt.Errorf("web: create board db dir: %w", err)
	}
	db, err := board.OpenSQLite(boardDBPath)
	if err != nil {
		return nil, fmt.Errorf("web: open board db: %w", err)
	}
	store := board.NewSQLiteStore(db)
	if err := store.Init(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("web: init board db: %w", err)
	}
	srv := web.New(web.Config{
		Addr:     addr,
		Store:    store,
		EventBus: bus,
	})
	go func() {
		if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("[web] server error", "error", err)
		}
		_ = db.Close()
	}()
	fmt.Fprintf(os.Stderr, "CodeN Kanban UI available at http://%s\n", addr)
	return srv, nil
}

// runRemoteConnect attaches to a remote kernel server.
func runRemoteConnect(addr, sessionID, prompt string, forcePlain bool, query checkpointQuery, sessionQuery sessionQuery, newSession bool) error {
	// R-08: enable TCP keepalive on the client-side connection too, so the
	// client can detect a dead server during long-running subscriptions.
	const keepalivePeriod = 30 * time.Second
	rwc, err := transport.DialTCPKeepalive(addr, keepalivePeriod)
	if err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}
	c := rpcclient.New(rwc)
	defer c.Close()

	info := tui.RuntimeInfo{
		Mode:         "remote",
		ConfigSource: "remote",
	}
	if err := runClientSession(context.Background(), c, sessionID, prompt, forcePlain, query, sessionQuery, newSession, info); err != nil {
		return err
	}
	return nil
}

type checkpointQuery struct {
	List       bool
	WorkflowID string
	Limit      int
}

func (q checkpointQuery) Enabled() bool {
	return q.List || q.WorkflowID != ""
}

type sessionQuery struct {
	List bool
}

func (q sessionQuery) Enabled() bool {
	return q.List
}

func runClientSession(ctx context.Context, client api.ClientAPI, sessionID, prompt string, forcePlain bool, query checkpointQuery, sessions sessionQuery, newSession bool, info tui.RuntimeInfo) error {
	if sessions.Enabled() {
		return runSessionQuery(ctx, client)
	}
	if query.Enabled() {
		return runCheckpointQuery(ctx, client, sessionID, query)
	}
	if newSession {
		created, err := client.CreateSession(ctx, "")
		if err != nil {
			return fmt.Errorf("create session failed: %w", err)
		}
		sessionID = created.ID
		fmt.Fprintf(os.Stdout, "session: %s\n", sessionID)
	}

	view := "tui"
	if forcePlain || !term.IsTerminal(os.Stdout.Fd()) {
		view = "plain"
	}
	if err := client.Attach(ctx, sessionID, "coden-cli", view); err != nil {
		return fmt.Errorf("attach failed: %w", err)
	}
	defer func() {
		detachCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Detach(detachCtx, sessionID, "coden-cli")
	}()

	if view == "plain" {
		if err := runPlain(ctx, client, sessionID, prompt); err != nil {
			return fmt.Errorf("plain run failed: %w", err)
		}
		return nil
	}

	if err := tui.RunWithRuntimeInfo(ctx, client, sessionID, prompt, info); err != nil {
		return fmt.Errorf("tui failed: %w", err)
	}
	return nil
}

func newLocalRPCClient(ctx context.Context, workspaceRoot, stateDBPath string, opts launcher.Options, reg launcher.Registry) (api.ClientAPI, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	k, workerCleanup, err := newKernel(ctx, workspaceRoot, stateDBPath, opts, reg)
	if err != nil {
		return nil, nil, err
	}
	srv := server.New(k)

	rpcCtx, cancel := context.WithCancel(ctx)
	go srv.ServeConn(rpcCtx, serverRWC)

	client := rpcclient.New(clientRWC)
	cleanup := func() {
		cancel()
		workerCleanup()
		_ = client.Close()
		_ = serverRWC.Close()
	}

	return client, cleanup, nil
}

func newKernel(ctx context.Context, workspaceRoot, stateDBPath string, opts launcher.Options, reg launcher.Registry) (*kernel.Kernel, func(), error) {
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create workspace root: %w", err)
	}
	opts.WorkspaceRoot = workspaceRoot
	if opts.ModuleRoot == "" {
		opts.ModuleRoot = moduleRoot()
	}
	deps, cleanup, err := reg.Start(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	k, err := kernel.NewPersistentWithWorkflowDependencies(workspaceRoot, stateDBPath, deps.Inputter, deps.Planner, deps.Coder, deps.Executor, deps.Acceptor)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	// M10-04: Wire RePlan step — uses Chatter (Broker or LLMServerClient).
	if chatter := reg.Chatter(); chatter != nil {
		k.SetReplanner(llm.NewLLMReplanner(chatter))
		// Critic reviews plan before execution for structural anti-narcissism.
		k.SetCritic(llm.NewLLMCritic(chatter))
	}
	// SA-10: Wire optional Searcher dependency from the launcher.
	if deps.Searcher != nil {
		k.SetSearcher(deps.Searcher)
	}
	if deps.MCPToolPrompt != "" {
		k.SetMCPToolPrompt(deps.MCPToolPrompt)
	}
	// Attach LLM to Secretary Agent for intelligent operations.
	if chatter := reg.Chatter(); chatter != nil {
		k.SetSecretaryLLM(llm.NewSecretaryAdapter(chatter))
	}
	k.SetAllowShell(opts.AllowShell)

	// Load unified configuration (user defaults + workspace overrides).
	loader := config.NewLoader(workspaceRoot)
	fullCfg, cfgErr := loader.Load()
	if cfgErr != nil {
		slog.Warn("failed to load config; using defaults", "error", cfgErr)
	} else {
		// Wire workflow settings from config → kernel.
		wf := fullCfg.Core.Workflow
		if wf.FailurePolicy != "" {
			k.SetFailurePolicy(wf.FailurePolicy)
		}
		if wf.MaxRetries > 0 {
			k.SetMaxTaskRetries(wf.MaxRetries)
		}

		// Load hooks from tools section into unified hook manager.
		hookMgr := hook.NewManager(k.Events())
		hookMgr.RegisterBatch(convertAllHooks(fullCfg.Tools.Hooks))
		k.SetHookManager(hookMgr)
		if count := len(hookMgr.List("")); count > 0 {
			slog.Info("hooks loaded", "count", count)
		}
	}

	return k, func() {
		_ = k.Close()
		cleanup()
	}, nil
}

// convertAllHooks converts config.HooksConfig into a flat []hook.Config slice
// covering all 9 hook points.
func convertAllHooks(cfg config.HooksConfig) []hook.Config {
	type pointEntries struct {
		point   hook.Point
		entries []config.HookEntry
	}
	all := []pointEntries{
		{hook.PreIntent, cfg.PreIntent},
		{hook.PostIntent, cfg.PostIntent},
		{hook.PostPlan, cfg.PostPlan},
		{hook.PreCode, cfg.PreCode},
		{hook.PostCode, cfg.PostCode},
		{hook.PreToolUse, cfg.PreToolUse},
		{hook.PostToolUse, cfg.PostToolUse},
		{hook.PostAccept, cfg.PostAccept},
		{hook.PostWorkflow, cfg.PostWorkflow},
	}
	var out []hook.Config
	for _, pe := range all {
		for _, e := range pe.entries {
			var d time.Duration
			if e.Timeout != "" {
				var err error
				d, err = time.ParseDuration(e.Timeout)
				if err != nil {
					slog.Warn("hooks: skipping entry with invalid timeout",
						"name", e.Name, "timeout", e.Timeout, "error", err)
					continue
				}
			}
			out = append(out, hook.Config{
				Name:     e.Name,
				Point:    pe.point,
				Command:  e.Command,
				Blocking: e.Blocking,
				Timeout:  d,
				Env:      e.Env,
				Source:   "config",
				Priority: e.Priority,
			})
		}
	}
	return out
}

func moduleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func dependencyOptions(workspaceRoot, inputMode, plannerMode, coderMode, acceptorMode, executorMode string) launcher.Options {
	opts := launcher.DefaultOptions(moduleRoot(), workspaceRoot)
	// Only override auto-detected modes when the user explicitly provides
	// a value via CLI flags.  Empty string means "use auto-detected default".
	if inputMode != "" {
		opts.Input = inputMode
	}
	if plannerMode != "" {
		opts.Planner = plannerMode
	}
	if coderMode != "" {
		opts.Coder = coderMode
	}
	if acceptorMode != "" {
		opts.Acceptor = acceptorMode
	}
	if executorMode != "" {
		opts.Executor = executorMode
	}
	return opts
}

// loadLLMServerConfig attempts to load the LLM server configuration from
// config.yaml. Returns a zero ServerConfig (Enabled=false) if no config
// is found or parsing fails — this means embedded mode is used by default.
func loadLLMServerConfig(workspaceRoot string) config.ServerConfig {
	loader := config.NewLoader(workspaceRoot)
	cfg, err := loader.Load()
	if err != nil {
		return config.ServerConfig{}
	}
	return cfg.LLM.Server
}

// findConfigPath returns the path to the first existing config.yaml
// that the sidecar should read. Returns empty if not found.
func findConfigPath(workspaceRoot string) string {
	loader := config.NewLoader(workspaceRoot)
	for _, p := range loader.SearchPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// needsLLM returns true if any workflow stage requires an LLM-backed worker.
// When all stages are "loopback" (stub), LLM keys are optional.
func needsLLM(opts launcher.Options) bool {
	llmModes := map[string]bool{
		"llm":     true,
		"process": true, // process is the default and resolves to llm when available
	}
	return llmModes[opts.Input] ||
		llmModes[opts.Planner] ||
		llmModes[opts.Coder] ||
		llmModes[opts.Acceptor] ||
		llmModes[opts.Executor]
}

func defaultStateDBPath(workspaceRoot string) string {
	path, err := storagepath.DefaultMainDBPath()
	if err != nil {
		return filepath.Join(workspaceRoot, ".coden", "main.sqlite")
	}
	return path
}

func runPlain(ctx context.Context, client api.ClientAPI, sessionID, prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("plain mode requires --prompt")
	}
	ui := plain.New()
	events, cancel, err := client.Subscribe(ctx, sessionID)
	if err != nil {
		return err
	}
	defer cancel()

	// Submit now returns workflowID immediately; wait for checkpoint.updated.
	workflowID, err := client.Submit(ctx, sessionID, prompt)
	if err != nil {
		return err
	}

	idle := time.NewTimer(plainIdleTimeout)
	defer idle.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return fmt.Errorf("timed out waiting for workflow completion")
		case event, ok := <-events:
			if !ok {
				return fmt.Errorf("event stream closed before workflow completion")
			}
			// Reset idle timer on every event — workflow is still making progress.
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(plainIdleTimeout)
			fmt.Println(ui.RenderEvent(event))
			switch event.Topic {
			case model.EventCheckpointUpdated:
				payload, err := model.DecodePayload[model.CheckpointUpdatedPayload](event)
				if err != nil {
					return fmt.Errorf("decode checkpoint.updated payload: %w", err)
				}
				if payload.WorkflowID == workflowID {
					goto done
				}
			case model.EventWorkflowCanceled:
				payload, err := model.DecodePayload[model.WorkflowCanceledPayload](event)
				if err != nil {
					return fmt.Errorf("decode workflow.canceled payload: %w", err)
				}
				if payload.WorkflowID == workflowID {
					if payload.Reason != "" {
						return fmt.Errorf("workflow canceled: %s", payload.Reason)
					}
					return fmt.Errorf("workflow canceled")
				}
			case model.EventWorkflowFailed:
				payload, err := model.DecodePayload[model.WorkflowFailedPayload](event)
				if err != nil {
					return fmt.Errorf("decode workflow.failed payload: %w", err)
				}
				if payload.WorkflowID == workflowID {
					reason := payload.Reason
					if reason == "" {
						reason = payload.Error
					}
					if reason != "" {
						return fmt.Errorf("workflow failed: %s", reason)
					}
					return fmt.Errorf("workflow failed")
				}
			}
		}
	}

done:
	// Fetch the final checkpoint by workflowID now that it has been persisted.
	checkpoint, err := client.GetCheckpoint(ctx, sessionID, workflowID)
	if err != nil {
		return fmt.Errorf("get checkpoint: %w", err)
	}
	fmt.Println(ui.RenderCheckpoint(checkpoint))
	return nil
}

func runCheckpointQuery(ctx context.Context, client api.ClientAPI, sessionID string, query checkpointQuery) error {
	ui := plain.New()

	if query.WorkflowID != "" {
		result, err := client.GetCheckpoint(ctx, sessionID, query.WorkflowID)
		if err != nil {
			return err
		}
		fmt.Println(ui.RenderCheckpoint(result))
		return nil
	}

	results, err := client.ListCheckpoints(ctx, sessionID, query.Limit)
	if err != nil {
		return err
	}
	fmt.Println(ui.RenderCheckpointList(results))
	return nil
}

func runSessionQuery(ctx context.Context, client api.ClientAPI) error {
	ui := plain.New()
	sessions, err := client.ListSessions(ctx, 100)
	if err != nil {
		return err
	}
	fmt.Println(ui.RenderSessionList(sessions))
	return nil
}

// runToolsCommand handles --tools-list and --tools-check CLI flags.
func runToolsCommand(workspaceRoot string, forceCheck bool) error {
	cfg, err := config.LoadConfig(workspaceRoot)
	if err != nil {
		// Use empty config if loading fails.
		cfg = &config.ToolsConfig{}
	}

	var inv *inventory.Inventory
	if forceCheck {
		fmt.Println("Force re-probing all tools (cache cleared)...")
		inv = inventory.DiscoverFresh(workspaceRoot, cfg)
	} else {
		inv = inventory.Discover(workspaceRoot, cfg)
	}

	// Detect languages
	langs := inventory.DetectProjectLanguages(workspaceRoot)
	if len(langs) > 0 {
		fmt.Printf("Detected languages: %s\n\n", strings.Join(langs, ", "))
	}

	// Print table
	fmt.Printf("%-20s %-14s %-12s %-10s %s\n", "NAME", "CATEGORY", "STATUS", "VERSION", "PATH")
	fmt.Printf("%-20s %-14s %-12s %-10s %s\n", "----", "--------", "------", "-------", "----")

	for _, entry := range inv.All() {
		status := string(entry.Status)
		version := entry.Version
		if version == "" {
			version = "-"
		}
		path := entry.Path
		if path == "" {
			path = "-"
		}
		fmt.Printf("%-20s %-14s %-12s %-10s %s\n",
			entry.Name, string(entry.Category), status, version, path)
	}

	summary := inv.Summary()
	fmt.Printf("\n%s\n", summary)
	return nil
}
