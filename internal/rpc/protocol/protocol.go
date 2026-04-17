// Package protocol defines JSON-RPC 2.0 wire types for CodeN's RPC layer.
// This follows the Neovim pattern: kernel is the server, clients attach over RPC.
package protocol

import "encoding/json"

const Version = "2.0"

// Request is a JSON-RPC 2.0 request from client to server.
type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"` // nil = notification
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response from server to client.
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *Error           `json:"error,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no id, no response expected).
// Used by server to push events to clients.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Error is a JSON-RPC 2.0 error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

type codedError struct {
	code    int
	message string
	cause   error
}

func (e *codedError) Error() string {
	return e.message
}

func (e *codedError) Unwrap() error {
	return e.cause
}

func (e *codedError) RPCCode() int {
	return e.code
}

// Standard error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// NewRequest creates a Request with auto-serialized params.
func NewRequest(id int64, method string, params any) (Request, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return Request{}, err
	}
	idRaw := json.RawMessage(mustMarshal(id))
	return Request{
		JSONRPC: Version,
		ID:      &idRaw,
		Method:  method,
		Params:  raw,
	}, nil
}

// NewNotification creates a Notification with auto-serialized params.
func NewNotification(method string, params any) (Notification, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return Notification{}, err
	}
	return Notification{
		JSONRPC: Version,
		Method:  method,
		Params:  raw,
	}, nil
}

// NewResult creates a success Response.
func NewResult(id *json.RawMessage, result any) Response {
	raw, _ := json.Marshal(result)
	return Response{
		JSONRPC: Version,
		ID:      id,
		Result:  raw,
	}
}

// NewError creates an error Response.
func NewError(id *json.RawMessage, code int, message string) Response {
	return Response{
		JSONRPC: Version,
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	}
}

func NewErrorFromErr(id *json.RawMessage, err error) Response {
	if err == nil {
		return NewError(id, CodeInternalError, "unknown error")
	}
	if coded, ok := err.(interface{ RPCCode() int }); ok {
		return NewError(id, coded.RPCCode(), err.Error())
	}
	return NewError(id, CodeInternalError, err.Error())
}

func InvalidParamsError(message string) error {
	return &codedError{code: CodeInvalidParams, message: message}
}

func MethodNotFoundError(method string) error {
	return &codedError{code: CodeMethodNotFound, message: "method not found: " + method}
}

// IsNotification returns true if the request has no ID (is a notification).
func (r *Request) IsNotification() bool { return r.ID == nil }

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// MarshalRaw marshals a value into a json.RawMessage.
func MarshalRaw(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
