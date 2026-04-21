// Package outputcompressor provides semantic-aware compression of tool
// execution output before it is fed back to the LLM. It replaces the naive
// character-based truncation with strategies that preserve key information
// (failures, errors, structure) while removing noise (progress bars,
// duplicate output).
package outputcompressor

import (
	"log/slog"
)

// Compressor applies a chain of strategies to compress tool output.
// The first matching strategy wins; if none match, the fallback truncation
// is applied.
type Compressor struct {
	strategies []Strategy
	// errorClassMap maps errorClass strings to strategy names for fast lookup.
	errorClassMap map[string]string
}

// New creates a Compressor with the default strategy chain.
func New() *Compressor {
	return &Compressor{
		strategies: DefaultStrategies(),
		errorClassMap: map[string]string{
			"compile_error": "compile_error",
			"test_failure":  "go_test",
		},
	}
}

// Compress applies semantic-aware compression to tool output.
//
// Parameters:
//   - kind: tool type ("run_shell", "read_file", "list_dir", etc.)
//   - command: the shell command string (non-empty only for run_shell)
//   - output: the raw output to compress
//   - budget: target maximum character count
//   - errorClass: optional error classification from toolruntime (e.g.
//     "compile_error", "test_failure"); empty string if not available.
//     When set, the matching strategy is tried first, avoiding unnecessary
//     Match() calls on other strategies.
//
// Returns the compressed output. If no strategy matches or compression fails,
// the output is truncated to budget characters as a fallback.
func (c *Compressor) Compress(kind, command, output string, budget int, errorClass string) string {
	// When errorClass is available, try the corresponding strategy first.
	if errorClass != "" {
		if targetName, ok := c.errorClassMap[errorClass]; ok {
			for _, s := range c.strategies {
				if s.Name() != targetName {
					continue
				}
				if result := s.Compress(output, budget); result != "" {
					return c.applyResult(s, kind, output, result, budget)
				}
				break
			}
		}
	}

	// Fall through to the full strategy chain.
	for _, s := range c.strategies {
		if !s.Match(kind, command, output) {
			continue
		}
		result := s.Compress(output, budget)
		if result == "" {
			continue
		}
		return c.applyResult(s, kind, output, result, budget)
	}

	// No strategy matched — only truncate if over budget.
	if len(output) <= budget {
		return output
	}
	return truncate(output, budget)
}

func (c *Compressor) applyResult(s Strategy, kind, input, result string, budget int) string {
	inputLen := len(input)
	if inputLen == 0 {
		inputLen = 1
	}
	slog.Debug("[outputcompressor] applied strategy",
		"strategy", s.Name(),
		"kind", kind,
		"input_len", len(input),
		"output_len", len(result),
		"saving_pct", 100-100*len(result)/inputLen,
	)
	if len(result) > budget {
		return truncate(result, budget)
	}
	return result
}

// truncate is the fail-safe fallback: simple character-based truncation.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}
