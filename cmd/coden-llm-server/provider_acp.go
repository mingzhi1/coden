package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/acp"
)

// Acp implements ChatProvider via ACP (Agent Client Protocol) over stdio.
type Acp struct {
	providerName string
	command      string
	args         []string
	env          map[string]string
	cwd          string

	mu   sync.Mutex
	conn *acp.Conn // lazy init on first Chat()
}

// AcpConfig holds ACP provider configuration.
type AcpConfig struct {
	Name    string            // display name (e.g. "claude-code")
	Command string            // executable
	Args    []string          // additional CLI arguments
	Env     map[string]string // environment variables for subprocess
	CWD     string            // working directory
}

func NewAcp(cfg AcpConfig) *Acp {
	name := cfg.Name
	if name == "" {
		name = "acp"
	}
	return &Acp{
		providerName: name,
		command:      cfg.Command,
		args:         cfg.Args,
		env:          cfg.Env,
		cwd:          cfg.CWD,
	}
}
func (p *Acp) Name() string      { return p.providerName }
func (p *Acp) IsConfigured() bool { return p.command != "" }

func (p *Acp) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	start := time.Now()

	conn, err := p.ensureConn(ctx)
	if err != nil {
		return "", fmt.Errorf("acp(%s): connect: %w", p.providerName, err)
	}

	sessionID, err := conn.NewSession(ctx, p.cwd)
	if err != nil {
		p.resetConn()
		return "", fmt.Errorf("acp(%s): newSession: %w", p.providerName, err)
	}
	defer conn.CloseSession(context.Background(), sessionID)

	acpMsgs := convertToAcpMessages(messages)

	if err := conn.Prompt(sessionID, acpMsgs); err != nil {
		return "", fmt.Errorf("acp(%s): prompt: %w", p.providerName, err)
	}

	var sb strings.Builder
	var stopReason string
	var usage *acp.Usage

	for {
		notification, err := conn.ReadNotification(ctx)
		if err != nil {
			partial := sb.String()
			if partial != "" {
				return partial, &TruncatedError{
					Content: partial,
					Err:     fmt.Errorf("acp(%s): stream interrupted: %w", p.providerName, err),
				}
			}
			return "", fmt.Errorf("acp(%s): read: %w", p.providerName, err)
		}

		switch notification.Type {
		case "sessionUpdate":
			if notification.SessionUpdate == nil {
				continue
			}
			switch notification.SessionUpdate.Type {
			case "agent_message_chunk":
				sb.WriteString(notification.SessionUpdate.Text)
			}
		case "promptResponse":
			stopReason = notification.StopReason
			usage = notification.Usage
			goto done
		}
	}
done:
	reply := strings.TrimSpace(sb.String())
	duration := time.Since(start)

	if reply == "" {
		slog.Warn("[acp] empty response", "name", p.providerName, "duration_ms", duration.Milliseconds())
		return "", fmt.Errorf("acp(%s): empty response", p.providerName)
	}

	if stopReason == "max_tokens" {
		return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("acp(%s): truncated", p.providerName)}
	}

	inputToks, outputToks := 0, 0
	if usage != nil {
		inputToks = usage.InputTokens
		outputToks = usage.OutputTokens
	}
	slog.Info("[acp] response received", "name", p.providerName,
		"duration_ms", duration.Milliseconds(),
		"input_tokens", inputToks, "output_tokens", outputToks,
		"reply_len", len(reply))
	return reply, nil
}

func (p *Acp) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		err := p.conn.Close()
		p.conn = nil
		return err
	}
	return nil
}

func (p *Acp) ensureConn(ctx context.Context) (*acp.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		return p.conn, nil
	}
	conn, err := acp.Dial(ctx, acp.DialConfig{
		Name:          p.providerName,
		Command:       p.command,
		Args:          p.args,
		Env:           p.env,
		ClientName:    "llm-server",
		ClientVersion: "0.1.0",
	})
	if err != nil {
		return nil, err
	}
	p.conn = conn
	return conn, nil
}

func (p *Acp) resetConn() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

func convertToAcpMessages(messages []Message) []acp.PromptMessage {
	var system strings.Builder
	var out []acp.PromptMessage

	for _, m := range messages {
		if m.Role == "system" {
			if system.Len() > 0 {
				system.WriteString("\n")
			}
			system.WriteString(m.Content)
			continue
		}

		content := m.Content
		if m.Role == "user" && system.Len() > 0 {
			content = system.String() + "\n\n" + content
			system.Reset()
		}

		out = append(out, acp.PromptMessage{
			Role:    m.Role,
			Content: []acp.ContentBlock{{Type: "text", Text: content}},
		})
	}
	return out
}
