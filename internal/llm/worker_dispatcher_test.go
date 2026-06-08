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
	plan, ok = parseDispatcherPlan(`{"mode":"execute","objectives":{"planner":"Plan X.","coder":"","wizard":"ignore me"}}`)
	if !ok {
		t.Fatalf("execute parse failed")
	}
	if plan.Objective(workflow.RolePlanner) != "Plan X." {
		t.Errorf("planner objective = %q want %q", plan.Objective(workflow.RolePlanner), "Plan X.")
	}
	if plan.Objective(workflow.RoleCoder) != "" {
		t.Errorf("empty coder objective should be dropped, got %q", plan.Objective(workflow.RoleCoder))
	}

	// no objectives → nil map, Objective returns "".
	plan, _ = parseDispatcherPlan(`{"mode":"answer"}`)
	if plan.Objective(workflow.RoleAnalyzer) != "" {
		t.Errorf("answer mode should have no objectives")
	}
}

func TestParseDispatcherPlan(t *testing.T) {
	tests := []struct {
		name     string
		reply    string
		wantOK   bool
		wantMode workflow.WorkflowMode
		// for execute: expected role presence
		critic, replan, coder, acceptor bool
		coderMode                       model.CoderMode
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
			critic:   true, replan: true, coder: true, acceptor: true,
			coderMode: model.CoderModeReadWrite,
		},
		{
			name:     "execute plan-only drops acceptor",
			reply:    `{"mode":"execute","coder":false,"accept":true}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeExecute,
			critic:   true, replan: true, coder: false, acceptor: false, // accept ignored without coder
			coderMode: model.CoderModeReadWrite,
		},
		{
			name:     "execute trims critic",
			reply:    `{"mode":"execute","critic":false,"replan":false,"coder":true,"accept":true}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeExecute,
			critic:   false, replan: false, coder: true, acceptor: true,
			coderMode: model.CoderModeReadWrite,
		},
		{
			name:     "execute readonly",
			reply:    `{"mode":"execute","coder_mode":"readonly"}`,
			wantOK:   true,
			wantMode: workflow.WorkflowModeExecute,
			critic:   true, replan: true, coder: true, acceptor: true,
			coderMode: model.CoderModeReadOnly,
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
			if got := plan.Has(workflow.RoleCoder); got != tc.coder {
				t.Errorf("Coder=%v want %v", got, tc.coder)
			}
			if got := plan.Has(workflow.RoleAcceptor); got != tc.acceptor {
				t.Errorf("Acceptor=%v want %v", got, tc.acceptor)
			}
			// Planner is always present in execute mode.
			if !plan.Has(workflow.RolePlanner) {
				t.Errorf("Planner must always be present in execute mode")
			}
			if plan.CoderMode != tc.coderMode {
				t.Errorf("CoderMode=%v want %v", plan.CoderMode, tc.coderMode)
			}
		})
	}
}
