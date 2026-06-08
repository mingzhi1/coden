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
	// Discovery: seed the Analyzer with code context before it starts reading.
	// policyForKind(analyze) declares Discovery:true, but the analyze branch in
	// runWorkflow returns before the shared discovery code runs — so without this
	// the Analyzer starts blind (only a file-tree listing) and burns its limited
	// read rounds rediscovering basics. Run discovery here and inject it into the
	// WorkflowContext that contextSummary() surfaces to the Analyzer.
	ctx = k.seedAnalyzeDiscovery(ctx, sessionID, workflowID, intent)

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

	// Extract insights from the analysis (zero-LLM-cost regex pass) and regenerate
	// MEMORY.md. The analysis is often the most valuable thing to remember, yet
	// the analyze path previously skipped this entirely (only code/question turns
	// wrote memory) — so findings never persisted across turns.
	if content != "" {
		now := time.Now().UTC()
		for _, ins := range insight.ExtractInsights(workflowID, content, now) {
			ins.SessionID = sessionID
			if saveErr := k.insights.Save(ins); saveErr != nil {
				slog.Warn("[analyze] failed to save insight", "workflow_id", workflowID, "error", saveErr)
			}
		}
		if wsRoot := k.workspace.Root(); wsRoot != "" {
			if memErr := insight.WriteMemoryFile(wsRoot, sessionID, k.insights); memErr != nil {
				slog.Warn("[analyze] failed to write memory file", "workflow_id", workflowID, "error", memErr)
			} else {
				clog.Session(sessionID).Info("memory file updated", "workflow_id", workflowID)
			}
		}
	}
}

// seedAnalyzeDiscovery runs the Discovery/Search phase for an analyze intent and
// injects the resulting snippets/evidence into the WorkflowContext so the
// Analyzer's contextSummary() starts with real code context instead of a blank
// slate. It is best-effort: on failure it logs and returns ctx unchanged so the
// Analyzer still runs (it can fall back to its own read loop).
func (k *Kernel) seedAnalyzeDiscovery(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec) context.Context {
	searcher := k.workflow.Searcher()
	if searcher == nil {
		searcher = NewLocalSearcher(k, sessionID, workflowID)
	}
	if searcher == nil {
		return ctx
	}

	queryID := workflowID + ":analyze"
	k.events.Emit(sessionID, model.EventSearchStarted, model.SearchStartedPayload{
		WorkflowID: workflowID,
		Query:      intent.Goal,
		QueryID:    queryID,
	})

	t0 := time.Now()
	dc, err := searcher.Search(ctx, intent, nil)
	if err != nil {
		slog.Warn("[analyze] discovery failed, Analyzer will run without pre-seeded context",
			"workflow_id", workflowID, "error", err)
		return ctx
	}

	wfCtx := model.WorkflowContextFrom(ctx)
	wfCtx.Discovery = dc
	wfCtx.DiscoveryContext = dc.Snippets

	k.events.Emit(sessionID, model.EventSearchFinished, model.SearchFinishedPayload{
		WorkflowID:    workflowID,
		QueryID:       queryID,
		SnippetCount:  len(dc.Snippets),
		EvidenceCount: len(dc.Evidence),
		Confidence:    dc.Confidence,
		DurationMs:    time.Since(t0).Milliseconds(),
	})
	return model.WithWorkflowContext(ctx, wfCtx)
}
