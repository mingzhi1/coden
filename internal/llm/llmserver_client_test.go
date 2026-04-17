package llm

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/llm/provider"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// fakeLLMServer is a minimal llm-server that returns canned responses,
// used to test LLMServerClient without the real cmd/coden-llm-server binary.
func fakeLLMServer(t *testing.T, ctx context.Context, reply string) string {
	t.Helper()
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
			go func(c net.Conn) {
				codec := transport.NewCodec(c)
				defer codec.Close()
				for {
					raw, err := codec.ReadMessage()
					if err != nil {
						return
					}
					var req protocol.Request
					if err := json.Unmarshal(raw, &req); err != nil {
						return
					}
					resp := protocol.NewResult(req.ID, map[string]any{
						"text":        reply,
						"stop_reason": "end_turn",
						"provider":    "fake",
						"model":       "fake-model",
					})
					_ = codec.WriteMessage(resp)
				}
			}(conn)
		}
	}()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	return ln.Addr().String()
}

func TestLLMServerClient_Chat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addr := fakeLLMServer(t, ctx, "Hello from fake server!")
	client := NewLLMServerClient(addr)
	defer client.Close()

	if !client.IsConfigured() {
		t.Fatal("expected IsConfigured=true")
	}

	msgs := []Message{{Role: "user", Content: "test"}}
	reply, err := client.Chat(ctx, "coder", msgs)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply != "Hello from fake server!" {
		t.Errorf("expected fake reply, got %q", reply)
	}
}

func TestLLMServerClient_Truncated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Set up a server that returns truncation errors
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
			go func(c net.Conn) {
				codec := transport.NewCodec(c)
				defer codec.Close()
				for {
					raw, err := codec.ReadMessage()
					if err != nil {
						return
					}
					var req protocol.Request
					_ = json.Unmarshal(raw, &req)

					data, _ := json.Marshal(map[string]any{
						"partial":   "partial output",
						"retryable": true,
					})
					resp := protocol.Response{
						JSONRPC: protocol.Version,
						ID:      req.ID,
						Error: &protocol.Error{
							Code:    -32001,
							Message: "truncated",
							Data:    data,
						},
					}
					_ = codec.WriteMessage(resp)
				}
			}(conn)
		}
	}()
	go func() { <-ctx.Done(); ln.Close() }()

	client := NewLLMServerClient(ln.Addr().String())
	defer client.Close()

	msgs := []Message{{Role: "user", Content: "test"}}
	reply, err := client.Chat(ctx, "coder", msgs)
	if err == nil {
		t.Fatal("expected truncation error")
	}

	var te *provider.TruncatedError
	if !isErrorType(err, &te) {
		t.Fatalf("expected TruncatedError, got %T: %v", err, err)
	}
	if reply != "partial output" {
		t.Errorf("expected partial='partial output', got %q", reply)
	}
}

// isErrorType is a test helper wrapping errors.As check.
func isErrorType[T error](err error, target *T) bool {
	return err != nil && func() bool {
		var t T
		ok := false
		for e := err; e != nil; {
			if v, vok := e.(T); vok {
				*target = v
				ok = true
				break
			}
			if u, uok := e.(interface{ Unwrap() error }); uok {
				e = u.Unwrap()
			} else {
				break
			}
		}
		_ = t
		return ok
	}()
}
