package llm

import (
	"strings"
	"testing"
)

// TestCompressAgenticHistory_BelowBudget verifies no compression when under budget.
func TestCompressAgenticHistory_BelowBudget(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "initial user msg"},
		{Role: "assistant", Content: `{"tool_calls": []}`},
		{Role: "user", Content: "result"},
	}
	budget := 100000
	result := compressAgenticHistory(msgs, budget)
	if len(result) != len(msgs) {
		t.Errorf("expected no compression, got %d messages (was %d)", len(result), len(msgs))
	}
}

// TestCompressAgenticHistory_TooFewMessages verifies short histories are left alone.
func TestCompressAgenticHistory_TooFewMessages(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: strings.Repeat("a", 10000)},
		{Role: "assistant", Content: "b"},
	}
	budget := 1 // force compression trigger
	result := compressAgenticHistory(msgs, budget)
	// len 3 ≤ 4, so returns unchanged.
	if len(result) != 3 {
		t.Errorf("expected 3 messages unchanged, got %d", len(result))
	}
}

// TestCompressAgenticHistory_CompressesMiddle verifies middle rounds are squashed.
func TestCompressAgenticHistory_CompressesMiddle(t *testing.T) {
	t.Parallel()

	// Build a long history: system + init_user + 4 middle pairs + 2 tail pairs = 2+8+4=14 msgs
	msgs := []Message{
		{Role: "system", Content: "instructions"},
		{Role: "user", Content: "initial context " + strings.Repeat("x", 5000)},
	}
	// Add 4 "old" pairs (middle rounds).
	for i := 0; i < 4; i++ {
		msgs = append(msgs,
			Message{Role: "assistant", Content: `{"tool_calls":[{"kind":"read_file","path":"foo.go"}]}` + strings.Repeat("z", 1000)},
			Message{Role: "user", Content: "Tool results:\n" + strings.Repeat("y", 1000)},
		)
	}
	// Tail pair (most recent 2 pairs = 4 msgs).
	msgs = append(msgs,
		Message{Role: "assistant", Content: `{"tool_calls":[{"kind":"write_file","path":"bar.go","content":"..."}]}`},
		Message{Role: "user", Content: "Tool results:\n### write_file bar.go\nwritten"},
		Message{Role: "assistant", Content: `{"tool_calls":[]}`},
		Message{Role: "user", Content: "continue"},
	)

	// Use a very small budget to force compression.
	budget := 10 // tokens — basically always triggers

	result := compressAgenticHistory(msgs, budget)

	// head (2) + compressed middle (≤8 pairs → up to 4 summaries = 8 msgs) + tail (4) = check structure
	if len(result) < 6 {
		t.Fatalf("expected at least 6 messages after compression, got %d", len(result))
	}
	// First two messages must be the head unchanged.
	if result[0].Content != msgs[0].Content {
		t.Errorf("system message was modified")
	}
	if result[1].Content != msgs[1].Content {
		t.Errorf("initial user message was modified")
	}
	// The tail (last 4 messages) must be preserved.
	tailStart := len(result) - 4
	expectedTail := msgs[len(msgs)-4:]
	for i := 0; i < 4; i++ {
		if result[tailStart+i].Content != expectedTail[i].Content {
			t.Errorf("tail message %d modified: got %q want %q", i, result[tailStart+i].Content, expectedTail[i].Content)
		}
	}
	// Compressed middle should contain the "[round compressed" marker.
	foundCompressed := false
	for _, m := range result[2:tailStart] {
		if strings.Contains(m.Content, "[round compressed") {
			foundCompressed = true
			break
		}
	}
	if !foundCompressed {
		t.Error("expected to find '[round compressed' marker in middle of compressed history")
	}
}

// TestMsgTokens verifies per-message token estimation.
func TestMsgTokens(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "user", Content: "1234"},    // 1 token
		{Role: "assistant", Content: "ab"}, // 1 token (ceiling)
	}
	got := msgTokens(msgs)
	if got != 2 {
		t.Errorf("expected 2 tokens, got %d", got)
	}
	if msgTokens(nil) != 0 {
		t.Errorf("expected 0 tokens for nil slice")
	}
}
