package workflow

import (
	"context"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/model"
)

type Engine struct {
	inputter  Inputter
	planner   Planner
	critic    Critic
	searcher  Searcher
	replanner Replanner
	coder     Coder
	acceptor  Acceptor
}

func New(planner Planner, coder Coder, acceptor ...Acceptor) *Engine {
	return NewWithInputter(nil, planner, coder, acceptor...)
}

func NewWithInputter(inputter Inputter, planner Planner, coder Coder, acceptor ...Acceptor) *Engine {
	if inputter == nil {
		inputter = NewLocalInputter()
	}
	if planner == nil {
		planner = NewLocalPlanner()
	}
	if coder == nil {
		coder = NewLocalCoder()
	}
	var a Acceptor
	if len(acceptor) > 0 {
		a = acceptor[0]
	}
	if a == nil {
		a = NewLocalAcceptor()
	}
	return &Engine{inputter: inputter, planner: planner, coder: coder, acceptor: a}
}

func (e *Engine) SetSearcher(s Searcher) { e.searcher = s }

func (e *Engine) SetCritic(c Critic) { e.critic = c }

func (e *Engine) SetReplanner(r Replanner) { e.replanner = r }

func (e *Engine) BuildIntent(ctx context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	return e.inputter.Build(ctx, sessionID, prompt)
}

func (e *Engine) Plan(ctx context.Context, workflowID string, intent model.IntentSpec) ([]model.Task, error) {
	return e.planner.Plan(ctx, workflowID, intent)
}

func (e *Engine) Critique(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (model.CritiqueResult, error) {
	c := e.critic
	if c == nil {
		c = NewLocalCritic()
	}
	return c.Critique(ctx, workflowID, intent, tasks)
}

func (e *Engine) RePlan(ctx context.Context, intent model.IntentSpec, tasks []model.Task, snippets []model.FileSnippet) ([]model.Task, error) {
	if e.replanner == nil {
		return tasks, nil
	}
	return e.replanner.RePlan(ctx, intent, tasks, snippets)
}

func (e *Engine) Code(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (CodePlan, error) {
	return e.coder.Build(ctx, workflowID, intent, tasks)
}

func (e *Engine) Accept(ctx context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact) (model.CheckpointResult, error) {
	return e.acceptor.Accept(ctx, workflowID, intent, artifact)
}

func (e *Engine) Inputter() Inputter {
	return e.inputter
}

func (e *Engine) InputWorker() Worker {
	return NewInputterWorker(e.inputter)
}

func (e *Engine) Planner() Planner {
	return e.planner
}

func (e *Engine) PlannerWorker() Worker {
	return NewPlannerWorker(e.planner)
}

func (e *Engine) Critic() Critic {
	return e.critic
}

func (e *Engine) CriticWorker() Worker {
	if e.critic == nil {
		return NewCriticWorker(NewLocalCritic())
	}
	return NewCriticWorker(e.critic)
}

func (e *Engine) Searcher() Searcher {
	return e.searcher
}

func (e *Engine) Replanner() Replanner {
	return e.replanner
}

func (e *Engine) ReplannerWorker() Worker {
	return NewReplannerWorker(e.replanner)
}

func (e *Engine) Coder() Coder {
	return e.coder
}

func (e *Engine) CoderWorker() Worker {
	return NewCoderWorker(e.coder)
}

func (e *Engine) Acceptor() Acceptor {
	return e.acceptor
}

func (e *Engine) AcceptorWorker() Worker {
	return NewAcceptorWorker(e.acceptor)
}

func filepathForIntent(intent model.IntentSpec) string {
	return fmt.Sprintf("artifacts/%s.md", intent.ID)
}
