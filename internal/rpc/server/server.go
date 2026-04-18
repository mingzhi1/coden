// Package server implements the JSON-RPC 2.0 server for the CodeN kernel.
// Following the Neovim pattern: kernel owns state, server dispatches commands,
// and pushes events as notifications to connected clients.
package server

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// KernelAPI is the subset of kernel.Kernel that the RPC server needs.
type KernelAPI interface {
	CreateSession(ctx context.Context, sessionID string) (model.Session, error)
	ListSessions(ctx context.Context, limit int) ([]model.Session, error)
	Attach(ctx context.Context, sessionID, clientName, view string) error
	Detach(ctx context.Context, sessionID, clientName string) error
	Submit(ctx context.Context, sessionID, prompt string) (string, error)
	CancelWorkflow(ctx context.Context, sessionID, workflowID string) error
	GetWorkflowRun(ctx context.Context, sessionID, workflowID string) (model.WorkflowRun, error)
	ListWorkflowRuns(ctx context.Context, sessionID string, limit int) ([]model.WorkflowRun, error)
	ListWorkflowRunObjects(ctx context.Context, sessionID, workflowID string) ([]model.Object, error)
	ReadWorkflowRunObject(ctx context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error)
	ListMessages(ctx context.Context, sessionID string, limit int) ([]model.Message, error)
	GetLatestIntent(ctx context.Context, sessionID string) (model.IntentSpec, error)
	WorkspaceChanges(ctx context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error)
	WorkspaceRead(ctx context.Context, sessionID, path string) ([]byte, error)
	WorkspaceWrite(ctx context.Context, sessionID, path string, content []byte) (string, error)
	GetCheckpoint(ctx context.Context, sessionID, workflowID string) (model.CheckpointResult, error)
	ListCheckpoints(ctx context.Context, sessionID string, limit int) ([]model.CheckpointResult, error)
	Subscribe(sessionID string) (<-chan model.Event, func())
	RenameSession(ctx context.Context, sessionID, name string) (model.Session, error)
	SubscribeSince(sessionID string, sinceSeq uint64) (<-chan model.Event, func())
	Snapshot(ctx context.Context, sessionID string, messageLimit int) (model.SessionSnapshot, error)
	GetWorkflowWorkers(ctx context.Context, sessionID, workflowID string) ([]model.WorkerState, error)
	// M11-05: task management
	SkipTask(ctx context.Context, sessionID, taskID string) error
	UndoTask(ctx context.Context, sessionID string) (string, error)
	// Hook management
	ListHooks(ctx context.Context, point string) ([]protocol.HookInfo, error)
	RegisterHook(ctx context.Context, p protocol.HookRegisterParams) error
	RemoveHook(ctx context.Context, name string) (bool, error)
}

// Handler processes a single RPC method and returns a result or error.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server is the JSON-RPC 2.0 server wrapping the kernel.
type Server struct {
	kernel   KernelAPI
	handlers map[string]Handler
	mu       sync.Mutex
	clients  map[*clientConn]struct{}
}

type clientConn struct {
	codec         *transport.Codec
	connCtx       context.Context    // connection-level context (lives for the entire connection)
	cancel        context.CancelFunc
	subMu         sync.Mutex
	subscriptions map[string]subscription
}

type subscription struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a Server wired to the given kernel.
func New(k KernelAPI) *Server {
	s := &Server{
		kernel:   k,
		handlers: make(map[string]Handler),
		clients:  make(map[*clientConn]struct{}),
	}
	s.registerBuiltins()
	return s
}

func (cc *clientConn) addSubscription(sessionID string, sub subscription) bool {
	cc.subMu.Lock()
	defer cc.subMu.Unlock()
	if _, exists := cc.subscriptions[sessionID]; exists {
		return false
	}
	cc.subscriptions[sessionID] = sub
	return true
}

func (cc *clientConn) cancelSubscription(sessionID string) bool {
	cc.subMu.Lock()
	sub, ok := cc.subscriptions[sessionID]
	if ok {
		delete(cc.subscriptions, sessionID)
	}
	cc.subMu.Unlock()
	if ok {
		sub.cancel()
		<-sub.done
	}
	return ok
}

func (cc *clientConn) dropSubscription(sessionID string) {
	cc.subMu.Lock()
	delete(cc.subscriptions, sessionID)
	cc.subMu.Unlock()
}

func (cc *clientConn) cancelAllSubscriptions() {
	cc.subMu.Lock()
	subs := make([]subscription, 0, len(cc.subscriptions))
	for sessionID, sub := range cc.subscriptions {
		subs = append(subs, sub)
		delete(cc.subscriptions, sessionID)
	}
	cc.subMu.Unlock()
	for _, sub := range subs {
		sub.cancel()
		<-sub.done
	}
}
