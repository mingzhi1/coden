package workflow

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
)

// TestPlanFromPolicy_PerKind locks the invariant: every intent kind maps to the
// same flow the hand-wired branches produced. If a future change to
// policyForKind alters routing, this catches it.
func TestPlanFromPolicy_PerKind(t *testing.T) {
	cases := []struct {
		kind     string
		wantMode WorkflowMode
		// roles expected true in execute mode (ignored for non-execute modes)
		critic, replan, coder, acceptor bool
	}{
		{model.IntentKindQuestion, WorkflowModeAnswer, false, false, false, false},
		{model.IntentKindChat, WorkflowModeAnswer, false, false, false, false},
		{model.IntentKindOther, WorkflowModeAnswer, false, false, false, false},
		{model.IntentKindAnalyze, WorkflowModeAnalyze, false, false, false, false},
		{model.IntentKindPlanOnly, WorkflowModeExecute, true, true, false, false},
		{model.IntentKindCodeGen, WorkflowModeExecute, true, true, true, true},
		{model.IntentKindDebug, WorkflowModeExecute, true, true, true, true},
		{model.IntentKindRefactor, WorkflowModeExecute, true, true, true, true},
		{model.IntentKindConfig, WorkflowModeExecute, true, true, true, true},
	}
	for _, tc := range cases {
		p := planFromPolicy(policyForKind(tc.kind))
		if p.Mode != tc.wantMode {
			t.Errorf("kind=%s Mode=%q want %q", tc.kind, p.Mode, tc.wantMode)
		}
		if tc.wantMode != WorkflowModeExecute {
			continue
		}
		if got := p.Has(RoleCritic); got != tc.critic {
			t.Errorf("kind=%s Critic=%v want %v", tc.kind, got, tc.critic)
		}
		if got := p.Has(RoleReplanner); got != tc.replan {
			t.Errorf("kind=%s Replanner=%v want %v", tc.kind, got, tc.replan)
		}
		if got := p.Has(RoleCoder); got != tc.coder {
			t.Errorf("kind=%s Coder=%v want %v", tc.kind, got, tc.coder)
		}
		if got := p.Has(RoleAcceptor); got != tc.acceptor {
			t.Errorf("kind=%s Acceptor=%v want %v", tc.kind, got, tc.acceptor)
		}
	}
}

// TestPlanFromPolicy_OptionalStagesHonored proves the gating reads the policy:
// combinations no current kind produces (e.g. Plan without Critic, Code without
// Accept) must still translate faithfully — locking that the runner gates on the
// plan, not on worker presence.
func TestPlanFromPolicy_OptionalStagesHonored(t *testing.T) {
	cases := []struct {
		name   string
		policy pipelinePolicy
		want   map[Role]bool
	}{
		{
			name:   "plan without critic",
			policy: pipelinePolicy{Plan: true, RePlan: true, Code: true, Accept: true},
			want: map[Role]bool{
				RolePlanner: true, RoleCritic: false,
				RoleReplanner: true, RoleCoder: true, RoleAcceptor: true,
			},
		},
		{
			name:   "code without accept",
			policy: pipelinePolicy{Plan: true, Critic: true, RePlan: true, Code: true},
			want: map[Role]bool{
				RolePlanner: true, RoleCritic: true,
				RoleReplanner: true, RoleCoder: true, RoleAcceptor: false,
			},
		},
		{
			name:   "plan only, no replan",
			policy: pipelinePolicy{Plan: true, Critic: true},
			want: map[Role]bool{
				RolePlanner: true, RoleCritic: true,
				RoleReplanner: false, RoleCoder: false, RoleAcceptor: false,
			},
		},
	}
	for _, tc := range cases {
		p := planFromPolicy(tc.policy)
		if p.Mode != WorkflowModeExecute {
			t.Errorf("%s: Mode=%q want execute", tc.name, p.Mode)
		}
		for role, want := range tc.want {
			if got := p.Has(role); got != want {
				t.Errorf("%s: role %q = %v want %v", tc.name, role, got, want)
			}
		}
	}
}

// TestLocalDispatcher_MatchesPolicy verifies the default Dispatcher reproduces
// the static policy table exactly (behavior-preserving seam for Stage 2).
func TestLocalDispatcher_MatchesPolicy(t *testing.T) {
	d := NewLocalDispatcher()
	for _, kind := range []string{
		model.IntentKindQuestion, model.IntentKindAnalyze, model.IntentKindPlanOnly,
		model.IntentKindCodeGen, model.IntentKindChat,
	} {
		got, err := d.Dispatch(context.Background(), model.IntentSpec{Kind: kind})
		if err != nil {
			t.Fatalf("kind=%s: unexpected error %v", kind, err)
		}
		want := planFromPolicy(policyForKind(kind))
		if got.Mode != want.Mode {
			t.Errorf("kind=%s Mode=%q want %q", kind, got.Mode, want.Mode)
		}
		for _, r := range []Role{RoleCritic, RoleReplanner, RoleCoder, RoleAcceptor} {
			if got.Has(r) != want.Has(r) {
				t.Errorf("kind=%s role %q = %v want %v", kind, r, got.Has(r), want.Has(r))
			}
		}
	}
}

// TestEngineDispatch_FallsBackOnError ensures Engine.Dispatch returns the policy
// plan when a wired Dispatcher errors — a misbehaving LLM dispatcher can never
// strand a workflow.
func TestEngineDispatch_FallsBackOnError(t *testing.T) {
	e := New(nil, nil)
	e.SetDispatcher(errDispatcher{})
	got := e.Dispatch(context.Background(), model.IntentSpec{Kind: model.IntentKindCodeGen})
	want := planFromPolicy(policyForKind(model.IntentKindCodeGen))
	if got.Mode != want.Mode || got.Has(RoleCoder) != want.Has(RoleCoder) {
		t.Errorf("fallback plan = %+v, want execute/coder like %+v", got, want)
	}
}

type errDispatcher struct{}

func (errDispatcher) Dispatch(context.Context, model.IntentSpec) (WorkflowPlan, error) {
	return WorkflowPlan{}, context.DeadlineExceeded
}
