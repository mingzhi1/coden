package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Summarizer produces a faithful prose digest of a slice of conversation
// messages. It is the L2 (semantic) layer of the two-layer Compact: when the
// cheap structural passes can't fit history into budget, the dropped middle is
// summarized by an LLM instead of being collapsed lossily. Implemented by
// chatSummarizer over the loop's own Chatter.
type Summarizer interface {
	Summarize(ctx context.Context, messages []Message) (string, error)
}

// autoCompactBuffer is the token headroom below budget at which the L2 collapse
// triggers. It is a package var (not a const) so config (Context.AutoCompactThreshold)
// can activate the previously-dead knob via SetAutoCompactBuffer.
var autoCompactBuffer = 13000

// SetAutoCompactBuffer wires the configured auto-compact threshold into the
// Compact chain. A non-positive value is ignored (keeps the default).
func SetAutoCompactBuffer(tokens int) {
	if tokens > 0 {
		autoCompactBuffer = tokens
	}
}

// CompactHistory is the two-layer convergence shared by every agentic loop
// (executor, analyzer): L1 cheap structural trims (Snip → MicroCompact →
// compressAgenticHistory), then — only if still over budget — L2 semantic
// compaction (a faithful LLM summary of the dropped middle), falling back to the
// lossy rule-based AutoCompact when no summarizer is wired or the LLM fails.
// L2 fires only on genuine overflow, so a normal task pays zero extra LLM cost.
func CompactHistory(ctx context.Context, messages []Message, round, budget int, sum Summarizer) []Message {
	// L1: cheap, zero-LLM-cost structural passes.
	messages = SnipHistory(messages, snipMaxMessages)
	messages = MicroCompact(messages, round)
	messages = compressAgenticHistory(messages, budget)
	if msgTokens(messages) <= budget-autoCompactBuffer {
		return messages
	}
	// L2: faithful semantic summary preferred; lossy collapse as the safety net.
	if sum != nil {
		if out, ok := llmCompact(ctx, messages, sum); ok {
			return out
		}
	}
	return AutoCompact(messages, budget)
}

// llmCompact replaces the conversation's middle (everything but the system +
// initial user and the latest round) with a faithful LLM-produced digest. Returns
// ok=false (caller falls back) when there is nothing to summarize or the LLM fails.
func llmCompact(ctx context.Context, messages []Message, sum Summarizer) ([]Message, bool) {
	if len(messages) < 6 {
		return nil, false
	}
	head := messages[:2]               // system + initial user
	tail := messages[len(messages)-2:] // latest assistant + tool-result
	middle := messages[2 : len(messages)-2]
	if len(middle) == 0 {
		return nil, false
	}
	digest, err := sum.Summarize(ctx, middle)
	if err != nil || strings.TrimSpace(digest) == "" {
		slog.Warn("[llm:compact] L2 summary unavailable, falling back to lossy collapse", "error", err)
		return nil, false
	}
	slog.Info("[llm:compact] L2 semantic summary applied", "summarized_messages", len(middle), "digest_len", len(digest))
	out := make([]Message, 0, 4)
	out = append(out, head...)
	out = append(out, Message{
		Role:    "user",
		Content: "[Earlier work summarized to fit the context window — facts below are authoritative]\n" + digest,
	})
	out = append(out, tail...)
	return out, true
}

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

// chatSummarizer is the production Summarizer: it asks the loop's own Chatter to
// digest a run of messages. Used as the L2 layer; it runs at most once per loop
// when history overflows, so the extra LLM call is rare and bounded.
type chatSummarizer struct {
	chatter Chatter
	role    string
}

func newChatSummarizer(c Chatter, role string) Summarizer {
	if c == nil {
		return nil
	}
	return chatSummarizer{chatter: c, role: role}
}

func (s chatSummarizer) Summarize(ctx context.Context, messages []Message) (string, error) {
	var b strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
	}
	prompt := []Message{
		{Role: "system", Content: "You compress an AI coding agent's work history. Produce a faithful, concise digest that PRESERVES: decisions made, files read and what was learned, mutations performed (writes/edits/commands and their results), and any unresolved problems. Drop only redundancy and verbatim file dumps. Output prose, no preamble."},
		{Role: "user", Content: "Summarize this work history:\n\n" + b.String()},
	}
	reply, err := RecoverableChat(ctx, s.chatter, s.role, prompt, defaultRecoveryConfig())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(reply), nil
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

	// Trigger when within autoCompactBuffer tokens of the limit (config-driven via
	// SetAutoCompactBuffer; the L1→L2 chain already gates on the same threshold).
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
