package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// --- low-level JSON-RPC ---

// Send sends a JSON-RPC request and returns the pending response channel.
func (c *Conn) Send(method string, params interface{}) (int64, chan json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return 0, nil, err
	}

	ch := make(chan json.RawMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	c.mu.Lock()
	_, err = c.stdin.Write(append(data, '\n'))
	c.mu.Unlock()
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return 0, nil, err
	}
	slog.Debug("[acp] sent", "name", c.Name, "method", method, "id", id)
	return id, ch, nil
}

// reply sends a JSON-RPC success response for an incoming agent request.
func (c *Conn) reply(id int64, result interface{}) {
	data, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": id, "result": result,
	})
	if err != nil {
		return
	}
	c.mu.Lock()
	_, _ = c.stdin.Write(append(data, '\n'))
	c.mu.Unlock()
}

// replyError sends a JSON-RPC error response for an incoming agent request.
func (c *Conn) replyError(id int64, code int, message string) {
	data, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]interface{}{"code": code, "message": message},
	})
	if err != nil {
		return
	}
	c.mu.Lock()
	_, _ = c.stdin.Write(append(data, '\n'))
	c.mu.Unlock()
}

// handleServerRequest answers requests the agent sends back to the client.
// CodeN's ACP integration treats the agent as a pure reasoning engine: the
// Kernel owns tool execution and file access, so any agent-initiated tool work
// is declined. Every request still gets a definitive reply — leaving one
// unanswered blocks the agent indefinitely.
func (c *Conn) handleServerRequest(id int64, method string) {
	switch method {
	case "session/request_permission":
		// Decline: the agent must not run its own tools. A "cancelled" outcome
		// unblocks the agent without granting permission.
		c.reply(id, map[string]interface{}{
			"outcome": map[string]interface{}{"outcome": "cancelled"},
		})
	default:
		// fs/read_text_file, fs/write_text_file, terminal/*, etc. are not provided
		// by CodeN in reasoning-only mode. Answer with method-not-found so the
		// agent fails fast instead of hanging.
		slog.Debug("[acp] declining agent request", "name", c.Name, "method", method)
		c.replyError(id, -32601, "method not supported by coden ACP client: "+method)
	}
}

// Call sends a request and waits for the response synchronously.
func (c *Conn) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	_, ch, err := c.Send(method, params)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case raw := <-ch:
		return raw, nil
	}
}

// readLoop continuously reads from stdout and dispatches responses/notifications.
func (c *Conn) readLoop() {
	defer close(c.NotifyCh)
	for c.stdout.Scan() {
		line := c.stdout.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Response
		if err := json.Unmarshal(line, &msg); err != nil {
			slog.Warn("[acp] bad ndJSON line", "name", c.Name, "error", err)
			continue
		}
		// An incoming message with both an id AND a method is a REQUEST from the
		// agent (e.g. session/request_permission, fs/read_text_file), not a reply
		// to one of ours. It MUST be answered or the agent blocks forever — the
		// previous code mistook these for responses and silently dropped them.
		if msg.ID != nil && msg.Method != "" {
			c.handleServerRequest(*msg.ID, msg.Method)
			continue
		}
		if msg.ID != nil {
			c.pendingMu.Lock()
			ch, ok := c.pending[*msg.ID]
			if ok {
				delete(c.pending, *msg.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				if msg.Error != nil {
					slog.Warn("[acp] RPC error", "name", c.Name, "code", msg.Error.Code)
				}
				ch <- msg.Result
			}
			continue
		}
		c.dispatchNotification(msg.Method, msg.Params)
	}
	if err := c.stdout.Err(); err != nil {
		slog.Warn("[acp] readLoop ended", "name", c.Name, "error", err)
	}
}

// dispatchNotification parses ACP notifications into typed Notification values.
func (c *Conn) dispatchNotification(method string, params json.RawMessage) {
	switch method {
	case "session/update", "sessionUpdate", "notifications/sessionUpdate":
		var raw struct {
			SessionID string          `json:"sessionId"`
			Update    json.RawMessage `json:"update"`
		}
		if err := json.Unmarshal(params, &raw); err != nil {
			return
		}
		var update SessionUpdate
		_ = json.Unmarshal(raw.Update, &update)
		if update.Type == "" {
			var kind struct {
				SessionUpdate string `json:"sessionUpdate"`
			}
			_ = json.Unmarshal(raw.Update, &kind)
			update.Type = kind.SessionUpdate
		}
		if update.Text == "" && update.Type == "agent_message_chunk" {
			var nested struct {
				Content []ContentBlock `json:"content"`
			}
			if err := json.Unmarshal(raw.Update, &nested); err == nil {
				for _, b := range nested.Content {
					if b.Type == "text" {
						update.Text += b.Text
					}
				}
			}
			var single struct {
				Content ContentBlock `json:"content"`
			}
			if err := json.Unmarshal(raw.Update, &single); err == nil && single.Content.Type == "text" {
				update.Text += single.Content.Text
			}
		}
		c.NotifyCh <- Notification{Type: "sessionUpdate", SessionUpdate: &update}

		// Note: the "promptResponse" terminal event is NOT handled here. In ACP,
		// session/prompt is a request whose RPC *response* carries stopReason —
		// Prompt() emits the single promptResponse Notification from that response.
		// Handling a duplicate notification here would enqueue a second terminal
		// event that leaks into the next call on the shared NotifyCh.
	}
}

// --- ACP protocol methods ---

func (c *Conn) initialize(ctx context.Context, clientName, clientVersion string) error {
	raw, err := c.Call(ctx, "initialize", InitParams{
		ProtocolVersion: 1,
		ClientInfo:      ClientInfo{Name: clientName, Version: clientVersion},
	})
	if err != nil {
		return err
	}
	// Log the negotiated protocol version / capabilities so a version mismatch
	// with the ACP agent surfaces clearly instead of failing opaquely later.
	slog.Debug("[acp] initialized", "name", c.Name, "result", string(raw))
	return nil
}

// NewSession creates a new ACP session.
func (c *Conn) NewSession(ctx context.Context, cwd string) (string, error) {
	raw, err := c.Call(ctx, "session/new", NewSessionParams{
		CWD:        cwd,
		MCPServers: []any{},
	})
	if err != nil {
		return "", err
	}
	var result NewSessionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("acp: parse newSession: %w", err)
	}
	return result.SessionID, nil
}

// Prompt sends a prompt to an existing session and forwards the eventual
// response into NotifyCh so callers can consume one stream of updates.
func (c *Conn) Prompt(sessionID string, prompt []ContentBlock) error {
	_, ch, err := c.Send("session/prompt", PromptParams{SessionID: sessionID, Prompt: prompt})
	if err != nil {
		return err
	}
	go func() {
		raw := <-ch
		var result struct {
			StopReason string `json:"stopReason"`
			Usage      Usage  `json:"usage"`
		}
		_ = json.Unmarshal(raw, &result)
		c.NotifyCh <- Notification{Type: "promptResponse", StopReason: result.StopReason, Usage: &result.Usage}
	}()
	return err
}

// SetModel selects the model for a session. claude-code-acp exposes this as the
// `session/set_model` request (its unstable_setSessionModel handler), letting the
// client pick e.g. opus vs sonnet per session. Best-effort: agents that don't
// support it return an error and the caller keeps the agent's default model.
func (c *Conn) SetModel(ctx context.Context, sessionID, modelID string) error {
	_, err := c.Call(ctx, "session/set_model", map[string]string{
		"sessionId": sessionID,
		"modelId":   modelID,
	})
	return err
}

// CloseSession closes an ACP session.
func (c *Conn) CloseSession(ctx context.Context, sessionID string) {
	_, _ = c.Call(ctx, "session/close", map[string]string{"sessionId": sessionID})
}

// Cancel asks the agent to stop generating for a session (fire-and-forget).
// Used when the caller's context is cancelled so the agent doesn't keep working
// (and consuming tokens) after CodeN has given up on the turn.
func (c *Conn) Cancel(sessionID string) {
	_, _, _ = c.Send("session/cancel", map[string]string{"sessionId": sessionID})
}

// ReadNotification waits for the next notification with context support.
func (c *Conn) ReadNotification(ctx context.Context) (Notification, error) {
	select {
	case <-ctx.Done():
		return Notification{}, ctx.Err()
	case n, ok := <-c.NotifyCh:
		if !ok {
			return Notification{}, fmt.Errorf("acp(%s): connection closed", c.Name)
		}
		return n, n.Err
	}
}

// Close kills the subprocess and cleans up.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	err := c.cmd.Process.Kill()
	_ = c.cmd.Wait()
	slog.Info("[acp] closed", "name", c.Name)
	return err
}
