package acp

import "encoding/json"

// --- JSON-RPC wire types (ACP protocol) ---

// Request is a JSON-RPC 2.0 request sent to the ACP subprocess.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response/notification from the ACP subprocess.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError is the error object in a JSON-RPC response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- ACP protocol types ---

// InitParams for the "initialize" call.
type InitParams struct {
	ClientInfo         ClientInfo  `json:"clientInfo"`
	ClientCapabilities interface{} `json:"clientCapabilities,omitempty"`
}

// ClientInfo identifies the caller to the ACP server.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// NewSessionParams for the "newSession" call.
type NewSessionParams struct {
	CWD     string         `json:"cwd,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// NewSessionResult from the "newSession" call.
type NewSessionResult struct {
	SessionID string `json:"sessionId"`
}

// PromptParams for the "prompt" call.
type PromptParams struct {
	SessionID string          `json:"sessionId"`
	Messages  []PromptMessage `json:"messages"`
}

// PromptMessage is a single message in a prompt request.
type PromptMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock is a content element (text, tool_use, etc).
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Notification represents a parsed ACP async notification.
type Notification struct {
	Type          string         // "sessionUpdate" | "promptResponse"
	SessionUpdate *SessionUpdate
	StopReason    string
	Usage         *Usage
	Err           error
}

// SessionUpdate from sessionUpdate notification.
type SessionUpdate struct {
	Type string `json:"type"` // agent_message_chunk | tool_call | ...
	Text string `json:"text,omitempty"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}
