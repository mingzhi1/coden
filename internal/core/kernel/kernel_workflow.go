package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/discovery"
	"github.com/mingzhi1/coden/internal/core/insight"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/taskqueue"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	clog "github.com/mingzhi1/coden/internal/log"
	"github.com/mingzhi1/coden/internal/secretary"
)

// Submit 为给定会话入队新工作流并立即返回 workflowID。
// 工作流在后台 goroutine 中运行，通过 checkpoint.updated 事件交付最终结果。
func (k *Kernel) Submit(ctx context.Context, sessionID, prompt string) (string, error) {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return "", fmt.Errorf("kernel is closed")
	}
	k.mu.Unlock()

	if err := k.ensureSession(sessionID); err != nil {
		return "", err
	}

	workflowID := nextKernelID("wf")

	// Per-session mutex: 序列化同一会话内的提交，但允许不同会话并发运行。
	mu := k.sessionMutex(sessionID)

	// 从调用者的上下文分离，使工作流在 HTTP 超时后仍能存活。
	runCtx, cancel := context.WithCancel(context.Background())
	if err := k.registerWorkflow(sessionID, workflowID, cancel); err != nil {
		cancel()
		return "", err
	}

	go func() {
		mu.Lock()
		defer mu.Unlock()
		defer k.finishWorkflow(workflowID)
		k.runWorkflow(runCtx, sessionID, workflowID, prompt)
	}()

	return workflowID, nil
}

// sessionMutex 返回（如需要则创建）每个会话的互斥锁。
func (k *Kernel) sessionMutex(sessionID string) *sync.Mutex {
	k.mu.Lock()
	defer k.mu.Unlock()
	if mu, ok := k.sessionMus[sessionID]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	k.sessionMus[sessionID] = mu
	return mu
}

// registerWorkflow 将工作流注册为活动状态。
func (k *Kernel) registerWorkflow(sessionID, workflowID string, cancel context.CancelFunc) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return fmt.Errorf("kernel is closed")
	}
	if existingID, running := k.activeSessionWorkflows[sessionID]; running {
		return &workflowRunningError{workflowID: existingID}
	}
	k.workflowGeneration[sessionID]++
	gen := k.workflowGeneration[sessionID]
	k.workflowWG.Add(1)
	k.activeWorkflows[workflowID] = &activeWorkflow{
		sessionID:  sessionID,
		cancel:     cancel,
		Generation: gen,
	}
	k.activeSessionWorkflows[sessionID] = workflowID
	return nil
}

// finishWorkflow 将工作流标记为已完成。
// It only removes the session-level tracking (activeSessionWorkflows) when the
// workflow's generation matches the current generation for that session. If a
// newer workflow has already been registered (generation mismatch), this is a
// stale cleanup from a cancelled workflow and must not disturb the new one.
func (k *Kernel) finishWorkflow(workflowID string) {
	k.mu.Lock()
	if aw, ok := k.activeWorkflows[workflowID]; ok {
		if k.workflowGeneration[aw.sessionID] == aw.Generation {
			delete(k.activeSessionWorkflows, aw.sessionID)
		}
		delete(k.activeWorkflows, workflowID)
	}
	k.mu.Unlock()
	k.workflowWG.Done()
}

// injectSecretaryContext updates the WorkflowContext with Secretary-assembled
// content for the given worker target. This must be called before each
// executeWorker() so that different workers see different skill sets.
func (k *Kernel) injectSecretaryContext(ctx context.Context, sessionID string, target secretary.Target) context.Context {
	if k.secretary == nil {
		return ctx
	}
	wfCtx := model.WorkflowContextFrom(ctx)
	blocks := k.secretary.AssembleContext(sessionID, target, wfCtx.DirtyPaths)
	wfCtx.SecretaryContext = secretary.FormatContextBlocks(target, blocks)
	// Inject MCP tool descriptions and inventory prompts for Coder only.
	if target == secretary.TargetCoder {
		if k.mcpToolPrompt != "" {
			wfCtx.MCPToolDescriptions = k.mcpToolPrompt
		}
		if k.inventoryToolsPrompt != "" {
			wfCtx.ToolsPrompt = k.inventoryToolsPrompt
		}
		if k.inventoryEnvPrompt != "" {
			wfCtx.EnvironmentPrompt = k.inventoryEnvPrompt
		}
	}
	return model.WithWorkflowContext(ctx, wfCtx)
}

// runWorkflow 执行完整的 Intent→Plan→Code→Accept 流水线。
// 它在每次 worker 调用前将 WorkflowContext 注入上下文，
// 使 LLM workers 可以使用会话历史和工作区文件树。
func (k *Kernel) runWorkflow(ctx context.Context, sessionID, workflowID, prompt string) {
	now := time.Now().UTC()
	if err := k.turns.Save(model.Turn{
		ID:         workflowID,
		SessionID:  sessionID,
		WorkflowID: workflowID,
		Prompt:     prompt,
		Status:     TurnStatusRunning,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("save turn: %w", err))
		return
	}

	userMessage := model.Message{
		ID:        nextKernelID("msg-user"),
		SessionID: sessionID,
		Role:      "user",
		Content:   prompt,
		CreatedAt: time.Now(),
	}
	if err := k.messages.Save(userMessage); err != nil {
		k.handleWorkflowError(sessionID, workflowID, err)
		return
	}
	k.events.Emit(sessionID, model.EventMessageCreated, model.MessageCreatedPayload{
		MessageID: userMessage.ID,
		Role:      userMessage.Role,
		Content:   userMessage.Content,
	})
	k.events.Emit(sessionID, model.EventWorkflowStarted, model.WorkflowStartedPayload{
		WorkflowID: workflowID,
	})
	clog.Session(sessionID).Info("workflow started",
		"workflow_id", workflowID,
		"prompt_len", len(prompt))

	// 构建 WorkflowContext 并注入到每次 worker 调用中。
	wfCtx := k.buildWorkflowContext(ctx, sessionID)
	ctx = model.WithWorkflowContext(ctx, wfCtx)

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "intent",
		Status:     "running",
	})
	ctx = k.injectSecretaryContext(ctx, sessionID, secretary.TargetInputter)
	inputResult, err := k.executeWorker(ctx, sessionID, workflowID, "intent", workflow.RoleInput, k.workflow.InputWorker(), workflow.WorkerInput{
		SessionID: sessionID,
		Prompt:    prompt,
	})
	if err != nil {
		k.handleWorkflowError(sessionID, workflowID, err)
		return
	}
	if inputResult.Intent == nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("inputter returned nil intent"))
		return
	}
	intentSpec := *inputResult.Intent
	if err := k.intents.Save(intentSpec); err != nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("save intent spec: %w", err))
		return
	}
	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "intent",
		Status:     "done",
	})

	// V1-01c: Route based on intent kind.
	// Questions skip Plan/Code/Accept and get a direct LLM answer.
	if intentSpec.IsQuestion() {
		k.emitTasksUpdated(sessionID, workflowID, []model.Task{{
			ID:     "answer",
			Title:  intentSpec.Goal,
			Status: model.TaskStatusCoding,
		}})
		k.runQuestionWorkflow(ctx, sessionID, workflowID, intentSpec, prompt)
		return
	}

	// SA-08: Fast grep-only macro context for Planner.
	// Run synchronously before Plan starts so the Planner's contextSummary
	// includes real code context. Grep-only keeps this under ~200ms on typical
	// repos; RAG/LSP layers are left to the full parallel prefetch below.
	{
		macroOrch := discovery.NewToolOrchestrator(k.tools)
		t0Macro := time.Now()
		k.events.Emit(sessionID, model.EventSearchStarted, model.SearchStartedPayload{
			WorkflowID: workflowID,
			Query:      intentSpec.Goal,
			QueryID:    workflowID + ":macro",
		})
		macroHits, macroErr := macroOrch.Search(ctx, discovery.SearchParams{
			Query:     intentSpec.Goal,
			Kinds:     []string{"grep"},
			Workspace: k.workspace.Root(),
		})
		if macroErr == nil && len(macroHits) > 0 {
			macroSnippets := snippetsFromEvidence(ctx, k, macroHits)
			if len(macroSnippets) > 0 {
				wfCtx.DiscoveryContext = macroSnippets
				wfCtx.Discovery = model.DiscoveryContext{
					Query:      intentSpec.Goal,
					QueryID:    workflowID + ":macro",
					Snippets:   macroSnippets,
					Confidence: discoveryConfidence(macroSnippets),
				}
				ctx = model.WithWorkflowContext(ctx, wfCtx)
				k.events.Emit(sessionID, model.EventSearchFinished, model.SearchFinishedPayload{
					WorkflowID:   workflowID,
					QueryID:      workflowID + ":macro",
					SnippetCount: len(macroSnippets),
					Layers:       []string{"grep"},
					DurationMs:   time.Since(t0Macro).Milliseconds(),
				})
			}
		}
	}

	// T3-03: Start full 3-layer Discovery prefetch — runs in parallel with Plan.
	// The Planner already has macro grep context; this enriches RePlan/Coder.
	type discoveryPrefetch struct {
		discovery model.DiscoveryContext
		err       error
	}
	discoveryCh := make(chan discoveryPrefetch, 1)
	go func() {
		slog.Info("[workflow] starting discovery prefetch", "workflow_id", workflowID)
		k.events.Emit(sessionID, model.EventSearchStarted, model.SearchStartedPayload{
			WorkflowID: workflowID,
			Query:      intentSpec.Goal,
			QueryID:    workflowID + ":prefetch",
		})
		s := k.workflow.Searcher()
		if s == nil {
			s = NewLocalSearcher(k, sessionID, workflowID)
		}
		t0 := time.Now()
		dc, searchErr := s.Search(ctx, intentSpec, nil)
		if searchErr == nil {
			k.events.Emit(sessionID, model.EventSearchFinished, model.SearchFinishedPayload{
				WorkflowID:    workflowID,
				QueryID:       workflowID + ":prefetch",
				SnippetCount:  len(dc.Snippets),
				EvidenceCount: len(dc.Evidence),
				Confidence:    dc.Confidence,
				DurationMs:    time.Since(t0).Milliseconds(),
			})
		}
		discoveryCh <- discoveryPrefetch{discovery: dc, err: searchErr}
	}()

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "plan",
		Status:     "running",
	})
	ctx = k.injectSecretaryContext(ctx, sessionID, secretary.TargetPlanner)
	planResult, err := k.executeWorker(ctx, sessionID, workflowID, "plan", workflow.RolePlanner, k.workflow.PlannerWorker(), workflow.WorkerInput{
		SessionID:  sessionID,
		WorkflowID: workflowID,
		Intent:     intentSpec,
	})
	if err != nil {
		k.handleWorkflowError(sessionID, workflowID, err)
		return
	}
	tasks := planResult.Tasks
	k.emitTasksUpdated(sessionID, workflowID, tasks)
	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "plan",
		Status:     "done",
		TaskCount:  len(tasks),
	})

	// 将所有任务标记为已计划，然后在执行任何操作前验证依赖图
	// （立即捕获 planner 产生的循环）。
	for i := range tasks {
		if tasks[i].Status == "" {
			tasks[i].Status = model.TaskStatusPlanned
		}
	}
	k.emitTasksUpdated(sessionID, workflowID, tasks)
	if len(tasks) == 0 {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("planner returned no tasks"))
		return
	}
	if err := validateTaskDAG(tasks); err != nil {
		k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("invalid task graph: %w", err))
		return
	}

	// T3-03: Consume discovery prefetch.
	prefetched := <-discoveryCh
	var discovery model.DiscoveryContext
	if prefetched.err != nil {
		slog.Warn("[workflow] prefetched discovery failed, running synchronous fallback",
			"workflow_id", workflowID, "error", prefetched.err)
		// Fallback: synchronous discovery WITH tasks for better precision
		searcher := k.workflow.Searcher()
		if searcher == nil {
			searcher = NewLocalSearcher(k, sessionID, workflowID)
		}
		if searcher != nil {
			var searchErr error
			discovery, searchErr = searcher.Search(ctx, intentSpec, tasks)
			if searchErr != nil {
				snippets := k.runDiscovery(ctx, sessionID, workflowID, intentSpec, tasks)
				discovery = model.DiscoveryContext{
					Query:      intentSpec.Goal,
					QueryID:    workflowID + ":discovery-fallback",
					Snippets:   snippets,
					Confidence: discoveryConfidence(snippets),
				}
			}
		}
	} else {
		discovery = prefetched.discovery
		slog.Info("[workflow] discovery prefetch completed",
			"workflow_id", workflowID, "snippets", len(discovery.Snippets))
	}
	discoverySnippets := discovery.Snippets
	searcher := k.workflow.Searcher()
	if searcher == nil {
		searcher = NewLocalSearcher(k, sessionID, workflowID)
	}
	if searcher != nil && shouldRefineDiscovery(discovery, tasks) {
		snippetsBefore := len(discovery.Snippets)
		t0Refine := time.Now()
		refined, refineErr := searcher.Refine(ctx, discovery, discoveryHints(tasks, intentSpec.Goal))
		if refineErr != nil {
			slog.Warn("discovery refine failed, continuing with initial discovery",
				"workflow", workflowID, "error", refineErr)
		} else if len(refined.Snippets) > 0 || len(refined.Evidence) > len(discovery.Evidence) {
			k.events.Emit(sessionID, model.EventSearchRefined, model.SearchRefinedPayload{
				WorkflowID:     workflowID,
				QueryID:        discovery.QueryID,
				SnippetsBefore: snippetsBefore,
				SnippetsAfter:  len(refined.Snippets),
				DurationMs:     time.Since(t0Refine).Milliseconds(),
			})
			discovery = refined
			discoverySnippets = refined.Snippets
		}
	}
	wfCtx.Discovery = discovery
	wfCtx.DiscoveryContext = discoverySnippets

	// Critic step — reviews the plan before execution (Plan → Critic → RePlan → Code).
	// Critic is best-effort: failure logs but does not abort the workflow.
	var critiqueIssues []string
	if critic := k.workflow.Critic(); critic != nil {
		k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
			WorkflowID: workflowID,
			Step:       "critique",
			Status:     "running",
		})
		critique, critiqueErr := critic.Critique(ctx, workflowID, intentSpec, tasks)
		if critiqueErr != nil {
			slog.Warn("[workflow] critic failed, continuing without critique",
				"workflow_id", workflowID, "error", critiqueErr)
			k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
				WorkflowID: workflowID,
				Step:       "critique",
				Status:     "skipped",
			})
		} else {
			slog.Info("[workflow] critique complete",
				"workflow_id", workflowID, "score", critique.Score,
				"approved", critique.Approved, "issues", len(critique.Issues))
			k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
				WorkflowID: workflowID,
				Step:       "critique",
				Status:     "done",
			})
			// Propagate critique issues to the Replanner as refinement hints.
			critiqueIssues = append(critique.Issues, critique.Suggestions...)
		}
	}

	// Inject critique issues and update context.
	wfCtx.CritiqueIssues = critiqueIssues
	ctx = model.WithWorkflowContext(ctx, wfCtx)

	// M10-04: RePlan step — refine high-level tasks into concrete steps.
	// Discovery = WHERE, RePlan = HOW, then Coder = DO (low-level worker).
	// RP-01: Always run RePlan when available, even with empty snippets.
	// Greenfield projects have no existing code, but still benefit from
	// step refinement (e.g. "create go.mod", "write main.go with ...").
	if rp := k.workflow.Replanner(); rp != nil {
		k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
			WorkflowID: workflowID,
			Step:       "replan",
			Status:     "running",
		})
		refined, err := rp.RePlan(ctx, intentSpec, tasks, discoverySnippets)
		if err != nil {
			// Replan is best-effort; log and continue with the original plan.
			slog.Warn("replan failed, continuing with original plan",
				"workflow", workflowID, "error", err)
			k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
				WorkflowID: workflowID,
				Step:       "replan",
				Status:     "skipped",
			})
		} else {
			if len(refined) > 0 {
				tasks = refined
				// Re-validate DAG after refinement.
				if dagErr := validateTaskDAG(tasks); dagErr != nil {
					k.handleWorkflowError(sessionID, workflowID, fmt.Errorf("replan produced invalid DAG: %w", dagErr))
					return
				}
			}
			k.emitTasksUpdated(sessionID, workflowID, tasks)
			k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
				WorkflowID: workflowID,
				Step:       "replan",
				Status:     "done",
			})
		}
	}

	// M11-02: Wrap tasks in a dynamic TaskQueue to enable append/skip/undo
	// during execution. The queue replaces the static for-range loop.
	queue := taskqueue.New(tasks)

	// M11-05: Expose queue on activeWorkflow so the Kernel API (SkipTask/UndoTask)
	// can operate on it while the workflow is running.
	k.mu.Lock()
	if aw, ok := k.activeWorkflows[workflowID]; ok {
		aw.mu.Lock()
		aw.queue = queue
		aw.mu.Unlock()
	}
	k.mu.Unlock()

	// N-08: Execute tasks concurrently using DAG-level scheduling.
	checkpointResult, artifact, llmOutputStr, taskErr := k.runTasksConcurrent(
		ctx, sessionID, workflowID, intentSpec, queue, discoverySnippets)
	if taskErr != nil {
		k.handleWorkflowError(sessionID, workflowID, taskErr)
		return
	}

	// From this point, always use queue.Snapshot() to get the authoritative task list.
	tasks = queue.Snapshot()

	// Guard: ensure checkpoint has a valid terminal status before saga commit.
	// An empty status can occur in edge cases (e.g. all tasks skipped accept).
	if checkpointResult.Status == "" {
		checkpointResult.Status = "pass"
	}

	// L4-07: Saga 事务提交 — 将所有工作流结束状态
	//（checkpoint、turn 摘要、assistant 消息、turn 状态）作为 saga 持久化。
	// 如果任何步骤失败，先前提交的步骤将按相反顺序回滚以防止部分状态。
	turnSummary := k.buildTurnSummary(sessionID, workflowID, intentSpec, tasks, checkpointResult)
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
	clog.Session(sessionID).Info("workflow completed",
		"workflow_id", workflowID,
		"status", checkpointResult.Status,
		"tasks", len(tasks))

	// Post-saga: clear workspace dirty set and notify tools (RAG incremental update).
	dirtyPaths := k.workspace.DirtyPaths()
	k.workspace.ClearAllDirty()
	k.tools.NotifyCheckpointPassed(dirtyPaths)

	// M12-01: Clean up spill directory after workflow completes.
	if wsRoot := k.workspace.Root(); wsRoot != "" {
		if cleanErr := toolruntime.CleanupSpillDir(wsRoot); cleanErr != nil {
			slog.Warn("[kernel] spill cleanup failed", "error", cleanErr)
		}
	}

	// Saga 后事件（非致命，即发即弃）。
	k.emitTasksUpdated(sessionID, workflowID, tasks)
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

	// M8-11 / N-07: 从 worker 原始 LLM 输出中零 LLM 成本提取洞察。
	// 非致命：保存失败不应中止工作流。
	if llmOutputStr != "" {
		now := time.Now().UTC()
		for _, ins := range insight.ExtractInsights(workflowID, llmOutputStr, now) {
			ins.SessionID = sessionID
			if saveErr := k.insights.Save(ins); saveErr != nil {
				slog.Warn("[workflow] failed to save insight", "workflow_id", workflowID, "error", saveErr)
			}
		}
	}

	// Secretary AfterTurn: LLM-powered post-turn analysis (async, non-fatal).
	// Upgrades the regex-based insight extraction with Light-model intelligence.
	if k.secretary != nil && k.secretary.HasLLM() {
		go func() {
			afterCtx, afterCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer afterCancel()

			taskTitles := make([]string, len(tasks))
			for i, t := range tasks {
				taskTitles[i] = t.Title
			}

			result := k.secretary.AfterTurn(afterCtx, sessionID, secretary.AfterTurnInput{
				WorkflowID:   workflowID,
				Goal:         intentSpec.Goal,
				TaskTitles:   taskTitles,
				WorkerOutput: llmOutputStr,
				Status:       checkpointResult.Status,
			})

			// Save LLM-extracted insights (supplements regex extraction above).
			now := time.Now().UTC()
			for _, ins := range result.Insights {
				modelIns := insight.Insight{
					ID:         fmt.Sprintf("sec-%s-%d", workflowID, now.UnixNano()),
					SessionID:  sessionID,
					Category:   insight.Category(ins.Category),
					Title:      ins.Title,
					Content:    ins.Content,
					Confidence: ins.Confidence,
					CreatedAt:  now,
					UpdatedAt:  now,
				}
				if saveErr := k.insights.Save(modelIns); saveErr != nil {
					slog.Warn("[secretary] failed to save LLM insight", "error", saveErr)
				}
			}

			// Regenerate .coden/MEMORY.md from all active insights for this session.
			// This gives workers a persistent, human-readable memory file they can
			// reference across turns without re-reading raw history.
			if wsRoot := k.workspace.Root(); wsRoot != "" {
				if memErr := insight.WriteMemoryFile(wsRoot, sessionID, k.insights); memErr != nil {
					slog.Warn("[secretary] failed to write memory file", "error", memErr)
				} else {
					slog.Info("[secretary] memory file updated", "path", wsRoot+"/.coden/MEMORY.md")
				}
			}
		}()
	}
}
