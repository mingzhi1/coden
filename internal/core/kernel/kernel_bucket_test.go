package kernel

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
)

// TestReadyTasksOrdered locks the readiness + priority contract: a task is ready
// only when status=planned and all deps are passed; ready tasks come out ordered
// by descending priority, ties by ascending ID (deterministic).
func TestReadyTasksOrdered(t *testing.T) {
	t.Parallel()

	tasks := []model.Task{
		{ID: "a", Status: model.TaskStatusPassed},                            // terminal → not ready
		{ID: "b", Status: model.TaskStatusPlanned, Priority: 1},              // ready (no deps)
		{ID: "c", Status: model.TaskStatusPlanned, Priority: 5},              // ready, higher priority
		{ID: "d", Status: model.TaskStatusPlanned, DependsOn: []string{"a"}}, // dep passed → ready
		{ID: "e", Status: model.TaskStatusPlanned, DependsOn: []string{"f"}}, // dep f not passed → blocked
		{ID: "f", Status: model.TaskStatusCoding},                            // running → not ready
		{ID: "g", Status: model.TaskStatusPlanned, Priority: 5},              // ready, ties with c → ID order
	}

	ready := readyTasksOrdered(tasks)
	gotIDs := make([]string, len(ready))
	for i, r := range ready {
		gotIDs[i] = r.ID
	}

	// Expected ready set: b, c, d, g. Order: priority desc (c=5,g=5 then b=1,d=0),
	// ties by ID (c before g). d has priority 0, b has 1 → b before d.
	want := []string{"c", "g", "b", "d"}
	if len(gotIDs) != len(want) {
		t.Fatalf("ready = %v, want %v", gotIDs, want)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Errorf("ready order = %v, want %v", gotIDs, want)
			break
		}
	}
}

// TestDepsSatisfied verifies dependency gating, including the dangling-dep guard.
func TestDepsSatisfied(t *testing.T) {
	t.Parallel()
	byID := indexTasks([]model.Task{
		{ID: "done", Status: model.TaskStatusPassed},
		{ID: "failed", Status: model.TaskStatusFailed},
		{ID: "running", Status: model.TaskStatusCoding},
	})

	cases := []struct {
		deps []string
		want bool
	}{
		{nil, true},
		{[]string{"done"}, true},
		{[]string{"failed"}, false},
		{[]string{"running"}, false},
		{[]string{"done", "failed"}, false},
		{[]string{"ghost"}, true}, // unknown dep treated as satisfied
	}
	for _, c := range cases {
		if got := depsSatisfied(model.Task{DependsOn: c.deps}, byID); got != c.want {
			t.Errorf("depsSatisfied(%v) = %v, want %v", c.deps, got, c.want)
		}
	}
}

// TestHasPendingWork verifies deadlock-detection input: pending = any non-terminal.
func TestHasPendingWork(t *testing.T) {
	t.Parallel()
	if hasPendingWork([]model.Task{{Status: model.TaskStatusPassed}, {Status: model.TaskStatusFailed}}) {
		t.Error("all terminal → no pending work")
	}
	if !hasPendingWork([]model.Task{{Status: model.TaskStatusPassed}, {Status: model.TaskStatusPlanned}}) {
		t.Error("a planned task → pending work")
	}
}
