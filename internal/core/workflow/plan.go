package workflow

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/model"
)

// ── WorkflowPlan: the declarative description of one workflow run ────────────

// WorkflowMode is the top-level execution shape selected for a single workflow
// run. It decides which handler the kernel runner dispatches to; the optional
// stages *within* a mode are controlled by WorkflowPlan.Roles.
type WorkflowMode string

const (
	// WorkflowModeAnswer: Intent → Responder (greeting / question / chat / other).
	WorkflowModeAnswer WorkflowMode = "answer"
	// WorkflowModeAnalyze: Intent → Discovery → Analyzer → Responder (read-only).
	WorkflowModeAnalyze WorkflowMode = "analyze"
	// WorkflowModeExecute: Plan → [Critic] → [RePlan] → [Code → [Accept]] → Responder.
	WorkflowModeExecute WorkflowMode = "execute"
)

// WorkflowPlan is the declarative description of a single workflow run: the mode
// to dispatch and which optional roles participate within it. It is the runtime
// source of truth that drives the kernel runner's routing and stage gating.
//
// The default LocalDispatcher derives it deterministically from the static
// policy table (planFromPolicy) so behavior matches the previous hand-wired
// branches. A Dispatcher role (LLM) can produce it directly instead, letting
// flow design move out of the static table without touching the runner.
//
// Discovery is intentionally NOT modeled here: in execute/analyze mode it is
// unconditional infrastructure (the prefetch goroutine), not an optional stage.
// It becomes plan-controlled only when a mode that skips it actually exists.
type WorkflowPlan struct {
	Mode      WorkflowMode
	Roles     map[Role]bool // optional stages enabled within Mode
	CoderMode model.CoderMode
	// Objectives is the per-role purpose the workflow hands each participating
	// agent: a concrete, bounded brief that sharpens the raw intent goal so the
	// agent knows exactly what to produce and when it is done. Empty for a role
	// means "fall back to intent.Goal" (the LocalDispatcher leaves it empty, so
	// behavior is unchanged; an LLM dispatcher fills it in).
	Objectives map[Role]string
}

// Has reports whether role r participates in this plan.
func (p WorkflowPlan) Has(r Role) bool {
	return p.Roles[r]
}

// Objective returns the workflow-assigned purpose for role r, or "" when none
// was set (caller should fall back to the intent goal).
func (p WorkflowPlan) Objective(r Role) string {
	return p.Objectives[r]
}

// ── Dispatcher: chooses the WorkflowPlan for an intent ──────────────────────

// Dispatcher selects the WorkflowPlan (mode + participating roles) for a given
// intent. LocalDispatcher is the deterministic default (static policy table); an
// LLM-backed implementation can replace it to design the flow per request.
type Dispatcher interface {
	Dispatch(ctx context.Context, intent model.IntentSpec) (WorkflowPlan, error)
}

// LocalDispatcher is the deterministic default Dispatcher: it maps the intent
// Kind through the static policy table. It never errors.
type LocalDispatcher struct{}

// NewLocalDispatcher returns the deterministic policy-table Dispatcher.
func NewLocalDispatcher() *LocalDispatcher { return &LocalDispatcher{} }

func (d *LocalDispatcher) Dispatch(_ context.Context, intent model.IntentSpec) (WorkflowPlan, error) {
	return planFromPolicy(policyForKind(intent.Kind)), nil
}

var _ Dispatcher = (*LocalDispatcher)(nil)

// ── static policy table (the deterministic routing source of truth) ─────────

// pipelinePolicy declares which workflow stages run for a given intent, and in
// what mode. Every path closes with the Responder regardless of this policy.
type pipelinePolicy struct {
	Discovery bool            // gather code context (grep/LSP/RAG)
	Analyzer  bool            // run the read-only Analyzer (analyze intent)
	Plan      bool            // produce task DAG
	Critic    bool            // review the plan
	RePlan    bool            // refine the plan against critique + discovery
	Code      bool            // run the (agentic) Coder
	CoderMode model.CoderMode // ReadWrite (may modify) or ReadOnly (analyze)
	Accept    bool            // verify the produced artifact
}

// policyForKind maps an IntentSpec.Kind to its pipeline policy. New kinds or
// stage adjustments are made here and nowhere else.
func policyForKind(kind string) pipelinePolicy {
	switch kind {
	case model.IntentKindQuestion, model.IntentKindChat, model.IntentKindOther:
		// Direct answer: Intent → Responder.
		return pipelinePolicy{}

	case model.IntentKindAnalyze:
		// Read code, never modify: Intent → Discovery → Analyzer → Responder.
		return pipelinePolicy{Discovery: true, Analyzer: true}

	case model.IntentKindPlanOnly:
		// Produce & review a plan, do not execute: skip Code/Accept.
		return pipelinePolicy{Discovery: true, Plan: true, Critic: true, RePlan: true}

	default:
		// code_gen / debug / refactor / config: full modifying pipeline.
		return pipelinePolicy{
			Discovery: true, Plan: true, Critic: true, RePlan: true,
			Code: true, CoderMode: model.CoderModeReadWrite, Accept: true,
		}
	}
}

// planFromPolicy maps a pipelinePolicy to the WorkflowPlan that reproduces it.
// Every optional policy boolean becomes a role entry, so the runner gates on the
// plan rather than re-reading the policy. This is the single place the static
// routing table is translated into a runnable plan.
func planFromPolicy(p pipelinePolicy) WorkflowPlan {
	switch {
	case p.Analyzer:
		return WorkflowPlan{Mode: WorkflowModeAnalyze, CoderMode: p.CoderMode}
	case !p.Plan:
		return WorkflowPlan{Mode: WorkflowModeAnswer, CoderMode: p.CoderMode}
	default:
		roles := map[Role]bool{RolePlanner: true}
		if p.Critic {
			roles[RoleCritic] = true
		}
		if p.RePlan {
			roles[RoleReplanner] = true
		}
		if p.Code {
			roles[RoleCoder] = true
		}
		if p.Accept {
			roles[RoleAcceptor] = true
		}
		return WorkflowPlan{Mode: WorkflowModeExecute, Roles: roles, CoderMode: p.CoderMode}
	}
}
