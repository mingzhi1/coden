package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/mingzhi1/coden/internal/ui/styles"
)

const panelHorizontalFrame = 4

func renderDiffDetailLine(line string) string {
	trimmed := strings.TrimRight(line, " \t")
	switch {
	case strings.HasPrefix(trimmed, "@@"):
		return styles.PrimaryText.Render(trimmed)
	case strings.HasPrefix(trimmed, "+++"), strings.HasPrefix(trimmed, "---"):
		return styles.WarningText.Render(trimmed)
	case strings.HasPrefix(trimmed, "+"):
		return styles.SuccessText.Render(trimmed)
	case strings.HasPrefix(trimmed, "-"):
		return styles.ErrorText.Render(trimmed)
	default:
		return styles.MutedText.Render(trimmed)
	}
}

func renderChangedLine(item changeItem, width int) string {
	statusStyle := styles.MutedText
	switch strings.ToLower(item.Status) {
	case "denied", "failed", "error":
		statusStyle = styles.ErrorText
	case "deleted", "removed":
		statusStyle = styles.ErrorText
	case "written", "added", "modified", "updated":
		statusStyle = styles.SuccessText
	case "running":
		statusStyle = styles.PrimaryText
	}

	statusText := strings.TrimSpace(item.Status)
	if statusText == "" {
		statusText = "unknown"
	}
	countText := fmt.Sprintf("x%d", item.Count)
	metaParts := make([]string, 0, 2)
	if item.Tool != "" {
		metaParts = append(metaParts, item.Tool)
	}
	if item.HasDuration {
		metaParts = append(metaParts, fmt.Sprintf("%dms", item.DurationMS))
	}
	metaText := ""
	if len(metaParts) > 0 {
		metaText = strings.Join(metaParts, " ")
	}

	nameText := strings.TrimSpace(item.Name)
	if nameText == "" {
		nameText = "unnamed"
	}
	if width > 0 {
		fixedWidth := lipgloss.Width(statusText) + 1 + lipgloss.Width(countText) + 2
		if metaText != "" {
			fixedWidth += 2 + lipgloss.Width(metaText)
		}
		nameText = truncatePanelText(nameText, max(6, width-fixedWidth))
	}

	status := statusStyle.Render(statusText)
	count := styles.MutedText.Render(countText)
	name := styles.NormalText.Render(nameText)
	line := fmt.Sprintf("%s %s  %s", status, count, name)
	if metaText != "" {
		line += "  " + styles.MutedText.Render(metaText)
	}
	if item.ExitCode != 0 {
		line += "  " + styles.MutedText.Render(fmt.Sprintf("exit=%d", item.ExitCode))
	}
	return line
}

func renderWorkerLine(item workerItem, width int) string {
	name := item.Role
	if name == "" {
		name = item.ID
	}
	if item.Step != "" {
		name += " / " + item.Step
	}

	marker := "-"
	switch item.Status {
	case "done":
		marker = "✓"
	case "running":
		marker = "›"
	case "warn":
		marker = "!"
	case "error":
		marker = "✗"
	}

	metaParts := make([]string, 0, 2)
	if item.ToolCallID != "" {
		metaParts = append(metaParts, "tool")
	}
	if item.HasDuration {
		metaParts = append(metaParts, fmt.Sprintf("%dms", item.DurationMS))
	}
	metaText := strings.Join(metaParts, " ")
	if width > 0 {
		fixedWidth := lipgloss.Width(marker) + 1
		if metaText != "" {
			fixedWidth += 2 + lipgloss.Width(metaText)
		}
		name = truncatePanelText(name, max(6, width-fixedWidth))
	}

	line := fmt.Sprintf("%s %s", marker, name)
	switch item.Status {
	case "running":
		line = styles.PrimaryText.Render(line)
	case "done":
		line = styles.MutedText.Render(line)
	case "warn":
		line = styles.WarningText.Render(line)
	case "error":
		line = styles.ErrorText.Render(line)
	default:
		line = styles.NormalText.Render(line)
	}

	if metaText != "" {
		line += "  " + styles.MutedText.Render(metaText)
	}
	return line
}

func statusWord(status string) string {
	switch strings.ToLower(status) {
	case "written", "added", "modified", "updated":
		return "wrote"
	case "deleted", "removed":
		return "removed"
	case "denied":
		return "denied"
	case "failed", "error":
		return "failed"
	default:
		return status
	}
}

func truncateSingleLine(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " "))
	runes := []rune(s)
	if limit <= 0 || len(runes) <= limit {
		return s
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func truncateMiddle(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " "))
	runes := []rune(s)
	if limit <= 0 || len(runes) <= limit {
		return s
	}
	if limit <= 3 {
		return string(runes[:limit])
	}

	head := (limit - 3) / 2
	tail := limit - 3 - head
	return string(runes[:head]) + "..." + string(runes[len(runes)-tail:])
}

func truncatePathMiddle(s string, limit int) string {
	if strings.IndexAny(s, `/\`) == -1 {
		return truncateMiddle(s, limit)
	}
	return truncateMiddle(s, limit)
}

func truncatePanelText(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " "))
	if strings.IndexAny(s, `/\`) != -1 {
		return truncatePathMiddle(s, limit)
	}
	return truncateSingleLine(s, limit)
}

func truncateDetail(s string, maxLines int) string {
	s = strings.TrimSpace(s)
	if s == "" || maxLines <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

func renderMessageBlock(kind, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	lines := []string{chatMetaLine(kind, "")}
	if kind == "assistant" {
		lines = append(lines, renderAssistantMarkdownLines(text)...)
	} else {
		for _, line := range strings.Split(text, "\n") {
			lines = append(lines, chatBodyLine(kind, line))
		}
	}
	return append(lines, "")
}

func renderMetaBlock(kind, title, text string) []string {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	head := title
	if head == "" {
		head = text
		text = ""
	} else if text != "" {
		head += "  " + text
	}
	lines := []string{chatMetaLine(kind, head)}
	return append(lines, "")
}

// stripANSI removes ANSI escape sequences from a string.
// Handles both CSI sequences (\x1b[...letter) and OSC sequences (\x1b]...\x1b\\ or \x1b]...\x07).
func stripANSI(s string) string {
	var result strings.Builder
	result.Grow(len(s))

	const (
		stateNormal = iota
		stateEsc    // saw \x1b, waiting for [ or ]
		stateCSI    // inside CSI sequence (\x1b[...)
		stateOSC    // inside OSC sequence (\x1b]...)
		stateOSCEsc // inside OSC, saw \x1b (waiting for \\)
	)
	state := stateNormal

	for _, r := range s {
		switch state {
		case stateNormal:
			if r == '\x1b' {
				state = stateEsc
			} else {
				result.WriteRune(r)
			}
		case stateEsc:
			switch r {
			case '[':
				state = stateCSI
			case ']':
				state = stateOSC
			default:
				// Unknown escape — consume the single char and return to normal
				state = stateNormal
			}
		case stateCSI:
			// CSI terminates on any letter (a-z, A-Z)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				state = stateNormal
			}
		case stateOSC:
			// OSC terminates on BEL (\x07) or ST (\x1b\\)
			if r == '\x07' {
				state = stateNormal
			} else if r == '\x1b' {
				state = stateOSCEsc
			}
		case stateOSCEsc:
			// Expecting \\ to complete ST, but accept anything as terminator
			state = stateNormal
		}
	}
	return result.String()
}

func sameChatBlock(existing, next []string) bool {
	if len(next) == 0 || len(existing) < len(next) {
		return false
	}
	start := len(existing) - len(next)
	for i := range next {
		// Compare with ANSI sequences stripped to avoid false negatives
		if stripANSI(existing[start+i]) != stripANSI(next[i]) {
			return false
		}
	}
	return true
}

func chatMetaLine(kind, text string) string {
	text = strings.TrimSpace(text)
	var label string
	switch kind {
	case "user":
		label = styles.ChatUserLabel.Render("YOU")
	case "assistant":
		label = styles.ChatAssistantLabel.Render("CODE")
	case "plan":
		label = styles.ChatPlanLabel.Render("PLAN")
	case "tool":
		label = styles.ChatToolLabel.Render("TOOL")
	case "edit":
		label = styles.ChatEditLabel.Render("EDIT")
	case "warn":
		label = styles.ChatWarnLabel.Render("WARN")
	case "error":
		label = styles.ChatErrorLabel.Render("ERR")
	default:
		label = styles.ChatSystemLabel.Render("SYS")
	}
	if text == "" {
		return label
	}
	return label + " " + styles.NormalText.Render(text)
}

func chatBodyLine(kind, text string) string {
	text = strings.TrimRight(text, " \t")
	if strings.TrimSpace(text) == "" {
		text = " "
	}
	return chatPrefixStyle(kind).Render("  | ") + styles.NormalText.Render(text)
}

func chatDetailLine(kind, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return chatPrefixStyle(kind).Render("  - ") + styles.MutedText.Render(text)
}

func chatPrefixStyle(kind string) lipgloss.Style {
	switch kind {
	case "user":
		return styles.ChatUserAccent
	case "assistant":
		return styles.ChatAssistantAccent
	case "plan":
		return styles.ChatPlanAccent
	case "tool":
		return styles.ChatToolAccent
	case "edit":
		return styles.ChatEditAccent
	case "warn":
		return styles.ChatWarnAccent
	case "error":
		return styles.ChatErrorAccent
	default:
		return styles.ChatSystemAccent
	}
}

func renderAssistantMarkdownLines(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			label := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			if label == "" {
				label = "code"
			}
			out = append(out, assistantMarkdownLine("  > ", styles.MarkdownCode.Render("["+label+"]")))
			continue
		}

		if inFence {
			if trimmed == "" {
				out = append(out, assistantMarkdownLine("  > ", " "))
			} else {
				out = append(out, assistantMarkdownLine("  > ", styles.MarkdownCode.Render(line)))
			}
			continue
		}

		if trimmed == "" {
			out = append(out, chatBodyLine("assistant", " "))
			continue
		}

		// Horizontal rule
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			out = append(out, assistantMarkdownLine("  ", styles.MutedText.Render("────────────────────────")))
			continue
		}

		if heading, ok := parseMarkdownHeading(trimmed); ok {
			out = append(out, assistantMarkdownLine("  # ", styles.MarkdownHeading.Render(heading)))
			continue
		}

		if quote, ok := parseMarkdownQuote(trimmed); ok {
			out = append(out, assistantMarkdownLine("  > ", styles.MarkdownQuote.Render(quote)))
			continue
		}

		if bullet, ok := parseMarkdownBullet(trimmed); ok {
			out = append(out, assistantMarkdownLine("  • ", highlightInlineMarkdown(bullet, styles.MarkdownBullet)))
			continue
		}

		// Numbered list: 1. item, 2. item, etc.
		if num, item, ok := parseNumberedItem(trimmed); ok {
			prefix := fmt.Sprintf("  %s ", num)
			out = append(out, assistantMarkdownLine(prefix, highlightInlineMarkdown(item, styles.NormalText)))
			continue
		}

		out = append(out, chatBodyLine("assistant", highlightInlineMarkdown(trimmed, styles.NormalText)))
	}

	return out
}

func parseMarkdownHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	trimmed := strings.TrimLeft(line, "#")
	if len(trimmed) == len(line) {
		return "", false
	}
	return strings.TrimSpace(trimmed), true
}

func parseMarkdownQuote(line string) (string, bool) {
	if !strings.HasPrefix(line, ">") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, ">")), true
}

func parseMarkdownBullet(line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "- "):
		return strings.TrimSpace(strings.TrimPrefix(line, "- ")), true
	case strings.HasPrefix(line, "* "):
		return strings.TrimSpace(strings.TrimPrefix(line, "* ")), true
	case strings.HasPrefix(line, "+ "):
		return strings.TrimSpace(strings.TrimPrefix(line, "+ ")), true
	default:
		return "", false
	}
}

func assistantMarkdownLine(prefix, text string) string {
	if strings.TrimSpace(text) == "" {
		text = " "
	}
	return styles.ChatAssistantAccent.Render(prefix) + text
}

// parseNumberedItem matches "1. item", "2. item", etc.
func parseNumberedItem(line string) (string, string, bool) {
	for i := 0; i < len(line); i++ {
		if line[i] >= '0' && line[i] <= '9' {
			continue
		}
		if line[i] == '.' && i > 0 && i+1 < len(line) && line[i+1] == ' ' {
			return line[:i+1], strings.TrimSpace(line[i+2:]), true
		}
		break
	}
	return "", "", false
}

// highlightInlineMarkdown parses **bold**, *italic*, and `code` spans.
// Plain text runs are accumulated and flushed as a single Render call
// to avoid wrapping each rune in its own ANSI escape sequence.
func highlightInlineMarkdown(text string, baseStyle lipgloss.Style) string {
	if !strings.ContainsAny(text, "`*") {
		return baseStyle.Render(text)
	}

	var out strings.Builder
	runes := []rune(text)
	i := 0
	plainStart := -1 // start index of current plain-text run, -1 = no active run

	// flushPlain renders accumulated plain runes [plainStart..i) as one batch.
	flushPlain := func() {
		if plainStart >= 0 && plainStart < i {
			out.WriteString(baseStyle.Render(string(runes[plainStart:i])))
		}
		plainStart = -1
	}

	for i < len(runes) {
		// Backtick code spans
		if runes[i] == '`' {
			end := indexRune(runes, '`', i+1)
			if end > i+1 {
				flushPlain()
				out.WriteString(styles.MarkdownInlineCode.Render(string(runes[i+1 : end])))
				i = end + 1
				continue
			}
		}
		// **bold**
		if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '*' {
			end := indexDoubleRune(runes, '*', i+2)
			if end > i+2 {
				flushPlain()
				out.WriteString(styles.BoldText.Render(string(runes[i+2 : end])))
				i = end + 2
				continue
			}
		}
		// *italic*
		if runes[i] == '*' && (i+1 < len(runes) && runes[i+1] != '*') {
			end := indexRune(runes, '*', i+1)
			if end > i+1 {
				flushPlain()
				out.WriteString(styles.MarkdownQuote.Render(string(runes[i+1 : end])))
				i = end + 1
				continue
			}
		}
		// Accumulate plain text instead of rendering per-rune
		if plainStart < 0 {
			plainStart = i
		}
		i++
	}
	// Flush remaining plain text
	flushPlain()
	return out.String()
}

func indexRune(runes []rune, target rune, start int) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

func indexDoubleRune(runes []rune, target rune, start int) int {
	for i := start; i < len(runes)-1; i++ {
		if runes[i] == target && runes[i+1] == target {
			return i
		}
	}
	return -1
}

