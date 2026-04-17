package llm

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// SnipHistory tests
// ---------------------------------------------------------------------------

func TestSnipHistory_NoOpUnderThreshold(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: fmt.Sprintf("msg %d", i)}
	}
	got := SnipHistory(msgs, 40)
	if len(got) != len(msgs) {
		t.Fatalf("expected no change for %d messages (max 40), got %d", len(msgs), len(got))
	}
}

func TestSnipHistory_ExactThreshold(t *testing.T) {
	msgs := make([]Message, 40)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: fmt.Sprintf("msg %d", i)}
	}
	got := SnipHistory(msgs, 40)
	if len(got) != 40 {
		t.Fatalf("expected no change at exact threshold, got %d", len(got))
	}
}

func TestSnipHistory_TrimsOverThreshold(t *testing.T) {
	msgs := make([]Message, 50)
	msgs[0] = Message{Role: "system", Content: "system prompt"}
	for i := 1; i < 50; i++ {
		msgs[i] = Message{Role: "user", Content: fmt.Sprintf("msg %d", i)}
	}

	got := SnipHistory(msgs, 40)

	// Expected: 1 (system) + 1 (boundary) + 38 (kept tail) = 40
	if len(got) != 40 {
		t.Fatalf("expected 40 messages after snip, got %d", len(got))
	}

	// First message must be the original system prompt.
	if got[0].Content != "system prompt" {
		t.Errorf("expected system prompt preserved, got %q", got[0].Content)
	}

	// Second message must be the boundary marker.
	if !strings.Contains(got[1].Content, "Context trimmed") {
		t.Errorf("expected boundary marker, got %q", got[1].Content)
	}
	if got[1].Role != "user" {
		t.Errorf("expected boundary marker role=user, got %q", got[1].Role)
	}
	// 50 - 1 (system) - 38 (kept) = 11 removed
	if !strings.Contains(got[1].Content, "11 older messages removed") {
		t.Errorf("expected 11 removed in marker, got %q", got[1].Content)
	}

	// Last message must be the original last message.
	if got[len(got)-1].Content != "msg 49" {
		t.Errorf("expected last message preserved, got %q", got[len(got)-1].Content)
	}

	// Third message should be msgs[12] (50-38=12).
	if got[2].Content != "msg 12" {
		t.Errorf("expected kept tail to start at msg 12, got %q", got[2].Content)
	}
}

func TestMicroCompact_TooFewMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "Tool results:\n### read_file foo.go\nbig content"},
	}
	got := MicroCompact(msgs, 1)
	if len(got) != len(msgs) {
		t.Fatalf("expected no change for %d messages, got %d", len(msgs), len(got))
	}
}

func TestMicroCompact_ClearsReadOnlyKeepsMutations(t *testing.T) {
	// Build 10 messages: head(2) + 3 round pairs(6) + tail needs to be last 4
	// So we need head(2) + middle(at least 2) + tail(4) = 8 minimum
	msgs := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "initial user prompt"},
		// Round 1 (middle — will be compacted)
		{Role: "assistant", Content: "round 1 reply"},
		{Role: "user", Content: "Tool results:\n### read_file foo.go\nline1\nline2\nline3\n### write_file bar.go\nwritten (100 bytes)\n"},
		// Round 2 (middle — will be compacted)
		{Role: "assistant", Content: "round 2 reply"},
		{Role: "user", Content: "Tool results:\n### search query1\nmatch1\nmatch2\n### edit_file baz.go\nedited baz.go\n"},
		// Round 3 (tail — preserved)
		{Role: "assistant", Content: "round 3 reply"},
		{Role: "user", Content: "Tool results:\n### read_file latest.go\nfresh content\n"},
		// Round 4 (tail — preserved)
		{Role: "assistant", Content: "round 4 reply"},
		{Role: "user", Content: "continue"},
	}

	got := MicroCompact(msgs, 3)

	// Head should be preserved
	if got[0].Content != "system prompt" || got[1].Content != "initial user prompt" {
		t.Fatal("head should be preserved")
	}

	// Middle round 1 user: read_file should be cleared, write_file kept
	middle1User := got[3]
	if !strings.Contains(middle1User.Content, "[Cleared: read_file") {
		t.Errorf("expected read_file to be cleared in middle, got: %s", middle1User.Content)
	}
	if !strings.Contains(middle1User.Content, "write_file") {
		t.Errorf("expected write_file to be preserved in middle, got: %s", middle1User.Content)
	}

	// Middle round 2 user: search should be cleared, edit_file kept
	middle2User := got[5]
	if !strings.Contains(middle2User.Content, "[Cleared: search") {
		t.Errorf("expected search to be cleared, got: %s", middle2User.Content)
	}
	if !strings.Contains(middle2User.Content, "edit_file") {
		t.Errorf("expected edit_file to be preserved, got: %s", middle2User.Content)
	}

	// Tail should be completely preserved
	tail := got[len(got)-4:]
	if tail[1].Content != "Tool results:\n### read_file latest.go\nfresh content\n" {
		t.Errorf("tail should be fully preserved, got: %s", tail[1].Content)
	}
}

func TestMicroCompact_Round1NoOp(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: fmt.Sprintf("msg %d", i)}
	}
	got := MicroCompact(msgs, 1) // round 1 = no-op
	if len(got) != len(msgs) {
		t.Fatalf("round 1 should be no-op, got %d msgs", len(got))
	}
}

func TestMicroCompact_PreservesAssistantMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "init"},
		{Role: "assistant", Content: "important analysis with ### read_file mention"},
		{Role: "user", Content: "### read_file big.go\nlots of content here\n"},
		{Role: "assistant", Content: "tail1"},
		{Role: "user", Content: "tail2"},
		{Role: "assistant", Content: "tail3"},
		{Role: "user", Content: "tail4"},
	}
	got := MicroCompact(msgs, 2)

	// Assistant message in middle should NOT be modified
	if got[2].Content != "important analysis with ### read_file mention" {
		t.Errorf("assistant message should not be modified: %s", got[2].Content)
	}
	// User message with read_file should be cleared
	if !strings.Contains(got[3].Content, "[Cleared:") {
		t.Errorf("user tool result should be cleared: %s", got[3].Content)
	}
}

func TestClearReadOnlyBlocks(t *testing.T) {
	input := "Tool results:\n### read_file foo.go\nline1\nline2\n### write_file bar.go\nwritten\n### search query\nresult1\n"
	got := clearReadOnlyBlocks(input)

	if !strings.Contains(got, "[Cleared: read_file") {
		t.Errorf("read_file should be cleared: %s", got)
	}
	if !strings.Contains(got, "### write_file bar.go") {
		t.Errorf("write_file should be preserved: %s", got)
	}
	if !strings.Contains(got, "[Cleared: search") {
		t.Errorf("search should be cleared: %s", got)
	}
}

func TestAutoCompact_NoOpUnderBudget(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "init"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
	}
	// Use a huge budget so AutoCompact is a no-op.
	got := AutoCompact(msgs, 999999)
	if len(got) != len(msgs) {
		t.Fatalf("expected no change when under budget, got %d msgs", len(got))
	}
}

func TestAutoCompact_CompressesMutations(t *testing.T) {
	// Build a conversation large enough to trigger AutoCompact.
	bigContent := strings.Repeat("x", 5000)
	msgs := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "initial task"},
		{Role: "assistant", Content: bigContent},
		{Role: "user", Content: "Tool results:\n### write_file calc.go\nwritten (500 bytes)\n### read_file big.go\n" + bigContent},
		{Role: "assistant", Content: bigContent},
		{Role: "user", Content: "Tool results:\n### edit_file calc.go\nedited (50 chars)\n### run_shell (exit 0)\nok\n"},
		{Role: "assistant", Content: "latest reply"},
		{Role: "user", Content: "continue"},
	}

	// Use a small budget to force compaction.
	got := AutoCompact(msgs, 100)

	// Should be compacted to ≤5 messages: sys + init + summary + last 2.
	if len(got) > 5 {
		t.Fatalf("expected ≤5 messages after AutoCompact, got %d", len(got))
	}

	// Check that system and initial user are preserved.
	if got[0].Content != "system prompt" {
		t.Errorf("system message should be preserved")
	}
	if got[1].Content != "initial task" {
		t.Errorf("initial user message should be preserved")
	}

	// Check summary includes mutation markers.
	summary := got[2].Content
	if !strings.Contains(summary, "write_file") {
		t.Errorf("summary should mention write_file: %s", summary)
	}
	if !strings.Contains(summary, "edit_file") {
		t.Errorf("summary should mention edit_file: %s", summary)
	}
	if !strings.Contains(summary, "run_shell") {
		t.Errorf("summary should mention run_shell: %s", summary)
	}
	// Verify ### read_file is NOT in the summary (it's read-only, not a mutation).
	if strings.Contains(summary, "### read_file") {
		t.Errorf("summary should NOT include ### read_file blocks: %s", summary)
	}

	// Check latest messages are preserved.
	last := got[len(got)-1]
	if last.Content != "continue" {
		t.Errorf("last message should be 'continue', got: %s", last.Content)
	}
}

func TestAutoCompact_TooFewMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "init"},
	}
	// Even with tiny budget, too few messages = no-op.
	got := AutoCompact(msgs, 1)
	if len(got) != len(msgs) {
		t.Fatalf("expected no change for %d messages, got %d", len(msgs), len(got))
	}
}
