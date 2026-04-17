package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMPlanner uses an LLM to decompose a goal into tasks.
type LLMPlanner struct {
	chatter Chatter
	msgBuffer
}

func NewLLMPlanner(chatter Chatter) *LLMPlanner {
	return &LLMPlanner{chatter: chatter}
}

func (p *LLMPlanner) Plan(ctx context.Context, workflowID string, intent model.IntentSpec) ([]model.Task, error) {
	kind := intent.Kind
	if kind == "" {
		kind = model.IntentKindCodeGen
	}

	systemPrompt := prompts.Planner(kind)

	ctxInfo := contextSummary(ctx)
	userMsg := fmt.Sprintf("Goal: %s\nSuccess criteria:\n%s",
		intent.Goal, bulletList(intent.SuccessCriteria))
	if ctxInfo != "" {
		userMsg = ctxInfo + "\n" + userMsg
	}

	reply, err := RecoverableChat(ctx, p.chatter, RolePlanner, []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}, defaultRecoveryConfig())
	if err != nil {
		return nil, fmt.Errorf("planner llm: %w", err)
	}

	var raw []struct {
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		Files      []string `json:"files"`
		DependsOn  []string `json:"depends_on"`
		SuccessCmd string   `json:"success_cmd"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &raw); err != nil || len(raw) == 0 {
		// Fallback: single task covering the whole goal.
		raw = []struct {
			ID         string   `json:"id"`
			Title      string   `json:"title"`
			Files      []string `json:"files"`
			DependsOn  []string `json:"depends_on"`
			SuccessCmd string   `json:"success_cmd"`
		}{{ID: "task-1", Title: intent.Goal}}
	}

	// Clamp to max 5 tasks to prevent LLM over-decomposition.
	const maxTasks = 5
	if len(raw) > maxTasks {
		raw = raw[:maxTasks]
	}

	now := time.Now()
	taskIDs := make(map[string]bool, len(raw))
	// Pre-populate taskIDs with all valid LLM-returned IDs so auto-generation
	// can avoid collisions.
	for _, r := range raw {
		if r.ID != "" && strings.HasPrefix(r.ID, "task-") {
			taskIDs[r.ID] = true
		}
	}
	tasks := make([]model.Task, 0, len(raw))
	autoCounter := 0
	for _, r := range raw {
		// Enforce id format: must start with "task-"
		id := r.ID
		if id == "" || !strings.HasPrefix(id, "task-") {
			autoCounter++
			candidate := fmt.Sprintf("task-%d", autoCounter)
			for taskIDs[candidate] {
				autoCounter++
				candidate = fmt.Sprintf("task-%d", autoCounter)
			}
			id = candidate
		}
		// Enforce title length limit (100 chars)
		title := r.Title
		if len(title) > 100 {
			title = title[:100]
		}
		taskIDs[id] = true
		tasks = append(tasks, model.Task{
			ID:         id,
			Title:      title,
			Status:     model.TaskStatusPlanned,
			Files:      r.Files,
			DependsOn:  r.DependsOn,
			SuccessCmd: r.SuccessCmd,
			Created:    now,
		})
	}
	// Strip dangling depends_on references (LLM hallucinated task IDs).
	for i := range tasks {
		valid := tasks[i].DependsOn[:0]
		for _, dep := range tasks[i].DependsOn {
			if taskIDs[dep] {
				valid = append(valid, dep)
			}
		}
		tasks[i].DependsOn = valid
	}

	p.push("info", "planner", fmt.Sprintf("planned %d tasks (kind=%s)", len(tasks), kind))
	return tasks, nil
}

var _ workflow.Planner = (*LLMPlanner)(nil)

func (p *LLMPlanner) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "llm-planner", Role: workflow.RolePlanner}
}
