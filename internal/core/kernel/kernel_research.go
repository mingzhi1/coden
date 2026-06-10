package kernel

import (
	"context"
	"fmt"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	clog "github.com/mingzhi1/coden/internal/log"
)

// runResearchWorkflow handles Kind=research intents: gather EXTERNAL knowledge
// (library docs, APIs, web) the codebase doesn't contain. It is the external,
// read-only counterpart of runAnalyzeWorkflow, but realized through MULTI-AGENT
// collaboration rather than a single role: the blackboard bucket engine runs the
// existing agent pool (Planner → Executor[web_search/web_fetch] → Acceptor),
// read-only, and the Responder synthesizes the findings. No new role is added —
// the workflow TYPE (chosen by intent recognition) shapes the flow, the existing
// agents collaborate, and read-only mode keeps research from touching the repo.
func (k *Kernel) runResearchWorkflow(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, wfCtx model.WorkflowContext) {
	wfCtx.ExecutorMode = model.ExecutorModeReadOnly // research never modifies the repo
	taskCtx := model.WithWorkflowContext(ctx, wfCtx)

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID, Step: "research", Status: "running",
	})

	// Multi-agent bucket engine: the Planner expands the research goal into
	// read-only investigation tasks, the Executor runs them with web_search /
	// web_fetch, the Acceptor judges each, Guardian bounds the loop.
	s, runErr := k.runBucketWorkflow(taskCtx, sessionID, workflowID, intent, wfCtx)
	content, checkpointResult := k.finishBucketWorkflow(taskCtx, sessionID, workflowID, intent, s, runErr)

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID, Step: "research", Status: "done",
	})

	tasks := s.finalTasks()
	if runErr != nil {
		// Persist a partial turn summary, then surface the failure (mirrors the
		// execute path's failure handling).
		failedTS := k.buildTurnSummary(sessionID, workflowID, intent, tasks, checkpointResult)
		if saveErr := k.turnSummaries.Save(failedTS); saveErr != nil {
			clog.Session(sessionID).Warn("failed to save partial research turn summary",
				"workflow_id", workflowID, "error", saveErr)
		}
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("research: %w", runErr))
		return
	}

	turnSummary := k.buildTurnSummary(sessionID, workflowID, intent, tasks, checkpointResult)
	assistantMessage := model.Message{
		ID:        nextKernelID("msg-assistant"),
		SessionID: sessionID,
		Role:      "assistant",
		Content:   content,
		CreatedAt: time.Now(),
	}

	if err := k.commitWorkflowSaga(sessionID, workflowID, checkpointResult, turnSummary, assistantMessage); err != nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("saga commit failed: %w", err))
		return
	}
	clog.Session(sessionID).Info("research workflow completed",
		"workflow_id", workflowID, "status", checkpointResult.Status, "tasks", len(tasks))

	k.events.Emit(sessionID, model.EventMessageCreated, model.MessageCreatedPayload{
		MessageID: assistantMessage.ID,
		Role:      assistantMessage.Role,
		Content:   assistantMessage.Content,
	})
	k.events.Emit(sessionID, model.EventCheckpointUpdated, model.CheckpointUpdatedPayload{
		WorkflowID: workflowID,
		Status:     checkpointResult.Status,
		Evidence:   checkpointResult.Evidence,
	})

	// External findings are the most valuable thing to remember across turns
	// (analyze_research.md §6 沉淀): persist via the shared memory pipeline.
	k.persistTurnMemory(ctx, sessionID, workflowID, intent.Goal, nil, content, checkpointResult.Status, nil)
}
