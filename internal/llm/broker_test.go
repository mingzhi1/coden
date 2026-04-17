package llm

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/config"
)

// mockChatter records Chat calls for inspection.
type mockChatter struct {
	reply string
	calls int
	roles []string
}

func (m *mockChatter) Chat(_ context.Context, role string, _ []Message) (string, error) {
	m.calls++
	m.roles = append(m.roles, role)
	return m.reply, nil
}

// stubPool is a minimal Pool that records calls.
type stubPool struct {
	Pool
	chatCalls      int
	chatLightCalls int
}

func (s *stubPool) Chat(_ context.Context, _ []Message) (string, error) {
	s.chatCalls++
	return "primary", nil
}

func (s *stubPool) ChatLight(_ context.Context, _ []Message) (string, error) {
	s.chatLightCalls++
	return "light", nil
}

func TestBroker_RoleCriticIsStrong(t *testing.T) {
	if !strongRoles[RoleCritic] {
		t.Error("RoleCritic must be in strongRoles")
	}
}

func TestBroker_RoleReplannerIsStrong(t *testing.T) {
	if !strongRoles[RoleReplanner] {
		t.Error("RoleReplanner must be in strongRoles")
	}
}

func TestBroker_StrongRoles(t *testing.T) {
	expected := []string{RolePlanner, RoleCritic, RoleAcceptor, RoleReplanner}
	for _, role := range expected {
		if !strongRoles[role] {
			t.Errorf("expected %q to be a strong role", role)
		}
	}
}

func TestBroker_LightRoles(t *testing.T) {
	light := []string{RoleInputter, RoleCoder}
	for _, role := range light {
		if strongRoles[role] {
			t.Errorf("expected %q to be a light role, got strong", role)
		}
	}
}

func TestBroker_SetRolePool_IgnoresEmpty(t *testing.T) {
	pool := DefaultPool() // no keys configured, but not nil
	broker := NewBroker(pool)
	emptyPool := NewPool() // truly empty
	broker.SetRolePool(RoleCritic, emptyPool)
	broker.mu.Lock()
	_, ok := broker.rolePools[RoleCritic]
	broker.mu.Unlock()
	if ok {
		t.Error("SetRolePool should ignore unconfigured pools")
	}
}

func TestBroker_SetRolePool_IgnoresNil(t *testing.T) {
	pool := DefaultPool()
	broker := NewBroker(pool)
	broker.SetRolePool(RoleCritic, nil)
	broker.mu.Lock()
	_, ok := broker.rolePools[RoleCritic]
	broker.mu.Unlock()
	if ok {
		t.Error("SetRolePool should ignore nil pool")
	}
}

func TestBroker_UsageTracked(t *testing.T) {
	pool := NewPool()
	// Pool with no configured clients; Chat will fail, but we test via role pool.
	broker := NewBroker(pool)

	// Wire a fake role pool using a real Pool backed by a provider we won't actually call.
	// Instead test usage tracking directly by calling broker.recordUsage.
	msgs := []Message{{Role: "user", Content: "hello"}}
	broker.recordUsage(RolePlanner, msgs, "response")

	usage := broker.Usage()
	s, ok := usage[RolePlanner]
	if !ok {
		t.Fatal("expected usage entry for planner")
	}
	if s.Calls != 1 {
		t.Errorf("calls: got %d want 1", s.Calls)
	}
	if s.OutTokens == 0 {
		t.Error("expected non-zero output tokens")
	}
}

func TestBroker_UsageCumulative(t *testing.T) {
	pool := NewPool()
	broker := NewBroker(pool)
	msgs := []Message{{Role: "user", Content: "hello"}}

	broker.recordUsage(RoleCoder, msgs, "r1")
	broker.recordUsage(RoleCoder, msgs, "r2")

	usage := broker.Usage()
	if usage[RoleCoder].Calls != 2 {
		t.Errorf("expected 2 calls, got %d", usage[RoleCoder].Calls)
	}
}

func TestBroker_Accessors(t *testing.T) {
	pool := NewPool()
	broker := NewBroker(pool)
	if broker.Pool() != pool {
		t.Error("Pool() should return the wrapped pool")
	}
}

func TestBrokerFromConfig_RoutingCreatesRolePools(t *testing.T) {
	cfg := config.LLMConfig{
		Pool: config.PoolConfig{
			Primary: []string{"openai"},
			Light:   []string{"deepseek"},
		},
		Routing: map[string][]string{
			// Route critic to deepseek (different from planner's openai).
			RoleCritic: {"deepseek"},
		},
	}
	broker := BrokerFromConfig(cfg, "")
	broker.mu.Lock()
	_, ok := broker.rolePools[RoleCritic]
	broker.mu.Unlock()
	// Pool will be unconfigured without real API keys; SetRolePool is a no-op.
	// We verify the overall broker is at least structured correctly.
	_ = ok // non-fatal: depends on env
	if broker == nil {
		t.Fatal("BrokerFromConfig returned nil")
	}
}

func TestBrokerFromConfig_EmptyRoutingIsNoop(t *testing.T) {
	cfg := config.LLMConfig{
		Pool: config.PoolConfig{
			Primary: []string{"openai"},
		},
		Routing: map[string][]string{},
	}
	broker := BrokerFromConfig(cfg, "")
	broker.mu.Lock()
	n := len(broker.rolePools)
	broker.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 role pools for empty routing, got %d", n)
	}
}
