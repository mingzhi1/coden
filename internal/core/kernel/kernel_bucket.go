package kernel

import (
	"sort"

	"github.com/mingzhi1/coden/internal/core/model"
)

// Blackboard-bucket scheduling primitives (migration S1).
//
// These pure helpers replace DAG-level wave scheduling (computeTaskLevels) with
// readiness + priority: a task runs as soon as ITS dependencies are satisfied,
// not when its whole topological "level" is done. New tasks (append / re-plan)
// join the bucket and become ready the moment their deps clear — no special
// post-DAG loop. The concurrent scheduler in runTasksConcurrent consumes these.
// See docs/design/blackboard_bucket_workflow.md.

// depsSatisfied reports whether every dependency of t has completed successfully
// (status passed). An unknown dependency ID (not in the bucket) is treated as
// satisfied — validateTaskDAG strips dangling refs, so this only guards against
// a stray ID blocking a task forever.
func depsSatisfied(t model.Task, byID map[string]model.Task) bool {
	for _, dep := range t.DependsOn {
		d, ok := byID[dep]
		if !ok {
			continue
		}
		if d.Status != model.TaskStatusPassed {
			return false
		}
	}
	return true
}

// indexTasks maps task ID → task for dependency lookups.
func indexTasks(tasks []model.Task) map[string]model.Task {
	byID := make(map[string]model.Task, len(tasks))
	for _, t := range tasks {
		if t.ID != "" {
			byID[t.ID] = t
		}
	}
	return byID
}

// readyTasksOrdered returns the tasks that are ready to dispatch — status
// "planned" with all dependencies satisfied — ordered by descending Priority,
// ties broken by ascending ID for deterministic (reproducible) scheduling.
// Tasks already running (coding/accepting), terminal (passed/failed/abandoned/
// skipped/removed), or retrying are excluded.
func readyTasksOrdered(tasks []model.Task) []model.Task {
	byID := indexTasks(tasks)
	var ready []model.Task
	for _, t := range tasks {
		if t.Status != model.TaskStatusPlanned {
			continue
		}
		if !depsSatisfied(t, byID) {
			continue
		}
		ready = append(ready, t)
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority // higher priority first
		}
		return ready[i].ID < ready[j].ID // deterministic tie-break
	})
	return ready
}

// hasPendingWork reports whether any task is still non-terminal (planned,
// running, or retrying). The scheduler uses it with readyTasksOrdered to detect
// a deadlock: pending work exists but nothing is ready and nothing is running.
func hasPendingWork(tasks []model.Task) bool {
	for _, t := range tasks {
		switch t.Status {
		case model.TaskStatusPlanned, model.TaskStatusCoding,
			model.TaskStatusAccepting, model.TaskStatusRetrying:
			return true
		}
	}
	return false
}
