package kernel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	clog "github.com/mingzhi1/coden/internal/log"
)

// Turn status constants. "fail" comes from CheckpointResult.Status (acceptor
// semantic: code was rejected) while "failed" comes from handleWorkflowError
// (system error: execution threw an error). Both are terminal states.
const (
	TurnStatusRunning  = "running"
	TurnStatusPass     = "pass"
	TurnStatusFail     = "fail"
	TurnStatusFailed   = "failed"
	TurnStatusCanceled = "canceled"
	TurnStatusCrashed  = "crashed"
)

// buildWorkflowContext 获取 workers 需要的上下文数据。
func (k *Kernel) buildWorkflowContext(_ context.Context, sessionID string) model.WorkflowContext {
	history := k.messages.List(sessionID, 20)

	files, _ := k.workspace.ListFiles("", 200)

	const prevTurnsLimit = 5
	rawTurns := k.turnSummaries.ListBySession(sessionID, prevTurnsLimit)
	previousTurns := make([]model.TurnSummary, len(rawTurns))
	for i, t := range rawTurns {
		previousTurns[len(rawTurns)-1-i] = t
	}

	accumChanges := buildAccumChanges(previousTurns)

	topInsights := k.formatTopInsights(sessionID)

	gitStatus := ""
	if k.git != nil {
		gitStatus = k.git.Snapshot().FormatForPrompt()
	}

	dirtyPaths := k.workspace.DirtyPaths()
	sort.Strings(dirtyPaths)

	return model.WorkflowContext{
		History:           history,
		FileTree:          files,
		WorkspaceRoot:     k.workspace.Root(),
		PreviousTurns:     previousTurns,
		AccumChanges:      accumChanges,
		TopInsights:       topInsights,
		GitStatus:         gitStatus,
		DirtyPaths:        dirtyPaths,
		ToolsPrompt:       k.inventoryToolsPrompt,
		EnvironmentPrompt: k.inventoryEnvPrompt,
	}
}

// buildAccumChanges 从所有先前的 turns 构建去重的 FileChange 列表。
func buildAccumChanges(turns []model.TurnSummary) []model.FileChange {
	seen := make(map[string]string, 16)
	for _, t := range turns {
		for _, fc := range t.ChangedFiles {
			if fc.Path != "" {
				seen[fc.Path] = fc.Op
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]model.FileChange, 0, len(seen))
	for path, op := range seen {
		out = append(out, model.FileChange{Path: path, Op: op})
	}
	return out
}

// formatTopInsights 加载会话的前 5 个活跃 insights。
func (k *Kernel) formatTopInsights(sessionID string) string {
	const topK = 5
	ins := k.insights.ListBySession(sessionID, topK)
	if len(ins) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Key insights from previous analysis\n")
	for _, item := range ins {
		if item.SupersededBy != "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("- [%s] **%s**: %s\n", item.Category, item.Title, item.Content))
	}
	return sb.String()
}

// commitWorkflowSaga 实现 L4-07: Saga 事务提交。
func (k *Kernel) commitWorkflowSaga(sessionID, workflowID string, cp model.CheckpointResult, ts model.TurnSummary, msg model.Message) error {
	type sagaStep struct {
		name       string
		action     func() error
		compensate func()
	}

	steps := []sagaStep{
		{
			name:   "save_checkpoint",
			action: func() error { return k.checkpoints.Save(cp) },
			compensate: func() {
				k.checkpoints.Delete(cp.WorkflowID, cp.SessionID)
			},
		},
		{
			name:   "save_turn_summary",
			action: func() error { return k.turnSummaries.Save(ts) },
			compensate: func() {
				k.turnSummaries.Delete(ts.ID)
			},
		},
		{
			name:   "save_assistant_message",
			action: func() error { return k.messages.Save(msg) },
			compensate: func() {
				k.messages.Delete(msg.ID)
			},
		},
		{
			name: "update_turn_status",
			action: func() error {
				if k.secretary != nil {
					if guardErr := k.secretary.ValidateTurnTransition(sessionID, workflowID, TurnStatusRunning, cp.Status); guardErr != nil {
						return guardErr
					}
				}
				return k.turns.UpdateStatus(workflowID, cp.Status, time.Now().UTC())
			},
			compensate: func() {
				_ = k.turns.UpdateStatus(workflowID, TurnStatusFailed, time.Now().UTC())
			},
		},
	}

	var committed []int
	for i, step := range steps {
		if err := step.action(); err != nil {
			for j := len(committed) - 1; j >= 0; j-- {
				steps[committed[j]].compensate()
			}
			return fmt.Errorf("saga step %q failed: %w", step.name, err)
		}
		committed = append(committed, i)
	}
	return nil
}

// buildTurnSummary 从完成的工作流状态构建 TurnSummary。
func (k *Kernel) buildTurnSummary(sessionID, workflowID string, intent model.IntentSpec, tasks []model.Task, cp model.CheckpointResult) model.TurnSummary {
	taskResults := make([]model.TaskResult, 0, len(tasks))
	for _, t := range tasks {
		taskResults = append(taskResults, model.TaskResult{
			TaskID:   t.ID,
			Title:    t.Title,
			Status:   t.Status,
			Attempts: t.Attempts,
		})
	}

	changedFiles := k.fileChangesForWorkflow(sessionID, workflowID)

	return model.TurnSummary{
		ID:           nextKernelID("ts"),
		TurnID:       workflowID,
		SessionID:    sessionID,
		Intent:       intent,
		TaskResults:  taskResults,
		ChangedFiles: changedFiles,
		Checkpoint:   cp,
		CreatedAt:    time.Now().UTC(),
	}
}

// fileChangesForWorkflow 将记录的工作区变更转换为 FileChange 结构。
func (k *Kernel) fileChangesForWorkflow(sessionID, workflowID string) []model.FileChange {
	k.mu.Lock()
	changes := append([]model.WorkspaceChangedPayload(nil), k.workspaceChanges[sessionID]...)
	k.mu.Unlock()

	seen := make(map[string]struct{}, len(changes))
	var out []model.FileChange
	for _, change := range changes {
		if change.WorkflowID != workflowID {
			continue
		}
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}

		op := model.FileChangeModified
		switch change.Operation {
		case "create", "write":
			op = model.FileChangeCreated
		case "delete":
			op = model.FileChangeDeleted
		}
		out = append(out, model.FileChange{
			Path: path,
			Op:   op,
		})
	}
	return out
}

// recordWorkspaceChange 记录工作区变更。
func (k *Kernel) recordWorkspaceChange(sessionID string, change model.WorkspaceChangedPayload) {
	k.mu.Lock()
	k.workspaceChanges[sessionID] = append(k.workspaceChanges[sessionID], change)
	// N-02: 滑动窗口限制，防止无界增长
	const maxWorkspaceChanges = 200
	if len(k.workspaceChanges[sessionID]) > maxWorkspaceChanges {
		k.workspaceChanges[sessionID] = k.workspaceChanges[sessionID][len(k.workspaceChanges[sessionID])-maxWorkspaceChanges:]
	}
	k.mu.Unlock()
}

// ensureSession 确保会话存在。
func (k *Kernel) ensureSession(sessionID string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.ensureSessionLocked(sessionID)
}

// ensureSessionLocked 确保会话存在（在持有 k.mu 时调用）。
func (k *Kernel) ensureSessionLocked(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("sessionID is required")
	}
	_, err := k.createSessionLocked(sessionID)
	return err
}

// createSessionLocked 创建新会话（在持有 k.mu 时调用）。
func (k *Kernel) createSessionLocked(sessionID string) (model.Session, error) {
	if sessionID == "" {
		sessionID = nextKernelID("sess")
	}
	if existing, ok := k.sessionStore.Get(sessionID); ok {
		return existing, nil
	}
	projectID, projectRoot := k.projectMetadataForSession(sessionID)
	item := model.Session{
		ID:          sessionID,
		ProjectID:   projectID,
		ProjectRoot: projectRoot,
		CreatedAt:   time.Now().UTC(),
	}
	if err := k.sessionStore.Save(item); err != nil {
		return model.Session{}, fmt.Errorf("save session: %w", err)
	}
	// Open a per-session log file for workflow lifecycle events.
	clog.OpenSession(sessionID)
	k.events.Emit(sessionID, model.EventSessionCreated, model.SessionCreatedPayload{
		SessionID:   sessionID,
		ProjectID:   item.ProjectID,
		ProjectRoot: item.ProjectRoot,
	})
	return item, nil
}

// projectMetadataForSession 返回会话的项目元数据。
func (k *Kernel) projectMetadataForSession(sessionID string) (projectID, projectRoot string) {
	return sessionID, k.workspace.Root()
}

// handleWorkflowError 处理工作流错误。
func (k *Kernel) handleWorkflowError(sessionID, workflowID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		if k.secretary != nil {
			if guardErr := k.secretary.ValidateTurnTransition(sessionID, workflowID, TurnStatusRunning, TurnStatusCanceled); guardErr != nil {
				clog.Session(sessionID).Warn("secretary blocked turn transition", "error", guardErr)
			}
		}
		if statusErr := k.turns.UpdateStatus(workflowID, TurnStatusCanceled, time.Now().UTC()); statusErr != nil {
			clog.Session(sessionID).Warn("failed to update turn status to canceled", "workflow_id", workflowID, "error", statusErr)
		}
		k.events.Emit(sessionID, model.EventWorkflowCanceled, model.WorkflowCanceledPayload{
			WorkflowID: workflowID,
			Reason:     "canceled",
		})
		clog.Session(sessionID).Warn("workflow canceled", "workflow_id", workflowID)
		return err
	}
	if k.secretary != nil {
		if guardErr := k.secretary.ValidateTurnTransition(sessionID, workflowID, TurnStatusRunning, TurnStatusFailed); guardErr != nil {
			clog.Session(sessionID).Warn("secretary blocked turn transition", "error", guardErr)
		}
	}
	if statusErr := k.turns.UpdateStatus(workflowID, TurnStatusFailed, time.Now().UTC()); statusErr != nil {
		clog.Session(sessionID).Warn("failed to update turn status to failed", "workflow_id", workflowID, "error", statusErr)
	}
	k.events.Emit(sessionID, model.EventWorkflowFailed, model.WorkflowFailedPayload{
		WorkflowID: workflowID,
		Reason:     err.Error(),
		Error:      err.Error(),
	})
	clog.Session(sessionID).Error("workflow failed", "workflow_id", workflowID, "error", err)
	return err
}

// buildRetryFeedback 构建重试反馈字符串。
func buildRetryFeedback(checkpoint model.CheckpointResult, artifact model.Artifact, retryCtx *model.RetryContext, oscillating bool) string {
	var sb strings.Builder
	sb.WriteString("PREVIOUS ATTEMPT REJECTED BY ACCEPTOR.\n\n")
	sb.WriteString(fmt.Sprintf("Artifact path: %s\n", artifact.Path))
	if artifact.Summary != "" {
		sb.WriteString(fmt.Sprintf("Artifact summary: %s\n", artifact.Summary))
	}
	if len(checkpoint.Evidence) > 0 {
		sb.WriteString("\nRejection reasons:\n")
		for _, ev := range checkpoint.Evidence {
			sb.WriteString(fmt.Sprintf("- %s\n", ev))
		}
	}
	if checkpoint.FixGuidance != "" {
		sb.WriteString("\n## FIX GUIDANCE (from acceptor)\n")
		sb.WriteString(checkpoint.FixGuidance)
		sb.WriteString("\n")
	}

	if retryCtx != nil && len(retryCtx.GuidanceHistory) > 1 {
		sb.WriteString("\n## GUIDANCE HISTORY (all prior rejections)\n")
		for _, entry := range retryCtx.GuidanceHistory {
			sb.WriteString(fmt.Sprintf("### Attempt %d\n", entry.Attempt+1))
			if entry.Evidence != "" {
				sb.WriteString(fmt.Sprintf("Evidence: %s\n", entry.Evidence))
			}
			if entry.FixGuidance != "" {
				sb.WriteString(fmt.Sprintf("Guidance: %s\n", entry.FixGuidance))
			}
		}
	}

	if oscillating {
		sb.WriteString("\n## ⚠ OSCILLATION DETECTED\n")
		sb.WriteString("The same error occurred in two consecutive attempts. You are stuck in a loop.\n")
		sb.WriteString("Do NOT repeat the previous approach. Instead:\n")
		sb.WriteString("  1. Read the file again in full to confirm the actual current content.\n")
		sb.WriteString("  2. Apply a fundamentally different fix strategy.\n")
		sb.WriteString("  3. If the error is environment-related (missing binary, wrong PATH), report it in your output rather than retrying the same command.\n")
	}

	sb.WriteString("\nPlease fix the issues and regenerate. Read the previous artifact if needed.")
	return sb.String()
}

// buildAssistantCompletionMessage 构建 assistant 完成消息。
func (k *Kernel) buildAssistantCompletionMessage(sessionID string, checkpointResult model.CheckpointResult, artifact model.Artifact) string {
	status := strings.TrimSpace(checkpointResult.Status)
	if status == "" {
		status = "completed"
	}

	lines := []string{fmt.Sprintf("Completed with status `%s`.", status)}

	changedPaths := k.changedPathsForWorkflow(sessionID, checkpointResult.WorkflowID)
	if len(changedPaths) > 0 {
		lines = append(lines, "", "Changed files:")
		for _, path := range changedPaths {
			lines = append(lines, fmt.Sprintf("- `%s`", path))
		}
	}

	artifactPath := strings.TrimSpace(artifact.Path)
	if artifactPath == "" && len(checkpointResult.ArtifactPaths) > 0 {
		artifactPath = strings.TrimSpace(checkpointResult.ArtifactPaths[0])
	}
	if artifactPath != "" {
		lines = append(lines, "", fmt.Sprintf("Primary artifact: `%s`", artifactPath))
	}

	if summary := strings.TrimSpace(artifact.Summary); summary != "" {
		lines = append(lines, fmt.Sprintf("Artifact summary: %s", summary))
	}

	if len(checkpointResult.Evidence) > 0 {
		lines = append(lines, "", "Evidence:")
		for _, item := range checkpointResult.Evidence {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s", item))
		}
	}

	return strings.Join(lines, "\n")
}

// changedPathsForWorkflow 返回工作流变更的文件路径。
func (k *Kernel) changedPathsForWorkflow(sessionID, workflowID string) []string {
	if sessionID == "" || workflowID == "" {
		return nil
	}

	k.mu.Lock()
	changes := append([]model.WorkspaceChangedPayload(nil), k.workspaceChanges[sessionID]...)
	k.mu.Unlock()

	seen := make(map[string]struct{}, len(changes))
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.WorkflowID != workflowID {
			continue
		}
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

// runSuccessCmd executes an arbitrary shell command in workspaceRoot (L3-05).
// Used by the kernel to verify task.SuccessCmd before calling the LLM acceptor.
// Returns (combined stdout+stderr output, ok): ok=true means exit code 0.
func runSuccessCmd(ctx context.Context, workspaceRoot, cmd string) (string, bool) {
	if workspaceRoot == "" || cmd == "" {
		return "", true
	}
	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(runCtx, "cmd", "/C", cmd)
	} else {
		c = exec.CommandContext(runCtx, "sh", "-c", cmd)
	}
	c.Dir = workspaceRoot
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out

	err := c.Run()
	output := strings.TrimSpace(out.String())
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("success_cmd timed out after 120s: %s", cmd), false
		}
		return output, false
	}
	return output, true
}

// truncateCmdOutput caps command output at max bytes for inclusion in evidence.
func truncateCmdOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
