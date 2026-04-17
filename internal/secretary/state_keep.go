package secretary

import "fmt"

// --- Turn FSM ---

// validTurnTransitions defines legal state transitions for Turns.
// Terminal states (pass, fail, canceled, failed, crashed) cannot transition.
var validTurnTransitions = map[string][]string{
	"running": {"pass", "fail", "canceled", "failed", "crashed"},
}

// ValidateTurnTransition checks if a turn state transition is legal.
// Returns error if the transition violates FSM rules.
func (s *Secretary) ValidateTurnTransition(sessionID, turnID, from, to string) error {
	allowed, ok := validTurnTransitions[from]
	if !ok {
		err := fmt.Errorf("secretary: illegal turn transition %s → %s (terminal state %q has no outgoing transitions)", from, to, from)
		s.audit(sessionID, AuditEntry{
			Type:    "state_transition",
			Allowed: false,
			Reason:  err.Error(),
			Details: map[string]any{
				"entity": "turn",
				"id":     turnID,
				"from":   from,
				"to":     to,
			},
		})
		return err
	}
	for _, a := range allowed {
		if a == to {
			s.audit(sessionID, AuditEntry{
				Type:    "state_transition",
				Allowed: true,
				Details: map[string]any{
					"entity": "turn",
					"id":     turnID,
					"from":   from,
					"to":     to,
				},
			})
			return nil
		}
	}
	err := fmt.Errorf("secretary: illegal turn transition %s → %s (allowed from %q: %v)", from, to, from, allowed)
	s.audit(sessionID, AuditEntry{
		Type:    "state_transition",
		Allowed: false,
		Reason:  err.Error(),
		Details: map[string]any{
			"entity": "turn",
			"id":     turnID,
			"from":   from,
			"to":     to,
		},
	})
	return err
}

// --- Task FSM ---

// validTaskTransitions defines legal state transitions for Tasks.
var validTaskTransitions = map[string][]string{
	"planned":   {"coding", "skipped", "removed", "abandoned"},
	"coding":    {"coded", "failed", "passed"}, // "passed" for direct pass (question workflow)
	"coded":     {"accepting"},
	"accepting": {"passed", "failed"},
	"failed":    {"retrying"},
	"retrying":  {"coding"},
	// Terminal: passed, skipped, removed, abandoned — no outgoing transitions
}

// ValidateTaskTransition checks if a task state transition is legal.
func (s *Secretary) ValidateTaskTransition(sessionID, taskID, from, to string) error {
	allowed, ok := validTaskTransitions[from]
	if !ok {
		err := fmt.Errorf("secretary: illegal task transition %s → %s (terminal state %q has no outgoing transitions)", from, to, from)
		s.audit(sessionID, AuditEntry{
			Type:    "state_transition",
			Allowed: false,
			Reason:  err.Error(),
			Details: map[string]any{
				"entity": "task",
				"id":     taskID,
				"from":   from,
				"to":     to,
			},
		})
		return err
	}
	for _, a := range allowed {
		if a == to {
			s.audit(sessionID, AuditEntry{
				Type:    "state_transition",
				Allowed: true,
				Details: map[string]any{
					"entity": "task",
					"id":     taskID,
					"from":   from,
					"to":     to,
				},
			})
			return nil
		}
	}
	err := fmt.Errorf("secretary: illegal task transition %s → %s (allowed from %q: %v)", from, to, from, allowed)
	s.audit(sessionID, AuditEntry{
		Type:    "state_transition",
		Allowed: false,
		Reason:  err.Error(),
		Details: map[string]any{
			"entity": "task",
			"id":     taskID,
			"from":   from,
			"to":     to,
		},
	})
	return err
}

// --- Failure Policy ---

// DecideFailureAction returns the action to take when a task fails
// after all retries are exhausted.
func (s *Secretary) DecideFailureAction(sessionID, taskID string) FailureAction {
	action := s.policy.ResolvedFailureAction()
	s.audit(sessionID, AuditEntry{
		Type:    "failure_policy",
		Allowed: true,
		Details: map[string]any{
			"task":   taskID,
			"policy": s.policy.FailurePolicy,
			"action": action.String(),
		},
	})
	return action
}
