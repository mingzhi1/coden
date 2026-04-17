package llm

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/llm/tokenbudget"
)

// Role names for per-role model dispatch.
// Strong roles (decision points) use the primary pool tier by default.
// Light roles (execution points) use the light pool tier by default.
const (
	RoleInputter  = "inputter"
	RolePlanner   = "planner"
	RoleCritic    = "critic"
	RoleCoder     = "coder"
	RoleAcceptor  = "acceptor"
	RoleReplanner = "replanner"
)

// strongRoles identifies which roles default to the primary (strong) pool tier.
// Decision points are strong; execution points are light.
var strongRoles = map[string]bool{
	RolePlanner:   true, // decides WHAT to build
	RoleCritic:    true, // cross-reviews the plan (anti-narcissism)
	RoleAcceptor:  true, // decides whether the build is correct
	RoleReplanner: true, // revises the plan based on critic + discovery
}

// UsageStats records accumulated LLM usage for a single role.
type UsageStats struct {
	Calls       int64 // number of completed calls
	InputTokens int64 // estimated input tokens sent
	OutTokens   int64 // estimated output tokens received
}

// Broker wraps a Pool and adds per-role model selection and usage tracking.
//
// Workers call broker.Chat(ctx, role, messages) instead of pool.Chat / pool.ChatLight.
// The dispatch order is:
//  1. Role-specific pool configured via SetRolePool (highest priority, multi-provider)
//  2. Role-specific client configured via SetRole (single-provider override)
//  3. pool.Chat()      for strong roles (Planner, Critic, Acceptor, Replanner)
//  4. pool.ChatLight() for light  roles (Inputter, Coder)
type Broker struct {
	pool          *Pool
	mu            sync.Mutex
	rolePools     map[string]*Pool   // role → dedicated pool (heterogeneous routing)
	roleOverrides map[string]*Client // role → single-client override
	usage         map[string]*UsageStats
}

// NewBroker creates a Broker backed by the given pool.
func NewBroker(pool *Pool) *Broker {
	return &Broker{
		pool:          pool,
		rolePools:     make(map[string]*Pool),
		roleOverrides: make(map[string]*Client),
		usage:         make(map[string]*UsageStats),
	}
}

// SetRolePool assigns a dedicated Pool to role, enabling multi-provider role routing.
// Use this to enforce the heterogeneous constraint (critic.provider ≠ planner.provider).
// A pool with no configured clients is ignored.
func (b *Broker) SetRolePool(role string, pool *Pool) {
	if pool == nil || !pool.IsConfigured() {
		return
	}
	b.mu.Lock()
	b.rolePools[role] = pool
	b.mu.Unlock()
}

// SetRole configures a specific LLM client for role, overriding the pool tier.
// The call is ignored when cfg yields an unconfigured client.
func (b *Broker) SetRole(role string, cfg Config) {
	c := New(cfg)
	if !c.IsConfigured() {
		return
	}
	b.mu.Lock()
	b.roleOverrides[role] = c
	b.mu.Unlock()
}

// Chat dispatches an LLM call for role and accumulates token usage on success.
func (b *Broker) Chat(ctx context.Context, role string, messages []Message) (string, error) {
	b.mu.Lock()
	rolePool := b.rolePools[role]
	override := b.roleOverrides[role]
	b.mu.Unlock()

	start := time.Now()
	promptLen := 0
	for _, m := range messages {
		promptLen += len(m.Content)
	}
	slog.Info("[llm:broker] dispatching", "role", role, "messages", len(messages), "prompt_len", promptLen)

	var reply string
	var err error
	switch {
	case rolePool != nil:
		slog.Info("[llm:broker] using role pool", "role", role, "provider", rolePool.Provider(), "model", rolePool.Model())
		reply, err = rolePool.Chat(ctx, messages)
	case override != nil:
		slog.Info("[llm:broker] using role override", "role", role, "provider", override.Provider(), "model", override.Model())
		reply, err = chatWithRetry(ctx, override, messages)
	case strongRoles[role]:
		reply, err = b.pool.Chat(ctx, messages)
	default:
		reply, err = b.pool.ChatLight(ctx, messages)
	}

	duration := time.Since(start)
	if err != nil {
		slog.Warn("[llm:broker] call failed", "role", role, "duration_ms", duration.Milliseconds(), "error", err)
	} else {
		slog.Info("[llm:broker] call completed", "role", role, "duration_ms", duration.Milliseconds(), "reply_len", len(reply))
		b.recordUsage(role, messages, reply)
	}
	return reply, err
}

func (b *Broker) recordUsage(role string, messages []Message, reply string) {
	var inputToks int64
	for _, m := range messages {
		inputToks += int64(tokenbudget.EstimateTokens(m.Content))
	}
	outToks := int64(tokenbudget.EstimateTokens(reply))

	b.mu.Lock()
	s, ok := b.usage[role]
	if !ok {
		s = &UsageStats{}
		b.usage[role] = s
	}
	s.Calls++
	s.InputTokens += inputToks
	s.OutTokens += outToks
	b.mu.Unlock()
}

// Usage returns a snapshot of accumulated usage stats per role.
func (b *Broker) Usage() map[string]UsageStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]UsageStats, len(b.usage))
	for role, s := range b.usage {
		out[role] = *s
	}
	return out
}

// Pool returns the underlying pool.
func (b *Broker) Pool() *Pool { return b.pool }

// IsConfigured returns true if the underlying pool has at least one configured client.
func (b *Broker) IsConfigured() bool { return b.pool.IsConfigured() }

// Model returns the first primary model name from the underlying pool.
func (b *Broker) Model() string { return b.pool.Model() }

// Provider returns the first primary provider name from the underlying pool.
func (b *Broker) Provider() string { return b.pool.Provider() }

// LightModel returns the first light model name from the underlying pool.
func (b *Broker) LightModel() string { return b.pool.LightModel() }

// Summary returns a human-readable description of the pool configuration.
func (b *Broker) Summary() string { return b.pool.Summary() }
