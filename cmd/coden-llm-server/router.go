package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// Router maps role_hint to a chain of providers.
type Router struct {
	routes map[string][]ChatProvider // role_hint → provider chain
}

// NewRouter creates a router from config.
func NewRouter(providers map[string]ChatProvider, routing map[string][]string) *Router {
	if routing == nil {
		routing = make(map[string][]string)
	}
	r := &Router{routes: make(map[string][]ChatProvider)}
	for roleHint, names := range routing {
		var chain []ChatProvider
		for _, name := range names {
			if p, ok := providers[name]; ok && p.IsConfigured() {
				chain = append(chain, p)
			} else {
				slog.Warn("[router] provider not configured or missing", "name", name)
			}
		}
		if len(chain) > 0 {
			r.routes[roleHint] = chain
		}
	}

	// If no explicit routes defined, create default from all available providers.
	if len(r.routes) == 0 {
		for name, p := range providers {
			if p.IsConfigured() {
				r.routes["coder"] = append(r.routes["coder"], p)
				slog.Info("[router] auto-route", "role", "coder", "provider", name)
			}
		}
	}
	return r
}

// Resolve returns the first available provider for the given role_hint.
// Returns error if no provider is available for this role.
func (r *Router) Resolve(roleHint string) (ChatProvider, string) {
	chain, ok := r.routes[roleHint]
	if !ok {
		// Fallback: try "coder" route
		chain = r.routes["coder"]
	}
	if len(chain) == 0 {
		return nil, ""
	}
	return chain[0], chain[0].Name()
}

// ChatWithFallback tries each provider in the chain until one succeeds.
// Returns the first successful response, or an error from the last attempt.
func (r *Router) ChatWithFallback(ctx context.Context, roleHint string, model string, messages []Message) (*chatResult, error) {
	chain, ok := r.routes[roleHint]
	if !ok {
		chain = r.routes["coder"]
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("no provider available for role=%s", roleHint)
	}

	var lastErr error
	for i, p := range chain {
		name := p.Name()
		slog.Debug("[router] trying provider", "index", i, "provider", name)

		text, err := p.Chat(ctx, model, messages)
		if err == nil {
			return &chatResult{
				Text:     text,
				Provider: name,
				Model:    model,
				StopReason: "end_turn",
			}, nil
		}

		lastErr = err
		slog.Warn("[router] provider failed, trying next",
			"provider", name, "err", err, "remaining", len(chain)-i-1)

		// Check for truncated — don't fallback on truncation, let caller handle it.
		if te, ok := err.(*TruncatedError); ok {
			return &chatResult{
				Text:       te.Content,
				Provider:   name,
				Model:      model,
				StopReason: "max_tokens",
			}, err
		}
	}

	return nil, fmt.Errorf("all providers failed for role=%s: %w", roleHint, lastErr)
}

// chatResult holds the result of a chat call through the router.
type chatResult struct {
	Text       string `json:"text"`
	StopReason string `json:"stop_reason"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
}

// toRPC converts chatResult to ChatResult (RPC wire format).
func (c *chatResult) toRPC() ChatResult {
	return ChatResult{
		Text:       c.Text,
		StopReason: c.StopReason,
		Provider:   c.Provider,
		Model:      c.Model,
	}
}

// toRPCError wraps an error as a JSON-RPC error response with appropriate codes.
func toRPCError(id *json.RawMessage, err error) protocol.Response {
	if te, ok := err.(*TruncatedError); ok {
		data, _ := json.Marshal(map[string]any{
			"partial":    te.Content,
			"retryable": true,
		})
		return protocol.Response{
			JSONRPC: protocol.Version,
			ID:      id,
			Error: &protocol.Error{Code: -32001, Message: "truncated", Data: data},
		}
	}

	// Generic internal error
	return protocol.NewError(id, -32603, err.Error())
}
