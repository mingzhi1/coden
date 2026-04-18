package launcher

import (
	"context"
	"errors"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

type fakePlanner struct{}

type fakeInputter struct{}

func (fakeInputter) Build(context.Context, string, string) (model.IntentSpec, error) {
	return model.IntentSpec{}, nil
}

func (fakePlanner) Plan(context.Context, string, model.IntentSpec) ([]model.Task, error) {
	return nil, nil
}

type fakeCoder struct{}

func (fakeCoder) Build(context.Context, string, model.IntentSpec, []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{}, nil
}

type fakeAcceptor struct{}

func (fakeAcceptor) Accept(context.Context, string, model.IntentSpec, model.Artifact, []model.Task) (model.CheckpointResult, error) {
	return model.CheckpointResult{}, nil
}

type fakeExecutor struct{}

func (fakeExecutor) Execute(context.Context, toolruntime.Request) (toolruntime.Result, error) {
	return toolruntime.Result{}, nil
}

func TestRegistryStartReturnsDependencies(t *testing.T) {
	t.Parallel()

	r := Registry{
		Inputs: map[string]InputterFactory{
			"test": func(context.Context, string) (workflow.Inputter, func(), error) {
				return fakeInputter{}, func() {}, nil
			},
		},
		Planners: map[string]PlannerFactory{
			"test": func(context.Context, string) (workflow.Planner, func(), error) {
				return fakePlanner{}, func() {}, nil
			},
		},
		Coders: map[string]CoderFactory{
			"test": func(context.Context, string) (workflow.Coder, func(), error) {
				return fakeCoder{}, func() {}, nil
			},
		},
		Acceptors: map[string]AcceptorFactory{
			"test": func(context.Context, string) (workflow.Acceptor, func(), error) {
				return fakeAcceptor{}, func() {}, nil
			},
		},
		Executors: map[string]ExecutorFactory{
			"test": func(context.Context, string, string) (toolruntime.Executor, func(), error) {
				return fakeExecutor{}, func() {}, nil
			},
		},
	}

	deps, cleanup, err := r.Start(context.Background(), Options{
		ModuleRoot:    ".",
		WorkspaceRoot: ".",
		Input:         "test",
		Planner:       "test",
		Coder:         "test",
		Acceptor:      "test",
		Executor:      "test",
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cleanup()

	if deps.Inputter == nil || deps.Planner == nil || deps.Coder == nil || deps.Acceptor == nil || deps.Executor == nil {
		t.Fatal("expected all dependencies")
	}
}

func TestRegistryStartCleansUpOnFailure(t *testing.T) {
	t.Parallel()

	cleaned := 0
	r := Registry{
		Inputs: map[string]InputterFactory{
			"test": func(context.Context, string) (workflow.Inputter, func(), error) {
				return fakeInputter{}, func() { cleaned++ }, nil
			},
		},
		Planners: map[string]PlannerFactory{
			"test": func(context.Context, string) (workflow.Planner, func(), error) {
				return fakePlanner{}, func() { cleaned += 10 }, nil
			},
		},
		Coders: map[string]CoderFactory{
			"test": func(context.Context, string) (workflow.Coder, func(), error) {
				return nil, nil, errors.New("boom")
			},
		},
		Acceptors: map[string]AcceptorFactory{},
		Executors: map[string]ExecutorFactory{},
	}

	_, cleanup, err := r.Start(context.Background(), Options{
		ModuleRoot: ".",
		Input:      "test",
		Planner:    "test",
		Coder:      "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	cleanup()

	if cleaned != 11 {
		t.Fatalf("expected input and planner cleanup, got %d", cleaned)
	}
}

func TestDefaultRegistrySupportsLoopback(t *testing.T) {
	t.Parallel()

	r := Default()
	if _, ok := r.Planners["loopback"]; !ok {
		t.Fatal("expected loopback planner launcher")
	}
	if _, ok := r.Inputs["loopback"]; !ok {
		t.Fatal("expected loopback input launcher")
	}
	if _, ok := r.Planners["process"]; !ok {
		t.Fatal("expected process planner launcher")
	}
	if _, ok := r.Executors["loopback"]; !ok {
		t.Fatal("expected loopback executor launcher")
	}
}

func TestDefaultOptionsUseProcessExecutor(t *testing.T) {
	t.Parallel()

	opts := DefaultOptions(".", "workspace")
	if opts.Executor != "process" {
		t.Fatalf("expected process executor, got %q", opts.Executor)
	}
}
