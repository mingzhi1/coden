package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// mockProvider is a simple ChatProvider for E2E testing.
type mockProvider struct {
	reply string
	err   error
}

func (m *mockProvider) Chat(_ context.Context, _ string, _ []Message) (string, error) {
	return m.reply, m.err
}
func (m *mockProvider) IsConfigured() bool { return true }
func (m *mockProvider) Name() string       { return "mock" }

// startTestServer creates a server with a mock provider, starts it on a random
// TCP port, and returns the address. The server shuts down when ctx is canceled.
func startTestServer(t *testing.T, ctx context.Context, mock *mockProvider) string {
	t.Helper()
	srv := &Server{
		provider: map[string]ChatProvider{"mock": mock},
		router:   NewRouter(map[string]ChatProvider{"mock": mock}, map[string][]string{"coder": {"mock"}}),
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.ServeConn(ctx, conn)
		}
	}()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	return ln.Addr().String()
}

func TestE2E_ChatRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mock := &mockProvider{reply: "Hello from mock provider!"}
	addr := startTestServer(t, ctx, mock)

	// Connect as a CodeN RPC client would
	conn, err := transport.DialTCP(addr)
	if err != nil {
		t.Fatal(err)
	}
	codec := transport.NewCodec(conn)
	defer codec.Close()

	// Send llm/chat request
	req, _ := protocol.NewRequest(1, "llm/chat", map[string]any{
		"role_hint": "coder",
		"messages":  []map[string]string{{"role": "user", "content": "test"}},
	})
	if err := codec.WriteMessage(req); err != nil {
		t.Fatal(err)
	}

	raw, err := codec.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}

	var result ChatResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello from mock provider!" {
		t.Errorf("expected mock reply, got %q", result.Text)
	}
	if result.Provider != "mock" {
		t.Errorf("expected provider=mock, got %q", result.Provider)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("expected stop_reason=end_turn, got %q", result.StopReason)
	}
}

func TestE2E_ChatTruncated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mock := &mockProvider{
		reply: "partial content",
		err:   &TruncatedError{Content: "partial content", Err: fmt.Errorf("truncated")},
	}
	addr := startTestServer(t, ctx, mock)

	conn, err := transport.DialTCP(addr)
	if err != nil {
		t.Fatal(err)
	}
	codec := transport.NewCodec(conn)
	defer codec.Close()

	req, _ := protocol.NewRequest(2, "llm/chat", map[string]any{
		"role_hint": "coder",
		"messages":  []map[string]string{{"role": "user", "content": "test"}},
	})
	if err := codec.WriteMessage(req); err != nil {
		t.Fatal(err)
	}

	raw, err := codec.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected truncation error")
	}
	if resp.Error.Code != -32001 {
		t.Errorf("expected code -32001, got %d", resp.Error.Code)
	}

	var data struct {
		Partial   string `json:"partial"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(resp.Error.Data, &data); err != nil {
		t.Fatalf("parse error data: %v", err)
	}
	if data.Partial != "partial content" {
		t.Errorf("expected partial='partial content', got %q", data.Partial)
	}
	if !data.Retryable {
		t.Error("expected retryable=true")
	}
}

func TestE2E_ConfigFromYAML(t *testing.T) {
	// Write a temp config.yaml
	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	cfgContent := `
llm:
  providers:
    openai:
      type: http
      api_key: "test-key-123"
      default_model: "gpt-4o-mini"
  routing:
    coder: [openai]
    light: [openai]
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(cfgPath)
	if srv.router == nil {
		t.Fatal("router should not be nil")
	}

	// The openai provider should be configured from the YAML api_key
	p, name := srv.router.Resolve("coder")
	if p == nil {
		t.Fatal("expected coder route to resolve")
	}
	t.Logf("resolved coder -> provider=%s", name)
}
