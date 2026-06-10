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

type fakeExecutor struct{}

func (fakeExecutor) Build(context.Context, string, model.IntentSpec, []model.Task) (workflow.CodePlan, error) {
	return workflow.CodePlan{}, nil
}

type fakeAcceptor struct{}

func (fakeAcceptor) Accept(context.Context, string, model.IntentSpec, model.Artifact, []model.Task) (model.CheckpointResult, error) {
	return model.CheckpointResult{}, nil
}

type fakeToolExecutor struct{}

func (fakeToolExecutor) Execute(context.Context, toolruntime.Request) (toolruntime.Result, error) {
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
		Executors: map[string]ExecutorFactory{
			"test": func(context.Context, string) (workflow.Executor, func(), error) {
				return fakeExecutor{}, func() {}, nil
			},
		},
		Acceptors: map[string]AcceptorFactory{
			"test": func(context.Context, string) (workflow.Acceptor, func(), error) {
				return fakeAcceptor{}, func() {}, nil
			},
		},
		ToolExecutors: map[string]ToolExecutorFactory{
			"test": func(context.Context, string, string) (toolruntime.Executor, func(), error) {
				return fakeToolExecutor{}, func() {}, nil
			},
		},
	}

	deps, cleanup, err := r.Start(context.Background(), Options{
		ModuleRoot:    ".",
		WorkspaceRoot: ".",
		Input:         "test",
		Planner:       "test",
		Executor:     "test",
		Acceptor:     "test",
		ToolExecutor: "test",
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cleanup()

	if deps.Inputter == nil || deps.Planner == nil || deps.Executor == nil || deps.Acceptor == nil || deps.ToolExecutor == nil {
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
		Executors: map[string]ExecutorFactory{
			"test": func(context.Context, string) (workflow.Executor, func(), error) {
				return nil, nil, errors.New("boom")
			},
		},
		Acceptors:     map[string]AcceptorFactory{},
		ToolExecutors: map[string]ToolExecutorFactory{},
	}

	_, cleanup, err := r.Start(context.Background(), Options{
		ModuleRoot: ".",
		Input:      "test",
		Planner:    "test",
		Executor:      "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	cleanup()

	if cleaned != 11 {
		t.Fatalf("expected input and planner cleanup, got %d", cleaned)
	}
}

func TestDefaultOptionsTreatsACPCommandAsLLM(t *testing.T) {
	t.Setenv("CODEN_ACP_COMMAND", "npx @agentclientprotocol/claude-agent-acp")

	opts := DefaultOptions(".", ".")
	if opts.Input != "llm" || opts.Planner != "llm" || opts.Executor != "llm" || opts.Acceptor != "llm" {
		t.Fatalf("expected llm workers from ACP env, got input=%s planner=%s executor=%s acceptor=%s",
			opts.Input, opts.Planner, opts.Executor, opts.Acceptor)
	}
	if !opts.Agentic {
		t.Fatal("expected agentic mode when ACP env is configured")
	}
}

func TestDefaultOptionsTreatsCodexAppServerAsLLM(t *testing.T) {
	t.Setenv("CODEN_CODEX_APP_SERVER_COMMAND", "codex app-server")

	opts := DefaultOptions(".", ".")
	if opts.Input != "llm" || opts.Planner != "llm" || opts.Executor != "llm" || opts.Acceptor != "llm" {
		t.Fatalf("expected llm workers from Codex app-server env, got input=%s planner=%s executor=%s acceptor=%s",
			opts.Input, opts.Planner, opts.Executor, opts.Acceptor)
	}
	if !opts.Agentic {
		t.Fatal("expected agentic mode when Codex app-server env is configured")
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

func TestDefaultOptionsUseProcessToolExecutor(t *testing.T) {
	t.Parallel()

	opts := DefaultOptions(".", "workspace")
	if opts.ToolExecutor != "process" {
		t.Fatalf("expected process tool executor, got %q", opts.ToolExecutor)
	}
}
