package llm

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/outputcompressor"
)

// executedMutations returns only the calls the agentic loop actually ran —
// dropping the discovery call refineCodePlanWithContext prepends for the kernel.
func executedMutations(calls []workflow.ToolCall) []workflow.ToolCall {
	var out []workflow.ToolCall
	for _, c := range calls {
		if c.Executed {
			out = append(out, c)
		}
	}
	return out
}

// stubToolExecutor only exists to make c.executor non-nil so Build takes the
// agentic path; actual tool execution is driven by the injected ExecutorDeps.
type stubToolExecutor struct{}

func (stubToolExecutor) Execute(context.Context, toolruntime.Request) (toolruntime.Result, error) {
	return toolruntime.Result{}, nil
}

// scriptedExecutor wires a stubbed chat script and tool responses into an
// agentic executor for driving the verify-in-loop gate deterministically.
func scriptedExecutor(chat func(int) string, exec func(toolruntime.Request) (toolruntime.Result, error)) *LLMExecutor {
	c := &LLMExecutor{executor: stubToolExecutor{}, outputCompressor: outputcompressor.New()}
	calls := 0
	c.SetDeps(ExecutorDeps{
		Chat: func(context.Context, []Message) (string, error) {
			calls++
			return chat(calls), nil
		},
		Execute: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
			return exec(req)
		},
		Compress: func(m []Message, _ int, _ int) []Message { return m },
	})
	return c
}

func verifyTask() (model.IntentSpec, []model.Task) {
	return model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "add Add", Kind: model.IntentKindCodeGen},
		[]model.Task{{ID: "t1", Title: "add Add", SuccessCmd: "go test ./..."}}
}

// TestVerifyInLoop_FailThenFix is the core red/green case: the executor declares
// "done", the verify command fails, the failure is fed back, the executor fixes
// it, declares done again, and the second verify passes.
func TestVerifyInLoop_FailThenFix(t *testing.T) {
	verifyRuns, writeRuns := 0, 0
	c := scriptedExecutor(
		func(n int) string {
			switch n {
			case 1:
				return `{"tool_calls":[]}` // declare done before doing anything
			case 2:
				return `{"tool_calls":[{"kind":"write_file","path":"calc.go","content":"package calc\nfunc Add(a,b int)int{return a+b}\n"}]}`
			default:
				return `{"tool_calls":[]}` // done again
			}
		},
		func(req toolruntime.Request) (toolruntime.Result, error) {
			if req.Kind == "run_shell" {
				verifyRuns++
				if verifyRuns == 1 {
					return toolruntime.Result{ExitCode: 1, Output: "FAIL: undefined: Add"}, nil
				}
				return toolruntime.Result{ExitCode: 0, Output: "ok"}, nil
			}
			writeRuns++
			return toolruntime.Result{After: req.Content, Summary: "wrote " + req.Path}, nil
		},
	)

	intent, tasks := verifyTask()
	plan, err := c.Build(context.Background(), "wf1", intent, tasks)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if verifyRuns != 2 {
		t.Errorf("expected verify to run twice (fail then pass), ran %d", verifyRuns)
	}
	if writeRuns != 1 {
		t.Errorf("expected one write_file fix, got %d", writeRuns)
	}
	muts := executedMutations(plan.Calls())
	if len(muts) != 1 || muts[0].Request.Kind != "write_file" {
		t.Errorf("expected one pre-executed write_file mutation, got %+v", muts)
	}
}

// TestVerifyInLoop_CapStopsLoop guards against an infinite fix cycle: when the
// verify command never passes and the model never fixes it, the attempt cap
// releases control (to the kernel's out-of-loop gate) instead of looping.
func TestVerifyInLoop_CapStopsLoop(t *testing.T) {
	verifyRuns := 0
	c := scriptedExecutor(
		func(n int) string {
			if n == 1 {
				return `{"tool_calls":[{"kind":"write_file","path":"calc.go","content":"package calc\n"}]}`
			}
			return `{"tool_calls":[]}` // keeps declaring done, never fixes
		},
		func(req toolruntime.Request) (toolruntime.Result, error) {
			if req.Kind == "run_shell" {
				verifyRuns++
				return toolruntime.Result{ExitCode: 1, Output: "still failing"}, nil
			}
			return toolruntime.Result{After: req.Content, Summary: "wrote " + req.Path}, nil
		},
	)

	intent, tasks := verifyTask()
	plan, err := c.Build(context.Background(), "wf1", intent, tasks)
	if err != nil {
		t.Fatalf("Build should release to kernel gate, not error: %v", err)
	}
	if verifyRuns != 2 {
		t.Errorf("verify should stop at the 2-attempt cap, ran %d", verifyRuns)
	}
	if len(executedMutations(plan.Calls())) != 1 {
		t.Errorf("expected the produced mutation to still be returned, got %+v", plan.Calls())
	}
}

// TestVerifyInLoop_ShellDisabled verifies that when the tool layer refuses to
// run the command (e.g. shell disabled), the gate steps aside and lets the
// executor finish — the kernel's gate remains the safety net.
func TestVerifyInLoop_ShellDisabled(t *testing.T) {
	verifyRuns := 0
	c := scriptedExecutor(
		func(n int) string {
			if n == 1 {
				return `{"tool_calls":[{"kind":"write_file","path":"calc.go","content":"package calc\n"}]}`
			}
			return `{"tool_calls":[]}`
		},
		func(req toolruntime.Request) (toolruntime.Result, error) {
			if req.Kind == "run_shell" {
				verifyRuns++
				return toolruntime.Result{}, context.Canceled // stand-in for "tool refused"
			}
			return toolruntime.Result{After: req.Content, Summary: "wrote " + req.Path}, nil
		},
	)

	intent, tasks := verifyTask()
	plan, err := c.Build(context.Background(), "wf1", intent, tasks)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if verifyRuns != 1 {
		t.Errorf("expected a single verify attempt before standing aside, got %d", verifyRuns)
	}
	if len(executedMutations(plan.Calls())) != 1 {
		t.Errorf("expected the mutation to be returned, got %+v", plan.Calls())
	}
}

// TestCollectVerifyCmds verifies de-duplication and empty filtering.
func TestCollectVerifyCmds(t *testing.T) {
	t.Parallel()
	got := collectVerifyCmds([]model.Task{
		{SuccessCmd: "go test ./..."},
		{SuccessCmd: "  "},
		{SuccessCmd: "go test ./..."},
		{SuccessCmd: "go build ./..."},
		{SuccessCmd: ""},
	})
	if len(got) != 2 || got[0] != "go test ./..." || got[1] != "go build ./..." {
		t.Errorf("expected de-duped non-empty cmds, got %v", got)
	}
}
