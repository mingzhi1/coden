package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/insight"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/secretary"
)

// runQuestionWorkflow handles Kind=question intents by getting a direct LLM
// answer instead of going through the Plan/Code/Accept pipeline.
// This saves 3 LLM calls for pure Q&A requests.
func (k *Kernel) runQuestionWorkflow(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, prompt string) {
	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "code",
		Status:     "running",
	})

	// Collect LLM output for insight extraction below.
	var llmOut strings.Builder

	// Use the Coder worker in "answer mode" — it will detect the question
	// intent and produce a text answer instead of tool calls.
	coderInput := workflow.WorkerInput{
		SessionID:  sessionID,
		WorkflowID: workflowID,
		TaskID:     "answer",
		Intent:     intent,
		Tasks: []model.Task{{
			ID:     "answer",
			Title:  intent.Goal,
			Status: model.TaskStatusCoding,
		}},
	}

	codeResult, err := k.executeWorker(ctx, sessionID, workflowID, "code", workflow.RoleCoder, k.workflow.CoderWorker(), coderInput)
	if err != nil {
		k.handleWorkflowError(sessionID, workflowID, err)
		return
	}
	for _, msg := range codeResult.Messages {
		if msg.Role == "assistant" && msg.Content != "" {
			llmOut.WriteString(msg.Content)
			llmOut.WriteString("\n")
		}
	}

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "code",
		Status:     "done",
	})

	// For questions, the coder output may contain tool calls that write an
	// answer artifact, or it may be empty. Either way we auto-pass.
	var artifact model.Artifact
	if codeResult.CodePlan != nil {
		calls := codeResult.CodePlan.Calls()
		if len(calls) > 0 {
			workerID := workerIDFor(roleOrDefault(codeResult.Metadata, workflow.RoleCoder))
			artifact, _, err = k.executeToolPlan(ctx, sessionID, workflowID, workerID, nil, calls)
			if err != nil {
				k.handleWorkflowError(sessionID, workflowID, err)
				return
			}
		}
	}

	// Auto-pass checkpoint for questions (no acceptor needed).
	checkpointResult := model.CheckpointResult{
		WorkflowID: workflowID,
		SessionID:  sessionID,
		Status:     "pass",
		Evidence:   []string{"question answered directly"},
		CreatedAt:  time.Now().UTC(),
	}
	if artifact.Path != "" {
		checkpointResult.ArtifactPaths = []string{artifact.Path}
	}

	turnSummary := k.buildTurnSummary(sessionID, workflowID, intent, []model.Task{{
		ID:     "answer",
		Title:  intent.Goal,
		Status: model.TaskStatusPassed,
	}}, checkpointResult)

	assistantMessage := model.Message{
		ID:        nextKernelID("msg-assistant"),
		SessionID: sessionID,
		Role:      "assistant",
		Content:   k.buildAssistantCompletionMessage(sessionID, checkpointResult, artifact),
		CreatedAt: time.Now(),
	}

	if err := k.commitWorkflowSaga(sessionID, workflowID, checkpointResult, turnSummary, assistantMessage); err != nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("saga commit failed: %w", err))
		return
	}

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

	// Extract insights from the answer (zero-LLM-cost regex pass).
	llmOutputStr := llmOut.String()
	if llmOutputStr != "" {
		now := time.Now().UTC()
		for _, ins := range insight.ExtractInsights(workflowID, llmOutputStr, now) {
			ins.SessionID = sessionID
			if saveErr := k.insights.Save(ins); saveErr != nil {
				slog.Warn("[question] failed to save insight", "workflow_id", workflowID, "error", saveErr)
			}
		}
	}

	// Secretary AfterTurn: LLM-powered post-turn analysis (async, non-fatal).
	if k.secretary != nil && k.secretary.HasLLM() {
		go func() {
			afterCtx, afterCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer afterCancel()

			result := k.secretary.AfterTurn(afterCtx, sessionID, secretary.AfterTurnInput{
				WorkflowID:   workflowID,
				Goal:         intent.Goal,
				TaskTitles:   []string{intent.Goal},
				WorkerOutput: llmOutputStr,
				Status:       checkpointResult.Status,
			})

			now := time.Now().UTC()
			for _, ins := range result.Insights {
				modelIns := insight.Insight{
					ID:         fmt.Sprintf("sec-q-%s-%d", workflowID, now.UnixNano()),
					SessionID:  sessionID,
					Category:   insight.Category(ins.Category),
					Title:      ins.Title,
					Content:    ins.Content,
					Confidence: ins.Confidence,
					CreatedAt:  now,
					UpdatedAt:  now,
				}
				if saveErr := k.insights.Save(modelIns); saveErr != nil {
					slog.Warn("[secretary] question: failed to save insight", "error", saveErr)
				}
			}

			if wsRoot := k.workspace.Root(); wsRoot != "" {
				if memErr := insight.WriteMemoryFile(wsRoot, sessionID, k.insights); memErr != nil {
					slog.Warn("[secretary] question: failed to write memory file", "error", memErr)
				}
			}
		}()
	}
}
