package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"

	"gopkg.in/yaml.v3"
)

// Server implements the llm-server RPC protocol over TCP/stdio.
// It follows the same pattern as internal/agent/plan.Server.
type Server struct {
	cfgPath  string
	router   *Router
	provider map[string]ChatProvider
}

// NewServer creates a new llm-server and initializes providers from config.
func NewServer(configPath string) *Server {
	s := &Server{cfgPath: configPath}
	s.initProviders()
	return s
}

// initProviders reads config (if provided) and sets up all providers + router.
func (s *Server) initProviders() {
	s.provider = make(map[string]ChatProvider)

	var routing map[string][]string

	if s.cfgPath != "" {
		s.initFromConfig(&routing)
	}

	// Auto-detect remaining providers from env (always runs as fallback enrichment)
	s.initFromEnv()

	// Set up routing
	if routing == nil {
		routing = map[string][]string{
			"coder":    {"acp", "anthropic", "openai"},
			"planner":  {"anthropic", "openai"},
			"reviewer": {"openai"},
			"light":    {"openai", "deepseek"},
		}
	}
	s.router = NewRouter(s.provider, routing)
}

// initFromConfig loads providers and routing from a shared config.yaml.
func (s *Server) initFromConfig(routing *map[string][]string) {
	data, err := os.ReadFile(s.cfgPath)
	if err != nil {
		slog.Warn("[server] config read failed, using env auto-detection", "path", s.cfgPath, "err", err)
		return
	}
	var fileCfg struct {
		LLM config.LLMConfig `yaml:"llm"`
	}
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		slog.Warn("[server] config parse failed", "path", s.cfgPath, "err", err)
		return
	}
	slog.Info("[server] loaded config", "path", s.cfgPath, "providers", len(fileCfg.LLM.Providers))

	for name, entry := range fileCfg.LLM.Providers {
		p, model := NewChatProvider(ProviderConfig{
			Provider:   entry.EffectiveType(),
			APIKey:     entry.APIKey,
			BaseURL:    entry.BaseURL,
			Model:      entry.DefaultModel,
			AcpName:    name,
			AcpCommand: entry.Command,
			AcpArgs:    entry.Args,
			AcpEnv:     entry.Env,
		})
		if p.IsConfigured() {
			s.provider[name] = p
			slog.Info("[server] provider ready (config)", "name", name, "model", model)
		}
	}
	if len(fileCfg.LLM.Routing) > 0 {
		*routing = fileCfg.LLM.Routing
	}
}

// initFromEnv auto-detects providers from environment variables.
func (s *Server) initFromEnv() {
	for _, name := range []string{"acp", "anthropic", "openai", "deepseek", "minimax", "copilot"} {
		if _, exists := s.provider[name]; exists {
			continue // already configured from config.yaml
		}
		p, model := NewChatProvider(ProviderConfig{Provider: name})
		if p.IsConfigured() {
			s.provider[name] = p
			slog.Info("[server] provider ready (env)", "name", name, "model", model)
		}
	}
}

// ServeConn handles a single client connection with JSON-RPC message loop.
func (s *Server) ServeConn(ctx context.Context, rwc io.ReadWriteCloser) {
	codec := transport.NewCodec(rwc)
	defer codec.Close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		raw, err := codec.ReadMessage()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			slog.Debug("read error", "err", err)
			return
		}

		var req protocol.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = codec.WriteMessage(protocol.NewError(nil, protocol.CodeParseError, "parse error"))
			continue
		}
		if req.JSONRPC != protocol.Version {
			_ = codec.WriteMessage(protocol.NewError(req.ID, protocol.CodeInvalidRequest, "invalid jsonrpc version"))
			continue
		}

		resp := s.dispatch(ctx, req)
		if !req.IsNotification() {
			_ = codec.WriteMessage(resp)
		}
	}
}

// dispatch routes incoming requests to handlers.
func (s *Server) dispatch(ctx context.Context, req protocol.Request) protocol.Response {
	switch req.Method {
	case protocol.MethodPing:
		return s.handlePing(req.ID)
	case protocol.MethodWorkerDescribe:
		return s.handleWorkerDescribe(req.ID)
	case protocol.MethodLLMChat:
		return s.handleChat(ctx, req.ID, req.Params)
	case protocol.MethodLLMSideQuery:
		return s.handleSideQuery(ctx, req.ID, req.Params)
	default:
		return protocol.NewError(req.ID, protocol.CodeMethodNotFound,
			fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handlePing responds to ping requests.
func (s *Server) handlePing(id *json.RawMessage) protocol.Response {
	return protocol.NewResult(id, protocol.PingResult{Status: "pong"})
}

// handleWorkerDescribe returns llm-server capabilities.
func (s *Server) handleWorkerDescribe(id *json.RawMessage) protocol.Response {
	return protocol.NewResult(id, protocol.WorkerDescribeResult{
		Name:           "llm-server",
		Role:           "llm",
		Version:        "0.1.0",
		SupportsCancel: false,
		MaxConcurrency: 4,
	})
}

// handleChat processes LLM chat requests.
func (s *Server) handleChat(ctx context.Context, id *json.RawMessage, params json.RawMessage) protocol.Response {
	var p ChatParams
	if err := json.Unmarshal(params, &p); err != nil {
		return protocol.NewErrorFromErr(id, err)
	}

	// Convert provider messages to internal Message format
	var messages []Message
	for _, m := range p.Messages {
		messages = append(messages, Message{Role: m.Role, Content: m.Content})
	}

	// Route to appropriate provider
	result, err := s.router.ChatWithFallback(ctx, p.RoleHint, p.Model, messages)
	if err != nil {
		return toRPCError(id, err)
	}
	return protocol.NewResult(id, result.toRPC())
}

// handleSideQuery processes lightweight LLM side query requests.
// This is the server-side counterpart of LLMServerClient.SideQuery().
func (s *Server) handleSideQuery(ctx context.Context, id *json.RawMessage, params json.RawMessage) protocol.Response {
	var p SideQueryParams
	if err := json.Unmarshal(params, &p); err != nil {
		return protocol.NewErrorFromErr(id, err)
	}

	// Build message slice: optional system prompt + user messages.
	var messages []Message
	if p.System != "" {
		messages = append(messages, Message{Role: "system", Content: p.System})
	}
	for _, m := range p.Messages {
		messages = append(messages, Message{Role: m.Role, Content: m.Content})
	}
	if len(messages) == 0 {
		return protocol.NewError(id, protocol.CodeInvalidParams,
			"sidequery: no messages provided")
	}

	// Apply timeout from params if specified.
	if p.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	// Route via "light" role hint — side queries use cheap/fast models.
	result, err := s.router.ChatWithFallback(ctx, "light", "", messages)
	if err != nil {
		return toRPCError(id, err)
	}
	return protocol.NewResult(id, result.toRPC())
}

// --- Protocol types specific to llm-server ---

// ChatParams are the parameters for llm/chat.
type ChatParams struct {
	RoleHint string          `json:"role_hint"`            // e.g. "coder", "planner", "light"
	Messages []providerMsg    `json:"messages,omitempty"`
	Model    string          `json:"model,omitempty"`
}

// SideQueryParams are the parameters for llm/sidequery.
type SideQueryParams struct {
	System    string        `json:"system,omitempty"`     // optional system prompt
	Messages  []providerMsg `json:"messages,omitempty"`   // user messages
	Purpose   string        `json:"purpose,omitempty"`    // label for logging
	TimeoutMs int           `json:"timeout_ms,omitempty"` // per-call timeout in ms
}

// providerMsg mirrors provider.Message without importing the provider package.
type providerMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResult is the successful result of llm/chat.
type ChatResult struct {
	Text       string     `json:"text"`
	StopReason string     `json:"stop_reason"`           // "end_turn" | "tool_use" | "max_tokens"
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	Usage      UsageInfo  `json:"usage,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents an LLM-requested tool invocation in chat results.
type ToolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// UsageInfo tracks token usage from the provider.
type UsageInfo struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
