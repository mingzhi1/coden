package kernel

import (
	"context"
	"fmt"
	"time"

	"github.com/mingzhi1/coden/internal/hook"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// ListHooks returns hooks registered at the given point.
// An empty point returns all hooks across all points.
func (k *Kernel) ListHooks(_ context.Context, point string) ([]protocol.HookInfo, error) {
	k.mu.Lock()
	mgr := k.hookManager
	k.mu.Unlock()
	if mgr == nil {
		return nil, nil
	}
	configs := mgr.List(hook.Point(point))
	out := make([]protocol.HookInfo, len(configs))
	for i, c := range configs {
		out[i] = protocol.HookInfo{
			Name:     c.Name,
			Point:    string(c.Point),
			Command:  c.Command,
			Blocking: c.Blocking,
			Timeout:  c.Timeout,
			Env:      c.Env,
			Source:   c.Source,
			Priority: c.Priority,
		}
	}
	return out, nil
}

// RegisterHook dynamically registers a hook via RPC.
func (k *Kernel) RegisterHook(_ context.Context, p protocol.HookRegisterParams) error {
	k.mu.Lock()
	mgr := k.hookManager
	k.mu.Unlock()
	if mgr == nil {
		return fmt.Errorf("hook manager not initialized")
	}
	pt := hook.Point(p.Point)
	if !hook.ValidPoint(pt) {
		return fmt.Errorf("invalid hook point: %q", p.Point)
	}
	timeout := 60 * time.Second
	if p.Timeout != "" {
		d, err := time.ParseDuration(p.Timeout)
		if err != nil {
			return fmt.Errorf("invalid timeout %q: %w", p.Timeout, err)
		}
		timeout = d
	}
	mgr.Register(hook.Config{
		Name:     p.Name,
		Point:    pt,
		Command:  p.Command,
		Blocking: p.Blocking,
		Timeout:  timeout,
		Env:      p.Env,
		Source:   "rpc",
		Priority: p.Priority,
	})
	return nil
}

// RemoveHook removes a hook by name.
func (k *Kernel) RemoveHook(_ context.Context, name string) (bool, error) {
	k.mu.Lock()
	mgr := k.hookManager
	k.mu.Unlock()
	if mgr == nil {
		return false, nil
	}
	return mgr.Remove(name), nil
}

// runHookPoint runs all hooks at the given point and returns true if a
// blocking hook failed. This is the main integration point used by
// kernel_workflow.go and kernel_task.go.
func (k *Kernel) runHookPoint(ctx context.Context, point hook.Point, sessionID, workflowID, taskID, taskTitle string, attempt int) bool {
	k.mu.Lock()
	mgr := k.hookManager
	k.mu.Unlock()
	if mgr == nil || !mgr.HasHooks(point) {
		return false
	}
	hookCtx := &hook.Context{
		SessionID:     sessionID,
		WorkflowID:    workflowID,
		WorkspaceRoot: k.workspace.Root(),
		TaskID:        taskID,
		TaskTitle:     taskTitle,
		Attempt:       attempt,
	}
	results := mgr.Run(ctx, point, hookCtx)
	return hook.HasBlockingFailure(results)
}

// runHookPointWithContext runs hooks with a fully populated Context.
func (k *Kernel) runHookPointWithContext(ctx context.Context, point hook.Point, hookCtx *hook.Context) bool {
	k.mu.Lock()
	mgr := k.hookManager
	k.mu.Unlock()
	if mgr == nil || !mgr.HasHooks(point) {
		return false
	}
	if hookCtx.WorkspaceRoot == "" {
		hookCtx.WorkspaceRoot = k.workspace.Root()
	}
	results := mgr.Run(ctx, point, hookCtx)
	return hook.HasBlockingFailure(results)
}
