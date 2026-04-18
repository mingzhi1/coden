package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mingzhi1/coden/internal/llm/provider"
)

// RecoveryConfig controls which recovery layers are active.
type RecoveryConfig struct {
	// EnableEmergencyCompress enables the prompt-too-long recovery layer.
	// When true, a 413 / "prompt is too long" error triggers aggressive
	// message trimming followed by a single retry.
	EnableEmergencyCompress bool

	// FallbackModel is the model identifier to switch to when the primary
	// model is overloaded (529/429). Empty string disables this layer.
	FallbackModel string
}

// emergencyCompressKeepLast is the number of trailing messages preserved
// by emergencyCompress (in addition to messages[0]).
const emergencyCompressKeepLast = 10

// Chatter is the minimal interface for sending messages to an LLM.
// Both Broker (embedded providers) and LLMServerClient (llm-server RPC)
// satisfy this contract, enabling drop-in switching.
type Chatter interface {
	Chat(ctx context.Context, role string, messages []Message) (string, error)
}

// SideQuerier is an optional interface for lightweight LLM calls.
// Both Broker and LLMServerClient implement this. Callers should
// type-assert from Chatter when needed.
type SideQuerier interface {
	SideQuery(ctx context.Context, opts SideQueryOpts) (string, error)
}

// RecoverableChat wraps a Chatter with multi-layer error recovery.
//
// Layer 1 – prompt-too-long recovery:
//
//	If the error indicates the prompt exceeded the model's context window
//	(HTTP 413 or message containing "prompt is too long"), the message
//	history is aggressively compressed (system prompt + last N messages)
//	and the call is retried once.
//
// Layer 2 – model fallback recovery:
//
//	If the error indicates the model is overloaded or rate-limited
//	(HTTP 529/429 or message containing "overloaded" / "rate limit"),
//	and a FallbackModel is configured, the chatter is temporarily
//	pointed at the fallback model and the call is retried once.
//	NOTE: Layer 2 only works when chatter is a *Broker (SetRole requires it).
//
// Truncation recovery (finish_reason=length) is handled separately by
// the coder worker and is NOT part of this wrapper.
func RecoverableChat(ctx context.Context, chatter Chatter, role string, messages []Message, config RecoveryConfig) (string, error) {
	reply, err := chatter.Chat(ctx, role, messages)
	if err == nil {
		return reply, nil
	}

	// Circuit breaker tripped — don't retry, propagate immediately.
	var cbe *CircuitBreakerOpenError
	if errors.As(err, &cbe) {
		return reply, err
	}

	// Context cancelled — don't retry.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return reply, err
	}

	// Auth error — don't retry (fatal).
	var pe *provider.ProviderError
	if errors.As(err, &pe) && pe.IsAuthError() {
		return reply, err
	}

	// --- Layer 1: prompt-too-long → emergency compress → retry ----------
	if config.EnableEmergencyCompress && isPromptTooLongError(err) {
		slog.Warn("[llm:recovery] prompt-too-long detected, attempting emergency compress",
			"role", role,
			"original_messages", len(messages),
			"error", err,
		)

		compressed := emergencyCompress(messages, emergencyCompressKeepLast)
		slog.Info("[llm:recovery] retrying with compressed messages",
			"role", role,
			"compressed_messages", len(compressed),
		)

		retryReply, retryErr := chatter.Chat(ctx, role, compressed)
		if retryErr == nil {
			slog.Info("[llm:recovery] emergency compress recovery succeeded",
				"role", role,
				"reply_len", len(retryReply),
			)
			return retryReply, nil
		}

		slog.Warn("[llm:recovery] emergency compress retry also failed",
			"role", role,
			"error", retryErr,
		)
		return "", fmt.Errorf("recovery: emergency compress retry failed: %w", retryErr)
	}

	// --- Layer 2: model overloaded → fallback model → retry -------------
	// Layer 2 requires a *Broker for SetRole; skip for LLMServerClient
	// (llm-server handles its own fallback internally).
	if broker, ok := chatter.(*Broker); ok && config.FallbackModel != "" && isModelOverloadedError(err) {
		slog.Warn("[llm:recovery] model overloaded/rate-limited, switching to fallback model",
			"role", role,
			"fallback_model", config.FallbackModel,
			"error", err,
		)

		// Temporarily configure the fallback model for this role, make
		// the retry call, then restore the original state.
		//
		// We use SetRole which overrides the pool-level dispatch for the
		// given role. After the retry we clear the override so that
		// subsequent calls go through the normal pool routing.
		broker.SetRole(role, Config{Model: config.FallbackModel})
		defer func() {
			broker.ClearRoleOverride(role)
			slog.Info("[llm:recovery] restored original model routing", "role", role)
		}()

		retryReply, retryErr := broker.Chat(ctx, role, messages)
		if retryErr == nil {
			slog.Info("[llm:recovery] fallback model recovery succeeded",
				"role", role,
				"fallback_model", config.FallbackModel,
				"reply_len", len(retryReply),
			)
			return retryReply, nil
		}

		slog.Warn("[llm:recovery] fallback model retry also failed",
			"role", role,
			"fallback_model", config.FallbackModel,
			"error", retryErr,
		)
		return "", fmt.Errorf("recovery: fallback model %s also failed: %w", config.FallbackModel, retryErr)
	}

	// No recovery layer matched — return the original error.
	return reply, err
}

// emergencyCompress aggressively trims messages to fit within the model's
// context window. It preserves:
//
//   - messages[0] — the system prompt (assumed to always be present)
//   - a synthetic boundary marker explaining the trim
//   - the last `keepLast` messages — the most recent conversation context
//
// If the input has fewer messages than keepLast+1, it is returned unchanged
// (there is nothing to trim).
func emergencyCompress(messages []Message, keepLast int) []Message {
	// Nothing to compress: need at least system + keepLast + 1 (something to drop).
	if len(messages) <= keepLast+1 {
		return messages
	}

	tail := messages[len(messages)-keepLast:]
	dropped := len(messages) - 1 - keepLast // messages between head and tail

	compressed := make([]Message, 0, 1+1+keepLast)
	compressed = append(compressed, messages[0]) // system prompt
	compressed = append(compressed, Message{
		Role: "user",
		Content: fmt.Sprintf(
			"[emergency context compression: %d intermediate messages were removed to fit within the model context limit. Only the system prompt and the %d most recent messages are preserved.]",
			dropped, keepLast,
		),
	})
	compressed = append(compressed, tail...)

	return compressed
}

// ---------------------------------------------------------------------------
// Error classification helpers
// ---------------------------------------------------------------------------

// isPromptTooLongError returns true when the error indicates the prompt
// exceeded the model's maximum context length.
func isPromptTooLongError(err error) bool {
	// Check typed ProviderError first.
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		if pe.IsPromptTooLong() {
			return true
		}
	}
	// Fallback: string-based check for non-typed errors or ambiguous status codes.
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "prompt is too long") {
		return true
	}
	if strings.Contains(s, "context length") || strings.Contains(s, "maximum context") || strings.Contains(s, "token limit") {
		return true
	}
	return false
}

// defaultRecoveryConfig returns a RecoveryConfig with sensible defaults:
// emergency compression enabled, no fallback model (set via env/config).
func defaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		EnableEmergencyCompress: true,
		FallbackModel:           "", // no fallback by default; set via env/config
	}
}

// isModelOverloadedError returns true when the error indicates the model is
// temporarily unavailable due to overload or rate limiting.
func isModelOverloadedError(err error) bool {
	// Check typed ProviderError first.
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		if pe.IsRateLimit() || pe.IsOverloaded() {
			return true
		}
	}
	// Fallback: string-based check for non-typed errors.
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "overloaded") || strings.Contains(s, "rate limit") || strings.Contains(s, "rate_limit") {
		return true
	}
	return false
}
