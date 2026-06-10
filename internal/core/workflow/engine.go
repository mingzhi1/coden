package workflow

import (
	"context"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/model"
)

type Engine struct {
	inputter   Inputter
	planner    Planner
	critic     Critic
	searcher   Searcher
	replanner  Replanner
	executor      Executor
	acceptor   Acceptor
	responder  Responder
	analyzer   Analyzer
	dispatcher Dispatcher
	profiler   Profiler
}

func New(planner Planner, executor Executor, acceptor ...Acceptor) *Engine {
	return NewWithInputter(nil, planner, executor, acceptor...)
}

func NewWithInputter(inputter Inputter, planner Planner, executor Executor, acceptor ...Acceptor) *Engine {
	if inputter == nil {
		inputter = NewLocalInputter()
	}
	if planner == nil {
		planner = NewLocalPlanner()
	}
	if executor == nil {
		executor = NewLocalExecutor()
	}
	var a Acceptor
	if len(acceptor) > 0 {
		a = acceptor[0]
	}
	if a == nil {
		a = NewLocalAcceptor()
	}
	return &Engine{inputter: inputter, planner: planner, executor: executor, acceptor: a}
}

func (e *Engine) SetSearcher(s Searcher) { e.searcher = s }

func (e *Engine) SetCritic(c Critic) { e.critic = c }

func (e *Engine) SetReplanner(r Replanner) { e.replanner = r }

func (e *Engine) SetResponder(r Responder) { e.responder = r }

func (e *Engine) SetAnalyzer(a Analyzer) { e.analyzer = a }

// SetDispatcher configures the Dispatcher that chooses each run's WorkflowPlan.
// When none is set, Dispatch falls back to the deterministic LocalDispatcher
// (static policy table).
func (e *Engine) SetDispatcher(d Dispatcher) { e.dispatcher = d }

// Dispatcher returns the configured Dispatcher, falling back to LocalDispatcher
// (static policy table) when none is wired.
func (e *Engine) Dispatcher() Dispatcher {
	if e.dispatcher == nil {
		return NewLocalDispatcher()
	}
	return e.dispatcher
}

// SetProfiler configures the optional one-time project Profiler (overview/style).
func (e *Engine) SetProfiler(p Profiler) { e.profiler = p }

// Profiler returns the configured Profiler, or nil when none is wired (the
// kernel then keeps the heuristic-only profile).
func (e *Engine) Profiler() Profiler { return e.profiler }

// Dispatch selects the WorkflowPlan for intent. It delegates to the configured
// Dispatcher and, if that errors, falls back to the deterministic policy table
// so a misbehaving LLM dispatcher can never strand a workflow.
func (e *Engine) Dispatch(ctx context.Context, intent model.IntentSpec) WorkflowPlan {
	plan, err := e.Dispatcher().Dispatch(ctx, intent)
	if err != nil {
		return planFromPolicy(policyForKind(intent.Kind))
	}
	return plan
}

// Analyzer returns the configured Analyzer, falling back to LocalAnalyzer when
// none is wired (offline/loopback mode).
func (e *Engine) Analyzer() Analyzer {
	if e.analyzer == nil {
		return NewLocalAnalyzer()
	}
	return e.analyzer
}

// Responder returns the configured Responder, falling back to LocalResponder
// (deterministic summary) when none is wired.
func (e *Engine) Responder() Responder {
	if e.responder == nil {
		return NewLocalResponder()
	}
	return e.responder
}

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
	return e.executor.Build(ctx, workflowID, intent, tasks)
}

func (e *Engine) Accept(ctx context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact, tasks []model.Task) (model.CheckpointResult, error) {
	return e.acceptor.Accept(ctx, workflowID, intent, artifact, tasks)
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

func (e *Engine) Executor() Executor {
	return e.executor
}

func (e *Engine) ExecutorWorker() Worker {
	return NewExecutorWorker(e.executor)
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
