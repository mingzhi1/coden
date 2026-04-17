package toolruntime

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/core/artifact"
	"github.com/mingzhi1/coden/internal/core/workspace"
)

const (
	// defaultShellTimeoutSec is used when Request.TimeoutSec is 0.
	defaultShellTimeoutSec = 60
	// maxShellOutputBytes caps each of stdout and stderr.
	maxShellOutputBytes = 32 * 1024
)

type Executor interface {
	Execute(ctx context.Context, req Request) (Result, error)
}

// Request is a tool invocation submitted by a Coder worker.
// Kind selects the operation; Path and Content carry the operands.
type Request struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Content string `json:"content"`
	// Dir is used by list_dir as an optional subdirectory.
	Dir     string `json:"dir,omitempty"`
	Query   string `json:"query,omitempty"`
	Command string `json:"command,omitempty"`
	// OldContent / NewContent are used by edit_file for search-and-replace.
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
	// TimeoutSec overrides the default 60s shell timeout (run_shell only).
	TimeoutSec int `json:"timeout_sec,omitempty"`
	// Search options (R-01, R-02, R-03)
	IsRegex      bool `json:"is_regex,omitempty"`      // R-03: use regex instead of literal
	Line         int  `json:"line,omitempty"`          // R-02: target line for context
	ContextLines int  `json:"context_lines,omitempty"` // R-02: lines of context (default 3)
	Column       int  `json:"column,omitempty"`        // R-06: column for LSP queries
	TopK         int  `json:"top_k,omitempty"`         // R-10: number of results for RAG
}

// Result is what the Tool Runtime returns after executing a Request.
type Result struct {
	ArtifactPath string
	Summary      string
	// Output carries read data (read_file content, list_dir listing).
	Output   string
	Stderr   string
	ExitCode int
	Before   string
	After    string
	Diff     string

	// StructuredData carries typed results (e.g. []retrieval.RetrievalEvidence)
	// so callers can avoid re-parsing the human-readable Output string.
	StructuredData any `json:"-"`

	// ErrorClass and ErrorHuman are populated when a run_shell command exits
	// non-zero. ErrorHuman is a single-sentence, LLM-friendly description that
	// replaces raw stderr in prompt injection.
	ErrorClass ErrorClass
	ErrorHuman string

	// SpilledPath is non-empty when the full output was written to a temp file
	// because it exceeded MaxResultChars. Preview contains the first N lines.
	SpilledPath string
	Preview     string
}

type Runtime struct {
	executor Executor
}

// LSPProvider is implemented by runtimes that can expose an in-process LSP manager.
type LSPProvider interface {
	LSPManager(path string) interface{}
}

// LSPManager returns the in-process LSP manager for a file path when available.
func (r *Runtime) LSPManager(path string) interface{} {
	if r == nil {
		return nil
	}
	provider, ok := r.executor.(LSPProvider)
	if !ok {
		return nil
	}
	return provider.LSPManager(path)
}

// CheckpointNotifier is an optional interface that executors can implement
// to receive post-checkpoint notifications for index maintenance.
type CheckpointNotifier interface {
	NotifyCheckpointPassed(dirtyPaths []string)
}

// NotifyCheckpointPassed notifies the executor that a checkpoint passed
// so it can update indexes. No-op if executor doesn't support it.
func (r *Runtime) NotifyCheckpointPassed(dirtyPaths []string) {
	if cn, ok := r.executor.(CheckpointNotifier); ok {
		cn.NotifyCheckpointPassed(dirtyPaths)
	}
}

// ArtifactManagerSetter is an optional interface for executors that support
// artifact persistence.
type ArtifactManagerSetter interface {
	SetArtifactManager(mgr artifact.Manager)
}

// SetArtifactManager injects an artifact Manager into the executor if it
// supports ArtifactManagerSetter. No-op otherwise.
func (r *Runtime) SetArtifactManager(mgr artifact.Manager) {
	if s, ok := r.executor.(ArtifactManagerSetter); ok {
		s.SetArtifactManager(mgr)
	}
}

// ToolRegisterer is an optional interface for executors that support
// runtime tool registration (e.g. for MCP tool discovery).
type ToolRegisterer interface {
	RegisterTool(meta ToolMeta)
}

// RegisterTool registers a tool in the executor's tool_search registry if
// the executor supports ToolRegisterer. No-op otherwise.
func (r *Runtime) RegisterTool(meta ToolMeta) {
	if tr, ok := r.executor.(ToolRegisterer); ok {
		tr.RegisterTool(meta)
	}
}

func New(workspace *workspace.Service) *Runtime {
	rt, err := NewWithExecutor(NewLocalExecutor(workspace))
	if err != nil {
		panic(err) // NewLocalExecutor never returns nil, so this is unreachable
	}
	return rt
}

// NewWithConfig creates a Runtime with LSP tools loaded from configuration.
// workspaceRoot is used to resolve the two-level config hierarchy
// (~/.coden/config.yaml → <workspace>/.coden/config.yaml → legacy tools.yaml).
func NewWithConfig(workspace *workspace.Service, workspaceRoot string) (*Runtime, error) {
	// Load configuration via two-level merge
	cfg, err := loadToolsConfig(workspaceRoot)
	if err != nil {
		return nil, err
	}

	// Create LSP tools
	lspTools, err := createLSPTools(workspace.Root(), cfg)
	if err != nil {
		return nil, err
	}

	// Create RAG tool
	var ragTool Executor
	if cfg.RAG.Enabled {
		ragTool, err = createRAGTool(workspace.Root(), cfg)
		if err != nil {
			return nil, err
		}
	}

	executor := NewLocalExecutorWithTools(workspace, lspTools, ragTool)
	executor.searchConfig = &cfg.Search
	return NewWithExecutor(executor)
}

// loadToolsConfig loads the effective tool configuration for the given workspace
// using the two-level config merge (user defaults → workspace overrides → legacy fallback).
func loadToolsConfig(workspaceRoot string) (*config.ToolsConfig, error) {
	return config.LoadConfig(workspaceRoot)
}

func NewWithExecutor(executor Executor) (*Runtime, error) {
	if executor == nil {
		return nil, fmt.Errorf("toolruntime: executor is required")
	}
	return &Runtime{executor: executor}, nil
}

type LocalExecutor struct {
	workspace    *workspace.Service
	lspTools     map[string]Executor  // language -> LSP tool executor
	ragTool      Executor             // RAG tool executor
	webFetchTool Executor             // Web fetch tool executor
	searchConfig *config.SearchConfig // search tool configuration (nil = defaults)
	registry     *ToolRegistry        // M12-02: tool metadata registry
	artifactMgr  artifact.Manager     // M13: optional artifact manager
}

// SetArtifactManager injects an artifact Manager into the executor.
// When set, all tool results are automatically persisted as artifacts.
func (r *LocalExecutor) SetArtifactManager(mgr artifact.Manager) {
	r.artifactMgr = mgr
}

// RegisterTool registers a tool in the tool_search registry so the Coder
// can discover it at runtime. Used to register MCP and plugin tools.
func (r *LocalExecutor) RegisterTool(meta ToolMeta) {
	if r.registry == nil {
		r.registry = NewToolRegistry()
	}
	r.registry.Register(meta)
}

// LSPManager returns the in-process LSP manager for a file path when available.
// Discovery uses this to obtain structured evidence without reparsing prompt text.
func (r *LocalExecutor) LSPManager(path string) interface{} {
	lang := detectLanguage(path)
	if lang == "" {
		return nil
	}
	tool, ok := r.lspTools[lang]
	if !ok {
		return nil
	}
	lspTool, ok := tool.(*LSPTool)
	if !ok {
		return nil
	}
	return lspTool.Manager()
}

func NewLocalExecutor(workspace *workspace.Service) *LocalExecutor {
	return &LocalExecutor{
		workspace: workspace,
		lspTools:  make(map[string]Executor),
		registry:  NewToolRegistry(),
	}
}

// NewLocalExecutorWithLSP creates a LocalExecutor with LSP tools.
func NewLocalExecutorWithLSP(workspace *workspace.Service, lspTools map[string]Executor) *LocalExecutor {
	return &LocalExecutor{
		workspace: workspace,
		lspTools:  lspTools,
		registry:  NewToolRegistry(),
	}
}

// NewLocalExecutorWithTools creates a LocalExecutor with LSP and RAG tools.
func NewLocalExecutorWithTools(workspace *workspace.Service, lspTools map[string]Executor, ragTool Executor) *LocalExecutor {
	return &LocalExecutor{
		workspace:    workspace,
		lspTools:     lspTools,
		ragTool:      ragTool,
		webFetchTool: NewWebFetchTool(),
		registry:     NewToolRegistry(),
	}
}

// NotifyCheckpointPassed triggers RAG incremental update for dirty paths.
// Implements CheckpointNotifier.
func (r *LocalExecutor) NotifyCheckpointPassed(dirtyPaths []string) {
	if r.ragTool == nil || len(dirtyPaths) == 0 {
		return
	}
	// Sync workspace dirty paths into RAG index before triggering update.
	if rt, ok := r.ragTool.(*RAGTool); ok {
		rt.index.MarkDirty(dirtyPaths)
	}
	// Fire RAG incremental update in background (non-blocking).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := r.ragTool.Execute(ctx, Request{Kind: "rag_index_update"})
		if err != nil {
			slog.Warn("[rag] incremental update failed", "error", err, "dirty_paths", len(dirtyPaths))
			return
		}
		slog.Info("[rag] incremental update completed", "summary", res.Summary, "dirty_paths", len(dirtyPaths))
	}()
}

var _ CheckpointNotifier = (*LocalExecutor)(nil)

func (r *Runtime) Execute(ctx context.Context, req Request) (Result, error) {
	return r.executor.Execute(ctx, req)
}

func (r *LocalExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}

	slog.Info("[tool] executing", "kind", req.Kind, "path", req.Path, "dir", req.Dir, "query", req.Query, "command", req.Command)
	start := time.Now()

	res, err := r.dispatch(ctx, req, start)

	// M13: Auto-save tool result as artifact when manager is present.
	if r.artifactMgr != nil && err == nil {
		r.saveResultArtifact(ctx, req, res, time.Since(start).Milliseconds())
	}

	return res, err
}

// dispatch routes a request to the appropriate tool handler.
func (r *LocalExecutor) dispatch(ctx context.Context, req Request, start time.Time) (Result, error) {
	switch req.Kind {
	case "write_file":
		beforeRaw, _ := r.workspace.Read(req.Path)
		path, err := r.workspace.Write(req.Path, []byte(req.Content))
		if err != nil {
			slog.Warn("[tool] write_file failed", "path", req.Path, "error", err, "duration_ms", time.Since(start).Milliseconds())
			return Result{}, err
		}
		afterRaw, readErr := r.workspace.Read(req.Path)
		if readErr != nil {
			return Result{}, fmt.Errorf("read written file %s: %w", req.Path, readErr)
		}
		beforeText, _ := normalizeReadText(beforeRaw)
		afterText, _ := normalizeReadText(afterRaw)
		res := Result{
			ArtifactPath: path,
			Summary:      fmt.Sprintf("wrote artifact to %s", path),
			Before:       beforeText,
			After:        afterText,
			Diff:         buildUnifiedDiff(req.Path, beforeText, afterText),
		}
		slog.Info("[tool] write_file completed", "path", path, "bytes", len(req.Content), "duration_ms", time.Since(start).Milliseconds())
		return res, nil

	case "read_file":
		data, err := r.workspace.Read(req.Path)
		if err != nil {
			slog.Warn("[tool] read_file failed", "path", req.Path, "error", err, "duration_ms", time.Since(start).Milliseconds())
			return Result{}, fmt.Errorf("read_file %s: %w", req.Path, err)
		}
		content, format := normalizeReadText(data)
		summary := fmt.Sprintf("read %d bytes from %s (%s)", len(data), req.Path, format.summary())
		slog.Info("[tool] read_file completed", "path", req.Path, "bytes", len(data), "duration_ms", time.Since(start).Milliseconds())
		res := Result{
			ArtifactPath: req.Path,
			Summary:      summary,
			Output:       content,
		}
		// M12-01 / M13: Spill large results — prefer artifact manager, fall back to file spill.
		if ShouldSpill(content) {
			preview := extractPreview(content, spillPreviewLines)
			res.Preview = preview
			if r.artifactMgr != nil {
				a, aErr := r.artifactMgr.SaveContent(ctx,
					workflowIDFromContext(ctx), sessionIDFromContext(ctx), "",
					artifact.KindSpill, "read_file:"+req.Path, []byte(content), nil)
				if aErr == nil {
					res.SpilledPath = "artifact:" + a.ID
					slog.Info("[tool] read_file spilled to artifact", "path", req.Path, "artifact", a.ID, "bytes", len(content))
				} else {
					slog.Warn("[tool] read_file artifact spill failed, falling back", "error", aErr)
					spillPath, _, spillErr := SpillResult(r.workspace.Root(), "read_file", req.Path, content)
					if spillErr == nil {
						res.SpilledPath = spillPath
					}
				}
			} else {
				spillPath, _, spillErr := SpillResult(r.workspace.Root(), "read_file", req.Path, content)
				if spillErr == nil {
					res.SpilledPath = spillPath
					slog.Info("[tool] read_file spilled to disk", "path", req.Path, "spill", spillPath, "bytes", len(content))
				} else {
					slog.Warn("[tool] read_file spill failed", "path", req.Path, "error", spillErr)
				}
			}
		}
		return res, nil

	case "list_dir":
		files, err := r.workspace.ListFiles(req.Dir, 200)
		if err != nil {
			slog.Warn("[tool] list_dir failed", "dir", req.Dir, "error", err, "duration_ms", time.Since(start).Milliseconds())
			return Result{}, fmt.Errorf("list_dir: %w", err)
		}
		listing := strings.Join(files, "\n")
		slog.Info("[tool] list_dir completed", "dir", req.Dir, "files", len(files), "duration_ms", time.Since(start).Milliseconds())
		return Result{
			Summary: fmt.Sprintf("listed %d files", len(files)),
			Output:  listing,
		}, nil

	case "search":
		res, err := r.executeSearch(ctx, req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] search failed", "query", req.Query, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] search completed", "query", req.Query, "summary", res.Summary, "duration_ms", dur)
		}
		return res, err
	case "grep_context":
		res, err := r.executeGrepContext(req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] grep_context failed", "path", req.Path, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] grep_context completed", "path", req.Path, "duration_ms", dur)
		}
		return res, err

	case "edit_file":
		res, err := r.executeEditFile(req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] edit_file failed", "path", req.Path, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] edit_file completed", "path", req.Path, "duration_ms", dur)
		}
		return res, err

	case "run_shell":
		res, err := r.executeRunShell(ctx, req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] run_shell failed", "command", req.Command, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] run_shell completed", "command", req.Command, "exit_code", res.ExitCode, "duration_ms", dur)
		}
		return res, err

	case "lsp_symbols", "lsp_definition", "lsp_references", "lsp_didopen":
		res, err := r.executeLSP(ctx, req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] lsp failed", "kind", req.Kind, "path", req.Path, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] lsp completed", "kind", req.Kind, "path", req.Path, "duration_ms", dur)
		}
		return res, err

	case "rag_search", "rag_index_build", "rag_index_update":
		res, err := r.executeRAG(ctx, req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] rag failed", "kind", req.Kind, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] rag completed", "kind", req.Kind, "duration_ms", dur)
		}
		return res, err

	case "web_fetch":
		res, err := r.webFetchTool.Execute(ctx, req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] web_fetch failed", "url", req.Path, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] web_fetch completed", "url", req.Path, "duration_ms", dur)
		}
		return res, err

	case "tool_search":
		res, err := r.executeToolSearch(req.Query)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] tool_search failed", "query", req.Query, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] tool_search completed", "query", req.Query, "duration_ms", dur)
		}
		return res, err

	case "read_artifact", "list_artifacts":
		if r.artifactMgr == nil {
			return Result{}, fmt.Errorf("%s: artifact manager not available", req.Kind)
		}
		res, err := NewArtifactTool(r.artifactMgr).Execute(ctx, req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			slog.Warn("[tool] artifact tool failed", "kind", req.Kind, "error", err, "duration_ms", dur)
		} else {
			slog.Info("[tool] artifact tool completed", "kind", req.Kind, "duration_ms", dur)
		}
		return res, err

	default:
		slog.Warn("[tool] unsupported kind", "kind", req.Kind)
		return Result{}, fmt.Errorf("unsupported tool request: %s", req.Kind)
	}
}

// saveResultArtifact persists a tool execution result through the artifact manager.
// This is best-effort — failures are logged but do not affect the tool result.
func (r *LocalExecutor) saveResultArtifact(ctx context.Context, req Request, res Result, durationMs int64) {
	// Determine workflow and session IDs from context (if available).
	wfID, sessID := workflowIDFromContext(ctx), sessionIDFromContext(ctx)
	if wfID == "" {
		wfID = "unknown"
	}
	if sessID == "" {
		sessID = "unknown"
	}

	status := artifact.StatusSuccess
	if res.ExitCode != 0 {
		status = artifact.StatusError
	}

	tc := artifact.ToolCall{
		ToolKind:    req.Kind,
		RequestJSON: fmt.Sprintf(`{"kind":%q,"path":%q}`, req.Kind, req.Path),
		Status:      status,
		DurationMs:  durationMs,
	}

	spillContent := ""
	if res.SpilledPath != "" {
		spillContent = res.Output
	}

	if _, err := r.artifactMgr.SaveToolResult(ctx, wfID, sessID, tc, res.Output, res.Stderr, res.Diff, spillContent); err != nil {
		slog.Warn("[artifact] auto-save failed", "kind", req.Kind, "error", err)
	}
}

// workflowIDFromContext extracts the workflow ID from context if present.
func workflowIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyWorkflowID).(string); ok {
		return v
	}
	return ""
}

// sessionIDFromContext extracts the session ID from context if present.
func sessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeySessionID).(string); ok {
		return v
	}
	return ""
}

type contextKey string

const (
	ctxKeyWorkflowID contextKey = "workflow_id"
	ctxKeySessionID  contextKey = "session_id"
)

// ContextWithIDs returns a context carrying workflow and session IDs for artifact tracking.
func ContextWithIDs(ctx context.Context, workflowID, sessionID string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyWorkflowID, workflowID)
	ctx = context.WithValue(ctx, ctxKeySessionID, sessionID)
	return ctx
}

func (r *LocalExecutor) executeRunShell(ctx context.Context, req Request) (Result, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = strings.TrimSpace(req.Content)
	}
	if command == "" {
		return Result{}, fmt.Errorf("run_shell: command is required")
	}

	// M12-04: Security checks before execution.
	if violation := CheckCommandSubstitution(command); violation != nil {
		slog.Warn("[tool] shell security: blocked command substitution", "command", command, "pattern", violation.Pattern)
		return Result{
			ExitCode:   -1,
			ErrorClass: ErrorClassPermission,
			ErrorHuman: violation.Detail,
			Summary:    fmt.Sprintf("blocked: %s", violation.Type),
		}, nil
	}

	if violation := CheckDangerousCommand(command); violation != nil {
		slog.Warn("[tool] shell security: blocked dangerous command", "command", command, "pattern", violation.Pattern)
		return Result{
			ExitCode:   -1,
			ErrorClass: ErrorClassPermission,
			ErrorHuman: violation.Detail,
			Summary:    fmt.Sprintf("blocked: %s", violation.Type),
		}, nil
	}

	if violation := CheckExfiltration(command); violation != nil {
		slog.Warn("[tool] shell security: blocked potential exfiltration", "command", command, "pattern", violation.Pattern)
		return Result{
			ExitCode:   -1,
			ErrorClass: ErrorClassPermission,
			ErrorHuman: violation.Detail,
			Summary:    fmt.Sprintf("blocked: %s", violation.Type),
		}, nil
	}

	semantics := ClassifyCommand(command)
	slog.Info("[tool] shell security", "command", command, "semantics", semantics)

	timeoutSec := req.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = defaultShellTimeoutSec
	}
	shellCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := shellCommand(shellCtx, command)
	cmd.Dir = r.workspace.Root()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	timedOut := false
	if err != nil {
		if shellCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, fmt.Errorf("run_shell %q: %w", command, err)
		}
	}

	stdoutStr := truncateOutput(stdout.Bytes(), maxShellOutputBytes)
	stderrStr := truncateOutput(stderr.Bytes(), maxShellOutputBytes)

	var summary string
	switch {
	case timedOut:
		summary = fmt.Sprintf("shell command timed out after %ds in %s", timeoutSec, r.workspace.Root())
	case exitCode != 0:
		summary = fmt.Sprintf("shell command exited with code %d in %s", exitCode, r.workspace.Root())
	default:
		summary = fmt.Sprintf("executed shell command in %s", r.workspace.Root())
	}

	res := Result{
		Summary:  summary,
		Output:   stdoutStr,
		Stderr:   stderrStr,
		ExitCode: exitCode,
	}
	if ce := ClassifyShellError(command, stdoutStr, stderrStr, exitCode, timedOut); ce != nil {
		res.ErrorClass = ce.Class
		res.ErrorHuman = ce.HumanMsg
	}
	return res, nil
}

// truncateOutput caps raw output bytes and appends a truncation notice.
func truncateOutput(data []byte, maxBytes int) string {
	if len(data) <= maxBytes {
		return string(data)
	}
	return string(data[:maxBytes]) + fmt.Sprintf("\n... (truncated, %d bytes total)", len(data))
}

func (r *LocalExecutor) executeEditFile(req Request) (Result, error) {
	if req.Path == "" {
		return Result{}, fmt.Errorf("edit_file: path is required")
	}
	old := req.OldContent
	newContent := req.NewContent
	if old == "" {
		return Result{}, fmt.Errorf("edit_file: old_content is required")
	}

	data, err := r.workspace.Read(req.Path)
	if err != nil {
		return Result{}, fmt.Errorf("edit_file read %s: %w", req.Path, err)
	}
	beforeText, _ := normalizeReadText(data)

	// Count occurrences for uniqueness check.
	count := strings.Count(beforeText, old)
	if count == 0 {
		return Result{}, fmt.Errorf("edit_file: old_content not found in %s", req.Path)
	}
	if count > 1 {
		ctxSnippet := editFileContext(beforeText, old)
		return Result{}, fmt.Errorf(
			"edit_file: %d matches found in %s, need more context to make edit unique.\n%s",
			count, req.Path, ctxSnippet,
		)
	}

	updated := strings.Replace(beforeText, old, newContent, 1)

	path, err := r.workspace.Write(req.Path, []byte(updated))
	if err != nil {
		return Result{}, fmt.Errorf("edit_file write %s: %w", req.Path, err)
	}

	return Result{
		ArtifactPath: path,
		Summary:      fmt.Sprintf("edited %s (replaced %d chars)", req.Path, len(old)),
		Before:       beforeText,
		After:        updated,
		Diff:         buildUnifiedDiff(req.Path, beforeText, updated),
	}, nil
}

// editFileContext returns a snippet showing the first occurrence of old with
// ±2 lines of surrounding context, including 1-based line numbers.
func editFileContext(text, old string) string {
	lines := strings.Split(text, "\n")
	// Find first line containing the start of old.
	firstLine := -1
	remaining := old
	for i, line := range lines {
		if strings.Contains(line, remaining) || strings.Contains(line, strings.SplitN(remaining, "\n", 2)[0]) {
			firstLine = i
			break
		}
	}
	if firstLine < 0 {
		return ""
	}
	start := firstLine - 2
	if start < 0 {
		start = 0
	}
	end := firstLine + 3
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	b.WriteString("First occurrence context:\n")
	for i := start; i < end; i++ {
		b.WriteString(fmt.Sprintf("%4d: %s\n", i+1, lines[i]))
	}
	return b.String()
}

// executeSearch uses ripgrep when available, with fallback to built-in search.
// Implements R-01 (ripgrep), R-02 (context), R-03 (regex).
func (r *LocalExecutor) executeSearch(ctx context.Context, req Request) (Result, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = strings.TrimSpace(req.Content)
	}
	if query == "" {
		return Result{}, fmt.Errorf("search: query is required")
	}

	// Set defaults
	contextLines := req.ContextLines
	if contextLines == 0 {
		contextLines = 3
	}

	// Resolve search directory relative to workspace root so the rg process
	// searches the correct absolute path regardless of the test runner's cwd.
	searchDir := req.Dir
	if searchDir != "" && !filepath.IsAbs(searchDir) {
		searchDir = filepath.Join(r.workspace.Root(), searchDir)
	} else if searchDir == "" {
		searchDir = r.workspace.Root()
	}

	// Use config values if available, otherwise defaults.
	maxResults := 50
	maxFilesize := "1M"
	rgCommand := "rg"
	if r.searchConfig != nil && r.searchConfig.Ripgrep.Enabled {
		if r.searchConfig.Ripgrep.Command != "" {
			rgCommand = r.searchConfig.Ripgrep.Command
		}
	}

	opts := SearchOptions{
		Query:         query,
		Dir:           searchDir,
		IsRegex:       req.IsRegex,
		CaseSensitive: false,
		MaxResults:    maxResults,
		MaxFilesize:   maxFilesize,
		ContextLines:  contextLines,
		RgCommand:     rgCommand,
	}

	hits, err := ExecuteRipgrep(ctx, opts)
	if err != nil {
		// Fallback to built-in search
		return r.executeBuiltinSearch(req, query)
	}

	// Strip the workspace root prefix from hit paths and normalise separators so
	// callers always see workspace-relative forward-slash paths.
	wsRoot := r.workspace.Root()
	wsRootSlash := filepath.ToSlash(wsRoot)
	for i := range hits {
		p := filepath.ToSlash(hits[i].Path)
		if strings.HasPrefix(p, wsRootSlash+"/") {
			p = p[len(wsRootSlash)+1:]
		} else if strings.HasPrefix(p, wsRootSlash) {
			p = p[len(wsRootSlash):]
		}
		hits[i].Path = p
	}

	// Convert to output format with line numbers
	output := FormatHits(hits, false) // Don't include context in main output

	res := Result{
		Summary: fmt.Sprintf("found %d match(es) for %q", len(hits), query),
		Output:  output,
	}
	// M12-01 / M13: Spill large search results — prefer artifact manager.
	if ShouldSpill(output) {
		res.Preview = extractPreview(output, spillPreviewLines)
		if r.artifactMgr != nil {
			a, aErr := r.artifactMgr.SaveContent(ctx,
				workflowIDFromContext(ctx), sessionIDFromContext(ctx), "",
				artifact.KindSpill, "search:"+query, []byte(output), nil)
			if aErr == nil {
				res.SpilledPath = "artifact:" + a.ID
				slog.Info("[tool] search spilled to artifact", "query", query, "artifact", a.ID)
			} else {
				spillPath, _, spillErr := SpillResult(r.workspace.Root(), "search", query, output)
				if spillErr == nil {
					res.SpilledPath = spillPath
				}
			}
		} else {
			spillPath, _, spillErr := SpillResult(r.workspace.Root(), "search", query, output)
			if spillErr == nil {
				res.SpilledPath = spillPath
				slog.Info("[tool] search spilled to disk", "query", query, "spill", spillPath)
			}
		}
	}
	return res, nil
}

// executeBuiltinSearch is the fallback when ripgrep is not available.
func (r *LocalExecutor) executeBuiltinSearch(req Request, query string) (Result, error) {
	files, err := r.workspace.ListFiles(req.Dir, 200)
	if err != nil {
		return Result{}, fmt.Errorf("search: %w", err)
	}

	var hits []string
	for _, file := range files {
		data, readErr := r.workspace.Read(file)
		if readErr != nil {
			continue
		}

		content := string(data)
		var match bool
		if req.IsRegex {
			// Simple regex search
			lines, _ := SearchInContent(content, query, true)
			match = len(lines) > 0
		} else {
			match = strings.Contains(content, query)
		}

		if match {
			hits = append(hits, file)
			if len(hits) >= 50 {
				break
			}
		}
	}

	return Result{
		Summary: fmt.Sprintf("found %d file(s) matching %q (builtin search)", len(hits), query),
		Output:  strings.Join(hits, "\n"),
	}, nil
}

// executeGrepContext extracts context lines around a specific line (R-02).
func (r *LocalExecutor) executeGrepContext(req Request) (Result, error) {
	if req.Path == "" {
		return Result{}, fmt.Errorf("grep_context: path is required")
	}
	if req.Line <= 0 {
		return Result{}, fmt.Errorf("grep_context: line is required")
	}

	data, err := r.workspace.Read(req.Path)
	if err != nil {
		return Result{}, fmt.Errorf("grep_context read %s: %w", req.Path, err)
	}

	content, _ := normalizeReadText(data)
	contextLines := req.ContextLines
	if contextLines == 0 {
		contextLines = 5
	}

	snippet := ExtractSnippet(content, req.Line, contextLines)
	if snippet == "" {
		return Result{}, fmt.Errorf("grep_context: line %d not found in %s", req.Line, req.Path)
	}

	return Result{
		Summary: fmt.Sprintf("context for %s:%d (±%d lines)", req.Path, req.Line, contextLines),
		Output:  snippet,
	}, nil
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/d", "/s", "/c", command)
	}
	return exec.CommandContext(ctx, "sh", "-lc", command)
}

type textFormat struct {
	Encoding string
	Newlines string
}

func (f textFormat) summary() string {
	parts := make([]string, 0, 2)
	if f.Encoding != "" {
		parts = append(parts, "encoding="+f.Encoding)
	}
	if f.Newlines != "" {
		parts = append(parts, "newlines="+f.Newlines)
	}
	if len(parts) == 0 {
		return "format=unknown"
	}
	return strings.Join(parts, ", ")
}

func normalizeReadText(data []byte) (string, textFormat) {
	format := detectTextFormat(data)
	if !utf8.Valid(data) && !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		return string(data), format
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	return string(data), format
}

func detectTextFormat(data []byte) textFormat {
	format := textFormat{
		Encoding: "utf-8",
		Newlines: "lf",
	}
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		format.Encoding = "utf-8-bom"
	}
	switch {
	case bytes.Contains(data, []byte("\r\n")):
		format.Newlines = "crlf"
	case bytes.Contains(data, []byte("\r")):
		format.Newlines = "cr"
	case bytes.Contains(data, []byte("\n")):
		format.Newlines = "lf"
	default:
		format.Newlines = "none"
	}
	if !utf8.Valid(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})) {
		format.Encoding = "binary-or-non-utf8"
	}
	return format
}

func buildUnifiedDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	var b strings.Builder
	left := "before"
	right := "after"
	if strings.TrimSpace(path) != "" {
		left = path
		right = path
	}
	b.WriteString("--- ")
	b.WriteString(left)
	b.WriteString("\n+++ ")
	b.WriteString(right)
	b.WriteString("\n")

	beforeLines := splitDiffLines(before)
	afterLines := splitDiffLines(after)
	maxLines := len(beforeLines)
	if len(afterLines) > maxLines {
		maxLines = len(afterLines)
	}
	for i := 0; i < maxLines; i++ {
		switch {
		case i >= len(beforeLines):
			b.WriteString("+")
			b.WriteString(afterLines[i])
			b.WriteString("\n")
		case i >= len(afterLines):
			b.WriteString("-")
			b.WriteString(beforeLines[i])
			b.WriteString("\n")
		case beforeLines[i] == afterLines[i]:
			b.WriteString(" ")
			b.WriteString(beforeLines[i])
			b.WriteString("\n")
		default:
			b.WriteString("-")
			b.WriteString(beforeLines[i])
			b.WriteString("\n")
			b.WriteString("+")
			b.WriteString(afterLines[i])
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func splitDiffLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// executeLSP handles LSP tool requests.
func (r *LocalExecutor) executeLSP(ctx context.Context, req Request) (Result, error) {
	lang := detectLanguage(req.Path)
	if lang == "" {
		return Result{}, fmt.Errorf("cannot detect language for file: %s", req.Path)
	}

	tool, ok := r.lspTools[lang]
	if !ok {
		return Result{}, fmt.Errorf("no LSP tool configured for language: %s", lang)
	}

	return tool.Execute(ctx, req)
}

// detectLanguage detects programming language from file extension.
func detectLanguage(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx", ".ts", ".tsx":
		return "typescript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".cpp", ".h", ".hpp":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	default:
		return ""
	}
}

// executeRAG handles RAG tool requests.
func (r *LocalExecutor) executeRAG(ctx context.Context, req Request) (Result, error) {
	if r.ragTool == nil {
		return Result{}, fmt.Errorf("RAG tool not configured")
	}

	return r.ragTool.Execute(ctx, req)
}
