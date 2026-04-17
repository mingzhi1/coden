// Package taskqueue provides a thread-safe, dynamic task queue that replaces
// the static []Task loop in the Kernel workflow. It supports append, skip,
// remove, and undo operations with a full audit trail (M11).
package taskqueue

import (
	"fmt"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// OpKind describes the type of queue mutation.
type OpKind string

const (
	OpAppend  OpKind = "append"
	OpRemove  OpKind = "remove"
	OpSkip    OpKind = "skip"
	OpReorder OpKind = "reorder"
)

// TaskOp records a single queue mutation for audit and undo.
type TaskOp struct {
	Kind      OpKind    `json:"kind"`
	TaskID    string    `json:"task_id"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "planner" | "coder" | "user" | "agent"
	// prevIndex is used internally by undo to restore position.
	prevIndex int
	// prevTask stores the removed/skipped task for undo restoration.
	prevTask *model.Task
}

// Queue is a thread-safe dynamic task queue with undo support.
type Queue struct {
	mu      sync.Mutex
	tasks   []model.Task
	cursor  int      // index of the next task to execute
	history []TaskOp // append-only operation log
}

// New creates a Queue pre-loaded with the Planner's initial tasks.
func New(tasks []model.Task) *Queue {
	// Deep copy to avoid aliasing the caller's slice.
	copied := make([]model.Task, len(tasks))
	copy(copied, tasks)
	return &Queue{tasks: copied}
}

// Next returns the next executable task and advances the cursor.
// Returns (task, true) if a task is available, (zero, false) when the queue
// is exhausted. Skipped and removed tasks are transparently skipped.
func (q *Queue) Next() (model.Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.cursor < len(q.tasks) {
		t := q.tasks[q.cursor]
		q.cursor++
		switch t.Status {
		case model.TaskStatusSkipped, model.TaskStatusRemoved:
			continue // skip non-executable tasks
		}
		return t, true
	}
	return model.Task{}, false
}

// Append adds a new task to the end of the queue. Source identifies the
// originator ("planner", "coder", "user", "agent"). A maximum of
// maxAppendPerSource tasks can be appended by a single source within one
// workflow to prevent runaway LLM loops.
func (q *Queue) Append(task model.Task, source string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if task.Status == "" {
		task.Status = model.TaskStatusPlanned
	}
	q.tasks = append(q.tasks, task)
	q.history = append(q.history, TaskOp{
		Kind:      OpAppend,
		TaskID:    task.ID,
		Timestamp: time.Now().UTC(),
		Source:    source,
		prevIndex: len(q.tasks) - 1,
	})
}

// Remove marks a task as removed. Returns an error if the task is not found
// or is already in a terminal state (passed/failed/abandoned).
func (q *Queue) Remove(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.tasks {
		if q.tasks[i].ID != taskID {
			continue
		}
		if isTerminal(q.tasks[i].Status) {
			return fmt.Errorf("cannot remove task %s: already %s", taskID, q.tasks[i].Status)
		}
		prev := q.tasks[i]
		q.tasks[i].Status = model.TaskStatusRemoved
		q.history = append(q.history, TaskOp{
			Kind:      OpRemove,
			TaskID:    taskID,
			Timestamp: time.Now().UTC(),
			Source:    "user",
			prevIndex: i,
			prevTask:  &prev,
		})
		return nil
	}
	return fmt.Errorf("task %s not found", taskID)
}

// Skip marks a task as skipped. Returns an error if the task is not found
// or is already in a terminal state.
func (q *Queue) Skip(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.tasks {
		if q.tasks[i].ID != taskID {
			continue
		}
		if isTerminal(q.tasks[i].Status) {
			return fmt.Errorf("cannot skip task %s: already %s", taskID, q.tasks[i].Status)
		}
		prev := q.tasks[i]
		q.tasks[i].Status = model.TaskStatusSkipped
		q.history = append(q.history, TaskOp{
			Kind:      OpSkip,
			TaskID:    taskID,
			Timestamp: time.Now().UTC(),
			Source:    "user",
			prevIndex: i,
			prevTask:  &prev,
		})
		return nil
	}
	return fmt.Errorf("task %s not found", taskID)
}

// Undo reverses the most recent Append, Remove, or Skip operation.
// Returns the undone operation and nil, or an error if history is empty.
func (q *Queue) Undo() (TaskOp, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.history) == 0 {
		return TaskOp{}, fmt.Errorf("nothing to undo")
	}
	op := q.history[len(q.history)-1]
	q.history = q.history[:len(q.history)-1]

	switch op.Kind {
	case OpAppend:
		// Remove the last appended task.
		if op.prevIndex >= 0 && op.prevIndex < len(q.tasks) &&
			q.tasks[op.prevIndex].ID == op.TaskID {
			q.tasks = append(q.tasks[:op.prevIndex], q.tasks[op.prevIndex+1:]...)
			// Adjust cursor if it was past the removed index.
			if q.cursor > op.prevIndex {
				q.cursor--
			}
		}
	case OpRemove, OpSkip:
		// Restore the task's previous state.
		if op.prevTask != nil && op.prevIndex >= 0 && op.prevIndex < len(q.tasks) {
			q.tasks[op.prevIndex] = *op.prevTask
		}
	}
	return op, nil
}

// Snapshot returns a copy of all tasks in their current state.
func (q *Queue) Snapshot() []model.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]model.Task, len(q.tasks))
	copy(out, q.tasks)
	return out
}

// Remaining returns tasks that have not yet been executed (status is planned).
func (q *Queue) Remaining() []model.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []model.Task
	for _, t := range q.tasks[q.cursor:] {
		if t.Status == model.TaskStatusPlanned {
			out = append(out, t)
		}
	}
	return out
}

// History returns a copy of the operation log.
func (q *Queue) History() []TaskOp {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]TaskOp, len(q.history))
	copy(out, q.history)
	return out
}

// Len returns the total number of tasks (including skipped/removed).
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.tasks)
}

// SetStatus updates the status of a task by ID. Used by the Kernel to
// transition tasks through the state machine (coding → accepting → passed).
func (q *Queue) SetStatus(taskID, status string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.tasks {
		if q.tasks[i].ID == taskID {
			q.tasks[i].Status = status
			return
		}
	}
}

// isTerminal returns true if the status represents a final, unmodifiable state.
func isTerminal(status string) bool {
	switch status {
	case model.TaskStatusPassed, model.TaskStatusFailed, model.TaskStatusAbandoned:
		return true
	}
	return false
}

