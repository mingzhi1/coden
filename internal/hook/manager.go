package hook

import (
	"context"
	"sort"
	"sync"

	"github.com/mingzhi1/coden/internal/core/events"
	"github.com/mingzhi1/coden/internal/core/model"
)

// Manager manages hook registration and execution.
// All hooks for a given Point are executed serially in priority order;
// a blocking failure short-circuits remaining hooks at that Point.
type Manager struct {
	mu    sync.RWMutex
	hooks map[Point][]Config
	bus   *events.Bus
}

// NewManager creates a hook manager. bus may be nil (events won't be emitted).
func NewManager(bus *events.Bus) *Manager {
	return &Manager{
		hooks: make(map[Point][]Config),
		bus:   bus,
	}
}

// Register adds a single hook configuration, maintaining priority sort order.
func (m *Manager) Register(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[cfg.Point] = append(m.hooks[cfg.Point], cfg)
	sort.Slice(m.hooks[cfg.Point], func(i, j int) bool {
		return m.hooks[cfg.Point][i].Priority < m.hooks[cfg.Point][j].Priority
	})
}

// RegisterBatch registers multiple hooks.
func (m *Manager) RegisterBatch(configs []Config) {
	for _, cfg := range configs {
		m.Register(cfg)
	}
}

// Remove deletes all hooks with the given name across all points.
// Returns true if at least one hook was removed.
func (m *Manager) Remove(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := false
	for pt, hooks := range m.hooks {
		filtered := hooks[:0]
		for _, h := range hooks {
			if h.Name == name {
				removed = true
				continue
			}
			filtered = append(filtered, h)
		}
		m.hooks[pt] = filtered
	}
	return removed
}

// List returns hooks registered at the given point.
// If point is empty, returns all hooks across all points.
func (m *Manager) List(point Point) []Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if point != "" {
		out := make([]Config, len(m.hooks[point]))
		copy(out, m.hooks[point])
		return out
	}
	var out []Config
	for _, pt := range AllPoints {
		out = append(out, m.hooks[pt]...)
	}
	return out
}

// HasHooks reports whether any hooks are registered at the given point.
func (m *Manager) HasHooks(point Point) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hooks[point]) > 0
}

// Run executes all hooks at the given point serially in priority order.
// If a blocking hook fails, execution short-circuits and returns immediately.
// Events are emitted to the bus for each hook execution.
func (m *Manager) Run(ctx context.Context, point Point, hookCtx *Context) []Result {
	m.mu.RLock()
	hooks := make([]Config, len(m.hooks[point]))
	copy(hooks, m.hooks[point])
	m.mu.RUnlock()

	if len(hooks) == 0 {
		return nil
	}

	sessionID := ""
	workflowID := ""
	if hookCtx != nil {
		sessionID = hookCtx.SessionID
		workflowID = hookCtx.WorkflowID
	}

	var results []Result
	for _, cfg := range hooks {
		if ctx.Err() != nil {
			break
		}

		m.emitStarted(sessionID, workflowID, cfg)
		result := execute(ctx, cfg, hookCtx)
		results = append(results, result)
		m.emitFinished(sessionID, workflowID, result)

		if result.Verdict == VerdictBlock {
			break
		}
	}
	return results
}

func (m *Manager) emitStarted(sessionID, workflowID string, cfg Config) {
	if m.bus == nil {
		return
	}
	m.bus.Emit(sessionID, model.EventHookStarted, model.HookStartedPayload{
		WorkflowID: workflowID,
		HookName:   cfg.Name,
		HookPoint:  string(cfg.Point),
	})
}

func (m *Manager) emitFinished(sessionID, workflowID string, r Result) {
	if m.bus == nil {
		return
	}
	m.bus.Emit(sessionID, model.EventHookFinished, model.HookFinishedPayload{
		WorkflowID: workflowID,
		HookName:   r.Name,
		HookPoint:  string(r.Point),
		Verdict:    string(r.Verdict),
		DurationMS: r.Duration.Milliseconds(),
		Output:     r.Output,
		Error:      r.Error,
	})
}
