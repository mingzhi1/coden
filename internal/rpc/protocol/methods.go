package protocol

// RPC method names.
const (
	MethodPing               = "ping"
	MethodSessionCreate      = "session.create"
	MethodSessionList        = "session.list"
	MethodSessionAttach      = "session.attach"
	MethodSessionDetach      = "session.detach"
	MethodSessionSnapshot    = "session.snapshot"
	MethodSessionRename      = "session.rename"
	MethodWorkflowSubmit     = "workflow.submit"
	MethodWorkflowCancel     = "workflow.cancel"
	MethodWorkflowGet        = "workflow.get"
	MethodWorkflowList       = "workflow.list"
	MethodWorkflowObjects    = "workflow.objects"
	MethodWorkflowObjectRead = "workflow.object.read"
	MethodEventSubscribe     = "event.subscribe"
	MethodEventUnsubscribe   = "event.unsubscribe"
	MethodEventPush          = "event.push"
	MethodMessageList        = "message.list"
	MethodCheckpointGet      = "checkpoint.get"
	MethodCheckpointList     = "checkpoint.list"
	MethodIntentGet          = "intent.get"
	MethodWorkspaceChanges   = "workspace.changes"
	MethodWorkspaceRead      = "workspace.read"
	MethodWorkspaceWrite     = "workspace.write"
	MethodWorkspaceDiff      = "workspace.diff"
	MethodWorkerDescribe     = "worker.describe"
	MethodWorkerExecute      = "worker.execute"
	MethodWorkerCancel       = "worker.cancel"
	MethodToolDescribe       = "tool.describe"
	MethodToolExec           = "tool.exec"
	MethodToolCancel         = "tool.cancel"
	// R-06: dedicated live-workers query endpoint.
	MethodWorkflowWorkers = "workflow.workers"
	// M11-05: task management commands from TUI.
	MethodTaskSkip = "task.skip"
	MethodTaskUndo = "task.undo"
	// LLM server sidecar methods (Kernel → llm-server).
	MethodLLMChat      = "llm/chat"
	MethodLLMSideQuery = "llm/sidequery"
)

// MethodCategory groups protocol methods by responsibility boundary.
type MethodCategory string

const (
	CategoryUnknown    MethodCategory = "unknown"
	CategorySystem     MethodCategory = "system"
	CategorySession    MethodCategory = "session"
	CategoryWorkflow   MethodCategory = "workflow"
	CategoryEvent      MethodCategory = "event"
	CategoryMessage    MethodCategory = "message"
	CategoryCheckpoint MethodCategory = "checkpoint"
	CategoryIntent     MethodCategory = "intent"
	CategoryWorkspace  MethodCategory = "workspace"
	CategoryWorker     MethodCategory = "worker"
	CategoryTool       MethodCategory = "tool"
	CategoryTask       MethodCategory = "task"
	CategoryLLM        MethodCategory = "llm"
)

// MethodPlane describes which RPC boundary a method belongs to.
type MethodPlane string

const (
	PlaneUnknown        MethodPlane = "unknown"
	PlaneShared         MethodPlane = "shared"
	PlaneClientToKernel MethodPlane = "client_to_kernel"
	PlaneKernelToWorker MethodPlane = "kernel_to_worker"
	PlaneKernelToTool   MethodPlane = "kernel_to_tool"
	PlaneKernelToLLM    MethodPlane = "kernel_to_llm"
	PlaneServerPush     MethodPlane = "server_push"
)

// CategoryOf reports the broad responsibility category for a method name.
func CategoryOf(method string) MethodCategory {
	switch method {
	case MethodPing:
		return CategorySystem
	case MethodSessionCreate, MethodSessionList, MethodSessionAttach, MethodSessionDetach, MethodSessionSnapshot, MethodSessionRename:
		return CategorySession
	case MethodWorkflowSubmit, MethodWorkflowCancel, MethodWorkflowGet, MethodWorkflowList,
		MethodWorkflowObjects, MethodWorkflowObjectRead, MethodWorkflowWorkers:
		return CategoryWorkflow
	case MethodEventSubscribe, MethodEventUnsubscribe, MethodEventPush:
		return CategoryEvent
	case MethodMessageList:
		return CategoryMessage
	case MethodCheckpointGet, MethodCheckpointList:
		return CategoryCheckpoint
	case MethodIntentGet:
		return CategoryIntent
	case MethodWorkspaceChanges, MethodWorkspaceRead, MethodWorkspaceWrite, MethodWorkspaceDiff:
		return CategoryWorkspace
	case MethodWorkerDescribe, MethodWorkerExecute, MethodWorkerCancel:
		return CategoryWorker
	case MethodToolDescribe, MethodToolExec, MethodToolCancel:
		return CategoryTool
	case MethodTaskSkip, MethodTaskUndo:
		return CategoryTask
	case MethodLLMChat, MethodLLMSideQuery:
		return CategoryLLM
	default:
		return CategoryUnknown
	}
}

// PlaneOf reports which RPC boundary owns the method.
func PlaneOf(method string) MethodPlane {
	switch method {
	case MethodPing:
		return PlaneShared
	case MethodSessionAttach,
		MethodSessionCreate,
		MethodSessionList,
		MethodSessionDetach,
		MethodSessionSnapshot,
		MethodSessionRename,
		MethodWorkflowSubmit,
		MethodWorkflowCancel,
		MethodWorkflowGet,
		MethodWorkflowList,
		MethodWorkflowObjects,
		MethodWorkflowObjectRead,
		MethodEventSubscribe,
		MethodEventUnsubscribe,
		MethodMessageList,
		MethodCheckpointGet,
		MethodCheckpointList,
		MethodIntentGet,
		MethodWorkspaceChanges,
		MethodWorkspaceRead,
		MethodWorkspaceWrite,
		MethodWorkspaceDiff,
		MethodWorkflowWorkers:
		return PlaneClientToKernel
	case MethodTaskSkip, MethodTaskUndo:
		return PlaneClientToKernel
	case MethodWorkerDescribe, MethodWorkerExecute, MethodWorkerCancel:
		return PlaneKernelToWorker
	case MethodToolDescribe, MethodToolExec, MethodToolCancel:
		return PlaneKernelToTool
	case MethodLLMChat, MethodLLMSideQuery:
		return PlaneKernelToLLM
	case MethodEventPush:
		return PlaneServerPush
	default:
		return PlaneUnknown
	}
}

func SupportsKernelServer(method string) bool {
	switch PlaneOf(method) {
	case PlaneShared, PlaneClientToKernel:
		return true
	default:
		return false
	}
}

func SupportsWorkerServer(method string) bool {
	switch PlaneOf(method) {
	case PlaneShared, PlaneKernelToWorker:
		return true
	default:
		return false
	}
}

func SupportsToolServer(method string) bool {
	switch PlaneOf(method) {
	case PlaneShared, PlaneKernelToTool:
		return true
	default:
		return false
	}
}

func SupportsLLMServer(method string) bool {
	switch PlaneOf(method) {
	case PlaneShared, PlaneKernelToLLM:
		return true
	default:
		return false
	}
}
