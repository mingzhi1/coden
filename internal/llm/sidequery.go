package llm

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ---------------------------------------------------------------------------
// SideQuery – lightweight LLM calls that bypass the heavy broker.Chat() path
// ---------------------------------------------------------------------------

// SideQueryOpts configures a lightweight side query.
type SideQueryOpts struct {
	// Model overrides the default model. Empty uses the broker's light-model tier.
	Model string
	// System is a short system prompt (no tool schemas, no full context).
	// If non-empty it is prepended as a system-role message to Messages.
	System string
	// Messages is typically just 1-2 messages (e.g. a single user prompt).
	Messages []Message
	// MaxTokens caps the response length. Default: 1024.
	MaxTokens int
	// Temperature for sampling. Default: 0.
	Temperature float64
	// Timeout for the entire call. Default: 10s.
	Timeout time.Duration
	// Purpose is a label for logging/telemetry (e.g. "intent-classify").
	Purpose string
}

// sideQueryDefaults fills zero-valued fields with sensible defaults.
func (o *SideQueryOpts) applyDefaults() {
	if o.MaxTokens <= 0 {
		o.MaxTokens = 1024
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Purpose == "" {
		o.Purpose = "side-query"
	}
	// Temperature 0 is the zero value and also our desired default, so
	// no special handling needed.
}

// SideQuery executes a lightweight LLM call that bypasses the heavy
// broker.Chat() path.  It skips role-based dispatch, per-role usage
// tracking, and the retry/circuit-breaker machinery of the full broker,
// going straight through the pool's light-model tier instead.
//
// Design rationale
//   - Auxiliary tasks (intent classification, summary generation, insight
//     extraction, search-query expansion, simple quality checks) don't need
//     the strongest model or the full retry budget.
//   - A short, hard timeout (default 10 s) prevents auxiliary work from
//     blocking the main coding loop.
//   - Debug-level logging keeps telemetry cheap while still being observable.
//
// When Model is empty the pool's ChatLight tier picks the cheapest
// configured model.  When a specific Model is requested it is noted in
// the logs for traceability, but model selection is ultimately governed
// by the pool (the underlying provider infrastructure does not expose
// per-call model overrides through the pool API today).
func (b *Broker) SideQuery(ctx context.Context, opts SideQueryOpts) (string, error) {
	opts.applyDefaults()

	// ── Build the message slice ─────────────────────────────────────
	var msgs []Message
	if opts.System != "" {
		msgs = append(msgs, Message{Role: "system", Content: opts.System})
	}
	msgs = append(msgs, opts.Messages...)

	if len(msgs) == 0 {
		return "", fmt.Errorf("llm.SideQuery: no messages provided (purpose=%s)", opts.Purpose)
	}

	// ── Derive a model label for logging ────────────────────────────
	modelLabel := opts.Model
	if modelLabel == "" {
		modelLabel = b.pool.LightModel()
	}

	slog.Debug("[llm:sidequery] start",
		"purpose", opts.Purpose,
		"model", modelLabel,
		"messages", len(msgs),
		"max_tokens", opts.MaxTokens,
		"temperature", opts.Temperature,
		"timeout", opts.Timeout.String(),
	)

	// ── Apply timeout ───────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// ── Dispatch via the pool's light tier ───────────────────────────
	start := time.Now()

	reply, err := b.pool.ChatLight(ctx, msgs)

	elapsed := time.Since(start)

	// ── Log outcome ─────────────────────────────────────────────────
	if err != nil {
		slog.Debug("[llm:sidequery] failed",
			"purpose", opts.Purpose,
			"model", modelLabel,
			"elapsed_ms", elapsed.Milliseconds(),
			"error", err,
		)
		return reply, fmt.Errorf("llm.SideQuery(%s): %w", opts.Purpose, err)
	}

	slog.Debug("[llm:sidequery] done",
		"purpose", opts.Purpose,
		"model", modelLabel,
		"elapsed_ms", elapsed.Milliseconds(),
		"reply_len", len(reply),
	)

	return reply, nil
}
