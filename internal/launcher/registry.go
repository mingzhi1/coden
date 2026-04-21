package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/mingzhi1/coden/internal/agent/accept"
	"github.com/mingzhi1/coden/internal/agent/code"
	"github.com/mingzhi1/coden/internal/agent/input"
	"github.com/mingzhi1/coden/internal/agent/plan"
	"github.com/mingzhi1/coden/internal/agent/search"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/llm"
	mcppkg "github.com/mingzhi1/coden/internal/mcp"
	"github.com/mingzhi1/coden/internal/plugin"
	"github.com/mingzhi1/coden/internal/tool/mux"
	"github.com/mingzhi1/coden/internal/tool/readfile"
	"github.com/mingzhi1/coden/internal/tool/shelltool"
	"github.com/mingzhi1/coden/internal/tool/writefile"
)

type InputterFactory func(ctx context.Context, moduleRoot string) (workflow.Inputter, func(), error)
type PlannerFactory func(ctx context.Context, moduleRoot string) (workflow.Planner, func(), error)
type CoderFactory func(ctx context.Context, moduleRoot string) (workflow.Coder, func(), error)
type AcceptorFactory func(ctx context.Context, moduleRoot string) (workflow.Acceptor, func(), error)
type ExecutorFactory func(ctx context.Context, moduleRoot, workspaceRoot string) (toolruntime.Executor, func(), error)

// SearcherFactory builds a workflow.Searcher (SA-10).  Returning a nil
// searcher is valid and indicates that the kernel should fall back to its
// built-in LocalSearcher.
type SearcherFactory func(ctx context.Context, moduleRoot, workspaceRoot string) (workflow.Searcher, func(), error)

type Registry struct {
	Inputs    map[string]InputterFactory
	Planners  map[string]PlannerFactory
	Coders    map[string]CoderFactory
	Acceptors map[string]AcceptorFactory
	Executors map[string]ExecutorFactory
	Searchers map[string]SearcherFactory // SA-10: optional Search worker factories
	broker    *llm.Broker                // shared across all LLM workers (may be nil in server mode)
	chatter   llm.Chatter                // LLM backend: *Broker or *LLMServerClient
}

// Pool returns the shared LLM pool (underlying the broker).
// Returns nil in server mode (no embedded providers).
func (r Registry) Pool() *llm.Pool {
	if r.broker == nil {
		return nil
	}
	return r.broker.Pool()
}

// Broker returns the shared LLM broker for external use (e.g. RuntimeInfo).
// Returns nil in server mode.
func (r Registry) Broker() *llm.Broker { return r.broker }

// Chatter returns the LLM backend — either *Broker (embedded) or
// *LLMServerClient (server mode). Always non-nil after Default/DefaultWithServer.
func (r Registry) Chatter() llm.Chatter { return r.chatter }

type Options struct {
	ModuleRoot    string
	WorkspaceRoot string
	AllowShell    bool
	Input         string
	Planner       string
	Coder         string
	Acceptor      string
	Executor      string
	Searcher      string // SA-10: "" (skip), "loopback", or "process"
	Agentic       bool   // enable multi-turn agentic loop for coder
}

type Dependencies struct {
	Inputter      workflow.Inputter
	Planner       workflow.Planner
	Coder         workflow.Coder
	Acceptor      workflow.Acceptor
	Executor      toolruntime.Executor
	Searcher      workflow.Searcher // SA-10: optional; nil means kernel falls back to LocalSearcher
	MCPToolPrompt string            // formatted MCP tool descriptions for Coder prompt
}

func Default() Registry {
	// Create a shared LLM broker for all workers. The broker wraps a pool that
	// auto-discovers available API keys and layers providers with fallback.
	broker := llm.DefaultBroker()
	llmInput, llmPlan, llmCode, llmAccept := sharedChatterFactories(broker, broker)

	return Registry{
		Inputs: map[string]InputterFactory{
			"process":  input.NewProcessRPCInputter,
			"loopback": input.NewLoopbackRPCInputterAdapter,
			"llm":      llmInput,
		},
		Planners: map[string]PlannerFactory{
			"process":  plan.NewProcessRPCPlanner,
			"loopback": plan.NewLoopbackRPCPlannerAdapter,
			"llm":      llmPlan,
		},
		Coders: map[string]CoderFactory{
			"process":  code.NewProcessRPCCoder,
			"loopback": code.NewLoopbackRPCCoderAdapter,
			"llm":      llmCode,
		},
		Acceptors: map[string]AcceptorFactory{
			"process":  accept.NewProcessRPCAcceptor,
			"loopback": accept.NewLoopbackRPCAcceptorAdapter,
			"llm":      llmAccept,
		},
		Executors: map[string]ExecutorFactory{
			"process":  newProcessToolExecutorFactory,
			"loopback": writefile.NewLoopbackRPCExecutorAdapter,
		},
		Searchers: defaultSearcherFactories(),
		broker:  broker,
		chatter: broker,
	}
}

// DefaultWithServer creates a registry that uses an LLMServerClient
// to communicate with the llm-server sidecar instead of embedded providers.
func DefaultWithServer(addr string) Registry {
	client := llm.NewLLMServerClient(addr)
	llmInput, llmPlan, llmCode, llmAccept := sharedChatterFactories(client, nil)

	slog.Info("[launcher] using llm-server mode", "addr", addr)
	return Registry{
		Inputs: map[string]InputterFactory{
			"process":  input.NewProcessRPCInputter,
			"loopback": input.NewLoopbackRPCInputterAdapter,
			"llm":      llmInput,
		},
		Planners: map[string]PlannerFactory{
			"process":  plan.NewProcessRPCPlanner,
			"loopback": plan.NewLoopbackRPCPlannerAdapter,
			"llm":      llmPlan,
		},
		Coders: map[string]CoderFactory{
			"process":  code.NewProcessRPCCoder,
			"loopback": code.NewLoopbackRPCCoderAdapter,
			"llm":      llmCode,
		},
		Acceptors: map[string]AcceptorFactory{
			"process":  accept.NewProcessRPCAcceptor,
			"loopback": accept.NewLoopbackRPCAcceptorAdapter,
			"llm":      llmAccept,
		},
		Executors: map[string]ExecutorFactory{
			"process":  newProcessToolExecutorFactory,
			"loopback": writefile.NewLoopbackRPCExecutorAdapter,
		},
		Searchers: defaultSearcherFactories(),
		chatter: client,
	}
}

// DefaultWithOverride creates a registry where the specified provider/model is prepended
// to the primary pool, overriding auto-detection priority.
func DefaultWithOverride(providerName, modelName string) Registry {
	pool := llm.NewPool()
	if providerName != "" || modelName != "" {
		pool.Add(llm.Config{Provider: providerName, Model: modelName})
	}
	pool.Add(llm.Config{Provider: "anthropic"})
	pool.Add(llm.Config{Provider: "openai"})
	pool.Add(llm.Config{Provider: "deepseek"})
	pool.AddLight(llm.Config{Provider: "deepseek"})
	pool.AddLight(llm.Config{Provider: "openai"})

	broker := llm.NewBroker(pool)
	llmInput, llmPlan, llmCode, llmAccept := sharedChatterFactories(broker, broker)
	return Registry{
		Inputs: map[string]InputterFactory{
			"process": input.NewProcessRPCInputter, "loopback": input.NewLoopbackRPCInputterAdapter, "llm": llmInput,
		},
		Planners: map[string]PlannerFactory{
			"process": plan.NewProcessRPCPlanner, "loopback": plan.NewLoopbackRPCPlannerAdapter, "llm": llmPlan,
		},
		Coders: map[string]CoderFactory{
			"process": code.NewProcessRPCCoder, "loopback": code.NewLoopbackRPCCoderAdapter, "llm": llmCode,
		},
		Acceptors: map[string]AcceptorFactory{
			"process": accept.NewProcessRPCAcceptor, "loopback": accept.NewLoopbackRPCAcceptorAdapter, "llm": llmAccept,
		},
		Executors: map[string]ExecutorFactory{
			"process": newProcessToolExecutorFactory, "loopback": writefile.NewLoopbackRPCExecutorAdapter,
		},
		Searchers: defaultSearcherFactories(),
		broker:  broker,
		chatter: broker,
	}
}

func DefaultOptions(moduleRoot, workspaceRoot string) Options {
	// Prefer in-process LLM workers when an API key is available; otherwise
	// fall back to loopback stubs so the system still runs without credentials.
	workerMode := "loopback"
	hasLLM := os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("DEEPSEEK_API_KEY") != "" || os.Getenv("MINIMAX_API_KEY") != "" || os.Getenv("GITHUB_COPILOT_TOKEN") != ""
	if hasLLM {
		workerMode = "llm"
	} else {
		slog.Warn("[launcher] no LLM API key detected, using loopback stubs (set OPENAI_API_KEY, ANTHROPIC_API_KEY, DEEPSEEK_API_KEY, MINIMAX_API_KEY, or GITHUB_COPILOT_TOKEN)")
	}
	return Options{
		ModuleRoot:    moduleRoot,
		WorkspaceRoot: workspaceRoot,
		Input:         workerMode,
		Planner:       workerMode,
		Coder:         workerMode,
		Acceptor:      workerMode,
		Executor:      "process",
		Agentic:       hasLLM, // enable agentic loop when LLM is available
	}
}

func (r Registry) Start(ctx context.Context, opts Options) (Dependencies, func(), error) {
	var deps Dependencies
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	inputFactory, ok := r.Inputs[opts.Input]
	if !ok {
		return Dependencies{}, func() {}, fmt.Errorf("unknown input launcher: %s", opts.Input)
	}
	inputter, inputCleanup, err := inputFactory(ctx, opts.ModuleRoot)
	if err != nil {
		return Dependencies{}, func() {}, err
	}
	cleanups = append(cleanups, inputCleanup)
	deps.Inputter = inputter

	plannerFactory, ok := r.Planners[opts.Planner]
	if !ok {
		return Dependencies{}, func() {}, fmt.Errorf("unknown planner launcher: %s", opts.Planner)
	}
	planner, plannerCleanup, err := plannerFactory(ctx, opts.ModuleRoot)
	if err != nil {
		return Dependencies{}, func() {}, err
	}
	cleanups = append(cleanups, plannerCleanup)
	deps.Planner = planner

	coderFactory, ok := r.Coders[opts.Coder]
	if !ok {
		cleanup()
		return Dependencies{}, func() {}, fmt.Errorf("unknown coder launcher: %s", opts.Coder)
	}
	var coder workflow.Coder
	var coderCleanup func()
	// If agentic mode + LLM coder, create an agentic coder with workspace access.
	if opts.Agentic && opts.Coder == "llm" && opts.WorkspaceRoot != "" {
		coder, coderCleanup, err = newAgenticCoderFactory(r.chatter, opts.WorkspaceRoot)
	} else {
		coder, coderCleanup, err = coderFactory(ctx, opts.ModuleRoot)
	}
	if err != nil {
		cleanup()
		return Dependencies{}, func() {}, err
	}
	cleanups = append(cleanups, coderCleanup)
	deps.Coder = coder

	acceptorFactory, ok := r.Acceptors[opts.Acceptor]
	if !ok {
		cleanup()
		return Dependencies{}, func() {}, fmt.Errorf("unknown acceptor launcher: %s", opts.Acceptor)
	}
	// For the LLM acceptor, pass workspaceRoot so InformedAcceptor can read
	// artifact files relative to the user's workspace (not the module root).
	acceptorArg := opts.ModuleRoot
	if opts.Acceptor == "llm" && opts.WorkspaceRoot != "" {
		acceptorArg = opts.WorkspaceRoot
	}
	acceptor, acceptorCleanup, err := acceptorFactory(ctx, acceptorArg)
	if err != nil {
		cleanup()
		return Dependencies{}, func() {}, err
	}
	cleanups = append(cleanups, acceptorCleanup)
	deps.Acceptor = acceptor

	if opts.Executor == "process" {
		// Process executor is handled directly to capture the MCP tool prompt.
		executor, mcpPrompt, executorCleanup, execErr := buildProcessToolExecutor(ctx, opts.ModuleRoot, opts.WorkspaceRoot)
		if execErr != nil {
			cleanup()
			return Dependencies{}, func() {}, execErr
		}
		cleanups = append(cleanups, executorCleanup)
		deps.Executor = executor
		deps.MCPToolPrompt = mcpPrompt
	} else {
		executorFactory, ok := r.Executors[opts.Executor]
		if !ok {
			cleanup()
			return Dependencies{}, func() {}, fmt.Errorf("unknown executor launcher: %s", opts.Executor)
		}
		executor, executorCleanup, err := executorFactory(ctx, opts.ModuleRoot, opts.WorkspaceRoot)
		if err != nil {
			cleanup()
			return Dependencies{}, func() {}, err
		}
		cleanups = append(cleanups, executorCleanup)
		deps.Executor = executor
	}

	// SA-10: Searcher is optional. Empty string skips, leaving the kernel to
	// fall back to its built-in LocalSearcher.
	if opts.Searcher != "" {
		searcherFactory, ok := r.Searchers[opts.Searcher]
		if !ok {
			cleanup()
			return Dependencies{}, func() {}, fmt.Errorf("unknown searcher launcher: %s", opts.Searcher)
		}
		searcher, searcherCleanup, err := searcherFactory(ctx, opts.ModuleRoot, opts.WorkspaceRoot)
		if err != nil {
			cleanup()
			return Dependencies{}, func() {}, err
		}
		cleanups = append(cleanups, searcherCleanup)
		deps.Searcher = searcher
	}

	return deps, cleanup, nil
}

// defaultSearcherFactories returns the built-in SearcherFactory map shared by
// every Registry constructor. The "loopback" entry wraps a kernel-free
// WorkspaceSearcher in an in-memory RPC pipe; "process" launches the
// coden-agent-search subprocess.
func defaultSearcherFactories() map[string]SearcherFactory {
	return map[string]SearcherFactory{
		"loopback": func(ctx context.Context, _, workspaceRoot string) (workflow.Searcher, func(), error) {
			if workspaceRoot == "" {
				return nil, func() {}, fmt.Errorf("loopback searcher requires workspaceRoot")
			}
			ws := workspace.New(workspaceRoot)
			rt, err := toolruntime.NewWithConfig(ws, workspaceRoot)
			if err != nil {
				rt = toolruntime.New(ws)
			}
			ws2 := search.NewWorkspaceSearcher(ws, rt, "loopback")
			return search.NewLoopbackRPCSearcher(ctx, ws2)
		},
		"process": func(ctx context.Context, moduleRoot, workspaceRoot string) (workflow.Searcher, func(), error) {
			return search.NewProcessRPCSearcher(ctx, moduleRoot, workspaceRoot)
		},
	}
}

// --- LLM factory helpers ---
// These wrap the in-process llm worker implementations so they satisfy
// the launcher factory signatures without spawning any subprocesses.
// All workers share a single Pool created once per Start() call.

// sharedChatterFactories returns factory functions that all close over the same Chatter.
// Both *Broker (embedded) and *LLMServerClient (server mode) satisfy Chatter.
func sharedChatterFactories(chatter llm.Chatter, _ *llm.Broker) (InputterFactory, PlannerFactory, CoderFactory, AcceptorFactory) {
	inputF := func(_ context.Context, _ string) (workflow.Inputter, func(), error) {
		return llm.NewLLMInputter(chatter), func() {}, nil
	}
	planF := func(_ context.Context, _ string) (workflow.Planner, func(), error) {
		return llm.NewLLMPlanner(chatter), func() {}, nil
	}
	codeF := func(_ context.Context, _ string) (workflow.Coder, func(), error) {
		return llm.NewLLMCoder(chatter), func() {}, nil
	}
	acceptF := func(_ context.Context, root string) (workflow.Acceptor, func(), error) {
		if root != "" {
			ws := workspace.New(root)
			executor := toolruntime.NewLocalExecutor(ws)
			return llm.NewInformedAcceptor(chatter, executor), func() {}, nil
		}
		return llm.NewLLMAcceptor(chatter), func() {}, nil
	}
	return inputF, planF, codeF, acceptF
}

func newProcessToolExecutorFactory(ctx context.Context, moduleRoot, workspaceRoot string) (toolruntime.Executor, func(), error) {
	exec, _, cl, err := buildProcessToolExecutor(ctx, moduleRoot, workspaceRoot)
	return exec, cl, err
}

// buildProcessToolExecutor creates the mux executor with all tool backends
// and returns the formatted MCP tool prompt alongside the executor.
func buildProcessToolExecutor(ctx context.Context, moduleRoot, workspaceRoot string) (toolruntime.Executor, string, func(), error) {
	writeExec, writeCleanup, err := writefile.NewProcessRPCExecutor(ctx, moduleRoot, workspaceRoot)
	if err != nil {
		return nil, "", nil, err
	}

	readExec, readCleanup, err := readfile.NewProcessRPCExecutor(ctx, moduleRoot, workspaceRoot)
	if err != nil {
		writeCleanup()
		return nil, "", nil, err
	}

	shellExec, shellCleanup, err := shelltool.NewProcessRPCExecutor(ctx, moduleRoot, workspaceRoot)
	if err != nil {
		readCleanup()
		writeCleanup()
		return nil, "", nil, err
	}

	// Create a local executor for tools that don't have dedicated subprocesses
	// (grep_context, rag_*, lsp_*). Best-effort: use config when available,
	// fall back to a basic workspace-only executor.
	ws := workspace.New(workspaceRoot)
	var localExec toolruntime.Executor
	configuredRT, cfgErr := toolruntime.NewWithConfig(ws, moduleRoot)
	if cfgErr != nil {
		slog.Info("[launcher] tools config not available, using basic local executor", "error", cfgErr)
		localExec = toolruntime.NewLocalExecutor(ws)
	} else {
		localExec = configuredRT
	}

	// Load MCP configuration and connect servers (best-effort).
	// Merge: user ~/.coden/mcp.json + project .mcp.json + plugin MCP servers.
	mcpCfg, mcpSources := mcppkg.LoadConfig(workspaceRoot)
	// Merge plugin-declared MCP servers (lower priority than explicit config).
	pluginReg := plugin.NewRegistry()
	pluginReg.LoadFromDirs(plugin.ScopeUser, plugin.UserPluginDir())
	pluginReg.LoadFromDirs(plugin.ScopeProject, plugin.ProjectPluginDir(workspaceRoot))
	for pluginName, pluginMCP := range pluginReg.MCPConfigs() {
		if _, exists := mcpCfg.MCPServers[pluginName]; !exists {
			mcpCfg.MCPServers[pluginName] = mcppkg.ServerConfig{
				Command: pluginMCP.Command,
				Args:    pluginMCP.Args,
				Env:     pluginMCP.Env,
			}
			mcpSources[pluginName] = "plugin"
		}
	}
	var mcpManager *mcppkg.Manager
	mcpToolKinds := make(map[string]toolruntime.Executor)
	if len(mcpCfg.MCPServers) > 0 {
		mcpManager = mcppkg.NewManager(mcpCfg, mcpSources)
		mcpManager.ConnectAll(ctx, mcpCfg)
		if mcpManager.ToolCount() > 0 {
			mcpExec := mcppkg.NewExecutor(mcpManager)
			for _, tool := range mcpManager.Tools() {
				mcpToolKinds[tool.Kind()] = mcpExec
				// Register in localExec's tool_search registry so Coder can
				// discover MCP tools dynamically via tool_search.
				if tr, ok := localExec.(toolruntime.ToolRegisterer); ok {
					tr.RegisterTool(toolruntime.ToolMeta{
						Name:        tool.Kind(),
						Description: tool.Description,
						Parameters:  mcppkg.FormatToolParams(tool),
						Deferred:    true,
						Category:    "mcp",
						SearchHints: []string{"mcp", tool.ServerName, tool.ToolName},
					})
				}
			}
			slog.Info("[launcher] MCP tools registered", "count", mcpManager.ToolCount(), "servers", mcpManager.ServerCount())
		}
	}

	byKind := map[string]toolruntime.Executor{
		"read_file":        readExec,
		"list_dir":         readExec,
		"search":           readExec,
		"run_shell":        shellExec,
		"write_file":       writeExec,
		"edit_file":        writeExec,
		"grep_context":     localExec,
		"rag_search":       localExec,
		"rag_index_build":  localExec,
		"rag_index_update": localExec,
		"lsp_symbols":      localExec,
		"lsp_definition":   localExec,
		"lsp_references":   localExec,
		"lsp_didopen":      localExec,
	}
	// Merge MCP tools
	for kind, exec := range mcpToolKinds {
		byKind[kind] = exec
	}

	executor := mux.New(writeExec, byKind)

	// Format MCP tool descriptions for Coder prompt injection.
	var mcpToolPrompt string
	if mcpManager != nil && mcpManager.ToolCount() > 0 {
		var sb strings.Builder
		sb.WriteString("## MCP Tools\n\n")
		sb.WriteString("The following MCP (Model Context Protocol) tools are available. ")
		sb.WriteString("Use them by emitting tool_calls with the specified kind.\n\n")
		for _, tool := range mcpManager.Tools() {
			sb.WriteString(fmt.Sprintf("### %s\n", tool.Kind()))
			sb.WriteString(fmt.Sprintf("Server: %s | Tool: %s\n", tool.ServerName, tool.ToolName))
			if tool.Description != "" {
				sb.WriteString(tool.Description)
				sb.WriteString("\n")
			}
			if tool.InputSchema != nil {
				if schemaJSON, jsonErr := json.Marshal(tool.InputSchema); jsonErr == nil {
					sb.WriteString(fmt.Sprintf("Input schema: %s\n", string(schemaJSON)))
				}
			}
			sb.WriteString("\n")
		}
		mcpToolPrompt = sb.String()
	}

	cleanup := func() {
		if mcpManager != nil {
			mcpManager.Close()
		}
		shellCleanup()
		readCleanup()
		writeCleanup()
	}
	return executor, mcpToolPrompt, cleanup, nil
}

// newAgenticCoderFactory creates an LLM coder with a local read-only executor
// for the agentic discovery loop. The coder can read_file/search/list_dir
// locally during generation, then emit mutations for the kernel to execute.
func newAgenticCoderFactory(chatter llm.Chatter, workspaceRoot string) (workflow.Coder, func(), error) {
	ws := workspace.New(workspaceRoot)
	executor := toolruntime.NewLocalExecutor(ws)
	return llm.NewAgenticCoder(chatter, executor), func() {}, nil
}
