package tokenbudget

import "fmt"

// Output-side continuation constants.
const (
	// completionThreshold is the fraction of the output budget that counts as
	// "effectively done". Once usage reaches 90 % we stop nudging.
	completionThreshold = 0.9

	// diminishingThreshold is the minimum token delta between consecutive
	// Check calls. If the model produces fewer than this many new tokens for
	// diminishingRuns consecutive checks we assume it is stuck or finished.
	diminishingThreshold = 500

	// diminishingRuns is the number of consecutive low-delta checks before we
	// declare diminishing returns and stop nudging.
	diminishingRuns = 3
)

// ContinuationDecision is the result of a single Check call.
type ContinuationDecision struct {
	// ShouldContinue is true when the caller should inject a nudge message
	// and let the model keep going.
	ShouldContinue bool

	// NudgeMessage is the user-role message to append when ShouldContinue is
	// true. Empty otherwise.
	NudgeMessage string

	// Pct is the percentage (0-100) of the output budget consumed so far.
	Pct int

	// TurnTokens is the delta (new tokens) observed in this check.
	TurnTokens int
}

// ContinuationTracker tracks cumulative output token usage across model turns
// and decides whether the model should be nudged to continue when it stops
// before exhausting the output budget.
//
// Usage:
//
//	tracker := NewContinuationTracker(outputBudget)
//	// after each model response with no tool calls:
//	decision := tracker.Check(estimatedOutputTokens)
//	if decision.ShouldContinue {
//	    messages = append(messages, assistantMsg, userMsg{Content: decision.NudgeMessage})
//	    round-- // don't count as a round
//	    continue
//	}
type ContinuationTracker struct {
	budget         int // total output-token budget
	lastTotal      int // cumulative tokens at previous Check
	lowDeltaStreak int // consecutive checks with delta < diminishingThreshold
	checks         int // total number of Check calls (informational)
}

// NewContinuationTracker returns a tracker for the given output token budget.
// A budget <= 0 effectively disables continuation nudging.
func NewContinuationTracker(outputBudget int) *ContinuationTracker {
	return &ContinuationTracker{
		budget: outputBudget,
	}
}

// Check evaluates whether the model should be nudged to continue.
//
// newTotalTokens is the cumulative number of output tokens produced so far
// (across all turns in this task). It must be monotonically non-decreasing
// between calls.
func (ct *ContinuationTracker) Check(newTotalTokens int) ContinuationDecision {
	ct.checks++

	// ── Guard: disabled tracker ──────────────────────────────────────────
	if ct.budget <= 0 {
		return ContinuationDecision{
			ShouldContinue: false,
			Pct:            100,
			TurnTokens:     0,
		}
	}

	// ── Compute delta ────────────────────────────────────────────────────
	delta := newTotalTokens - ct.lastTotal
	if delta < 0 {
		delta = 0 // guard against callers passing a stale/lower value
	}
	ct.lastTotal = newTotalTokens

	// ── Percentage of budget consumed ────────────────────────────────────
	pct := (newTotalTokens * 100) / ct.budget
	if pct > 100 {
		pct = 100
	}

	remaining := ct.budget - newTotalTokens
	if remaining < 0 {
		remaining = 0
	}

	// ── Build the base decision ──────────────────────────────────────────
	dec := ContinuationDecision{
		Pct:        pct,
		TurnTokens: delta,
	}

	// ── Already at or above the completion threshold → done ──────────────
	usageFraction := float64(newTotalTokens) / float64(ct.budget)
	if usageFraction >= completionThreshold {
		ct.lowDeltaStreak = 0
		return dec
	}

	// ── Diminishing-returns detection ────────────────────────────────────
	if delta < diminishingThreshold {
		ct.lowDeltaStreak++
	} else {
		ct.lowDeltaStreak = 0
	}

	if ct.lowDeltaStreak >= diminishingRuns {
		// Model has produced very little new content multiple times in a row.
		return dec
	}

	// ── Budget remains and output is still growing → nudge ───────────────
	dec.ShouldContinue = true
	dec.NudgeMessage = fmt.Sprintf(
		"[Budget update: %d%% used (%d/%d tokens). You have %d tokens remaining. "+
			"Continue with next steps without repeating completed work.]",
		pct, newTotalTokens, ct.budget, remaining,
	)

	return dec
}

// Reset restores the tracker to its initial state, keeping the same budget.
// Useful when starting a brand-new task within the same session.
func (ct *ContinuationTracker) Reset() {
	ct.lastTotal = 0
	ct.lowDeltaStreak = 0
	ct.checks = 0
}

// Budget returns the configured output token budget.
func (ct *ContinuationTracker) Budget() int {
	return ct.budget
}

// Checks returns the number of times Check has been called since creation or
// the last Reset.
func (ct *ContinuationTracker) Checks() int {
	return ct.checks
}
