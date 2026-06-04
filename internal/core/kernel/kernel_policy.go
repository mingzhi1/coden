package kernel

import "github.com/mingzhi1/coden/internal/core/model"

// pipelinePolicy declares which workflow stages run for a given intent, and in
// what mode. It is the single source of truth for intent-adaptive routing
// (replacing scattered IsQuestion()/if branches). Every path closes with the
// Responder regardless of this policy.
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
