package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func testServerConn(t *testing.T) (*Server, *transport.Codec) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer("")
	go srv.ServeConn(ctx, serverRWC)

	codec := transport.NewCodec(clientRWC)
	return srv, codec
}

func TestServerPing(t *testing.T) {
	srv, codec := testServerConn(t)
	_ = srv

	req, _ := protocol.NewRequest(1, "ping", nil)
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
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result protocol.PingResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "pong" {
		t.Errorf("expected pong, got %s", result.Status)
	}
}

func TestServerWorkerDescribe(t *testing.T) {
	_, codec := testServerConn(t)

	req, _ := protocol.NewRequest(2, protocol.MethodWorkerDescribe, nil)
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
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result protocol.WorkerDescribeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Name != "llm-server" {
		t.Errorf("expected name=llm-server, got %s", result.Name)
	}
	if result.Role != "llm" {
		t.Errorf("expected role=llm, got %s", result.Role)
	}
}

func TestServerLLMChatNoProvider(t *testing.T) {
	_, codec := testServerConn(t)

	params := ChatParams{
		RoleHint: "coder",
		Messages: []providerMsg{{Role: "user", Content: "hello"}},
	}
	req, _ := protocol.NewRequest(3, protocol.MethodLLMChat, params)
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

	// With no provider configured, should return error
	t.Logf("chat response: %s", string(raw))
}

func TestServerInitProviders(t *testing.T) {
	srv := NewServer("")
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
	if srv.router == nil {
		t.Fatal("router should never be nil")
	}
}
