// Package llm provides LLM-backed workflow workers and a multi-provider chat client.
package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/llm/provider"
)

// Message re-exports provider.Message for backward compatibility.
type Message = provider.Message

// Client wraps a single LLM provider+model pair.
type Client struct {
	model   string
	name    string
	backend provider.ChatProvider
}

// Config holds single-client configuration.
type Config struct {
	Provider string // "openai" | "anthropic" | "deepseek" | "acp" | ""=auto
	APIKey   string
	BaseURL  string
	Model    string
	// ACP-specific fields (only used when Provider == "acp").
	AcpName    string
	AcpCommand string
	AcpArgs    []string
	AcpEnv     map[string]string
	AcpCwd     string
}

// New creates a Client from config.
func New(cfg Config) *Client {
	backend, model := provider.New(provider.Config{
		Provider:   cfg.Provider,
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		AcpName:    cfg.AcpName,
		AcpCommand: cfg.AcpCommand,
		AcpArgs:    cfg.AcpArgs,
		AcpEnv:     cfg.AcpEnv,
		AcpCwd:     cfg.AcpCwd,
	})
	return &Client{model: model, name: backend.Name(), backend: backend}
}

func (c *Client) IsConfigured() bool { return c.backend.IsConfigured() }
func (c *Client) Model() string      { return c.model }
func (c *Client) Provider() string   { return c.name }

func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	if !c.backend.IsConfigured() {
		return "", fmt.Errorf("llm: no API key for %s", c.name)
	}
	return c.backend.Chat(ctx, c.model, messages)
}

// =====================================================================
// Pool: primary pool + light pool, both with fallback
// =====================================================================

// Pool manages two tiers of LLM clients with automatic fallback.
//
//	pool.Chat(ctx, msgs)       — tries primary clients in order
//	pool.ChatLight(ctx, msgs)  — tries light clients, falls back to primary
type Pool struct {
	primary []*Client // heavy-duty models (coder, acceptor)
	light   []*Client // cheap/fast models (inputter, planner)
	// Simple circuit breaker: consecutive failures per provider key.
	breakerMu      sync.Mutex
	breakerCounts  map[string]int
	breakerTripped map[string]time.Time
}

const (
	breakerThreshold = 5
	breakerCooldown  = 60 * time.Second
)

// NewPool creates an empty pool. Use Add/AddLight to populate.
func NewPool() *Pool {
	return &Pool{
		breakerCounts:  make(map[string]int),
		breakerTripped: make(map[string]time.Time),
	}
}

// Add appends a client to the primary pool (only if configured).
func (p *Pool) Add(cfg Config) {
	c := New(cfg)
	if c.IsConfigured() {
		p.primary = append(p.primary, c)
	}
}

// AddLight appends a client to the light pool (only if configured).
func (p *Pool) AddLight(cfg Config) {
	c := New(cfg)
	if c.IsConfigured() {
		p.light = append(p.light, c)
	}
}

// AddWithProvider appends a client wrapping a pre-constructed ChatProvider
// to the primary pool. Useful for testing or when a provider is obtained
// outside the standard factory path.
func (p *Pool) AddWithProvider(backend provider.ChatProvider, model string) {
	p.primary = append(p.primary, &Client{model: model, name: backend.Name(), backend: backend})
}

// Chat tries primary clients in order. Returns the first success.
func (p *Pool) Chat(ctx context.Context, messages []Message) (string, error) {
	return p.chatFromTier(ctx, p.primary, messages)
}

// ChatLight tries light clients first, falls back to primary pool.
func (p *Pool) ChatLight(ctx context.Context, messages []Message) (string, error) {
	if len(p.light) > 0 {
		reply, err := p.chatFromTier(ctx, p.light, messages)
		if err == nil {
			return reply, nil
		}
		// Truncation carries partial content — don't fall back to a different tier.
		var te *provider.TruncatedError
		if errors.As(err, &te) {
			return reply, err
		}
		slog.Warn("[llm] light tier exhausted, falling back to primary", "error", err)
	}
	return p.Chat(ctx, messages)
}

func (p *Pool) chatFromTier(ctx context.Context, tier []*Client, messages []Message) (string, error) {
	var lastErr error
	for i, c := range tier {
		key := c.Provider() + "/" + c.Model()
		if p.isCircuitOpen(key) {
			slog.Info("[llm] circuit breaker open, skipping", "provider", c.Provider(), "model", c.Model())
			continue
		}
		slog.Info("[llm] trying provider", "provider", c.Provider(), "model", c.Model(), "attempt", i+1, "total", len(tier))
		reply, err := chatWithRetry(ctx, c, messages)
		if err == nil {
			p.recordSuccess(key)
			slog.Info("[llm] provider succeeded", "provider", c.Provider(), "model", c.Model(), "reply_len", len(reply))
			return reply, nil
		}
		// Truncation is not a provider failure — return partial content immediately
		// so the caller can attempt recovery.
		var te *provider.TruncatedError
		if errors.As(err, &te) {
			return reply, err
		}
		p.recordFailure(key, err)
		slog.Warn("[llm] provider failed", "provider", c.Provider(), "model", c.Model(), "error", err)
		if i+1 < len(tier) {
			next := tier[i+1]
			slog.Info("[llm] falling back", "to_provider", next.Provider(), "to_model", next.Model())
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("llm: no configured providers in pool")
}

func (p *Pool) isCircuitOpen(key string) bool {
	p.breakerMu.Lock()
	defer p.breakerMu.Unlock()
	if p.breakerCounts[key] >= breakerThreshold {
		if time.Since(p.breakerTripped[key]) < breakerCooldown {
			return true
		}
		// Cooldown expired, reset
		delete(p.breakerCounts, key)
		delete(p.breakerTripped, key)
	}
	return false
}

func (p *Pool) recordFailure(key string, err error) {
	p.breakerMu.Lock()
	defer p.breakerMu.Unlock()
	// Only count retryable (transient) failures towards circuit breaker.
	// Non-retryable errors (parse errors, auth failures) should not trip the breaker.
	if !isRetryableError(err) {
		return
	}
	p.breakerCounts[key]++
	if p.breakerCounts[key] >= breakerThreshold && p.breakerTripped[key].IsZero() {
		p.breakerTripped[key] = time.Now()
		slog.Warn("[llm] circuit breaker tripped", "key", key)
	}
}

func (p *Pool) recordSuccess(key string) {
	p.breakerMu.Lock()
	defer p.breakerMu.Unlock()
	delete(p.breakerCounts, key)
	delete(p.breakerTripped, key)
}

// chatWithRetry retries transient errors (429, 5xx, timeouts) with exponential backoff.
// Returns immediately on non-retryable errors or context cancellation.
const maxRetries = 2

func chatWithRetry(ctx context.Context, c *Client, messages []Message) (string, error) {
	var lastErr error
	delay := 500 * time.Millisecond

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}

		reply, err := c.Chat(ctx, messages)
		if err == nil {
			return reply, nil
		}
		lastErr = err

		// Only retry on transient errors.
		if !isRetryableError(err) {
			return reply, err // preserve partial content (e.g., truncated responses)
		}
	}
	return "", lastErr
}

func isRetryableError(err error) bool {
	// Check for explicit retryable error type first
	var retryable *RetryableError
	if errors.As(err, &retryable) {
		return true
	}

	s := err.Error()
	// Rate limit
	if strings.Contains(s, "429") || strings.Contains(s, "rate") {
		return true
	}
	// Server errors
	for _, code := range []string{"500", "502", "503", "529"} {
		if strings.Contains(s, "API "+code) || strings.Contains(s, "error "+code) {
			return true
		}
	}
	// Network/timeout
	if strings.Contains(s, "timeout") || strings.Contains(s, "connection") {
		return true
	}
	return false
}

// RetryableError wraps an error to explicitly mark it as retryable.
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// Model returns the first primary model name.
func (p *Pool) Model() string {
	if len(p.primary) > 0 {
		return p.primary[0].Model()
	}
	return ""
}

// Provider returns the first primary provider name.
func (p *Pool) Provider() string {
	if len(p.primary) > 0 {
		return p.primary[0].Provider()
	}
	return ""
}

// IsConfigured returns true if at least one primary client is configured.
func (p *Pool) IsConfigured() bool {
	for _, c := range p.primary {
		if c.IsConfigured() {
			return true
		}
	}
	return false
}

// LightModel returns the first light model name (or primary fallback).
func (p *Pool) LightModel() string {
	for _, c := range p.light {
		if c.IsConfigured() {
			return c.Model()
		}
	}
	return p.Model()
}

// Summary returns a human-readable description of the pool.
func (p *Pool) Summary() string {
	var parts []string
	if len(p.primary) > 0 {
		names := make([]string, 0, len(p.primary))
		for _, c := range p.primary {
			names = append(names, c.Provider()+"/"+c.Model())
		}
		parts = append(parts, "primary: "+strings.Join(names, " → "))
	}
	if len(p.light) > 0 {
		names := make([]string, 0, len(p.light))
		for _, c := range p.light {
			names = append(names, c.Provider()+"/"+c.Model())
		}
		parts = append(parts, "light: "+strings.Join(names, " → "))
	}
	if len(parts) == 0 {
		return "no providers configured"
	}
	return strings.Join(parts, " | ")
}

// Primary returns a direct reference for backward compat.
// Deprecated: use Pool methods directly.
func (p *Pool) Primary() *Client {
	if len(p.primary) > 0 {
		return p.primary[0]
	}
	return nil
}

// =====================================================================
// Convenience constructors
// =====================================================================

// DefaultClient returns a single Client auto-configured from env.
func DefaultClient() *Client {
	return New(Config{})
}

// PoolFromConfig builds a Pool driven by the unified LLMConfig.
// Provider entries are resolved by name from cfg.Providers; the Pool tiers
// (Primary/Light) are populated in the order specified by cfg.Pool.
// If workspaceCwd is non-empty, ACP providers receive it as the session CWD.
func PoolFromConfig(cfg config.LLMConfig, workspaceCwd string) *Pool {
	pool := NewPool()

	resolve := func(name string) Config {
		entry, ok := cfg.Providers[name]
		if !ok {
			// Name might be a bare provider like "openai" — pass as-is.
			return Config{Provider: name}
		}
		if entry.EffectiveType() == "acp" {
			return Config{
				Provider:   "acp",
				AcpName:    name,
				AcpCommand: entry.Command,
				AcpArgs:    entry.Args,
				AcpEnv:     entry.Env,
				AcpCwd:     workspaceCwd,
			}
		}
		// HTTP provider
		return Config{
			Provider: name,
			APIKey:   entry.APIKey,
			BaseURL:  entry.BaseURL,
			Model:    entry.DefaultModel,
		}
	}

	for _, name := range cfg.Pool.Primary {
		pool.Add(resolve(name))
	}
	for _, name := range cfg.Pool.Light {
		pool.AddLight(resolve(name))
	}

	return pool
}

// BrokerFromConfig creates a Broker backed by a config-driven Pool.
// Per-role routing entries from cfg.Routing are applied as role-specific pools,
// enabling the heterogeneous constraint (e.g. critic.provider ≠ planner.provider).
func BrokerFromConfig(cfg config.LLMConfig, workspaceCwd string) *Broker {
	broker := NewBroker(PoolFromConfig(cfg, workspaceCwd))

	resolve := func(name string) Config {
		entry, ok := cfg.Providers[name]
		if !ok {
			return Config{Provider: name}
		}
		if entry.EffectiveType() == "acp" {
			return Config{
				Provider:   "acp",
				AcpName:    name,
				AcpCommand: entry.Command,
				AcpArgs:    entry.Args,
				AcpEnv:     entry.Env,
				AcpCwd:     workspaceCwd,
			}
		}
		return Config{
			Provider: name,
			APIKey:   entry.APIKey,
			BaseURL:  entry.BaseURL,
			Model:    entry.DefaultModel,
		}
	}

	// Apply per-role pool overrides from cfg.Routing.
	// routing: { critic: ["deepseek", "openai"] } wires a dedicated pool to the
	// critic role, enforcing the heterogeneous provider constraint.
	for role, providers := range cfg.Routing {
		if len(providers) == 0 {
			continue
		}
		rolePool := NewPool()
		for _, name := range providers {
			rolePool.Add(resolve(name))
		}
		broker.SetRolePool(role, rolePool)
	}

	return broker
}

// DefaultPool creates a pool auto-configured from env.
// Discovers all available API keys and adds them to appropriate tiers.
func DefaultPool() *Pool {
	pool := NewPool()

	// Primary tier: add all configured providers (strongest first)
	pool.Add(Config{Provider: "anthropic"})
	pool.Add(Config{Provider: "openai"})
	pool.Add(Config{Provider: "copilot"})
	pool.Add(Config{Provider: "deepseek"})
	pool.Add(Config{Provider: "minimax"})

	// Light tier: add cheaper/faster options
	pool.AddLight(Config{Provider: "deepseek"})
	pool.AddLight(Config{Provider: "openai"})
	pool.AddLight(Config{Provider: "copilot"})
	pool.AddLight(Config{Provider: "minimax"})

	return pool
}

// DefaultBroker creates a Broker backed by a default-configured Pool.
// The critic role is wired to an alternate provider order (openai-first) to
// implement the anti-narcissism heterogeneous constraint: when both anthropic
// and openai are configured, the critic uses a different provider than the planner.
func DefaultBroker() *Broker {
	broker := NewBroker(DefaultPool())

	// Critic pool: prefer openai over anthropic so the critic is likely to use a
	// different underlying model than the planner (which prefers anthropic first).
	// Falls back gracefully when only one provider is available.
	criticPool := NewPool()
	criticPool.Add(Config{Provider: "openai"})
	criticPool.Add(Config{Provider: "deepseek"})
	criticPool.Add(Config{Provider: "anthropic"})
	criticPool.Add(Config{Provider: "copilot"})
	criticPool.Add(Config{Provider: "minimax"})
	broker.SetRolePool(RoleCritic, criticPool)

	return broker
}
