// Package lsp provides a minimal LSP client for CodeN.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Request represents a JSON-RPC request.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// Response represents a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError represents a JSON-RPC error.
type ResponseError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("LSP error %d: %s", e.Code, e.Message)
}

// Notification represents a JSON-RPC notification (no ID).
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// Client is a minimal LSP JSON-RPC client.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	writer io.Writer

	// Request ID generation
	idCounter atomic.Int32

	// Pending requests
	pending   map[int]chan *Response
	pendingMu sync.Mutex

	// Server capabilities (from initialize result)
	capabilities ServerCapabilities

	// Closed state
	closed   bool
	closedMu sync.Mutex
}

// ServerCapabilities represents LSP server capabilities.
type ServerCapabilities struct {
	TextDocumentSync        interface{} `json:"textDocumentSync,omitempty"`
	DefinitionProvider      bool        `json:"definitionProvider,omitempty"`
	ReferencesProvider      bool        `json:"referencesProvider,omitempty"`
	DocumentSymbolProvider  bool        `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider bool        `json:"workspaceSymbolProvider,omitempty"`
	HoverProvider           bool        `json:"hoverProvider,omitempty"`
	DiagnosticProvider      interface{} `json:"diagnosticProvider,omitempty"`
}

// InitializeParams represents LSP initialize params.
type InitializeParams struct {
	ProcessID    int                `json:"processId,omitempty"`
	RootPath     string             `json:"rootPath,omitempty"`
	RootURI      string             `json:"rootUri,omitempty"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities represents LSP client capabilities.
type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

// TextDocumentClientCapabilities represents text document capabilities.
type TextDocumentClientCapabilities struct {
	Synchronization interface{} `json:"synchronization,omitempty"`
	Definition      interface{} `json:"definition,omitempty"`
	References      interface{} `json:"references,omitempty"`
	DocumentSymbol  interface{} `json:"documentSymbol,omitempty"`
}

// InitializeResult represents LSP initialize result.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// ServerInfo represents LSP server info.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// NewClient creates a new LSP client over the given connection.
func NewClient(conn net.Conn) *Client {
	c := &Client{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		writer:  conn,
		pending: make(map[int]chan *Response),
	}
	go c.readLoop()
	return c
}

// readLoop processes incoming messages.
func (c *Client) readLoop() {
	for {
		msg, err := c.readMessage()
		if err != nil {
			c.closedMu.Lock()
			c.closed = true
			c.closedMu.Unlock()
			// Signal all pending requests
			c.pendingMu.Lock()
			for _, ch := range c.pending {
				ch <- &Response{Error: &ResponseError{Code: -32900, Message: "connection closed"}}
			}
			c.pending = make(map[int]chan *Response)
			c.pendingMu.Unlock()
			return
		}

		// Try to parse as response. A JSON-RPC response always carries an "id"
		// field (unlike notifications). We inspect the raw JSON rather than
		// checking resp.ID != 0, because ID 0 is a valid request identifier.
		var resp Response
		if err := json.Unmarshal(msg, &resp); err == nil {
			// Check if the raw message contains an "id" key to distinguish
			// responses from notifications.
			var raw map[string]json.RawMessage
			if json.Unmarshal(msg, &raw) == nil {
				if _, hasID := raw["id"]; hasID {
					c.pendingMu.Lock()
					ch, ok := c.pending[resp.ID]
					if ok {
						delete(c.pending, resp.ID)
					}
					c.pendingMu.Unlock()
					if ok {
						ch <- &resp
					}
					continue
				}
			}
		}

		// Could be a notification - log or handle as needed
		// For MVP, we ignore notifications
	}
}

// readMessage reads an LSP message with Content-Length header.
func (c *Client) readMessage() ([]byte, error) {
	// Read Content-Length header
	var contentLength int
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break // End of headers
		}
		if strings.HasPrefix(line, "Content-Length:") {
			lenStr := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			n, err := strconv.Atoi(lenStr)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length %q: %w", lenStr, err)
			}
			contentLength = n
		}
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("no Content-Length header")
	}

	// Read body
	body := make([]byte, contentLength)
	_, err := io.ReadFull(c.reader, body)
	return body, err
}

// writeMessage writes an LSP message with Content-Length header.
func (c *Client) writeMessage(msg []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(msg))
	if _, err := c.writer.Write([]byte(header)); err != nil {
		return err
	}
	_, err := c.writer.Write(msg)
	return err
}

// Call sends a request and waits for response.
func (c *Client) Call(ctx context.Context, method string, params interface{}) (*Response, error) {
	c.closedMu.Lock()
	if c.closed {
		c.closedMu.Unlock()
		return nil, fmt.Errorf("client closed")
	}
	c.closedMu.Unlock()

	id := int(c.idCounter.Add(1))
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan *Response, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.writeMessage(data); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp, nil
	}
}

// Notify sends a notification (no response expected).
func (c *Client) Notify(method string, params interface{}) error {
	c.closedMu.Lock()
	if c.closed {
		c.closedMu.Unlock()
		return fmt.Errorf("client closed")
	}
	c.closedMu.Unlock()

	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}

	return c.writeMessage(data)
}

// Initialize performs LSP initialize handshake.
func (c *Client) Initialize(ctx context.Context, rootPath string) (*InitializeResult, error) {
	params := InitializeParams{
		ProcessID: 0, // 0 means no process ID
		RootURI:   pathToURI(rootPath),
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Synchronization: struct{}{},
				Definition:      struct{}{},
				References:      struct{}{},
				DocumentSymbol:  struct{}{},
			},
		},
	}

	resp, err := c.Call(ctx, "initialize", params)
	if err != nil {
		return nil, err
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}

	c.capabilities = result.Capabilities
	return &result, nil
}

// Shutdown performs LSP shutdown sequence.
func (c *Client) Shutdown(ctx context.Context) error {
	_, err := c.Call(ctx, "shutdown", nil)
	return err
}

// Exit sends exit notification.
func (c *Client) Exit() {
	c.Notify("exit", nil)
}

// Close closes the connection.
func (c *Client) Close() error {
	c.closedMu.Lock()
	c.closed = true
	c.closedMu.Unlock()
	return c.conn.Close()
}

// Capabilities returns server capabilities.
func (c *Client) Capabilities() ServerCapabilities {
	return c.capabilities
}

// pathToURI converts a file path to a file:// URI.
// On Windows, paths like C:\foo become file:///C:/foo per RFC 8089.
func pathToURI(path string) string {
	if strings.HasPrefix(path, "file://") {
		return path
	}
	// Normalize path separators for Windows
	path = strings.ReplaceAll(path, "\\", "/")
	// Windows drive letter needs an extra leading slash: file:///C:/...
	if len(path) >= 2 && path[1] == ':' {
		return "file:///" + path
	}
	return "file://" + path
}

// uriToPath converts URI to file path.
func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

// TextDocumentIdentifier represents a text document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier represents a versioned text document.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// Position represents a position in a document.
type Position struct {
	Line      int `json:"line"`      // 0-based
	Character int `json:"character"` // 0-based
}

// Location represents a location in a document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Range represents a range in a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// TextDocumentItem represents a text document to open.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// DidOpenTextDocumentParams represents didOpen params.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams represents didChange params.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent represents a content change.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// DocumentSymbolParams represents documentSymbol params.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbol represents a document symbol.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolKind represents symbol kinds.
const (
	SymbolKindFile          = 1
	SymbolKindModule        = 2
	SymbolKindNamespace     = 3
	SymbolKindPackage       = 4
	SymbolKindClass         = 5
	SymbolKindMethod        = 6
	SymbolKindProperty      = 7
	SymbolKindField         = 8
	SymbolKindConstructor   = 9
	SymbolKindEnum          = 10
	SymbolKindInterface     = 11
	SymbolKindFunction      = 12
	SymbolKindVariable      = 13
	SymbolKindConstant      = 14
	SymbolKindString        = 15
	SymbolKindNumber        = 16
	SymbolKindBoolean       = 17
	SymbolKindArray         = 18
	SymbolKindObject        = 19
	SymbolKindKey           = 20
	SymbolKindNull          = 21
	SymbolKindEnumMember    = 22
	SymbolKindStruct        = 23
	SymbolKindEvent         = 24
	SymbolKindOperator      = 25
	SymbolKindTypeParameter = 26
)

// DefinitionParams represents definition params.
type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ReferencesParams represents references params.
type ReferencesParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// ReferenceContext represents reference context.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}
