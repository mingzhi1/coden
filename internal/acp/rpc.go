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
	case "sessionUpdate", "notifications/sessionUpdate":
		var raw struct {
			SessionID string          `json:"sessionId"`
			Update    json.RawMessage `json:"update"`
		}
		if err := json.Unmarshal(params, &raw); err != nil {
			return
		}
		var update SessionUpdate
		_ = json.Unmarshal(raw.Update, &update)
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
		}
		c.NotifyCh <- Notification{Type: "sessionUpdate", SessionUpdate: &update}

	case "promptResponse", "notifications/promptResponse":
		var raw struct {
			StopReason string `json:"stopReason"`
			Usage      Usage  `json:"usage"`
		}
		_ = json.Unmarshal(params, &raw)
		c.NotifyCh <- Notification{Type: "promptResponse", StopReason: raw.StopReason, Usage: &raw.Usage}
	}
}

// --- ACP protocol methods ---

func (c *Conn) initialize(ctx context.Context, clientName, clientVersion string) error {
	_, err := c.Call(ctx, "initialize", InitParams{
		ClientInfo: ClientInfo{Name: clientName, Version: clientVersion},
	})
	return err
}

// NewSession creates a new ACP session.
func (c *Conn) NewSession(ctx context.Context, cwd string) (string, error) {
	raw, err := c.Call(ctx, "newSession", NewSessionParams{
		CWD: cwd,
		Options: map[string]any{
			"permissionMode": "bypassPermissions",
		},
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

// Prompt sends a prompt to an existing session (fire-and-forget via Send).
func (c *Conn) Prompt(sessionID string, messages []PromptMessage) error {
	_, _, err := c.Send("prompt", PromptParams{SessionID: sessionID, Messages: messages})
	return err
}

// CloseSession closes an ACP session.
func (c *Conn) CloseSession(ctx context.Context, sessionID string) {
	_, _ = c.Call(ctx, "closeSession", map[string]string{"sessionId": sessionID})
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
