package llm

import (
	"fmt"
	"strings"
	"testing"
)

// M12-03i: Integration test that simulates a 10-round agentic loop and verifies
// the 4-layer compression chain (Snip → MicroCompact → compressAgenticHistory →
// AutoCompact) keeps total tokens within the configured budget.

// simulateAgenticRound simulates one round of the agentic loop:
// 1. LLM produces an assistant reply with tool calls
// 2. Tools execute and return results (user message)
// The returned messages include the new round appended to the input.
func simulateAgenticRound(msgs []Message, round int, readFileContent string) []Message {
	// Assistant reply: simulate LLM producing tool calls
	assistantReply := fmt.Sprintf(
		"Round %d: I'll read the relevant file and make changes.\n"+
			"```tool_call\nread_file\n{\"path\": \"file_r%d.go\"}\n```\n"+
			"```tool_call\nwrite_file\n{\"path\": \"output_r%d.go\", \"content\": \"...\"}\n```",
		round, round, round)
	msgs = append(msgs, Message{Role: "assistant", Content: assistantReply})

	// User message: tool results
	toolResults := fmt.Sprintf(
		"Tool results:\n### read_file file_r%d.go\n%s\n### write_file output_r%d.go\nwritten (%d bytes)\n",
		round, readFileContent, round, len(readFileContent))
	msgs = append(msgs, Message{Role: "user", Content: toolResults})
	return msgs
}

func TestCompressionChain_10Rounds_TokensBounded(t *testing.T) {
	// Configure a realistic token budget — the compression chain should keep
	// total token count below this throughout the entire 10-round simulation.
	const tokenBudget = 12000

	// Simulate a 2KB file read per round (realistic for code files).
	fileContent := strings.Repeat("func example() { return nil }\n", 70) // ~2100 chars

	// Build initial messages: system prompt + user task.
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("System prompt with tool descriptions. ", 50)}, // ~350 tokens
		{Role: "user", Content: "Refactor the calculator module to use interfaces instead of concrete types."},
	}

	// Simulate the 4-layer compression chain as wired in ProductionCoderDeps.
	compress := func(messages []Message, round int) []Message {
		messages = SnipHistory(messages, snipMaxMessages)
		messages = MicroCompact(messages, round)
		messages = compressAgenticHistory(messages, tokenBudget)
		messages = AutoCompact(messages, tokenBudget)
		return messages
	}

	var peakTokens int

	for round := 1; round <= 10; round++ {
		// Compress before each LLM call (mirrors agenticBuild).
		msgs = compress(msgs, round)

		// Simulate this round's tool calls + results.
		msgs = simulateAgenticRound(msgs, round, fileContent)

		// Track peak token usage.
		tokens := msgTokens(msgs)
		if tokens > peakTokens {
			peakTokens = tokens
		}

		t.Logf("Round %2d: %d messages, ~%d tokens", round, len(msgs), tokens)
	}

	// Final compression pass.
	msgs = compress(msgs, 11)
	finalTokens := msgTokens(msgs)

	t.Logf("Final: %d messages, ~%d tokens (peak: %d)", len(msgs), finalTokens, peakTokens)

	// Assertion 1: Final tokens must be within budget.
	if finalTokens > tokenBudget {
		t.Errorf("final tokens %d exceed budget %d", finalTokens, tokenBudget)
	}

	// Assertion 2: System prompt must be preserved.
	if msgs[0].Role != "system" {
		t.Error("system prompt lost during compression")
	}

	// Assertion 3: Latest round results should be present (not compacted away).
	lastMsg := msgs[len(msgs)-1]
	if !strings.Contains(lastMsg.Content, "output_r10") && !strings.Contains(lastMsg.Content, "continue") {
		// Check if at least the latest round's write is somewhere in the history.
		found := false
		for _, m := range msgs {
			if strings.Contains(m.Content, "write_file output_r10") {
				found = true
				break
			}
		}
		if !found {
			t.Error("latest round (10) mutation result should be preserved")
		}
	}

	// Assertion 4: Old read-only results should have been cleared.
	for _, m := range msgs {
		if strings.Contains(m.Content, "### read_file file_r1.go") {
			// Round 1 read result should be cleared by MicroCompact.
			t.Error("round 1 read_file result should have been cleared by MicroCompact")
		}
	}
}

func TestCompressionChain_MutationCausalChainPreserved(t *testing.T) {
	const tokenBudget = 8000

	msgs := []Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Create a REST API server"},
	}

	compress := func(messages []Message, round int) []Message {
		messages = SnipHistory(messages, snipMaxMessages)
		messages = MicroCompact(messages, round)
		messages = compressAgenticHistory(messages, tokenBudget)
		messages = AutoCompact(messages, tokenBudget)
		return messages
	}

	// Simulate 6 rounds with distinct mutations.
	mutations := []string{"main.go", "handler.go", "router.go", "db.go", "config.go", "test.go"}
	for round := 1; round <= 6; round++ {
		msgs = compress(msgs, round)

		msgs = append(msgs, Message{
			Role:    "assistant",
			Content: fmt.Sprintf("Creating %s", mutations[round-1]),
		})
		msgs = append(msgs, Message{
			Role: "user",
			Content: fmt.Sprintf(
				"Tool results:\n### write_file %s\nwritten (500 bytes)\n### read_file existing.go\n%s\n",
				mutations[round-1], strings.Repeat("code line\n", 50)),
		})
	}

	// Final compression.
	msgs = compress(msgs, 7)

	// After AutoCompact triggers, the mutation summary should still reference
	// the write_file operations (causal chain preservation).
	allContent := ""
	for _, m := range msgs {
		allContent += m.Content + "\n"
	}

	// At minimum, the latest mutations should be present either directly
	// or in the auto-compact summary.
	hasWriteRef := strings.Contains(allContent, "write_file")
	if !hasWriteRef {
		t.Error("compressed history should preserve write_file references in summary or recent messages")
	}

	t.Logf("Final: %d messages, ~%d tokens", len(msgs), msgTokens(msgs))
}
