package kernel

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mingzhi1/coden/internal/core/artifact"
	"github.com/mingzhi1/coden/internal/hook"
	"github.com/mingzhi1/coden/internal/core/checkpoint"
	"github.com/mingzhi1/coden/internal/core/events"
	"github.com/mingzhi1/coden/internal/core/gitstate"
	"github.com/mingzhi1/coden/internal/core/insight"
	"github.com/mingzhi1/coden/internal/core/intent"
	"github.com/mingzhi1/coden/internal/core/message"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/objectstore"
	"github.com/mingzhi1/coden/internal/core/session"
	"github.com/mingzhi1/coden/internal/core/taskqueue"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/turn"
	"github.com/mingzhi1/coden/internal/core/turnsummary"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/secretary"
)

// Kernel 是核心编排引擎，管理会话、工作流执行、存储和事件。
type Kernel struct {
	mu sync.Mutex
	// sessionMus holds per-session mutexes so concurrent sessions don't block
	// each other. Access is guarded by mu.
	sessionMus             map[string]*sync.Mutex
	sessions               map[string]map[string]string
	activeWorkflows        map[string]*activeWorkflow
	activeSessionWorkflows map[string]string // sessionID → workflowID; guarded by mu
	workflowGeneration     map[string]uint64 // sessionID → monotonic generation counter; guarded by mu
	workflowWG             sync.WaitGroup
	workspaceChanges       map[string][]model.WorkspaceChangedPayload
	events                 *events.Bus
	sessionStore           session.Store
	intents                intent.Store
	messages               message.Store
	checkpoints            checkpoint.Store
	turns                  turn.Store
	turnSummaries          turnsummary.Store
	objects                objectstore.Store
	insights               insight.Store // M8-11: per-session analysis insight accumulation
	mainDBPath             string
	workspace              *workspace.Service
	tools                  *toolruntime.Runtime
	git                    *gitstate.Service // M8-04: git state provider
	workflow               *workflow.Engine
	allowShell             bool
	secretary              *secretary.Secretary // Secretary Agent for context/permission/state management
	mcpToolPrompt          string               // pre-formatted MCP tool descriptions for Coder prompt
	inventoryToolsPrompt   string               // dynamic "Available tools" section from inventory discovery
	inventoryEnvPrompt     string               // environment info (interpreters, formatters etc.) from inventory
	rollbackPolicy         string               // "auto" | "manual" | "off"; default "auto"
	maxTaskRetries         int                  // N-06: per-task retry budget; default 1 (= 2 total attempts)
	failurePolicy          string               // M11-04: "stop" | "skip" | "replan"; default "stop"
	hookManager            *hook.Manager        // unified hook manager (9 hook points)
	artifactMgr            artifact.Manager     // M13: optional artifact lifecycle manager
	closed                 bool
}

// activeWorkflow 追踪一个正在运行的工作流及其 workers
type activeWorkflow struct {
	sessionID  string
	cancel     context.CancelFunc
	Generation uint64              // generation at registration time; used to detect stale cleanup
	mu         sync.Mutex          // guards workers slice
	workers    []model.WorkerState // live snapshot; updated by executeWorker
	queue      *taskqueue.Queue    // M11-05: live TaskQueue; set during runWorkflow, nil before plan
}

// workflowRunningError 当同一会话已有工作流在运行时返回。
// RPCCode -32001 用于让客户端检测这个特定条件。
type workflowRunningError struct {
	workflowID string
}

func (e *workflowRunningError) Error() string {
	return fmt.Sprintf("workflow already running: %s", e.workflowID)
}

func (e *workflowRunningError) RPCCode() int { return -32001 }

// SetSecretary sets the Secretary Agent. Must be called before workflow execution.
func (k *Kernel) SetSecretary(s *secretary.Secretary) {
	k.secretary = s
}

// SetMCPToolPrompt sets the pre-formatted MCP tool descriptions that will be
// injected into the Coder's context. Must be called before workflow execution.
func (k *Kernel) SetMCPToolPrompt(s string) {
	k.mcpToolPrompt = s
}

// SetInventoryPrompts sets the pre-formatted inventory discovery prompts.
// Must be called before workflow execution.
func (k *Kernel) SetInventoryPrompts(toolsPrompt, envPrompt string) {
	k.inventoryToolsPrompt = toolsPrompt
	k.inventoryEnvPrompt = envPrompt
}

// SetSecretaryLLM attaches a Light model to the Secretary Agent.
func (k *Kernel) SetSecretaryLLM(l secretary.LLM) {
	if k.secretary != nil {
		k.secretary.SetLLM(l)
	}
}

// SetFailurePolicy configures what happens when a task fails after exhausting retries.
// Valid values: "stop" (default — abandon remaining tasks), "skip" (continue with next task),
// "replan" (reserved for future re-plan integration).
func (k *Kernel) SetFailurePolicy(policy string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	switch policy {
	case "stop", "skip", "replan":
		k.failurePolicy = policy
	default:
		k.failurePolicy = "stop"
	}
}

// SetHookManager injects the unified hook manager.
func (k *Kernel) SetHookManager(m *hook.Manager) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.hookManager = m
}

// HookManager returns the hook manager (may be nil).
func (k *Kernel) HookManager() *hook.Manager {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.hookManager
}

var kernelIDSeq atomic.Uint64
