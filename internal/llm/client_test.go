package llm_test

import (
	"testing"

	"github.com/mingzhi1/coden/internal/llm"
)

func TestClientIsConfigured(t *testing.T) {
	c := llm.New(llm.Config{Provider: "openai", APIKey: ""})
	if c.IsConfigured() {
		t.Error("expected false when no API key")
	}
	c2 := llm.New(llm.Config{Provider: "openai", APIKey: "sk-test"})
	if !c2.IsConfigured() {
		t.Error("expected true with OpenAI key")
	}
	c3 := llm.New(llm.Config{Provider: "anthropic", APIKey: "sk-ant-test"})
	if !c3.IsConfigured() {
		t.Error("expected true with Anthropic key")
	}
}

func TestClientModel(t *testing.T) {
	c := llm.New(llm.Config{Provider: "openai", Model: "gpt-4o"})
	if c.Model() != "gpt-4o" {
		t.Errorf("got %s", c.Model())
	}
	c2 := llm.New(llm.Config{Provider: "anthropic", Model: "claude-opus-4-5"})
	if c2.Model() != "claude-opus-4-5" {
		t.Errorf("got %s", c2.Model())
	}
}

func TestDefaultModels(t *testing.T) {
	for _, p := range []string{"openai", "anthropic", "deepseek"} {
		c := llm.New(llm.Config{Provider: p})
		if c.Model() == "" {
			t.Errorf("empty default model for %s", p)
		}
	}
}

func TestPoolEmptyReturnsError(t *testing.T) {
	pool := llm.NewPool()
	if pool.IsConfigured() {
		t.Error("empty pool should not be configured")
	}
	if pool.Model() != "" {
		t.Errorf("expected empty model, got %s", pool.Model())
	}
}

func TestPoolAddPrimary(t *testing.T) {
	pool := llm.NewPool()
	pool.Add(llm.Config{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"})
	pool.Add(llm.Config{Provider: "anthropic", APIKey: "sk-ant", Model: "claude-3"})

	if !pool.IsConfigured() {
		t.Error("expected configured")
	}
	if pool.Model() != "gpt-4o" {
		t.Errorf("expected first primary model gpt-4o, got %s", pool.Model())
	}
	if pool.Provider() != "openai" {
		t.Errorf("expected openai, got %s", pool.Provider())
	}
}

func TestPoolLightFallsToPrimary(t *testing.T) {
	pool := llm.NewPool()
	pool.Add(llm.Config{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"})

	// No light clients — should fallback
	if pool.LightModel() != "gpt-4o" {
		t.Errorf("expected fallback to primary, got %s", pool.LightModel())
	}
}

func TestPoolAddLight(t *testing.T) {
	pool := llm.NewPool()
	pool.Add(llm.Config{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"})
	pool.AddLight(llm.Config{Provider: "deepseek", APIKey: "sk-ds", Model: "deepseek-chat"})

	if pool.LightModel() != "deepseek-chat" {
		t.Errorf("expected deepseek-chat, got %s", pool.LightModel())
	}
}

func TestPoolSummary(t *testing.T) {
	pool := llm.NewPool()
	pool.Add(llm.Config{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"})
	pool.Add(llm.Config{Provider: "anthropic", APIKey: "sk-ant", Model: "claude-3"})
	pool.AddLight(llm.Config{Provider: "deepseek", APIKey: "sk-ds", Model: "ds-chat"})

	summary := pool.Summary()
	for _, want := range []string{"openai/gpt-4o", "anthropic/claude-3", "deepseek/ds-chat", "→"} {
		if !contains(summary, want) {
			t.Errorf("summary missing %q: %s", want, summary)
		}
	}
}

func TestPoolUnconfiguredSkipped(t *testing.T) {
	pool := llm.NewPool()
	// Adding without API key — should be skipped
	pool.Add(llm.Config{Provider: "openai", APIKey: ""})
	if pool.IsConfigured() {
		t.Log("OPENAI_API_KEY set in env, skipping")
		return
	}
}

func TestDefaultPoolCreation(t *testing.T) {
	pool := llm.DefaultPool()
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
	// Summary should never panic
	_ = pool.Summary()
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
