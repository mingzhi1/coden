package kernel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	clog "github.com/mingzhi1/coden/internal/log"
)

// runResearchWorkflow handles Kind=research intents: gather EXTERNAL knowledge
// (library docs, APIs, web) the codebase doesn't contain. It is the EXTERNAL,
// read-only TWIN of runAnalyzeWorkflow and shares its shape: a single read-only
// investigate loop (the Analyzer machinery — no new role) that produces a PROSE
// answer.
//
// It deliberately does NOT use the Planner→Executor→Acceptor bucket engine: that
// shape PRODUCES code artifacts (success_cmd gates, file writes, accept), which is
// wrong for a "look up and explain" task — there is no artifact to verify and the
// deliverable is prose, not files. The Analyzer's investigate loop reaches
// web_search / web_fetch via tool_search, so it researches the outside world; being
// an investigate loop it never mutates the repo (read-only by construction).
func (k *Kernel) runResearchWorkflow(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec) {
	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID, Step: "research", Status: "running",
	})

	findings, err := k.workflow.Analyzer().Analyze(ctx, intent)
	if err != nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("research: %w", err))
		return
	}
	findings = strings.TrimSpace(findings)

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID, Step: "research", Status: "done",
	})

	// Read-only by construction — auto-pass with no artifacts.
	checkpointResult := model.CheckpointResult{
		WorkflowID: workflowID,
		SessionID:  sessionID,
		Status:     "pass",
		Evidence:   []string{"external research (read-only, no changes made)"},
		CreatedAt:  time.Now().UTC(),
	}

	// The investigate loop's prose IS the user-facing answer; fall back to the
	// Responder only when it came back empty.
	content := findings
	if content == "" {
		content = k.buildResponderMessage(ctx, sessionID, intent, nil, checkpointResult, model.Artifact{})
	}

	turnSummary := k.buildTurnSummary(sessionID, workflowID, intent, []model.Task{{
		ID:     "research",
		Title:  intent.Goal,
		Status: model.TaskStatusPassed,
	}}, checkpointResult)

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
	clog.Session(sessionID).Info("research workflow completed", "workflow_id", workflowID, "findings_len", len(content))

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

	// External findings are valuable to remember across turns (analyze_research.md §6).
	k.persistTurnMemory(ctx, sessionID, workflowID, intent.Goal, nil, content, checkpointResult.Status, nil)
}
