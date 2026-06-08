package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
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
		// A question/chat answer must never modify the repo. Drop mutation tool
		// calls (write_file/edit_file/run_shell); the answer is the coder's text
		// reply. This guards against the model trying to write its answer to a file.
		var safe []workflow.ToolCall
		for _, call := range codeResult.CodePlan.Calls() {
			switch call.Request.Kind {
			case "write_file", "edit_file", "run_shell":
				slog.Info("[question] dropping mutation tool call in answer mode",
					"session", sessionID, "kind", call.Request.Kind)
			default:
				safe = append(safe, call)
			}
		}
		if len(safe) > 0 {
			workerID := workerIDFor(roleOrDefault(codeResult.Metadata, workflow.RoleCoder))
			artifact, _, err = k.executeToolPlan(ctx, sessionID, workflowID, workerID, nil, safe)
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
		// Question/chat: Responder answers directly (nil tasks → "no work done").
		Content:   k.buildResponderMessage(ctx, sessionID, intent, nil, checkpointResult, artifact),
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

	// Persist memory (regex fallback + async Secretary extraction) via the shared
	// pipeline.
	k.persistTurnMemory(ctx, sessionID, workflowID, intent.Goal, []string{intent.Goal}, llmOut.String(), checkpointResult.Status, nil)
}
