package kernel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// toolCtx enriches ctx with workflow/session IDs so the tool runtime can
// attach them to artifact auto-saves and other per-execution tracking.
func toolCtx(ctx context.Context, workflowID, sessionID string) context.Context {
	return toolruntime.ContextWithIDs(ctx, workflowID, sessionID)
}

// executeToolPlan 执行 Coder 的工具计划并收集结果 artifact。
// allowedPaths 限制 mutation 调用（write_file/edit_file）可针对的工作区路径。
// 空切片表示无限制。返回 artifact 和 beforeState 快照（路径→原始字节），
// 用于失败时回滚。
func (k *Kernel) executeToolPlan(ctx context.Context, sessionID, workflowID, workerID string, allowedPaths []string, calls []workflow.ToolCall) (model.Artifact, map[string][]byte, error) {
	var artifact model.Artifact
	beforeState := make(map[string][]byte)

	for _, call := range calls {
		k.events.Emit(sessionID, model.EventToolStarted, model.ToolStartedPayload{
			WorkflowID: workflowID,
			ToolCallID: call.ToolCallID,
			WorkerID:   workerID,
			Tool:       call.Request.Kind,
			Path:       call.Request.Path,
		})
		toolStarted := time.Now()
		if err := validateToolPath(call.Request.Kind, call.Request.Path); err != nil {
			k.events.Emit(sessionID, model.EventToolFinished, model.ToolFinishedPayload{
				WorkflowID: workflowID,
				ToolCallID: call.ToolCallID,
				WorkerID:   workerID,
				Tool:       call.Request.Kind,
				Path:       call.Request.Path,
				Status:     "error",
				Summary:    err.Error(),
				Detail:     err.Error(),
				DurationMS: durationMillis(toolStarted),
			})
			return model.Artifact{}, beforeState, err
		}
		if scopeWarn, scopeErr := validateToolScope(call.Request, allowedPaths); scopeErr != nil {
			k.events.Emit(sessionID, model.EventToolFinished, model.ToolFinishedPayload{
				WorkflowID: workflowID,
				ToolCallID: call.ToolCallID,
				WorkerID:   workerID,
				Tool:       call.Request.Kind,
				Path:       call.Request.Path,
				Status:     "error",
				Summary:    scopeErr.Error(),
				Detail:     scopeErr.Error(),
				DurationMS: durationMillis(toolStarted),
			})
			return model.Artifact{}, beforeState, scopeErr
		} else if scopeWarn != "" {
			k.events.Emit(sessionID, model.EventWorkerMessage, model.WorkerMessagePayload{
				WorkflowID: workflowID,
				Kind:       "warn",
				Content:    scopeWarn,
			})
		}
		if err := k.authorizeToolCall(sessionID, workflowID, workerID, call, toolStarted); err != nil {
			return model.Artifact{}, beforeState, err
		}

		var result toolruntime.Result
		if call.Executed {
			result = call.ExecResult
			// Capture beforeState from the pre-executed result so that rollback
			// can restore files written/edited by the agentic loop.
			if (call.Request.Kind == "write_file" || call.Request.Kind == "edit_file") && call.Request.Path != "" {
				if _, already := beforeState[call.Request.Path]; !already {
					beforeState[call.Request.Path] = []byte(call.ExecResult.Before)
				}
			}
		} else {
			if (call.Request.Kind == "write_file" || call.Request.Kind == "edit_file") && call.Request.Path != "" {
				if _, already := beforeState[call.Request.Path]; !already {
					if existing, readErr := k.workspace.Read(call.Request.Path); readErr == nil {
						beforeState[call.Request.Path] = existing
					} else {
						beforeState[call.Request.Path] = nil
					}
				}
			}
			var err error
			// Inject workflow/session IDs so the tool runtime can tag artifact
			// auto-saves with the correct execution context (avoids "unknown" IDs).
			result, err = k.tools.Execute(toolCtx(ctx, workflowID, sessionID), call.Request)
			if err != nil {
				status := "failed"
				artifactPath := call.Request.Path
				k.events.Emit(sessionID, model.EventToolFinished, model.ToolFinishedPayload{
					WorkflowID: workflowID,
					ToolCallID: call.ToolCallID,
					WorkerID:   workerID,
					Tool:       call.Request.Kind,
					Path:       artifactPath,
					Status:     status,
					Summary:    err.Error(),
					Detail:     err.Error(),
					DurationMS: durationMillis(toolStarted),
				})
				if _, saveErr := k.objects.SaveModify(workflowID, artifactPath, k.mainDBPath, toolAuditPayload(call, toolruntime.Result{}, status, "", err.Error())); saveErr != nil {
					return model.Artifact{}, beforeState, fmt.Errorf("%w (save object: %v)", err, saveErr)
				}
				return model.Artifact{}, beforeState, err
			}
			if err := shellResultError(call.Request, result); err != nil {
				status := "failed"
				artifactPath := result.ArtifactPath
				if artifactPath == "" {
					artifactPath = call.Request.Path
				}
				k.events.Emit(sessionID, model.EventToolFinished, model.ToolFinishedPayload{
					WorkflowID: workflowID,
					ToolCallID: call.ToolCallID,
					WorkerID:   workerID,
					Tool:       call.Request.Kind,
					Path:       artifactPath,
					Status:     status,
					Summary:    result.Summary,
					Detail:     toolEventDetail(call.Request, result),
					ExitCode:   result.ExitCode,
					DurationMS: durationMillis(toolStarted),
				})
				if _, saveErr := k.objects.SaveModify(workflowID, artifactPath, k.mainDBPath, toolAuditPayload(call, result, status, "", "")); saveErr != nil {
					return model.Artifact{}, beforeState, fmt.Errorf("%w (save object: %v)", err, saveErr)
				}
				return model.Artifact{}, beforeState, err
			}
		}

		status := toolStatus(call.Request.Kind)
		artifactPath := result.ArtifactPath
		if artifactPath == "" {
			artifactPath = call.Request.Path
		}

		k.events.Emit(sessionID, model.EventToolFinished, model.ToolFinishedPayload{
			WorkflowID: workflowID,
			ToolCallID: call.ToolCallID,
			WorkerID:   workerID,
			Tool:       call.Request.Kind,
			Path:       artifactPath,
			Status:     status,
			Summary:    result.Summary,
			Detail:     toolEventDetail(call.Request, result),
			ExitCode:   result.ExitCode,
			DurationMS: durationMillis(toolStarted),
		})

		if _, err := k.objects.SaveModify(workflowID, artifactPath, k.mainDBPath, toolAuditPayload(call, result, status, "", "")); err != nil {
			return model.Artifact{}, beforeState, fmt.Errorf("save object: %w", err)
		}

		if change, ok := workspaceChangeFor(workflowID, call.Request.Kind, artifactPath); ok {
			k.recordWorkspaceChange(sessionID, change)
			k.events.Emit(sessionID, model.EventWorkspaceChanged, change)
		}

		if result.ArtifactPath != "" {
			artifact.Path = result.ArtifactPath
			artifact.Summary = result.Summary
		} else if artifact.Path == "" && artifactPath != "" {
			artifact.Path = artifactPath
			artifact.Summary = result.Summary
		}
	}

	return artifact, beforeState, nil
}

// rollbackFiles 将工作区文件恢复到 mutation 前的状态。
// 之前不存在的文件（beforeState[path] == nil）将被删除。
// 回滚错误会被记录但不会导致工作流失败。
func (k *Kernel) rollbackFiles(sessionID, workflowID string, beforeState map[string][]byte) {
	for path, before := range beforeState {
		if before == nil {
			if err := k.workspace.Delete(path); err != nil {
				k.events.Emit(sessionID, model.EventWorkerMessage, model.WorkerMessagePayload{
					WorkflowID: workflowID,
					Kind:       "warn",
					Content:    fmt.Sprintf("rollback: delete %s failed: %v", path, err),
				})
			}
			continue
		}
		if _, err := k.workspace.Write(path, before); err != nil {
			k.events.Emit(sessionID, model.EventWorkerMessage, model.WorkerMessagePayload{
				WorkflowID: workflowID,
				Kind:       "warn",
				Content:    fmt.Sprintf("rollback: restore %s failed: %v", path, err),
			})
		}
	}
	if len(beforeState) > 0 {
		k.events.Emit(sessionID, model.EventWorkerMessage, model.WorkerMessagePayload{
			WorkflowID: workflowID,
			Kind:       "info",
			Content:    fmt.Sprintf("rollback: restored %d file(s) to pre-mutation state", len(beforeState)),
		})
	}
}

// authorizeToolCall 授权工具调用（目前仅检查 run_shell 权限）。
func (k *Kernel) authorizeToolCall(sessionID, workflowID, workerID string, call workflow.ToolCall, started time.Time) error {
	if call.Request.Kind != "run_shell" {
		return nil
	}
	k.mu.Lock()
	allowShell := k.allowShell
	k.mu.Unlock()
	if allowShell {
		return nil
	}

	status := "denied"
	err := fmt.Errorf("run_shell requires explicit approval (--allow-shell)")
	k.events.Emit(sessionID, model.EventToolFinished, model.ToolFinishedPayload{
		WorkflowID: workflowID,
		ToolCallID: call.ToolCallID,
		WorkerID:   workerID,
		Tool:       call.Request.Kind,
		Status:     status,
		Summary:    err.Error(),
		Detail:     err.Error(),
		DurationMS: durationMillis(started),
	})
	if _, saveErr := k.objects.SaveModify(workflowID, call.Request.Path, k.mainDBPath, toolAuditPayload(call, toolruntime.Result{}, status, "shell_requires_explicit_approval", err.Error())); saveErr != nil {
		return fmt.Errorf("%w (save object: %v)", err, saveErr)
	}
	return err
}

// shellResultError 为失败的 shell 命令构造错误。
func shellResultError(req toolruntime.Request, result toolruntime.Result) error {
	if req.Kind != "run_shell" || result.ExitCode == 0 {
		return nil
	}
	if result.ErrorHuman != "" {
		return fmt.Errorf("[%s] %s (exit %d)", result.ErrorClass, result.ErrorHuman, result.ExitCode)
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Output)
	}
	if detail == "" {
		return fmt.Errorf("run_shell exited with code %d", result.ExitCode)
	}
	return fmt.Errorf("run_shell exited with code %d: %s", result.ExitCode, detail)
}

// toolEventDetail 为工具事件生成详细内容。
func toolEventDetail(req toolruntime.Request, result toolruntime.Result) string {
	switch req.Kind {
	case "write_file", "edit_file":
		return diffPreview(result.Diff, 6)
	case "read_file", "list_dir", "search":
		return outputPreview(result.Output, 8)
	case "run_shell":
		if result.ExitCode == 0 {
			combined := strings.TrimSpace(result.Output)
			if errText := strings.TrimSpace(result.Stderr); errText != "" {
				if combined != "" {
					combined += "\n"
				}
				combined += errText
			}
			return outputPreview(combined, 8)
		}
		if detail := strings.TrimSpace(result.Stderr); detail != "" {
			return detail
		}
		return strings.TrimSpace(result.Output)
	default:
		return ""
	}
}

// diffPreview 返回 diff 的前 maxLines 行。
func diffPreview(diff string, maxLines int) string {
	diff = strings.TrimSpace(diff)
	if diff == "" || maxLines <= 0 {
		return ""
	}
	lines := strings.Split(diff, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

// outputPreview 返回输出的前 maxLines 行。
func outputPreview(text string, maxLines int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxLines <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

// toolAuditPayload 构造工具审计 payload。
func toolAuditPayload(call workflow.ToolCall, result toolruntime.Result, status, policyRule, errorMessage string) map[string]any {
	request := map[string]any{
		"kind":    call.Request.Kind,
		"path":    call.Request.Path,
		"dir":     call.Request.Dir,
		"query":   call.Request.Query,
		"command": call.Request.Command,
		"content": call.Request.Content,
	}
	response := map[string]any{
		"artifact_path": result.ArtifactPath,
		"summary":       result.Summary,
		"stdout":        result.Output,
		"stderr":        result.Stderr,
		"exit_code":     result.ExitCode,
		"before":        result.Before,
		"after":         result.After,
		"diff":          result.Diff,
	}
	payload := map[string]any{
		"schema":       "tool_audit.v1",
		"tool_call_id": call.ToolCallID,
		"tool":         call.Request.Kind,
		"status":       status,
		"request":      request,
		"response":     response,
	}
	if policyRule != "" || call.Request.Kind == "run_shell" {
		payload["policy"] = map[string]any{
			"approved": policyRule == "",
			"rule":     policyRule,
		}
	}
	if errorMessage != "" {
		payload["error"] = errorMessage
	}
	return payload
}

// workspaceChangeFor 为工作区变更创建 payload。
func workspaceChangeFor(workflowID, kind, path string) (model.WorkspaceChangedPayload, bool) {
	switch kind {
	case "write_file", "edit_file":
		return model.WorkspaceChangedPayload{
			WorkflowID: workflowID,
			Path:       path,
			Operation:  "write",
		}, true
	default:
		return model.WorkspaceChangedPayload{}, false
	}
}

// toolStatus 返回工具的状态字符串。
func toolStatus(kind string) string {
	switch kind {
	case "write_file":
		return "written"
	case "edit_file":
		return "edited"
	case "read_file":
		return "read"
	case "list_dir":
		return "listed"
	case "search":
		return "searched"
	case "run_shell":
		return "executed"
	default:
		return "ok"
	}
}
