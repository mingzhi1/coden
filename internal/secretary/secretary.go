package secretary

import (
	"log/slog"

	"github.com/mingzhi1/coden/internal/skill"
)

// EventEmitter is the interface for publishing audit events.
// Satisfied by *events.Bus.
type EventEmitter interface {
	Emit(sessionID, topic string, payload any)
}

// noopEmitter is used when no event bus is available.
type noopEmitter struct{}

func (noopEmitter) Emit(string, string, any) {}

// Secretary is the Kernel's central policy engine.
//
// It manages three concerns:
//   - ContextGate: what content enters which Worker's prompt
//   - ExecGate:    which tools are authorized to execute
//   - StateKeep:   Turn/Task FSM guards and failure policy
//
// Secretary does NOT own state. It only enforces rules.
// All decisions are logged to the events bus for audit.
type Secretary struct {
	skills *skill.Registry
	policy Policy
	events EventEmitter
	llm    LLM // Light model for intelligent operations; nil = degraded mode
}

// New creates a Secretary with the given skill registry and policy.
// events may be nil (audit logging will be silently skipped).
func New(skills *skill.Registry, policy Policy, events EventEmitter) *Secretary {
	if events == nil {
		events = noopEmitter{}
	}
	return &Secretary{
		skills: skills,
		policy: policy,
		events: events,
	}
}

// Policy returns the current policy (read-only snapshot).
func (s *Secretary) Policy() Policy {
	return s.policy
}

// SetPolicy replaces the policy at runtime (e.g. from CLI flags).
func (s *Secretary) SetPolicy(p Policy) {
	s.policy = p
}

// SetLLM attaches a Light model to the Secretary for intelligent operations.
// When nil, Secretary operates in degraded mode (pure code rules only).
func (s *Secretary) SetLLM(l LLM) {
	s.llm = l
}

// HasLLM returns true if the Secretary has an attached Light model.
func (s *Secretary) HasLLM() bool {
	return s.llm != nil
}

// audit emits an audit entry to the events bus.
func (s *Secretary) audit(sessionID string, entry AuditEntry) {
	s.events.Emit(sessionID, "secretary.decision", entry)
	if !entry.Allowed {
		slog.Debug("[secretary] denied",
			"type", entry.Type,
			"reason", entry.Reason,
		)
	}
}
