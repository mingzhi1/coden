package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	EventSessionAttached      = "session.attached"
	EventSessionDetached      = "session.detached"
	EventSessionCreated       = "session.created"
	EventMessageCreated       = "message.created"
	EventWorkflowStarted      = "workflow.started"
	EventWorkflowCanceled     = "workflow.canceled"
	EventWorkflowFailed       = "workflow.failed"
	EventWorkflowRetry        = "workflow.retry"
	EventWorkflowStepUpdate   = "workflow.step_updated"
	EventWorkflowTasks        = "workflow.tasks_updated"
	EventWorkerStarted        = "worker.started"
	EventWorkerFinished       = "worker.finished"
	EventWorkerMessage        = "worker.message"
	EventToolStarted          = "tool.started"
	EventToolFinished         = "tool.finished"
	EventCheckpointUpdated    = "checkpoint.updated"
	EventWorkspaceChanged     = "workspace.changed"
	EventWorkflowTaskAppended = "workflow.task_appended" // M11: task dynamically added to queue
	EventWorkflowTaskSkipped  = "workflow.task_skipped"  // M11: task skipped by user or agent
	EventSearchStarted        = "search.started"         // SA-09: discovery search phase started
	EventSearchFinished       = "search.finished"        // SA-09: discovery search phase finished
	EventSearchRefined        = "search.refined"         // SA-09: discovery refinement pass completed
)

var ErrEmptyEventPayload = errors.New("empty event payload")

type Event struct {
	Seq       uint64
	SessionID string
	Topic     string
	Timestamp time.Time
	Payload   json.RawMessage
}

type SessionAttachedPayload struct {
	ClientName string `json:"client_name"`
	View       string `json:"view"`
}

type SessionDetachedPayload struct {
	ClientName string `json:"client_name"`
}

type SessionCreatedPayload struct {
	SessionID   string `json:"session_id"`
	ProjectID   string `json:"project_id,omitempty"`
	ProjectRoot string `json:"project_root,omitempty"`
}

type MessageCreatedPayload struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
}

type WorkflowStartedPayload struct {
	WorkflowID string `json:"workflow_id"`
}

type WorkflowCanceledPayload struct {
	WorkflowID string `json:"workflow_id"`
	Reason     string `json:"reason,omitempty"`
}

type WorkflowFailedPayload struct {
	WorkflowID string `json:"workflow_id"`
	Reason     string `json:"reason,omitempty"`
	Error      string `json:"error,omitempty"`
}

type WorkflowRetryPayload struct {
	WorkflowID string   `json:"workflow_id"`
	Attempt    int      `json:"attempt"`
	MaxRetries int      `json:"max_retries"`
	Reason     string   `json:"reason,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

type WorkflowStepUpdatedPayload struct {
	WorkflowID string `json:"workflow_id"`
	Step       string `json:"step"`
	Status     string `json:"status"`
	TaskCount  int    `json:"task_count,omitempty"`
}

type WorkflowTasksUpdatedPayload struct {
	WorkflowID string `json:"workflow_id"`
	Tasks      []Task `json:"tasks"`
}

type WorkerStartedPayload struct {
	WorkflowID string `json:"workflow_id"`
	WorkerID   string `json:"worker_id"`
	WorkerRole string `json:"worker_role"`
	Step       string `json:"step"`
}

type WorkerFinishedPayload struct {
	WorkflowID string `json:"workflow_id"`
	WorkerID   string `json:"worker_id"`
	WorkerRole string `json:"worker_role"`
	Step       string `json:"step"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type WorkerMessagePayload struct {
	WorkflowID  string `json:"workflow_id"`
	WorkerID    string `json:"worker_id"`
	WorkerRole  string `json:"worker_role"`
	Step        string `json:"step"`
	Kind        string `json:"kind,omitempty"`
	MessageRole string `json:"message_role,omitempty"`
	Content     string `json:"content"`
}

type ToolStartedPayload struct {
	WorkflowID string `json:"workflow_id"`
	ToolCallID string `json:"tool_call_id"`
	WorkerID   string `json:"worker_id"`
	Tool       string `json:"tool"`
	Path       string `json:"path,omitempty"`
}

type ToolFinishedPayload struct {
	WorkflowID string `json:"workflow_id"`
	ToolCallID string `json:"tool_call_id"`
	WorkerID   string `json:"worker_id"`
	Tool       string `json:"tool"`
	Path       string `json:"path,omitempty"`
	Status     string `json:"status,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Detail     string `json:"detail,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type CheckpointUpdatedPayload struct {
	WorkflowID string   `json:"workflow_id"`
	Status     string   `json:"status"`
	Evidence   []string `json:"evidence,omitempty"`
}

type WorkspaceChangedPayload struct {
	WorkflowID string `json:"workflow_id"`
	Path       string `json:"path"`
	Operation  string `json:"operation"`
}

// M11: TaskAppendedPayload is emitted when a task is dynamically added to the queue.
type TaskAppendedPayload struct {
	WorkflowID string `json:"workflow_id"`
	TaskID     string `json:"task_id"`
	TaskTitle  string `json:"task_title"`
	Source     string `json:"source"` // "planner" | "coder" | "user" | "agent"
}

// M11: TaskSkippedPayload is emitted when a task is skipped by user or agent.
type TaskSkippedPayload struct {
	WorkflowID string `json:"workflow_id"`
	TaskID     string `json:"task_id"`
	Source     string `json:"source"`
}

// SA-09: SearchStartedPayload is emitted when the discovery search phase begins.
type SearchStartedPayload struct {
	WorkflowID string `json:"workflow_id"`
	Query      string `json:"query"`
	QueryID    string `json:"query_id"`
}

// SA-09: SearchFinishedPayload is emitted when the discovery search phase ends.
type SearchFinishedPayload struct {
	WorkflowID   string   `json:"workflow_id"`
	QueryID      string   `json:"query_id"`
	SnippetCount int      `json:"snippet_count"`
	EvidenceCount int     `json:"evidence_count"`
	Confidence   float64  `json:"confidence"`
	Layers       []string `json:"layers"`   // which retrieval layers were used: grep/lsp/rag
	DurationMs   int64    `json:"duration_ms"`
}

// SA-09: SearchRefinedPayload is emitted after a discovery refinement pass.
type SearchRefinedPayload struct {
	WorkflowID    string  `json:"workflow_id"`
	QueryID       string  `json:"query_id"`
	SnippetsBefore int    `json:"snippets_before"`
	SnippetsAfter  int    `json:"snippets_after"`
	DurationMs     int64  `json:"duration_ms"`
}

func EncodePayload(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	buf, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("encode event payload: %v", err))
	}
	return json.RawMessage(buf)
}

func (e Event) DecodePayload(dst any) error {
	if len(e.Payload) == 0 {
		return ErrEmptyEventPayload
	}
	return json.Unmarshal(e.Payload, dst)
}

func DecodePayload[T any](e Event) (T, error) {
	var out T
	if err := e.DecodePayload(&out); err != nil {
		return out, err
	}
	return out, nil
}

type WorkerMessage struct {
	Kind    string
	Role    string
	Content string
}

type Message struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	CreatedAt time.Time
}

type Session struct {
	ID          string
	ProjectID   string
	ProjectRoot string
	Name        string // human-readable label; empty until renamed (R-07)
	CreatedAt   time.Time
}

type Workspace struct {
	ID        string
	Root      string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IntentKind classifies the user's request to enable pipeline routing.
const (
	IntentKindCodeGen  = "code_gen" // generate or modify code
	IntentKindDebug    = "debug"    // fix a bug or error
	IntentKindRefactor = "refactor" // restructure existing code
	IntentKindQuestion = "question" // ask a question (no code changes)
	IntentKindConfig   = "config"   // configuration or setup task
	IntentKindChat     = "chat"     // conversational discussion, explanation
	IntentKindAnalyze  = "analyze"  // code analysis, review, understanding
	IntentKindOther    = "other"    // fallback for unclassifiable requests
)

type IntentSpec struct {
	ID              string
	SessionID       string
	Goal            string
	SuccessCriteria []string
	Kind            string // IntentKind* constant; empty defaults to code_gen
	CreatedAt       time.Time
}

// IsQuestion returns true if this intent should skip the Plan/Code/Accept pipeline.
// Note: "analyze" is NOT included here — it needs tool access (read_file, search)
// to examine logs, code, and workspace files before producing a diagnosis.
func (i IntentSpec) IsQuestion() bool {
	return i.Kind == IntentKindQuestion || i.Kind == IntentKindChat || i.Kind == IntentKindOther
}

// IsOther returns true if this intent is unclassifiable (greetings, meta-requests, ambiguous).
func (i IntentSpec) IsOther() bool {
	return i.Kind == IntentKindOther
}

// IsChat returns true if this intent is a conversational discussion.
func (i IntentSpec) IsChat() bool {
	return i.Kind == IntentKindChat
}

// IsAnalyze returns true if this intent is a code analysis or review request.
func (i IntentSpec) IsAnalyze() bool {
	return i.Kind == IntentKindAnalyze
}

// IsDebug returns true if this intent is a bug fix request.
func (i IntentSpec) IsDebug() bool {
	return i.Kind == IntentKindDebug
}

// IsRefactor returns true if this intent is a refactoring request.
func (i IntentSpec) IsRefactor() bool {
	return i.Kind == IntentKindRefactor
}

// IsCodeGen returns true if this intent involves code generation or modification.
func (i IntentSpec) IsCodeGen() bool {
	return i.Kind == IntentKindCodeGen
}

// IsConfig returns true if this intent is a configuration task.
func (i IntentSpec) IsConfig() bool {
	return i.Kind == IntentKindConfig
}

// Task status constants used by the Kernel state machine.
const (
	TaskStatusPlanned   = "planned"
	TaskStatusCoding    = "coding"
	TaskStatusAccepting = "accepting"
	TaskStatusPassed    = "passed"
	TaskStatusFailed    = "failed"
	TaskStatusRetrying  = "retrying"
	TaskStatusAbandoned = "abandoned"
	TaskStatusSkipped   = "skipped" // M11: user or agent chose to skip this task
	TaskStatusRemoved   = "removed" // M11: removed from queue (undo-able)
)

type Task struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Status  string    `json:"status"`
	Created time.Time `json:"created,omitempty"`

	// Files declares the set of workspace-relative paths this task is allowed
	// to write. Kernel enforces this at runtime (task scope guard). Empty means
	// unrestricted (backward-compatible).
	Files []string `json:"files,omitempty"`

	// DependsOn holds IDs of tasks that must complete before this one starts.
	// Used for DAG cycle detection and topological ordering.
	DependsOn []string `json:"depends_on,omitempty"`

	// SuccessCmd is an optional shell command whose exit-0 is a deterministic
	// acceptance criterion (e.g. "go build ./..." or "go test ./pkg/...").
	// Acceptor runs this before the LLM judgment step.
	SuccessCmd string `json:"success_cmd,omitempty"`

	// Steps holds refined implementation instructions produced by the Replanner.
	// Each step is a concrete, actionable instruction (max 120 chars).
	// Kept separate from Title to avoid corrupting the task's display name.
	Steps []string `json:"steps,omitempty"`

	// Attempts records the number of Code→Accept cycles executed for this task.
	// Set by the kernel at runtime; not part of the planner output.
	Attempts int `json:"attempts,omitempty"`
}

type Artifact struct {
	Path    string
	Summary string
}

type CheckpointResult struct {
	WorkflowID    string
	SessionID     string
	Status        string
	ArtifactPaths []string
	Evidence      []string
	FixGuidance   string // non-empty on fail: actionable instructions for the coder retry
	CreatedAt     time.Time
}

// FileChangeOp describes the kind of file modification within a turn.
const (
	FileChangeCreated  = "created"
	FileChangeModified = "modified"
	FileChangeDeleted  = "deleted"
)

// FileChange records a single file modification that occurred during a turn.
type FileChange struct {
	Path         string `json:"path"`
	Op           string `json:"op"` // FileChangeCreated | FileChangeModified | FileChangeDeleted
	LinesAdded   int    `json:"lines_added,omitempty"`
	LinesRemoved int    `json:"lines_removed,omitempty"`
}

// TaskResult captures the terminal state of one task within a turn.
type TaskResult struct {
	TaskID       string   `json:"task_id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"` // passed | failed | abandoned
	Attempts     int      `json:"attempts"`
	FilesWritten []string `json:"files_written,omitempty"`
}

// TurnSummary is a zero-LLM-cost structured snapshot auto-generated by the
// Kernel at the end of every workflow run. It captures what was done (intent,
// task results, file changes) so that future turns can receive structured
// execution history without re-reading raw messages.
type TurnSummary struct {
	ID           string           `json:"id"`
	TurnID       string           `json:"turn_id"`
	SessionID    string           `json:"session_id"`
	Intent       IntentSpec       `json:"intent"`
	TaskResults  []TaskResult     `json:"task_results"`
	ChangedFiles []FileChange     `json:"changed_files"`
	Checkpoint   CheckpointResult `json:"checkpoint"`
	CreatedAt    time.Time        `json:"created_at"`
}

// FixGuidanceEntry records one round of acceptor rejection with its fix guidance.
// Used by RetryContext to build a chain of guidance across retries (L3-06).
type FixGuidanceEntry struct {
	Attempt     int    `json:"attempt"`
	Evidence    string `json:"evidence"`     // joined Evidence lines
	FixGuidance string `json:"fix_guidance"` // from CheckpointResult.FixGuidance
}

// RetryContext accumulates the history of acceptor rejections for one task.
// It grows by one entry per retry and is used by buildRetryFeedback to:
//  1. Show the full rejection chain to the LLM (avoids amnesia).
//  2. Detect oscillation: if the last two evidence strings are identical the
//     LLM is cycling and buildRetryFeedback emits an explicit anti-loop hint.
type RetryContext struct {
	GuidanceHistory []FixGuidanceEntry
}

// AppendGuidance adds a new entry and returns whether oscillation is detected
// (last two evidence strings are identical, indicating LLM is stuck in a loop).
func (rc *RetryContext) AppendGuidance(attempt int, cp CheckpointResult) (oscillating bool) {
	entry := FixGuidanceEntry{
		Attempt:     attempt,
		Evidence:    strings.Join(cp.Evidence, "; "),
		FixGuidance: cp.FixGuidance,
	}
	rc.GuidanceHistory = append(rc.GuidanceHistory, entry)
	n := len(rc.GuidanceHistory)
	if n >= 2 &&
		rc.GuidanceHistory[n-1].Evidence != "" &&
		rc.GuidanceHistory[n-1].Evidence == rc.GuidanceHistory[n-2].Evidence {
		return true
	}
	return false
}

type Turn struct {
	ID         string
	SessionID  string
	WorkflowID string
	Prompt     string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WorkflowRun records one workflow execution within a session.
// The MVP implementation persists it in the turn store, so Turn is the
// current storage shape for workflow-run state.
type WorkflowRun = Turn

// WorkerState is a live snapshot of a single worker's execution status within
// an active workflow. Only populated while the workflow is running.
type WorkerState struct {
	WorkerID  string `json:"worker_id"`
	Role      string `json:"role"`
	Step      string `json:"step"`
	Status    string `json:"status"`             // "running" | "done" | "failed"
	StartedAt int64  `json:"started_at"`         // Unix milliseconds
	EndedAt   int64  `json:"ended_at,omitempty"` // Unix milliseconds; 0 while running
}

// SessionSnapshot is an atomic point-in-time view of a session returned by
// Kernel.Snapshot. All fields are read within a single lock acquisition to
// eliminate the race window that exists when callers make multiple sequential
// RPCs (message.list → checkpoint.get → workflow.get …).
//
// LastEventSeq is the event-bus sequence number at the moment the snapshot was
// taken. Pair it with R-01 since_seq replay: subscribe with
// since_seq=LastEventSeq to receive all events that occurred after the
// snapshot with zero gaps and zero duplicates.
type SessionSnapshot struct {
	SessionID        string
	Messages         []Message
	ActiveWorkflow   *WorkflowRun
	LatestCheckpoint *CheckpointResult
	LatestIntent     *IntentSpec
	WorkspaceChanges []WorkspaceChangedPayload
	LastEventSeq     uint64
}

type Object struct {
	ID           string
	TurnID       string
	Kind         string
	Sequence     int
	FilePath     string
	PrevObjectID string
	StoragePath  string
	ContentHash  string
	CreatedAt    time.Time
}

// WorkflowContext carries ambient data injected by the Kernel into the
// context.Context before each worker call. Workers may extract it via
// WorkflowContextFrom to enrich their LLM prompts.
type WorkflowContext struct {
	// History is the last N messages from the session (oldest first).
	History []Message
	// FileTree is a list of workspace-relative file paths (capped at 200).
	FileTree []string
	// WorkspaceRoot is the absolute path to the workspace directory.
	WorkspaceRoot string
	// RetryFeedback is non-empty when the coder is being retried after
	// an acceptor rejection. It contains the rejection evidence.
	RetryFeedback string
	// PreviousTurns holds the last 5 TurnSummary records for this session
	// (oldest first). Populated by the Kernel at workflow start (M8-08).
	PreviousTurns []TurnSummary
	// AccumChanges holds the deduplicated set of file changes across all
	// turns in this session. Useful for cross-turn context.
	AccumChanges []FileChange
	// TopInsights holds the top-K insights for this session, pre-formatted as
	// a string for direct injection into the context summary (M8-11).
	// The kernel formats them to avoid a model→insight circular import.
	TopInsights string
	// GitStatus holds a pre-formatted markdown section summarising the
	// workspace's git state (branch, uncommitted changes, diff stat, recent
	// commits). Empty when the workspace is not a git repository (M8-04).
	GitStatus string
	// DiscoveryContext holds pre-fetched file snippets and search results
	// gathered after planning but before coding. This gives the Coder
	// real code understanding from the start (M10-04).
	DiscoveryContext []FileSnippet
	// Discovery stores the structured search result for the current workflow.
	// This is the forward-compatible form of DiscoveryContext.
	Discovery DiscoveryContext
	// DirtyPaths are workspace-relative files modified since the last checkpoint.
	// Search and prompt builders can use this to surface possibly stale evidence.
	DirtyPaths []string

	// SecretaryContext holds the pre-formatted skill/extension content
	// assembled by the Secretary Agent for the current Worker target.
	// Set per-worker-call (not once for the whole workflow).
	SecretaryContext string

	// MCPToolDescriptions holds pre-formatted MCP tool definitions
	// for inclusion in the Coder's system prompt. Only set for TargetCoder.
	MCPToolDescriptions string

	// ToolsPrompt holds the dynamically generated "Available tools" section
	// for the Coder's system prompt, replacing the hardcoded tool list.
	// Generated by inventory.FormatToolsPrompt(). Empty means use hardcoded default.
	ToolsPrompt string

	// EnvironmentPrompt holds information about detected environment tools
	// (interpreters, package managers, formatters, linters) that the LLM
	// can use via run_shell. Generated by inventory.FormatEnvironmentPrompt().
	EnvironmentPrompt string

	// CritiqueIssues holds issues and suggestions from the Critic step.
	// Non-empty when the Critic ran and found problems; injected into the
	// Replanner prompt so the refined plan addresses the critique.
	CritiqueIssues []string
}

// FileSnippet holds a pre-fetched excerpt of a workspace file.
type FileSnippet struct {
	Path    string `json:"path"`
	Content string `json:"content"` // first N lines or full content if small
	Exists  bool   `json:"exists"`
	Lines   int    `json:"lines"` // total line count; 0 if !Exists
}

// DiscoveryEvidence is a lightweight, model-local representation of a search
// hit used inside WorkflowContext to avoid cross-package coupling.
type DiscoveryEvidence struct {
	Source      string  `json:"source"`
	Path        string  `json:"path"`
	Line        int     `json:"line"`
	Column      int     `json:"column"`
	Symbol      string  `json:"symbol"`
	Snippet     string  `json:"snippet"`
	Score       float64 `json:"score"`
	Stale       bool    `json:"stale"`
	Verified    bool    `json:"verified"`
	Explanation string  `json:"explanation"`
}

// CritiqueResult is the output of the Critic worker reviewing a proposed plan.
type CritiqueResult struct {
	// Score in [0, 1]: 1 = perfect plan, 0 = fundamentally broken.
	Score float64 `json:"score"`
	// Approved is true when the plan is good enough to proceed without changes.
	Approved bool `json:"approved"`
	// Issues lists specific problems found in the plan (missing steps, risks, etc.).
	Issues []string `json:"issues,omitempty"`
	// Suggestions lists concrete improvements for the Replanner to act on.
	Suggestions []string `json:"suggestions,omitempty"`
	// Summary is a brief human-readable critique for logging and TUI display.
	Summary string `json:"summary,omitempty"`
}

// SymbolInfo holds structured information about a code symbol (function, type,
// variable, etc.) extracted from LSP or grep-based symbol analysis.
type SymbolInfo struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`       // "func" | "type" | "var" | "const" | "method" | "field"
	Path      string   `json:"path"`       // file path where the symbol is defined
	Line      int      `json:"line"`       // 1-based line number of the definition
	Signature string   `json:"signature"`  // e.g. "func Foo(x int) error"
	Package   string   `json:"package"`    // Go package name or equivalent
	Exported  bool     `json:"exported"`   // whether the symbol is exported/public
	Refs      []string `json:"refs,omitempty"` // paths of files that reference this symbol
}

// DiscoveryContext stores the structured result of the Discovery/Search phase.
// Snippets are kept for prompt injection compatibility; Evidence carries the
// richer source-aware retrieval metadata for future Search Agent integration.
// Symbols carries structured symbol information extracted from LSP analysis.
type DiscoveryContext struct {
	Query      string              `json:"query"`
	QueryID    string              `json:"query_id"`
	Evidence   []DiscoveryEvidence `json:"evidence"`
	Snippets   []FileSnippet       `json:"snippets"`
	Symbols    []SymbolInfo        `json:"symbols,omitempty"` // G5: structured symbol info from LSP
	Confidence float64             `json:"confidence"`
}

// ctxKey is an unexported type for context keys to avoid collisions.
type ctxKey struct{}

// WorkflowContextKey is the context key used to store WorkflowContext.
var WorkflowContextKey = ctxKey{}

// WithWorkflowContext returns a new context carrying wc.
func WithWorkflowContext(ctx context.Context, wc WorkflowContext) context.Context {
	return context.WithValue(ctx, WorkflowContextKey, wc)
}

// WorkflowContextFrom extracts the WorkflowContext from ctx.
// Returns a zero-value WorkflowContext if none was set.
func WorkflowContextFrom(ctx context.Context) WorkflowContext {
	wc, _ := ctx.Value(WorkflowContextKey).(WorkflowContext)
	return wc
}
