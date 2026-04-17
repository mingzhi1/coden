package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/acp"
)

// Acp implements ChatProvider via the ACP (Agent Client Protocol) over stdio.
//
// Instead of sending HTTP requests to a cloud API, it communicates with a
// long-lived ACP server subprocess via ndJSON. Each Chat() call creates a
// fresh ACP session (stateless, matching CodeN's "Workers Are Stateless"
// design principle), sends a prompt, collects agent_message_chunk events,
// and returns the assembled text.
//
// The ACP server's built-in tools are disabled — it acts as a pure LLM
// reasoning engine. CodeN's Coder Worker parses tool_calls from the text
// response and executes them through the Kernel's Tool Runtime.
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
	Name    string            // provider display name
	Command string            // executable
	Args    []string          // additional CLI arguments
	Env     map[string]string // environment variables
	CWD     string            // working directory for ACP sessions
}

// NewAcp creates an ACP-backed ChatProvider.
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

// Chat sends messages to the ACP server and returns the assembled text reply.
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
			case "agent_thought_chunk":
				// Thinking — skip.
			case "tool_call":
				slog.Warn("[acp] unexpected tool_call in reasoning mode",
					"name", p.providerName, "session", sessionID)
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
		slog.Warn("[acp] empty response", "name", p.providerName,
			"duration_ms", duration.Milliseconds())
		return "", fmt.Errorf("acp(%s): empty response", p.providerName)
	}

	if stopReason == "max_tokens" {
		slog.Warn("[acp] response truncated", "name", p.providerName,
			"duration_ms", duration.Milliseconds(), "partial_len", len(reply))
		return reply, &TruncatedError{
			Content: reply,
			Err:     fmt.Errorf("acp(%s): truncated (stop_reason=max_tokens)", p.providerName),
		}
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

// Close shuts down the ACP server subprocess.
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
