package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// CodexAppServer implements ChatProvider by driving `codex app-server` over
// JSONL stdio. In provider mode it asks Codex for text in a read-only,
// approval-free ephemeral thread so CodeN remains the owner of tool execution.
type CodexAppServer struct {
	name    string
	command string
	args    []string
	env     map[string]string
	cwd     string
}

type CodexAppServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	CWD     string
}

func NewCodexAppServer(cfg CodexAppServerConfig) *CodexAppServer {
	name := cfg.Name
	if name == "" {
		name = "codex"
	}
	command := cfg.Command
	if command == "" {
		command = "codex"
	}
	args := append([]string(nil), cfg.Args...)
	if len(args) == 0 {
		args = []string{"app-server"}
	}
	return &CodexAppServer{
		name:    name,
		command: command,
		args:    args,
		env:     cfg.Env,
		cwd:     cfg.CWD,
	}
}

func (p *CodexAppServer) Name() string { return p.name }

func (p *CodexAppServer) IsConfigured() bool { return strings.TrimSpace(p.command) != "" }

func (p *CodexAppServer) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	if !p.IsConfigured() {
		return "", fmt.Errorf("codex app-server: empty command")
	}
	client, err := startCodexAppServer(ctx, *p)
	if err != nil {
		return "", err
	}
	defer client.Close()

	if _, err := client.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "coden",
			"title":   "CodeN",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"optOutNotificationMethods": []string{
				"thread/status/changed",
				"thread/tokenUsage/updated",
				"remoteControl/status/changed",
			},
		},
	}); err != nil {
		return "", fmt.Errorf("codex app-server: initialize: %w", err)
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		return "", fmt.Errorf("codex app-server: initialized: %w", err)
	}

	cwd := p.cwd
	if strings.TrimSpace(cwd) == "" {
		cwd, _ = os.Getwd()
	}
	threadParams := map[string]any{
		"cwd":            cwd,
		"model":          model,
		"ephemeral":      true,
		"sandbox":        "read-only",
		"approvalPolicy": "never",
		"threadSource":   "api",
	}
	threadRaw, err := client.call(ctx, "thread/start", threadParams)
	if err != nil {
		return "", fmt.Errorf("codex app-server: thread/start: %w", err)
	}
	threadID := extractThreadID(threadRaw)
	if threadID == "" {
		return "", fmt.Errorf("codex app-server: thread/start returned no thread id")
	}

	turnID, err := client.startTurn(ctx, threadID, model, cwd, codexPromptText(messages))
	if err != nil {
		return "", err
	}
	reply, err := client.waitTurn(ctx, threadID, turnID)
	if err != nil {
		return "", err
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", fmt.Errorf("codex app-server: empty response")
	}
	return reply, nil
}

type codexAppServerClient struct {
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        *bufio.Scanner
	pending       map[int64]chan codexRPCMessage
	notifications chan codexRPCMessage
	nextID        int64
	mu            sync.Mutex
}

type codexRPCMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func startCodexAppServer(ctx context.Context, cfg CodexAppServer) (*codexAppServerClient, error) {
	parts := strings.Fields(cfg.command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("codex app-server: empty command")
	}
	cmdName := parts[0]
	cmdArgs := append(append([]string(nil), parts[1:]...), cfg.args...)
	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	if cfg.cwd != "" {
		cmd.Dir = cfg.cwd
	}
	cmd.Stderr = os.Stderr
	if len(cfg.env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range cfg.env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex app-server: start: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	c := &codexAppServerClient{
		cmd:           cmd,
		stdin:         stdin,
		stdout:        scanner,
		pending:       make(map[int64]chan codexRPCMessage),
		notifications: make(chan codexRPCMessage, 128),
	}
	go c.readLoop()
	return c, nil
}

func (c *codexAppServerClient) Close() {
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
}

func (c *codexAppServerClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, ch, err := c.send(method, params, true)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("codex app-server: connection closed")
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("%s", msg.Error.Message)
		}
		return msg.Result, nil
	}
}

func (c *codexAppServerClient) notify(method string, params any) error {
	_, _, err := c.send(method, params, false)
	return err
}

func (c *codexAppServerClient) send(method string, params any, withID bool) (int64, chan codexRPCMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	msg := map[string]any{"method": method, "params": params}
	var ch chan codexRPCMessage
	var id int64
	if withID {
		c.nextID++
		id = c.nextID
		msg["id"] = id
		ch = make(chan codexRPCMessage, 1)
		c.pending[id] = ch
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return 0, nil, err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		if withID {
			delete(c.pending, id)
		}
		return 0, nil, err
	}
	return id, ch, nil
}

func (c *codexAppServerClient) readLoop() {
	defer close(c.notifications)
	for c.stdout.Scan() {
		var msg codexRPCMessage
		if err := json.Unmarshal(c.stdout.Bytes(), &msg); err != nil {
			continue
		}
		if msg.ID != nil {
			c.mu.Lock()
			ch := c.pending[*msg.ID]
			delete(c.pending, *msg.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
				close(ch)
			} else if msg.Method != "" {
				_ = c.respondUnsupported(*msg.ID, msg.Method)
			}
			continue
		}
		c.notifications <- msg
	}
}

func (c *codexAppServerClient) respondUnsupported(id int64, method string) error {
	return c.writeRaw(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    -32601,
			"message": "CodeN codex provider does not support client callback: " + method,
		},
	})
}

func (c *codexAppServerClient) writeRaw(msg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *codexAppServerClient) startTurn(ctx context.Context, threadID, model, cwd, prompt string) (string, error) {
	params := map[string]any{
		"threadId":       threadID,
		"model":          model,
		"cwd":            cwd,
		"approvalPolicy": "never",
		"sandboxPolicy": map[string]any{
			"type":          "readOnly",
			"networkAccess": false,
		},
		"input": []map[string]any{
			{"type": "text", "text": prompt},
		},
	}
	raw, err := c.call(ctx, "turn/start", params)
	if err != nil {
		return "", fmt.Errorf("codex app-server: turn/start: %w", err)
	}
	turnID := extractTurnID(raw)
	if turnID == "" {
		return "", fmt.Errorf("codex app-server: turn/start returned no turn id")
	}
	return turnID, nil
}

func (c *codexAppServerClient) waitTurn(ctx context.Context, threadID, turnID string) (string, error) {
	var sb strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case msg, ok := <-c.notifications:
			if !ok {
				return "", fmt.Errorf("codex app-server: connection closed")
			}
			switch msg.Method {
			case "item/agentMessage/delta":
				var p struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Delta    string `json:"delta"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				if p.ThreadID == threadID && p.TurnID == turnID {
					sb.WriteString(p.Delta)
				}
			case "error":
				if err := parseCodexTurnError(msg.Params); err != nil {
					return "", err
				}
			case "turn/completed":
				done, err := parseCodexTurnCompleted(msg.Params, threadID, turnID)
				if err != nil {
					return "", err
				}
				if done {
					return sb.String(), nil
				}
			}
		}
	}
}

func codexPromptText(messages []Message) string {
	var b strings.Builder
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.ToUpper(role))
		b.WriteString(":\n")
		b.WriteString(content)
	}
	return b.String()
}

func extractThreadID(raw json.RawMessage) string {
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(raw, &result)
	return result.Thread.ID
}

func extractTurnID(raw json.RawMessage) string {
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &result)
	if result.Turn.ID != "" {
		return result.Turn.ID
	}
	return result.ID
}

func parseCodexTurnCompleted(raw json.RawMessage, threadID, turnID string) (bool, error) {
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.ThreadID != threadID || p.Turn.ID != turnID {
		return false, nil
	}
	switch p.Turn.Status {
	case "completed":
		return true, nil
	case "failed", "interrupted":
		if p.Turn.Error != nil && p.Turn.Error.Message != "" {
			return true, errors.New(p.Turn.Error.Message)
		}
		return true, fmt.Errorf("codex app-server: turn %s", p.Turn.Status)
	default:
		return false, nil
	}
}

func parseCodexTurnError(raw json.RawMessage) error {
	var p struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.Error.Message == "" {
		return nil
	}
	return errors.New(p.Error.Message)
}
