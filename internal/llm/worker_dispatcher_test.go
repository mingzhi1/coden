package llm

import (
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

func TestParseDispatcherPlan_Objectives(t *testing.T) {
	// analyze with a concrete analyzer objective — the key Stage-2c behavior.
	plan, ok := parseDispatcherPlan(`{"mode":"analyze","objectives":{"analyzer":"Determine the RAG storage engine and embedding model; done when both are cited."}}`)
	if !ok || plan.Mode != workflow.WorkflowModeAnalyze {
		t.Fatalf("analyze parse failed: ok=%v mode=%q", ok, plan.Mode)
	}
	if got := plan.Objective(workflow.RoleAnalyzer); got == "" {
		t.Errorf("analyzer objective missing; want non-empty")
	}

	// unknown role and empty brief are dropped; known roles kept.
	plan, ok = parseDispatcherPlan(`{"mode":"execute","objectives":{"planner":"Plan X.","executor":"","wizard":"ignore me"}}`)
	if !ok {
		t.Fatalf("execute parse failed")
	}
	if plan.Objective(workflow.RolePlanner) != "Plan X." {
		t.Errorf("planner objective = %q want %q", plan.Objective(workflow.RolePlanner), "Plan X.")
	}
	if plan.Objective(workflow.RoleExecutor) != "" {
		t.Errorf("empty executor objective should be dropped, got %q", plan.Objective(workflow.RoleExecutor))
	}

	// no objectives → nil map, Objective returns "".
	plan, _ = parseDispatcherPlan(`{"mode":"answer"}`)
	if plan.Objective(workflow.RoleAnalyzer) != "" {
		t.Errorf("answer mode should have no objectives")
	}

	// critic/replanner/acceptor briefs survive when their stages run.
	plan, ok = parseDispatcherPlan(`{"mode":"execute","critic":true,"replan":true,"executor":true,"accept":true,"objectives":{"critic":"Scrutinize b==0.","replanner":"Pin the file paths.","acceptor":"go test passes."}}`)
	if !ok {
		t.Fatalf("execute parse failed")
	}
	for _, r := range []workflow.Role{workflow.RoleCritic, workflow.RoleReplanner, workflow.RoleAcceptor} {
		if plan.Objective(r) == "" {
			t.Errorf("objective for running role %q was dropped", r)
		}
	}
}

// TestParseDispatcherPlan_PrunesObjectivesForSkippedRoles verifies that a brief
// the model wrote for a role that will NOT run is discarded, so it can never
// leak into an agent outside the flow.
func TestParseDispatcherPlan_PrunesObjectivesForSkippedRoles(t *testing.T) {
	// execute with executor=false (plan-only): executor/acceptor briefs must be dropped,
	// planner/critic kept.
	plan, ok := parseDispatcherPlan(`{"mode":"execute","executor":false,"objectives":{"planner":"Lay out the plan.","critic":"Check it.","executor":"do not run","acceptor":"never verifies"}}`)
	if !ok {
		t.Fatalf("execute parse failed")
	}
	if plan.Objective(workflow.RolePlanner) == "" || plan.Objective(workflow.RoleCritic) == "" {
		t.Errorf("running roles lost their objectives: %+v", plan.Objectives)
	}
	if plan.Objective(workflow.RoleExecutor) != "" {
		t.Errorf("executor objective should be pruned when executor=false, got %q", plan.Objective(workflow.RoleExecutor))
	}
	if plan.Objective(workflow.RoleAcceptor) != "" {
		t.Errorf("acceptor objective should be pruned when executor=false, got %q", plan.Objective(workflow.RoleAcceptor))
	}

	// analyze mode: only the analyzer participates — a planner brief is dropped.
	plan, _ = parseDispatcherPlan(`{"mode":"analyze","objectives":{"analyzer":"Map the RAG flow.","planner":"irrelevant here"}}`)
	if plan.Objective(workflow.RoleAnalyzer) == "" {
		t.Errorf("analyzer objective lost in analyze mode")
	}
	if plan.Objective(workflow.RolePlanner) != "" {
		t.Errorf("planner objective should be pruned in analyze mode, got %q", plan.Objective(workflow.RolePlanner))
	}
}

func TestParseDispatcherPlan(t *testing.T) {
	tests := []struct {
		name     string
		reply    string
		wantOK   bool
		wantMode workflow.WorkflowMode
		// for execute: expected role presence
		critic, replan, executor, acceptor bool
		executorMode                       model.ExecutorMode
	}{
		{
			name:     "answer",
			reply:    `{"mode":"answer"}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeAnswer,
		},
		{
			name:     "analyze",
			reply:    `{"mode":"analyze"}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeAnalyze,
		},
		{
			name:     "execute full (flags omitted default true)",
			reply:    `{"mode":"execute"}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeExecute,
			critic:   true, replan: true, executor: true, acceptor: true,
			executorMode: model.ExecutorModeReadWrite,
		},
		{
			name:     "execute plan-only drops acceptor",
			reply:    `{"mode":"execute","executor":false,"accept":true}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeExecute,
			critic:   true, replan: true, executor: false, acceptor: false, // accept ignored without executor
			executorMode: model.ExecutorModeReadWrite,
		},
		{
			name:     "execute trims critic",
			reply:    `{"mode":"execute","critic":false,"replan":false,"executor":true,"accept":true}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeExecute,
			critic:   false, replan: false, executor: true, acceptor: true,
			executorMode: model.ExecutorModeReadWrite,
		},
		{
			name:     "execute readonly",
			reply:    `{"mode":"execute","executor_mode":"readonly"}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeExecute,
			critic:   true, replan: true, executor: true, acceptor: true,
			executorMode: model.ExecutorModeReadOnly,
		},
		{
			name:     "json with markdown fences",
			reply:    "```json\n{\"mode\":\"answer\"}\n```",
			wantOK:   true,
			wantMode: workflow.WorkflowModeAnswer,
		},
		{
			name:   "unknown mode falls through",
			reply:  `{"mode":"banana"}`,
			wantOK: false,
		},
		{
			name:   "garbage",
			reply:  `not json at all`,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, ok := parseDispatcherPlan(tc.reply)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (plan=%+v)", ok, tc.wantOK, plan)
			}
			if !ok {
				return
			}
			if plan.Mode != tc.wantMode {
				t.Errorf("Mode=%q want %q", plan.Mode, tc.wantMode)
			}
			if tc.wantMode != workflow.WorkflowModeExecute {
				return
			}
			if got := plan.Has(workflow.RoleCritic); got != tc.critic {
				t.Errorf("Critic=%v want %v", got, tc.critic)
			}
			if got := plan.Has(workflow.RoleReplanner); got != tc.replan {
				t.Errorf("Replanner=%v want %v", got, tc.replan)
			}
			if got := plan.Has(workflow.RoleExecutor); got != tc.executor {
				t.Errorf("Executor=%v want %v", got, tc.executor)
			}
			if got := plan.Has(workflow.RoleAcceptor); got != tc.acceptor {
				t.Errorf("Acceptor=%v want %v", got, tc.acceptor)
			}
			// Planner is always present in execute mode.
			if !plan.Has(workflow.RolePlanner) {
				t.Errorf("Planner must always be present in execute mode")
			}
			if plan.ExecutorMode != tc.executorMode {
				t.Errorf("ExecutorMode=%v want %v", plan.ExecutorMode, tc.executorMode)
			}
		})
	}
}
