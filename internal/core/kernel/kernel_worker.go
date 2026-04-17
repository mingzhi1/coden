package kernel

import (
	"context"
	"fmt"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// executeWorker 执行单个 worker 并追踪其状态。
func (k *Kernel) executeWorker(ctx context.Context, sessionID, workflowID, step string, role workflow.Role, worker workflow.Worker, input workflow.WorkerInput) (workflow.WorkerOutput, error) {
	workerID := nextWorkerID(string(role))
	started := time.Now()
	k.events.Emit(sessionID, model.EventWorkerStarted, model.WorkerStartedPayload{
		WorkflowID: workflowID,
		WorkerID:   workerID,
		WorkerRole: string(role),
		Step:       step,
	})

	// 在 active-workflow 快照中将 worker 记录为 "running"。
	wsEntry := model.WorkerState{
		WorkerID:  workerID,
		Role:      string(role),
		Step:      step,
		Status:    "running",
		StartedAt: started.UnixMilli(),
	}
	k.mu.Lock()
	aw := k.activeWorkflows[workflowID]
	k.mu.Unlock()
	var workerIdx int
	if aw != nil {
		aw.mu.Lock()
		workerIdx = len(aw.workers)
		aw.workers = append(aw.workers, wsEntry)
		aw.mu.Unlock()
	}

	result, err := worker.Execute(ctx, input)

	// 执行完成后更新 worker 状态。
	if aw != nil {
		aw.mu.Lock()
		if workerIdx < len(aw.workers) {
			aw.workers[workerIdx].EndedAt = time.Now().UnixMilli()
			if err != nil {
				aw.workers[workerIdx].Status = "failed"
			} else {
				aw.workers[workerIdx].Status = "done"
			}
		}
		aw.mu.Unlock()
	}

	if err != nil {
		k.events.Emit(sessionID, model.EventWorkerFinished, model.WorkerFinishedPayload{
			WorkflowID: workflowID,
			WorkerID:   workerID,
			WorkerRole: string(role),
			Step:       step,
			DurationMS: durationMillis(started),
		})
		return workflow.WorkerOutput{}, err
	}
	meta := roleOrDefault(result.Metadata, role)
	result.Metadata = meta
	result.Metadata.Worker = workerID
	k.emitWorkerMessages(sessionID, workflowID, workerID, string(meta.Role), step, result.Messages)
	k.events.Emit(sessionID, model.EventWorkerFinished, model.WorkerFinishedPayload{
		WorkflowID: workflowID,
		WorkerID:   workerID,
		WorkerRole: string(meta.Role),
		Step:       step,
		ToolCallID: toolCallID(result),
		DurationMS: durationMillis(started),
	})
	return result, nil
}

func roleOrDefault(meta workflow.WorkerMetadata, role workflow.Role) workflow.WorkerMetadata {
	if meta.Role == "" {
		meta.Role = role
	}
	if meta.Worker == "" {
		meta.Worker = string(meta.Role)
	}
	return meta
}

func workerIDFor(meta workflow.WorkerMetadata) string {
	return meta.Worker
}

func toolCallID(result workflow.WorkerOutput) string {
	if result.CodePlan == nil {
		return ""
	}
	calls := result.CodePlan.Calls()
	if len(calls) == 0 {
		return ""
	}
	return calls[0].ToolCallID
}

// nextWorkerID 生成 worker ID。
func nextWorkerID(role string) string {
	return fmt.Sprintf("worker-%s-%s", role, nextKernelID("id"))
}

// nextKernelID 生成带前缀的唯一 ID。
func nextKernelID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixMilli(), kernelIDSeq.Add(1))
}

// durationMillis 返回自 start 以来的毫秒数。
func durationMillis(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// emitTasksUpdated 发出任务更新事件。
func (k *Kernel) emitTasksUpdated(sessionID, workflowID string, tasks []model.Task) {
	if len(tasks) == 0 {
		return
	}

	k.events.Emit(sessionID, model.EventWorkflowTasks, model.WorkflowTasksUpdatedPayload{
		WorkflowID: workflowID,
		Tasks:      tasks,
	})
}

// emitWorkerMessages 发出 worker 消息事件。
func (k *Kernel) emitWorkerMessages(sessionID, workflowID, workerID, role, step string, messages []model.WorkerMessage) {
	for _, msg := range messages {
		k.events.Emit(sessionID, model.EventWorkerMessage, model.WorkerMessagePayload{
			WorkflowID:  workflowID,
			WorkerID:    workerID,
			WorkerRole:  role,
			Step:        step,
			Kind:        msg.Kind,
			MessageRole: msg.Role,
			Content:     msg.Content,
		})
	}
}
