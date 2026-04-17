// Package secretary implements the Secretary Agent — the Kernel's
// central policy engine for context curation, tool authorization,
// and state machine management.
//
// Secretary does NOT own state (Kernel is the single writer).
// Secretary only ENFORCES rules: filtering, guarding, deciding.
//
// MVP sub-capabilities (all pure code, zero LLM cost):
//   - ContextGate: Skill injection filtering by trust matrix
//   - ExecGate:    Tool authorization (MCP stubs for now)
//   - StateKeep:   Turn/Task FSM guards + failure policy
package secretary

import "context"

// Target represents a workflow worker that content can be injected into.
type Target string

const (
	TargetCoder    Target = "coder"
	TargetPlanner  Target = "planner"
	TargetAcceptor Target = "acceptor"
	TargetInputter Target = "inputter"
)

// AllTargets is the ordered list of all worker targets.
var AllTargets = []Target{TargetCoder, TargetPlanner, TargetAcceptor, TargetInputter}

// MinTrustForTarget returns the minimum trust level required to inject
// content into the given worker target.
func MinTrustForTarget(t Target) int {
	switch t {
	case TargetCoder:
		return 10 // all sources (L5 mcp = 10)
	case TargetPlanner:
		return 50 // L3 project and above
	case TargetAcceptor:
		return 70 // L2 user and above
	case TargetInputter:
		return 70 // L2 user and above
	default:
		return 100 // unknown target = deny
	}
}

// ContextBlock is a single piece of content authorized for injection
// into a Worker's prompt.
type ContextBlock struct {
	Kind      string // "skill" | "rules" | "memory" (future)
	Name      string // skill name or section identifier
	Source    string // "builtin" / "user" / "project" / "plugin" / "mcp"
	Content   string // the markdown text to inject
	Truncated bool   // true if content was cut to fit source budget
	Priority  int    // sorting key (higher = earlier in prompt)
}

// FailureAction is the decision Secretary makes when a task fails
// after all retries are exhausted.
type FailureAction int

const (
	ActionStop   FailureAction = iota // abandon remaining tasks (default)
	ActionSkip                        // mark failed, continue next task
	ActionReplan                      // trigger re-plan with failure evidence (future)
)

func (a FailureAction) String() string {
	switch a {
	case ActionStop:
		return "stop"
	case ActionSkip:
		return "skip"
	case ActionReplan:
		return "replan"
	default:
		return "unknown"
	}
}

// AuditEntry is emitted to the events bus for every Secretary decision.
type AuditEntry struct {
	Type    string         `json:"type"` // "skill_injection" | "tool_auth" | "state_transition" | "failure_policy" | "queue_op" | "insight_extraction"
	Allowed bool           `json:"allowed"`
	Reason  string         `json:"reason,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// ---------------------------------------------------------------------------
// LLM interface — Secretary's brain (Light model)
// ---------------------------------------------------------------------------

// LLM is the interface Secretary uses for Light-model intelligence.
// Satisfied by an adapter wrapping *llm.Broker to avoid circular imports.
//
// When nil, Secretary operates in degraded mode: pure-code rules only,
// no insight extraction, no context ranking, no history compression.
type LLM interface {
	// Chat sends messages to the Light model and returns the text response.
	Chat(ctx context.Context, role string, messages []LLMMessage) (string, error)
}

// LLMMessage is a minimal chat message to avoid importing the llm package.
type LLMMessage struct {
	Role    string // "system", "user", "assistant"
	Content string
}
