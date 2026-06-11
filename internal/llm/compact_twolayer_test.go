package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type tlStubSummarizer struct {
	digest string
	err    error
	calls  int
}

func (s *tlStubSummarizer) Summarize(_ context.Context, _ []Message) (string, error) {
	s.calls++
	return s.digest, s.err
}

// withBuffer overrides the package auto-compact buffer for one test.
func withBuffer(t *testing.T, n int) {
	t.Helper()
	old := autoCompactBuffer
	autoCompactBuffer = n
	t.Cleanup(func() { autoCompactBuffer = old })
}

// bigHistory builds an 8-message agentic history whose middle far exceeds any
// small budget, so the Compact chain is forced past L1 into L2.
func bigHistory() []Message {
	blob := strings.Repeat("data data data ", 400)
	return []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "initial task: build the thing"},
		{Role: "assistant", Content: "round1 " + blob},
		{Role: "user", Content: "### read_file a.go\n" + blob},
		{Role: "assistant", Content: "round2 " + blob},
		{Role: "user", Content: "### write_file b.go\nwritten"},
		{Role: "assistant", Content: "latest assistant"},
		{Role: "user", Content: "latest tool result"},
	}
}

func joined(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// TestCompactHistory_L2SemanticSummary verifies that on overflow the dropped
// middle is replaced by the LLM summarizer's faithful digest (L2), preserving the
// system prompt and the latest round.
func TestCompactHistory_L2SemanticSummary(t *testing.T) {
	withBuffer(t, 0)
	sum := &tlStubSummarizer{digest: "DIGEST: read a.go, wrote b.go"}

	out := CompactHistory(context.Background(), bigHistory(), 3, 500, sum)

	if sum.calls != 1 {
		t.Fatalf("L2 summarizer should run exactly once on overflow, ran %d", sum.calls)
	}
	if !strings.Contains(joined(out), "DIGEST: read a.go, wrote b.go") {
		t.Errorf("L2 digest not applied:\n%s", joined(out))
	}
	if out[0].Content != "system prompt" {
		t.Error("system prompt must be preserved")
	}
	if out[len(out)-1].Content != "latest tool result" {
		t.Error("latest round must be preserved")
	}
}

// TestCompactHistory_FallbackOnSummarizerError verifies that when L2's LLM fails
// the chain falls back to the lossy rule-based AutoCompact rather than erroring.
func TestCompactHistory_FallbackOnSummarizerError(t *testing.T) {
	withBuffer(t, 0)
	sum := &tlStubSummarizer{err: errors.New("llm down")}

	out := CompactHistory(context.Background(), bigHistory(), 3, 500, sum)

	if !strings.Contains(joined(out), "auto-compacted") {
		t.Errorf("expected lossy AutoCompact fallback, got:\n%s", joined(out))
	}
}

// TestCompactHistory_NilSummarizerUsesLossy verifies no-summarizer wiring still
// converges via the lossy fallback (back-compat with the old behavior).
func TestCompactHistory_NilSummarizerUsesLossy(t *testing.T) {
	withBuffer(t, 0)
	out := CompactHistory(context.Background(), bigHistory(), 3, 500, nil)
	if !strings.Contains(joined(out), "auto-compacted") {
		t.Errorf("nil summarizer should fall back to lossy collapse, got:\n%s", joined(out))
	}
}

// TestCompactHistory_NoOpWhenUnderBudget verifies L2 never fires (no LLM cost)
// when L1 already fits the history within budget.
func TestCompactHistory_NoOpWhenUnderBudget(t *testing.T) {
	withBuffer(t, 0)
	sum := &tlStubSummarizer{digest: "should not be used"}
	small := []Message{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "task"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "done"},
	}
	CompactHistory(context.Background(), small, 1, 1_000_000, sum)
	if sum.calls != 0 {
		t.Errorf("L2 must not run under budget, ran %d times", sum.calls)
	}
}

// TestSetAutoCompactBuffer verifies the previously-dead config knob now drives the
// buffer, and that a non-positive value is ignored.
func TestSetAutoCompactBuffer(t *testing.T) {
	old := autoCompactBuffer
	t.Cleanup(func() { autoCompactBuffer = old })

	SetAutoCompactBuffer(4321)
	if autoCompactBuffer != 4321 {
		t.Errorf("SetAutoCompactBuffer(4321) → %d", autoCompactBuffer)
	}
	SetAutoCompactBuffer(0)
	if autoCompactBuffer != 4321 {
		t.Errorf("non-positive value must be ignored, got %d", autoCompactBuffer)
	}
}
