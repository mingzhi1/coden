package llm_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/llm"
)

// TestAnthropicProxy tests the Anthropic provider via a configurable proxy.
// Set TEST_ANTHROPIC_BASE_URL to the proxy base URL (e.g. https://api.openai.com/v1).
func TestAnthropicProxy(t *testing.T) {
	apiKey := os.Getenv("TEST_ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("TEST_ANTHROPIC_API_KEY not set")
	}
	baseURL := os.Getenv("TEST_ANTHROPIC_BASE_URL")
	if baseURL == "" {
		t.Skip("TEST_ANTHROPIC_BASE_URL not set")
	}

	pool := llm.NewPool()
	pool.Add(llm.Config{
		Provider: "openai",
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Model:    "coding-minimax-m2.7-free",
	})

	if !pool.IsConfigured() {
		t.Fatal("pool not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	reply, err := pool.Chat(ctx, []llm.Message{
		{Role: "user", Content: "Say hello in exactly 3 words."},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	t.Logf("reply: %s", reply)
	if len(reply) == 0 {
		t.Fatal("empty reply")
	}
}

// TestMiniMaxProvider tests the MiniMax provider.
// Set TEST_MINIMAX_BASE_URL to the API base URL (e.g. https://api.minimax.chat/v1).
func TestMiniMaxProvider(t *testing.T) {
	apiKey := os.Getenv("TEST_MINIMAX_API_KEY")
	if apiKey == "" {
		t.Skip("TEST_MINIMAX_API_KEY not set")
	}
	baseURL := os.Getenv("TEST_MINIMAX_BASE_URL")
	if baseURL == "" {
		t.Skip("TEST_MINIMAX_BASE_URL not set")
	}

	pool := llm.NewPool()
	pool.Add(llm.Config{
		Provider: "minimax",
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Model:    "coding-minimax-m2.7-free",
	})

	if !pool.IsConfigured() {
		t.Fatal("pool not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	reply, err := pool.Chat(ctx, []llm.Message{
		{Role: "user", Content: "Say hello in exactly 3 words."},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	t.Logf("reply: %s", reply)
	if len(reply) == 0 {
		t.Fatal("empty reply")
	}
}

// TestBothProvidersIntegration tests both providers in a single pool with fallback.
func TestBothProvidersIntegration(t *testing.T) {
	anthroKey := os.Getenv("TEST_ANTHROPIC_API_KEY")
	miniMaxKey := os.Getenv("TEST_MINIMAX_API_KEY")

	if anthroKey == "" && miniMaxKey == "" {
		t.Skip("neither TEST_ANTHROPIC_API_KEY nor TEST_MINIMAX_API_KEY set")
	}

	anthroBaseURL := os.Getenv("TEST_ANTHROPIC_BASE_URL")
	miniMaxBaseURL := os.Getenv("TEST_MINIMAX_BASE_URL")

	pool := llm.NewPool()

	if anthroKey != "" && anthroBaseURL != "" {
		pool.Add(llm.Config{
			Provider: "openai",
			APIKey:   anthroKey,
			BaseURL:  anthroBaseURL,
			Model:    "coding-minimax-m2.7-free",
		})
	}
	if miniMaxKey != "" && miniMaxBaseURL != "" {
		pool.Add(llm.Config{
			Provider: "minimax",
			APIKey:   miniMaxKey,
			BaseURL:  miniMaxBaseURL,
			Model:    "coding-minimax-m2.7-free",
		})
	}

	if !pool.IsConfigured() {
		t.Skip("no providers configured — set TEST_*_BASE_URL alongside TEST_*_API_KEY")
	}

	t.Logf("pool summary: %s", pool.Summary())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	reply, err := pool.Chat(ctx, []llm.Message{
		{Role: "user", Content: "What is 2+2? Answer with just the number."},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	t.Logf("reply: %s", reply)
	if len(reply) == 0 {
		t.Fatal("empty reply")
	}
}

// TestBrokerWithNewProviders tests the broker dispatch with the new providers.
func TestBrokerWithNewProviders(t *testing.T) {
	miniMaxKey := os.Getenv("TEST_MINIMAX_API_KEY")
	if miniMaxKey == "" {
		t.Skip("TEST_MINIMAX_API_KEY not set")
	}
	miniMaxBaseURL := os.Getenv("TEST_MINIMAX_BASE_URL")
	if miniMaxBaseURL == "" {
		t.Skip("TEST_MINIMAX_BASE_URL not set")
	}

	pool := llm.NewPool()
	pool.Add(llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})
	pool.AddLight(llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})

	broker := llm.NewBroker(pool)

	broker.SetRole(llm.RoleCoder, llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})
	broker.SetRole(llm.RolePlanner, llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	reply, err := broker.Chat(ctx, llm.RoleCoder, []llm.Message{
		{Role: "user", Content: "Write a single line of Go that prints hello world."},
	})
	if err != nil {
		t.Fatalf("coder chat failed: %v", err)
	}
	t.Logf("coder reply: %s", reply)

	reply, err = broker.Chat(ctx, llm.RolePlanner, []llm.Message{
		{Role: "user", Content: "Plan a simple todo app in 3 steps."},
	})
	if err != nil {
		t.Fatalf("planner chat failed: %v", err)
	}
	t.Logf("planner reply: %s", reply)

	reply, err = broker.Chat(ctx, llm.RoleInputter, []llm.Message{
		{Role: "user", Content: "Summarize in 10 words: the quick brown fox jumps over the lazy dog."},
	})
	if err != nil {
		t.Fatalf("inputter chat failed: %v", err)
	}
	t.Logf("inputter reply: %s", reply)

	usage := broker.Usage()
	for role, stats := range usage {
		t.Logf("role=%s calls=%d input_tokens=%d output_tokens=%d", role, stats.Calls, stats.InputTokens, stats.OutTokens)
	}
}
