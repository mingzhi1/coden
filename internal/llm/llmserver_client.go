// Package llm — llmserver_client.go provides a thin RPC client that connects
// to the llm-server sidecar over TCP. It replaces the in-process provider call
// chain with a single JSON-RPC round-trip, keeping the Chat() interface
// identical to the existing ChatProvider contract.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mingzhi1/coden/internal/llm/provider"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// LLMServerClient is a thin RPC client for the llm-server sidecar.
// It implements the same Chat(ctx, messages) → (string, error) contract
// as the local Pool, allowing drop-in replacement.
type LLMServerClient struct {
	addr  string
	mu    sync.Mutex
	codec *transport.Codec
	idSeq atomic.Int64
}

// NewLLMServerClient creates a client that connects to the given address.
// Connection is established lazily on the first call.
func NewLLMServerClient(addr string) *LLMServerClient {
	return &LLMServerClient{addr: addr}
}

// ensureConnLocked returns the codec, creating the TCP connection if needed.
// MUST be called with c.mu held.
func (c *LLMServerClient) ensureConnLocked() (*transport.Codec, error) {
	if c.codec != nil {
		return c.codec, nil
	}
	conn, err := transport.DialTCPKeepalive(c.addr, 30e9) // 30s keepalive
	if err != nil {
		return nil, fmt.Errorf("llm-server dial %s: %w", c.addr, err)
	}
	c.codec = transport.NewCodec(conn)
	return c.codec, nil
}

// Chat sends a chat request to llm-server and returns the response text.
// The entire write→read round-trip is serialized by mu because JSON-RPC
// responses on a single connection are ordered, and interleaving would
// corrupt the stream.
func (c *LLMServerClient) Chat(ctx context.Context, roleHint string, messages []Message) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	codec, err := c.ensureConnLocked()
	if err != nil {
		return "", err
	}

	id := c.idSeq.Add(1)
	req, err := protocol.NewRequest(id, protocol.MethodLLMChat, map[string]any{
		"role_hint": roleHint,
		"messages":  messages,
	})
	if err != nil {
		return "", fmt.Errorf("llm-server: build request: %w", err)
	}

	if err := codec.WriteMessage(req); err != nil {
		c.resetConnLocked()
		return "", fmt.Errorf("llm-server: write: %w", err)
	}

	// Read with context cancellation: spawn a goroutine for the blocking
	// read and select on ctx.Done() to honour deadlines/cancellation.
	type readResult struct {
		raw []byte
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		raw, err := codec.ReadMessage()
		ch <- readResult{raw, err}
	}()

	var raw []byte
	select {
	case <-ctx.Done():
		c.resetConnLocked()
		return "", fmt.Errorf("llm-server: %w", ctx.Err())
	case r := <-ch:
		if r.err != nil {
			c.resetConnLocked()
			return "", fmt.Errorf("llm-server: read: %w", r.err)
		}
		raw = r.raw
	}

	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("llm-server: parse response: %w", err)
	}

	if resp.Error != nil {
		return c.handleRPCError(resp.Error)
	}

	var result struct {
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("llm-server: parse result: %w", err)
	}
	return result.Text, nil
}

// handleRPCError converts JSON-RPC error codes into typed Go errors.
func (c *LLMServerClient) handleRPCError(e *protocol.Error) (string, error) {
	switch e.Code {
	case -32001: // TRUNCATED
		var data struct {
			Partial   string `json:"partial"`
			Retryable bool   `json:"retryable"`
		}
		if e.Data != nil {
			_ = json.Unmarshal(e.Data, &data)
		}
		return data.Partial, &provider.TruncatedError{
			Content: data.Partial,
			Err:     fmt.Errorf("llm-server: %s", e.Message),
		}
	default:
		return "", fmt.Errorf("llm-server: rpc error %d: %s", e.Code, e.Message)
	}
}

// resetConnLocked closes and nils the codec. MUST be called with c.mu held.
func (c *LLMServerClient) resetConnLocked() {
	if c.codec != nil {
		_ = c.codec.Close()
		c.codec = nil
	}
}

// Close releases the underlying connection.
func (c *LLMServerClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codec != nil {
		err := c.codec.Close()
		c.codec = nil
		return err
	}
	return nil
}

// IsConfigured returns true (always configured if addr is set).
func (c *LLMServerClient) IsConfigured() bool { return c.addr != "" }

// SideQuery executes a lightweight LLM call via the llm-server sidecar.
// This is the server-mode equivalent of Broker.SideQuery().
func (c *LLMServerClient) SideQuery(ctx context.Context, opts SideQueryOpts) (string, error) {
	opts.applyDefaults()

	c.mu.Lock()
	defer c.mu.Unlock()

	codec, err := c.ensureConnLocked()
	if err != nil {
		return "", err
	}

	id := c.idSeq.Add(1)
	timeoutMs := int(opts.Timeout.Milliseconds())
	req, err := protocol.NewRequest(id, protocol.MethodLLMSideQuery, map[string]any{
		"system":     opts.System,
		"messages":   opts.Messages,
		"purpose":    opts.Purpose,
		"timeout_ms": timeoutMs,
	})
	if err != nil {
		return "", fmt.Errorf("llm-server: build sidequery request: %w", err)
	}

	if err := codec.WriteMessage(req); err != nil {
		c.resetConnLocked()
		return "", fmt.Errorf("llm-server: write sidequery: %w", err)
	}

	type readResult struct {
		raw []byte
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		raw, err := codec.ReadMessage()
		ch <- readResult{raw, err}
	}()

	var raw []byte
	select {
	case <-ctx.Done():
		c.resetConnLocked()
		return "", fmt.Errorf("llm-server: sidequery %w", ctx.Err())
	case r := <-ch:
		if r.err != nil {
			c.resetConnLocked()
			return "", fmt.Errorf("llm-server: read sidequery: %w", r.err)
		}
		raw = r.raw
	}

	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("llm-server: parse sidequery response: %w", err)
	}
	if resp.Error != nil {
		return c.handleRPCError(resp.Error)
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("llm-server: parse sidequery result: %w", err)
	}
	return result.Text, nil
}
