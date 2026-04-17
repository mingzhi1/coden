package taskqueue

import (
	"fmt"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
)

func makeTasks(n int) []model.Task {
	tasks := make([]model.Task, n)
	for i := range tasks {
		tasks[i] = model.Task{
			ID:     fmt.Sprintf("task-%d", i+1),
			Title:  fmt.Sprintf("Task %d", i+1),
			Status: model.TaskStatusPlanned,
		}
	}
	return tasks
}

func TestNext_BasicIteration(t *testing.T) {
	q := New(makeTasks(3))
	for i := 1; i <= 3; i++ {
		task, ok := q.Next()
		if !ok {
			t.Fatalf("expected task %d, got exhausted", i)
		}
		want := fmt.Sprintf("task-%d", i)
		if task.ID != want {
			t.Errorf("Next() = %s, want %s", task.ID, want)
		}
	}
	_, ok := q.Next()
	if ok {
		t.Error("expected queue exhausted after 3 tasks")
	}
}

func TestNext_SkipsRemovedAndSkipped(t *testing.T) {
	q := New(makeTasks(4))
	_ = q.Skip("task-2")
	_ = q.Remove("task-3")

	task1, ok := q.Next()
	if !ok || task1.ID != "task-1" {
		t.Errorf("expected task-1, got %v", task1.ID)
	}
	// task-2 (skipped) and task-3 (removed) should be transparently skipped
	task4, ok := q.Next()
	if !ok || task4.ID != "task-4" {
		t.Errorf("expected task-4, got %v", task4.ID)
	}
	_, ok = q.Next()
	if ok {
		t.Error("expected queue exhausted")
	}
}

func TestAppend(t *testing.T) {
	q := New(makeTasks(2))
	q.Append(model.Task{ID: "task-3", Title: "Appended"}, "coder")

	if q.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", q.Len())
	}

	// Drain and verify order
	var ids []string
	for {
		task, ok := q.Next()
		if !ok {
			break
		}
		ids = append(ids, task.ID)
	}
	if len(ids) != 3 || ids[2] != "task-3" {
		t.Errorf("got %v, want [task-1, task-2, task-3]", ids)
	}

	// Verify history
	h := q.History()
	if len(h) != 1 || h[0].Kind != OpAppend || h[0].Source != "coder" {
		t.Errorf("history = %v, want append by coder", h)
	}
}

func TestSkip_TerminalError(t *testing.T) {
	q := New(makeTasks(1))
	q.SetStatus("task-1", model.TaskStatusPassed)
	err := q.Skip("task-1")
	if err == nil {
		t.Error("expected error skipping a passed task")
	}
}

func TestRemove_TerminalError(t *testing.T) {
	q := New(makeTasks(1))
	q.SetStatus("task-1", model.TaskStatusFailed)
	err := q.Remove("task-1")
	if err == nil {
		t.Error("expected error removing a failed task")
	}
}

func TestSkip_NotFound(t *testing.T) {
	q := New(makeTasks(1))
	err := q.Skip("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestUndoAppend(t *testing.T) {
	q := New(makeTasks(2))
	q.Append(model.Task{ID: "task-3", Title: "New"}, "user")
	if q.Len() != 3 {
		t.Fatalf("pre-undo Len() = %d", q.Len())
	}

	op, err := q.Undo()
	if err != nil {
		t.Fatalf("Undo() error: %v", err)
	}
	if op.Kind != OpAppend || op.TaskID != "task-3" {
		t.Errorf("undone op = %v", op)
	}
	if q.Len() != 2 {
		t.Errorf("post-undo Len() = %d, want 2", q.Len())
	}
}

func TestUndoSkip(t *testing.T) {
	q := New(makeTasks(3))
	_ = q.Skip("task-2")

	snap := q.Snapshot()
	if snap[1].Status != model.TaskStatusSkipped {
		t.Fatalf("task-2 should be skipped, got %s", snap[1].Status)
	}

	op, err := q.Undo()
	if err != nil {
		t.Fatalf("Undo() error: %v", err)
	}
	if op.Kind != OpSkip {
		t.Errorf("op.Kind = %s, want skip", op.Kind)
	}

	snap = q.Snapshot()
	if snap[1].Status != model.TaskStatusPlanned {
		t.Errorf("task-2 should be restored to planned, got %s", snap[1].Status)
	}
}

func TestUndoRemove(t *testing.T) {
	q := New(makeTasks(2))
	_ = q.Remove("task-1")

	_, err := q.Undo()
	if err != nil {
		t.Fatalf("Undo() error: %v", err)
	}

	snap := q.Snapshot()
	if snap[0].Status != model.TaskStatusPlanned {
		t.Errorf("task-1 should be restored, got %s", snap[0].Status)
	}
}

func TestUndoEmpty(t *testing.T) {
	q := New(makeTasks(1))
	_, err := q.Undo()
	if err == nil {
		t.Error("expected error undoing with empty history")
	}
}

func TestRemaining(t *testing.T) {
	q := New(makeTasks(3))
	// Execute first task
	q.Next()
	remaining := q.Remaining()
	if len(remaining) != 2 {
		t.Errorf("Remaining() = %d, want 2", len(remaining))
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	q := New(makeTasks(2))
	snap := q.Snapshot()
	snap[0].Title = "mutated"
	// Original should be unaffected
	snap2 := q.Snapshot()
	if snap2[0].Title == "mutated" {
		t.Error("Snapshot should return a deep copy")
	}
}

func TestSetStatus(t *testing.T) {
	q := New(makeTasks(1))
	q.SetStatus("task-1", model.TaskStatusCoding)
	snap := q.Snapshot()
	if snap[0].Status != model.TaskStatusCoding {
		t.Errorf("SetStatus: got %s, want coding", snap[0].Status)
	}
}

