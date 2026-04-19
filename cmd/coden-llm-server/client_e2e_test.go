package main

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/llm"
)

// TestE2E_LLMServerClientRoundTrip tests the full client→server→mock path
// using LLMServerClient (the same client used by the Kernel in server mode).
func TestE2E_LLMServerClientRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mock := &mockProvider{reply: "Hello from LLMServerClient!"}
	addr := startTestServer(t, ctx, mock)

	client := llm.NewLLMServerClient(addr)
	defer client.Close()

	// Verify IsConfigured
	if !client.IsConfigured() {
		t.Fatal("client should be configured")
	}

	// Send a Chat through the full RPC pipeline
	reply, err := client.Chat(ctx, "coder", []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Say hello"},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if reply != "Hello from LLMServerClient!" {
		t.Errorf("expected mock reply, got %q", reply)
	}
}

// TestE2E_LLMServerClientContextCancel ensures context cancellation
// is properly handled by the client.
func TestE2E_LLMServerClientContextCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use a slow provider that blocks forever
	slowMock := &mockProvider{}
	addr := startTestServer(t, ctx, slowMock)

	client := llm.NewLLMServerClient(addr)
	defer client.Close()

	// Cancel the context immediately
	cancelCtx, cancelFn := context.WithCancel(ctx)
	cancelFn() // cancel right away

	_, err := client.Chat(cancelCtx, "coder", []llm.Message{
		{Role: "user", Content: "test"},
	})
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

// TestE2E_LLMServerClientMultipleCalls verifies the client can make
// sequential calls over the same connection.
func TestE2E_LLMServerClientMultipleCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mock := &mockProvider{reply: "reply"}
	addr := startTestServer(t, ctx, mock)

	client := llm.NewLLMServerClient(addr)
	defer client.Close()

	for i := 0; i < 5; i++ {
		reply, err := client.Chat(ctx, "coder", []llm.Message{
			{Role: "user", Content: "test"},
		})
		if err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
		if reply != "reply" {
			t.Errorf("call %d: expected 'reply', got %q", i, reply)
		}
	}
}
