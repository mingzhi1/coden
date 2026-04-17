package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/events"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	clog "github.com/mingzhi1/coden/internal/log"
)

// Subscribe 订阅指定会话的事件。
func (k *Kernel) Subscribe(sessionID string) (<-chan model.Event, func()) {
	return k.events.Subscribe(sessionID)
}

// SubscribeSince 像 Subscribe 但会重播所有 ring-buffered 事件中 Seq > sinceSeq 的。
// 使用 sinceSeq = SessionSnapshot.LastEventSeq 来关闭快照和实时事件之间的间隙 (R-01)。
func (k *Kernel) SubscribeSince(sessionID string, sinceSeq uint64) (<-chan model.Event, func()) {
	return k.events.SubscribeSince(sessionID, sinceSeq)
}

// Close 关闭 kernel 并释放资源。
func (k *Kernel) Close() error {
	k.mu.Lock()
	k.closed = true
	cancels := make([]context.CancelFunc, 0, len(k.activeWorkflows))
	for _, active := range k.activeWorkflows {
		k.workflowGeneration[active.sessionID]++
		cancels = append(cancels, active.cancel)
	}
	k.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	k.workflowWG.Wait()

	var firstErr error
	if err := k.sessionStore.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := k.intents.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := k.messages.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := k.checkpoints.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := k.turns.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := k.turnSummaries.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := k.objects.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := k.insights.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if k.artifactMgr != nil {
		if err := k.artifactMgr.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// N-05: release per-session mutex entries so long-running serve processes
	// don't accumulate entries for sessions that have been closed.
	k.mu.Lock()
	k.sessionMus = make(map[string]*sync.Mutex)
	k.mu.Unlock()
	// Close all per-session log files.
	clog.CloseAll()
	// Close all event subscriber channels so readers unblock.
	k.events.Close()
	return firstErr
}

// Events returns the kernel's internal event bus, for external systems
// (e.g. the web UI) that need to subscribe to workflow events.
func (k *Kernel) Events() *events.Bus {
	return k.events
}

// SetAllowShell 设置是否允许 shell 执行。
func (k *Kernel) SetAllowShell(allow bool) {
	k.mu.Lock()
	k.allowShell = allow
	k.mu.Unlock()
}

// SetMaxTaskRetries 设置每个 task 的最大重试次数（N-06）。
// 默认值为 1（即每个 task 最多尝试 2 次）。
// n=0 表示不重试（只尝试 1 次）。
func (k *Kernel) SetMaxTaskRetries(n int) {
	k.mu.Lock()
	k.maxTaskRetries = n
	k.mu.Unlock()
}

// SetRollbackPolicy 控制 Acceptor 拒绝 artifact 时的回滚行为。
// "auto"   — 自动回滚失败尝试中写入的所有文件（默认）
// "manual" — 保留脏文件让开发者检查
// "off"    — "manual" 的别名
func (k *Kernel) SetRollbackPolicy(policy string) {
	k.mu.Lock()
	k.rollbackPolicy = policy
	k.mu.Unlock()
}

// SetReplanner configures the optional RePlan step that refines
// high-level tasks into concrete steps after Discovery (M10-04).
func (k *Kernel) SetReplanner(r workflow.Replanner) {
	k.workflow.SetReplanner(r)
}

// SetSearcher configures the optional Search/Discovery boundary.
// When nil, the kernel falls back to the legacy in-process discovery helper.
func (k *Kernel) SetSearcher(s workflow.Searcher) {
	k.workflow.SetSearcher(s)
}

// SetCritic configures the optional Critic step that reviews plans before
// execution (Plan → Critic → RePlan → Code).
func (k *Kernel) SetCritic(c workflow.Critic) {
	k.workflow.SetCritic(c)
}

// Start 执行一次性启动任务：
//   - L4-08: 扫描孤儿 turns（状态=running）并标记为 "crashed"
//
// 在构造 Kernel 并设置选项后调用一次 Start()。
// 可以在任何 Submit 之前从 main 调用。
func (k *Kernel) Start() {
	orphans := k.turns.ListRunning()
	if len(orphans) == 0 {
		return
	}
	now := time.Now().UTC()
	for _, t := range orphans {
		if err := k.turns.UpdateStatus(t.ID, TurnStatusCrashed, now); err != nil {
			slog.Warn("[kernel] failed to mark orphan turn as crashed", "turn_id", t.ID, "error", err)
		}
		k.events.Emit(t.SessionID, model.EventWorkerMessage, model.WorkerMessagePayload{
			WorkflowID: t.ID,
			Kind:       "warn",
			Content: fmt.Sprintf(
				"orphan turn %s (session %s) was left with status=running from a previous process crash; marked as crashed",
				t.ID, t.SessionID,
			),
		})
	}
}
