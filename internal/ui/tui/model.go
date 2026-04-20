package tui

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/ui/plain"
	"github.com/mingzhi1/coden/internal/ui/styles"
)

type EventMsg struct {
	SessionID string
	Event     model.Event
}

type CheckpointMsg struct {
	Result model.CheckpointResult
}

type WorkflowObjectsLoadedMsg struct {
	WorkflowID string
	Items      []api.ObjectDetail
}

type SessionSnapshotLoadedMsg struct {
	Snapshot api.SessionSnapshot
}

type ErrMsg struct {
	Err error
}

type StreamClosedMsg struct{}

type WorkflowCancelRequestedMsg struct {
	WorkflowID string
}

type WorkflowCancelFailedMsg struct {
	WorkflowID string
	Err        error
}

type WorkspaceReadLoadedMsg struct {
	Path    string
	Content string
	Err     error
}

// WorkflowAcceptedMsg is sent when Submit() returns immediately with a workflowID.
// The actual result arrives later via checkpoint.updated event.
type WorkflowAcceptedMsg struct {
	WorkflowID string
}

// M11-05: Result messages for task management slash commands.
type TaskSkipResultMsg struct {
	TaskID string
	Err    error
}

type TaskUndoResultMsg struct {
	RestoredTaskID string
	Err            error
}

type SessionRenameResultMsg struct {
	Name string
	Err  error
}

// Overlay-initiated session commands — bubbled from Model to AppModel.
type OverlayRequestNewSessionMsg struct{}
type OverlayRequestCloseSessionMsg struct{}

type Submitter func(prompt string) tea.Cmd

type startSubmitMsg struct {
	Prompt string
}

type WorkflowObjectsLoader func(workflowID string) tea.Cmd

type SessionSnapshotLoader func() tea.Cmd

type WorkflowCanceler func(workflowID string) tea.Cmd

type CheckpointLoader func(workflowID string) tea.Cmd

type WorkspaceReader func(path string) tea.Cmd

// M11-05: Task management callbacks.
type TaskSkipper func(taskID string) tea.Cmd
type TaskUndoer func() tea.Cmd

// SessionRenamer renames the session (wired to client.RenameSession).
type SessionRenamer func(name string) tea.Cmd

type RuntimeInfo struct {
	Model           string
	Provider        string
	LightModel      string // light pool model (for inputter/planner)
	PoolSummary     string // human-readable pool description
	Mode            string
	AllowShellKnown bool
	AllowShell      bool
	ConfigSource    string
}

type alertState struct {
	level  string
	title  string
	lines  []string
	items  []overlayItem
	footer string
	cursor int
}

type overlayItem struct {
	kind   string
	text   string
	action string
}

type Model struct {
	sessionID           string
	status              string
	width               int
	height              int
	chatScroll          int
	changedSel          int
	changedDetailScroll int
	followChat          bool
	focus               panelFocus

	chatLines           []string
	chatTabActive       chatTab
	turns               []turnEntry
	turnSel             int
	turnExpanded        bool
	turnDetailScroll    int
	checkpoint          *model.CheckpointResult
	err                 error
	lastSubmittedPrompt string
	activeWorkflowID    string
	lastToolChange      workspaceEchoSuppression
	latestRun           *model.WorkflowRun
	latestIntent        *model.IntentSpec
	snapshotLoaded      bool
	runtimeInfo         RuntimeInfo
	alert               *alertState
	workers             []workerItem
	todos               []todoItem
	changed             []changeItem

	eventStream      <-chan model.Event
	submitter        Submitter
	objectsLoader    WorkflowObjectsLoader
	snapshotLoader   SessionSnapshotLoader
	canceler         WorkflowCanceler
	checkpointLoader CheckpointLoader
	workspaceReader  WorkspaceReader

	// M11-05: task management callbacks
	taskSkipper    TaskSkipper
	taskUndoer     TaskUndoer
	sessionRenamer SessionRenamer

	// Prompt history for up/down arrow recall
	promptHistory    []string
	promptHistoryIdx int // -1 = editing new, 0..N = browsing history
	promptDraft      string // draft text before history browsing started

	plain *plain.Adapter

	spinner       spinner.Model
	spinnerActive bool
	ti            textarea.Model

	// pendingCheckpointWorkflowID stores the workflowID from a
	// checkpoint.updated event that arrived before WorkflowAcceptedMsg set
	// m.activeWorkflowID.  When WorkflowAcceptedMsg subsequently arrives with a
	// matching ID we immediately fire checkpointLoader to close the gap.
	pendingCheckpointWorkflowID string

	// currentStep tracks the latest workflow step from EventWorkflowStepUpdate.
	// Cleared on submit and workflow termination. Used by the chat spinner line.
	currentStep string

	// workflowStartedAt records when the current workflow started, for elapsed display.
	workflowStartedAt time.Time

	// Claude Code-like tool tracking.
	activeToolName string // name of tool currently executing (empty when idle)
	toolCallCount  int    // total tool calls in current workflow
}

type panelFocus string

const (
	focusChat    panelFocus = "chat"
	focusInput   panelFocus = "input"
	focusTodo    panelFocus = "todo"
	focusChanged panelFocus = "changed"
)

type chatTab int

const (
	tabChat chatTab = iota
	tabHistory
)

const (
	minWidth     = 100
	minHeight    = 24
	maxChatLines = 5000
)

type todoItem struct {
	ID     string
	Name   string
	Status string
}

type workerItem struct {
	ID          string
	Role        string
	Step        string
	Status      string
	DurationMS  int64
	HasDuration bool
	ToolCallID  string
}

type changeItem struct {
	Path        string
	Name        string
	Status      string
	Summary     string
	Detail      string
	ExitCode    int
	Count       int
	Tool        string
	ToolCallID  string
	DurationMS  int64
	HasDuration bool
	Preview     string
}

type workspaceEchoSuppression struct {
	WorkflowID string
	Path       string
}

// turnEntry represents one user interaction turn (prompt → response).
type turnEntry struct {
	Prompt       string // user prompt (truncated for list display)
	Response     string // assistant response (truncated for list display)
	Status       string // "pass", "fail", "running", ""
	WorkflowID   string
	FileCount    int      // number of files changed
	ChangedFiles []string // paths of changed files
	FullPrompt   string   // full text for detail view
	FullResponse string   // full text for detail view
}

func NewModel(sessionID, prompt string) *Model {
	spin := spinner.New(
		spinner.WithSpinner(spinner.Line),
		spinner.WithStyle(styles.PrimaryText),
	)
	ti := textarea.New()
	ti.Placeholder = "What would you like me to do?"
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "input> "
	ti.ShowLineNumbers = false
	if prompt != "" {
		ti.SetValue(prompt)
	}

	return &Model{
		sessionID:  sessionID,
		status:     "idle",
		followChat: true,
		focus:      focusInput,
		plain:      plain.New(),
		spinner:    spin,
		ti:         ti,
	}
}

func (m *Model) WithEventStream(events <-chan model.Event) *Model {
	m.eventStream = events
	return m
}

func (m *Model) WithSubmitter(submitter Submitter) *Model {
	m.submitter = submitter
	return m
}

func (m *Model) WithWorkflowObjectsLoader(loader WorkflowObjectsLoader) *Model {
	m.objectsLoader = loader
	return m
}

func (m *Model) WithSessionSnapshotLoader(loader SessionSnapshotLoader) *Model {
	m.snapshotLoader = loader
	return m
}

func (m *Model) WithCanceler(canceler WorkflowCanceler) *Model {
	m.canceler = canceler
	return m
}

func (m *Model) WithCheckpointLoader(loader CheckpointLoader) *Model {
	m.checkpointLoader = loader
	return m
}

func (m *Model) WithWorkspaceReader(reader WorkspaceReader) *Model {
	m.workspaceReader = reader
	return m
}

func (m *Model) WithRuntimeInfo(info RuntimeInfo) *Model {
	m.runtimeInfo = info
	return m
}

func (m *Model) WithTaskSkipper(skipper TaskSkipper) *Model {
	m.taskSkipper = skipper
	return m
}

func (m *Model) WithTaskUndoer(undoer TaskUndoer) *Model {
	m.taskUndoer = undoer
	return m
}

func (m *Model) WithSessionRenamer(renamer SessionRenamer) *Model {
	m.sessionRenamer = renamer
	return m
}
