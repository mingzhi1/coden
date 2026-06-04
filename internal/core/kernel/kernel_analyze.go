package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	clog "github.com/mingzhi1/coden/internal/log"

	"github.com/mingzhi1/coden/internal/core/insight"
	"github.com/mingzhi1/coden/internal/core/model"
)

// runAnalyzeWorkflow handles Kind=analyze intents: a read-only investigation of
// the code (Intent → Discovery → Analyzer → Responder). The Analyzer reads code
// through tools but NEVER modifies it; its prose findings are the user-facing
// answer. This replaces the earlier interim path that ran the Coder in
// read-only mode — keeping the Coder pure-write and giving analysis its own
// Strong-tier role with an analysis-focused prompt.
func (k *Kernel) runAnalyzeWorkflow(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec) {
	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "analyze",
		Status:     "running",
	})

	analysis, err := k.workflow.Analyzer().Analyze(ctx, intent)
	if err != nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("analyze: %w", err))
		return
	}
	analysis = strings.TrimSpace(analysis)

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "analyze",
		Status:     "done",
	})

	// Analysis is read-only by construction — auto-pass with no artifacts.
	checkpointResult := model.CheckpointResult{
		WorkflowID: workflowID,
		SessionID:  sessionID,
		Status:     "pass",
		Evidence:   []string{"code analyzed (read-only, no changes made)"},
		CreatedAt:  time.Now().UTC(),
	}

	// The Analyzer's prose IS the user-facing analysis. Present it directly to
	// avoid degrading the Strong-tier analysis through a Light-tier re-summary.
	// Fall back to the Responder only when the analysis came back empty.
	content := analysis
	if content == "" {
		content = k.buildResponderMessage(ctx, sessionID, intent, nil, checkpointResult, model.Artifact{})
	}

	turnSummary := k.buildTurnSummary(sessionID, workflowID, intent, []model.Task{{
		ID:     "analyze",
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
	clog.Session(sessionID).Info("analyze workflow completed", "workflow_id", workflowID, "analysis_len", len(content))

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

	// Extract insights from the analysis (zero-LLM-cost regex pass).
	if content != "" {
		now := time.Now().UTC()
		for _, ins := range insight.ExtractInsights(workflowID, content, now) {
			ins.SessionID = sessionID
			if saveErr := k.insights.Save(ins); saveErr != nil {
				slog.Warn("[analyze] failed to save insight", "workflow_id", workflowID, "error", saveErr)
			}
		}
	}
}
