package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/artifact"
	"github.com/mingzhi1/coden/internal/core/model"
	clog "github.com/mingzhi1/coden/internal/log"
)

// researchTaskIDPrefix is the RESERVED id namespace for in-bucket research tasks.
// The scheduler mints these ids deterministically (research-<slug-of-query>), so a
// shared research need maps to a single task id and the bucket's id-uniqueness
// dedups it for free (single-writer apply). dispatchTask routes any task in this
// namespace to runResearchTask; the Planner must never emit ids under it.
const researchTaskIDPrefix = "research-"

// isResearchTaskID reports whether id is in the reserved research-task namespace.
func isResearchTaskID(id string) bool {
	return strings.HasPrefix(id, researchTaskIDPrefix)
}

// runResearchTask is the bucketScheduler's worker for an in-bucket research task:
// gather EXTERNAL knowledge for the task's query (carried in SubGoal, else Title)
// via the read-only Analyzer investigate loop, persist the prose findings as a
// TEMP artifact, and hand them back in the outcome's Artifact. The findings ride
// model.Artifact.Summary, so depArtifactsFor → formatDepFindings projects them to
// any task that DEPENDS on this one (§11) — the mechanism that lets a blocked code
// task resume with the knowledge it lacked. A research task NEVER mutates the repo
// (Analyzer is read-only by construction), so it auto-passes when findings exist.
//
// Unlike runResearchWorkflow (the top-level research INTENT, which answers the user
// in prose), this is a DEPENDENCY producer inside a code/execute bucket: its
// deliverable is the artifact other tasks consume, not a user-facing message.
func (k *Kernel) runResearchTask(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, in taskInput, wfCtx model.WorkflowContext) taskOutcome {
	task := in.task
	wfCtx.DepFindings = formatDepFindings(in.depArtifacts) // a research task may build on prior findings too
	taskCtx := model.WithWorkflowContext(ctx, wfCtx)

	query := strings.TrimSpace(task.SubGoal)
	if query == "" {
		query = strings.TrimSpace(task.Title)
	}

	// Scope a derived intent to the query so the Analyzer researches THIS need.
	researchIntent := intent
	researchIntent.Goal = query
	researchIntent.Kind = model.IntentKindResearch

	findings, err := k.workflow.Analyzer().Analyze(taskCtx, researchIntent)
	if err != nil {
		return taskOutcome{taskID: task.ID, status: model.TaskStatusFailed, err: fmt.Errorf("research %q: %w", query, err)}
	}
	findings = strings.TrimSpace(findings)
	if findings == "" {
		return taskOutcome{taskID: task.ID, status: model.TaskStatusFailed, err: fmt.Errorf("research %q produced no findings", query)}
	}

	ref := k.saveResearchArtifact(taskCtx, sessionID, workflowID, task.ID, query, findings)
	return taskOutcome{
		taskID: task.ID,
		status: model.TaskStatusPassed,
		artifact: model.Artifact{
			Path:     ref, // artifact id (read_artifact-able for full content); empty if no store
			Summary:  findings,
			Evidence: []string{"external research (read-only, no changes)"},
		},
	}
}

// saveResearchArtifact persists research findings as a TEMP artifact via the
// artifact store and returns its id (read_artifact-able), or "" when no store is
// wired. Best-effort: a persistence failure never fails the research task — the
// findings already ride the outcome's Artifact.Summary, so DepArtifacts projection
// is unaffected; the stored artifact only adds a durable, full-content handle.
func (k *Kernel) saveResearchArtifact(ctx context.Context, sessionID, workflowID, taskID, query, findings string) string {
	if k.artifactMgr == nil {
		return ""
	}
	name := "research:" + capName(query, 80)
	art, err := k.artifactMgr.SaveContent(ctx, workflowID, sessionID, taskID,
		artifact.KindToolOutput, name, []byte(findings), map[string]string{"research_query": query})
	if err != nil || art == nil {
		clog.Session(sessionID).Warn("failed to persist research artifact", "task_id", taskID, "error", err)
		return ""
	}
	return art.ID
}

// capName trims s to at most n runes for use as an artifact name label.
func capName(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
