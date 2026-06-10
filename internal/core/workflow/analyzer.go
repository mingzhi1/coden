package workflow

import (
	"context"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
)

// Analyzer performs read-only code investigation for analyze intents (code
// review, architecture understanding, diagnosis). It reads code but NEVER
// modifies it, and returns its findings as user-facing prose. It is the
// analyze-path counterpart of the Executor: where the Executor writes, the Analyzer
// only reads and reasons.
type Analyzer interface {
	Analyze(ctx context.Context, intent model.IntentSpec) (string, error)
}

// LocalAnalyzer is the deterministic fallback used when no LLM analyzer is
// configured. It cannot actually inspect code, so it returns a terse
// acknowledgement that keeps the workflow functional in offline/loopback mode.
type LocalAnalyzer struct{}

func NewLocalAnalyzer() *LocalAnalyzer { return &LocalAnalyzer{} }

func (a *LocalAnalyzer) Analyze(_ context.Context, intent model.IntentSpec) (string, error) {
	if g := strings.TrimSpace(intent.Goal); g != "" {
		return "Analysis requested: " + g + " (no analyzer model configured — unable to inspect code).", nil
	}
	return "No analysis available (no analyzer model configured).", nil
}
