package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMAcceptor uses an LLM to verify that the artifact meets the success criteria.
// When an Executor is provided, it reads the actual artifact content before judging.
type LLMAcceptor struct {
	chatter  Chatter
	executor toolruntime.Executor // optional; reads artifact content
	msgBuffer
}

func NewLLMAcceptor(chatter Chatter) *LLMAcceptor {
	return &LLMAcceptor{chatter: chatter}
}

// NewInformedAcceptor creates an acceptor that reads artifact content before judging.
func NewInformedAcceptor(chatter Chatter, executor toolruntime.Executor) *LLMAcceptor {
	return &LLMAcceptor{chatter: chatter, executor: executor}
}

func (a *LLMAcceptor) Accept(ctx context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact) (model.CheckpointResult, error) {
	wc := model.WorkflowContextFrom(ctx)
	_ = wc // used for reading files below

	// Build/lint verification is handled by task.SuccessCmd in the kernel
	// BEFORE this method is called. The Planner decides the right commands
	// (go build, cargo check, npm test, etc.) per task. The Acceptor only
	// performs LLM-based code review.

	// --- LLM acceptance review ---
	systemPrompt := prompts.Acceptor()

	// Helper: strip workspace root prefix so read_file gets a relative path.
	// write_file returns absolute paths (e.g. D:\_home\...\workspace\calc.go)
	// but workspace.Read expects workspace-relative paths (e.g. calc.go).
	toRelative := func(p string) string {
		root := wc.WorkspaceRoot
		if root == "" {
			return p
		}
		// Normalize separators for comparison.
		norm := strings.ReplaceAll(p, "\\", "/")
		normRoot := strings.ReplaceAll(root, "\\", "/")
		if !strings.HasSuffix(normRoot, "/") {
			normRoot += "/"
		}
		if strings.HasPrefix(norm, normRoot) {
			return norm[len(normRoot):]
		}
		return p
	}

	artifactContent := ""
	artifactReadPath := toRelative(artifact.Path)
	if a.executor != nil && artifactReadPath != "" {
		result, readErr := a.executor.Execute(ctx, toolruntime.Request{
			Kind: "read_file",
			Path: artifactReadPath,
		})
		if readErr == nil {
			artifactContent = truncateOutput(result.Output, 4000)
		} else {
			a.push("warn", "acceptor", fmt.Sprintf("failed to read artifact %s: %v", artifactReadPath, readErr))
		}
	}

	// Read additional workspace-changed files beyond the primary artifact.
	// This gives the LLM visibility into test files and other generated code.
	var additionalFiles strings.Builder
	if a.executor != nil {
		const maxExtraFiles = 3
		const extraBudget = 4000 // chars total for additional files
		extraUsed := 0
		seen := map[string]bool{artifactReadPath: true}
		for _, change := range wc.AccumChanges {
			changePath := toRelative(change.Path)
			if seen[changePath] || change.Op == "deleted" {
				continue
			}
			seen[changePath] = true
			if len(seen) > maxExtraFiles+1 {
				break
			}
			result, readErr := a.executor.Execute(ctx, toolruntime.Request{
				Kind: "read_file",
				Path: changePath,
			})
			if readErr != nil {
				continue
			}
			content := truncateOutput(result.Output, 2000)
			if extraUsed+len(content) > extraBudget {
				break
			}
			additionalFiles.WriteString(fmt.Sprintf("\n\nAdditional file: %s\n```\n%s\n```", changePath, content))
			extraUsed += len(content)
		}
	}

	// Build execution evidence section from the coder's tool results.
	var evidenceSection string
	if len(artifact.Evidence) > 0 {
		evidenceSection = "\n\nCoder execution results (verified):\n" + bulletList(artifact.Evidence)
	}

	var userMsg string
	extraContent := additionalFiles.String()
	if artifactContent != "" {
		userMsg = fmt.Sprintf(
			"Goal: %s\n\nSuccess criteria:\n%s\n\nArtifact path: %s\nArtifact summary: %s%s\n\nArtifact content:\n```\n%s\n```%s",
			intent.Goal,
			bulletList(intent.SuccessCriteria),
			artifact.Path,
			artifact.Summary,
			evidenceSection,
			artifactContent,
			extraContent,
		)
	} else {
		userMsg = fmt.Sprintf(
			"Goal: %s\n\nSuccess criteria:\n%s\n\nArtifact path: %s\nArtifact summary: %s%s%s",
			intent.Goal,
			bulletList(intent.SuccessCriteria),
			artifact.Path,
			artifact.Summary,
			evidenceSection,
			extraContent,
		)
	}

	reply, err := RecoverableChat(ctx, a.chatter, RoleAcceptor, []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}, defaultRecoveryConfig())
	if err != nil {
		return model.CheckpointResult{
			WorkflowID:    workflowID,
			SessionID:     intent.SessionID,
			Status:        "fail",
			ArtifactPaths: []string{artifact.Path},
			Evidence:      []string{"acceptor llm unavailable: " + err.Error()},
			FixGuidance:   "The acceptance reviewer could not reach the LLM. Verify the code compiles and meets the success criteria manually, then retry.",
			CreatedAt:     time.Now(),
		}, nil
	}

	var parsed struct {
		Status      string   `json:"status"`
		Evidence    []string `json:"evidence"`
		FixGuidance string   `json:"fix_guidance"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &parsed); err != nil || parsed.Status == "" {
		return model.CheckpointResult{
			WorkflowID:    workflowID,
			SessionID:     intent.SessionID,
			Status:        "fail",
			ArtifactPaths: []string{artifact.Path},
			Evidence:      []string{"acceptor could not parse LLM response", "raw response: " + truncateOutput(reply, 200)},
			FixGuidance:   "The acceptance reviewer received an unparseable response from the LLM. Verify the code meets the success criteria and retry.",
			CreatedAt:     time.Now(),
		}, nil
	}
	if parsed.Status != "pass" && parsed.Status != "fail" {
		return model.CheckpointResult{
			WorkflowID:    workflowID,
			SessionID:     intent.SessionID,
			Status:        "fail",
			ArtifactPaths: []string{artifact.Path},
			Evidence:      []string{"acceptor received invalid status from LLM: " + parsed.Status},
			FixGuidance:   "The acceptance reviewer received an unexpected status value. Verify the code meets the success criteria and retry.",
			CreatedAt:     time.Now(),
		}, nil
	}
	if len(parsed.Evidence) == 0 {
		parsed.Evidence = []string{artifact.Summary}
	}
	// Enforce evidence count and length limits (1-3 items, 150 chars each)
	if len(parsed.Evidence) > 3 {
		parsed.Evidence = parsed.Evidence[:3]
	}
	for i := range parsed.Evidence {
		if len(parsed.Evidence[i]) > 150 {
			parsed.Evidence[i] = parsed.Evidence[i][:150]
		}
	}
	// Enforce fix_guidance length limit (200 chars)
	if len(parsed.FixGuidance) > 200 {
		parsed.FixGuidance = parsed.FixGuidance[:200]
	}
	// Only keep guidance on failure — it's meaningless on pass.
	if parsed.Status != "fail" {
		parsed.FixGuidance = ""
	}

	a.push("info", "acceptor", fmt.Sprintf("verdict: %s", parsed.Status))

	return model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     intent.SessionID,
		Status:        parsed.Status,
		ArtifactPaths: []string{artifact.Path},
		Evidence:      parsed.Evidence,
		FixGuidance:   parsed.FixGuidance,
		CreatedAt:     time.Now(),
	}, nil
}

var _ workflow.Acceptor = (*LLMAcceptor)(nil)

func (a *LLMAcceptor) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "llm-acceptor", Role: workflow.RoleAcceptor}
}
