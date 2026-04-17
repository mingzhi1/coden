package llm

import (
	"fmt"
	"log/slog"
	"strings"
)

// snipMaxMessages is the default threshold for SnipHistory. When the
// conversation exceeds this many messages the oldest middle messages are
// dropped (zero LLM cost).
const snipMaxMessages = 40

// SnipHistory performs a zero-cost bounded trim on messages.
// When len(messages) exceeds maxMessages, it keeps:
//   - messages[0] (system prompt)
//   - a boundary marker: "[Context trimmed: N messages removed]"
//   - the last (maxMessages-2) messages
//
// This runs before MicroCompact/AutoCompact to reduce their input size.
func SnipHistory(messages []Message, maxMessages int) []Message {
	if len(messages) <= maxMessages {
		return messages
	}

	keep := maxMessages - 2             // room for system prompt + boundary marker
	removed := len(messages) - 1 - keep // messages[1:] minus the kept tail
	kept := keep

	slog.Info("[llm:snip] trimmed history", "removed", removed, "kept", kept)

	out := make([]Message, 0, maxMessages)
	out = append(out, messages[0]) // system prompt
	out = append(out, Message{
		Role:    "user",
		Content: fmt.Sprintf("[Context trimmed: %d older messages removed to fit context window]", removed),
	})
	out = append(out, messages[len(messages)-keep:]...)
	return out
}

// readOnlyTools lists tool kinds whose results are safe to strip from older
// agentic rounds. Mutation results (write_file, edit_file, run_shell) are
// always preserved because they form the causal chain of state changes.
var readOnlyTools = map[string]bool{
	"read_file":      true,
	"search":         true,
	"list_dir":       true,
	"grep_context":   true,
	"lsp_symbols":    true,
	"lsp_definition": true,
	"lsp_references": true,
	"lsp_didopen":    true,
	"rag_search":     true,
}

// MicroCompact is a zero-LLM-cost first pass that strips verbose read-only
// tool results from older agentic rounds while preserving mutation results.
//
// Strategy:
//   - Head (system + initial user) and the latest 2 round pairs (tail 4
//     messages) are never touched.
//   - In middle rounds, each "### <tool_kind> <target>\n..." block inside
//     user tool-result messages is checked: if tool_kind is read-only, the
//     entire block is replaced with a 1-line "[Cleared: ...]" stub.
//   - Mutation blocks (write_file, edit_file, run_shell) are kept intact.
func MicroCompact(messages []Message, currentRound int) []Message {
	// Need at least head(2) + 2 middle + tail(4) = 8 messages, and at
	// least round 2 to have anything worth clearing.
	if len(messages) < 8 || currentRound < 2 {
		return messages
	}

	head := messages[:2]
	tail := messages[len(messages)-4:]
	middle := messages[2 : len(messages)-4]
	if len(middle) == 0 {
		return messages
	}

	// Deep-copy so we don't mutate the caller's slice.
	out := make([]Message, 0, len(messages))
	out = append(out, head...)

	for _, msg := range middle {
		if msg.Role != "user" || !strings.Contains(msg.Content, "### ") {
			out = append(out, msg)
			continue
		}
		cleaned := clearReadOnlyBlocks(msg.Content)
		out = append(out, Message{Role: msg.Role, Content: cleaned})
	}

	out = append(out, tail...)
	return out
}

// clearReadOnlyBlocks replaces read-only tool result blocks with 1-line stubs.
// Blocks start with "### <kind> <target>" and end at the next "### " or end-of-string.
func clearReadOnlyBlocks(content string) string {
	lines := strings.Split(content, "\n")
	var out strings.Builder
	var blockKind, blockTarget string
	var blockContent strings.Builder
	inBlock := false

	flushBlock := func() {
		if !inBlock {
			return
		}
		if readOnlyTools[blockKind] {
			out.WriteString(fmt.Sprintf("[Cleared: %s %s, %d bytes]\n",
				blockKind, blockTarget, blockContent.Len()))
		} else {
			out.WriteString(fmt.Sprintf("### %s %s\n", blockKind, blockTarget))
			out.WriteString(blockContent.String())
		}
		inBlock = false
		blockContent.Reset()
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "### ") {
			flushBlock()
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				blockKind = parts[1]
				blockTarget = ""
				if len(parts) >= 3 {
					blockTarget = parts[2]
				}
				inBlock = true
				continue
			}
		}
		if inBlock {
			blockContent.WriteString(line)
			blockContent.WriteString("\n")
		} else {
			out.WriteString(line)
			out.WriteString("\n")
		}
	}
	flushBlock()

	// Trim trailing double-newline that split+join can produce.
	result := out.String()
	if strings.HasSuffix(result, "\n\n") && !strings.HasSuffix(content, "\n\n") {
		result = result[:len(result)-1]
	}
	return result
}

// AutoCompact is the L1 compression layer (zero LLM cost). When the token
// budget is still exceeded after MicroCompact + compressAgenticHistory, this
// collapses all history to: system + initial user + mutation summary + latest 2 messages.
//
// This is a rule-based approach suitable for MVP. A full LLM-based summarizer
// (Secretary Worker) can replace this post-MVP for higher-fidelity summaries.
func AutoCompact(messages []Message, tokenBudget int) []Message {
	if len(messages) < 6 || msgTokens(messages) <= tokenBudget {
		return messages
	}

	// The auto-compact buffer: we trigger when within 13000 tokens of the limit.
	const autoCompactBuffer = 13000
	if msgTokens(messages) < tokenBudget-autoCompactBuffer {
		return messages
	}

	// Extract key mutation facts from the conversation for context preservation.
	var mutations []string
	for _, msg := range messages[2:] { // skip system + initial user
		if msg.Role != "user" {
			continue
		}
		// Extract mutation result headers (write_file, edit_file, run_shell).
		for _, line := range strings.Split(msg.Content, "\n") {
			if !strings.HasPrefix(line, "### ") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			kind := parts[1]
			if kind == "write_file" || kind == "edit_file" || kind == "run_shell" {
				mutations = append(mutations, line)
			}
		}
	}

	// Build compact summary message.
	var summary strings.Builder
	summary.WriteString("(Context auto-compacted to save tokens. Key mutations performed so far:\n")
	if len(mutations) == 0 {
		summary.WriteString("- No mutations yet.\n")
	} else {
		for _, m := range mutations {
			summary.WriteString(m)
			summary.WriteString("\n")
		}
	}
	summary.WriteString(")\n\nContinue with the task. Review current file state with read_file if needed.")

	// Reconstruct: system + initial user + summary + latest 2 messages.
	out := make([]Message, 0, 5)
	out = append(out, messages[0], messages[1]) // system + initial user
	out = append(out, Message{
		Role:    "user",
		Content: summary.String(),
	})
	// Append latest round (last 2 messages: assistant + user).
	if len(messages) >= 4 {
		out = append(out, messages[len(messages)-2:]...)
	}
	return out
}
