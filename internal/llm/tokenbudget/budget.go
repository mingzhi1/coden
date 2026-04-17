// Package tokenbudget provides token estimation and context budget management for LLM calls.
// It uses a character-based heuristic (4 chars ≈ 1 token) to avoid external tokenizer dependencies.
package tokenbudget

// EstimateTokens returns an approximate token count for the given text.
// Uses the widely-accepted 4-chars-per-token heuristic for English/code.
func EstimateTokens(text string) int {
	n := len(text)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4 // ceiling division by 4
}

// Budget manages token allocation across different context components.
type Budget struct {
	MaxContextTokens int // total context window of the model
	ReservedOutput   int // tokens reserved for the model's response
	SystemPrompt     int // tokens used by the system prompt (estimated at call time)
}

// Available returns the remaining tokens available for user/context content.
func (b Budget) Available() int {
	avail := b.MaxContextTokens - b.ReservedOutput - b.SystemPrompt
	if avail < 0 {
		return 0
	}
	return avail
}

// DefaultBudget returns a budget suitable for common models.
// A 128k context model with 4k reserved for output is the default.
func DefaultBudget() Budget {
	return Budget{
		MaxContextTokens: 128000,
		ReservedOutput:   4096,
	}
}

// BudgetForModel returns a model-specific budget based on known context window sizes.
func BudgetForModel(model string) Budget {
	switch {
	case contains(model, "claude-3-5-sonnet", "claude-3.5-sonnet"):
		return Budget{MaxContextTokens: 200000, ReservedOutput: 8192}
	case contains(model, "claude-3-5-haiku", "claude-3.5-haiku"):
		return Budget{MaxContextTokens: 200000, ReservedOutput: 8192}
	case contains(model, "claude-3-opus"):
		return Budget{MaxContextTokens: 200000, ReservedOutput: 4096}
	case contains(model, "gpt-4o"):
		return Budget{MaxContextTokens: 128000, ReservedOutput: 4096}
	case contains(model, "gpt-4-turbo"):
		return Budget{MaxContextTokens: 128000, ReservedOutput: 4096}
	case contains(model, "gpt-4"):
		return Budget{MaxContextTokens: 8192, ReservedOutput: 2048}
	case contains(model, "gpt-3.5"):
		return Budget{MaxContextTokens: 16384, ReservedOutput: 2048}
	case contains(model, "deepseek"):
		return Budget{MaxContextTokens: 64000, ReservedOutput: 4096}
	default:
		return DefaultBudget()
	}
}

// TruncateHistory trims messages to fit within the given token budget.
// It keeps the most recent messages, dropping older ones first.
// Returns the truncated slice and the tokens used.
func TruncateHistory(messages []string, budgetTokens int) ([]string, int) {
	if len(messages) == 0 || budgetTokens <= 0 {
		return nil, 0
	}

	// Walk from newest (end) to oldest, accumulating tokens.
	used := 0
	start := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		cost := EstimateTokens(messages[i])
		if used+cost > budgetTokens {
			break
		}
		used += cost
		start = i
	}

	if start >= len(messages) {
		return nil, 0
	}
	return messages[start:], used
}

// TruncateFileTree trims a file tree listing to fit within the token budget.
// Files at the beginning of the list (top-level, most relevant) are preferred.
func TruncateFileTree(files []string, budgetTokens int) ([]string, int) {
	if len(files) == 0 || budgetTokens <= 0 {
		return nil, 0
	}

	used := 0
	end := 0
	for i := 0; i < len(files); i++ {
		cost := EstimateTokens("- "+files[i]+"\n") // include formatting overhead
		if used+cost > budgetTokens {
			break
		}
		used += cost
		end = i + 1
	}

	if end == 0 {
		return nil, 0
	}
	return files[:end], used
}

// contains checks if s contains any of the given substrings.
func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
