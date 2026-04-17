package workflow

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// Planner is the planning boundary used by the workflow engine.
// It may be backed by local logic or an RPC worker.
type Planner interface {
	Plan(ctx context.Context, workflowID string, intent model.IntentSpec) ([]model.Task, error)
}

// LocalPlanner provides the built-in fallback planner.
type LocalPlanner struct{}

func NewLocalPlanner() *LocalPlanner {
	return &LocalPlanner{}
}

func (p *LocalPlanner) Plan(_ context.Context, _ string, _ model.IntentSpec) ([]model.Task, error) {
	now := time.Now()
	return []model.Task{
		{
			ID:      "task-1",
			Title:   "capture the user goal as an artifact",
			Status:  "planned",
			Created: now,
		},
		{
			ID:      "task-2",
			Title:   "validate that an artifact exists and record evidence",
			Status:  "planned",
			Created: now,
		},
	}, nil
}

func (p *LocalPlanner) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "local-planner", Role: RolePlanner}
}

// Replanner refines high-level tasks into concrete implementation steps
// using discovered code context. Called after Discovery, before Code.
//
//	Plan = WHAT direction → Discovery = WHERE in code → RePlan = HOW specifically
type Replanner interface {
	RePlan(ctx context.Context, intent model.IntentSpec, tasks []model.Task, snippets []model.FileSnippet) ([]model.Task, error)
}

// Searcher is the workflow boundary for the Discovery/Search phase.
// It finds WHERE the relevant code lives and returns a structured
// DiscoveryContext that later workers can consume.
type Searcher interface {
	Search(ctx context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error)
	Refine(ctx context.Context, current model.DiscoveryContext, hints []string) (model.DiscoveryContext, error)
}

// Critic reviews a proposed task plan and returns a structured critique.
// By design it should use a DIFFERENT LLM provider than the Planner to avoid
// same-provider blind spots (structural anti-narcissism).
type Critic interface {
	Critique(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (model.CritiqueResult, error)
}

// LocalCritic is a no-op critic that always approves plans. Used as the
// default when no external Critic is configured.
type LocalCritic struct{}

func NewLocalCritic() *LocalCritic { return &LocalCritic{} }

func (c *LocalCritic) Critique(_ context.Context, _ string, _ model.IntentSpec, _ []model.Task) (model.CritiqueResult, error) {
	return model.CritiqueResult{Score: 1.0, Approved: true}, nil
}
