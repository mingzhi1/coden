package provider

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/acp"
)

// maxAcpReasoningToolCalls bounds how many tool-call attempts a reasoning-only
// ACP turn may emit before coden gives up and lets the broker fall back. An
// agent that needs tools coden can't provide will otherwise loop indefinitely.
const maxAcpReasoningToolCalls = 4

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

	// promptMu serializes Chat calls on the shared connection. The single conn
	// has one NotifyCh with no per-session demux, so concurrent prompts (the
	// kernel runs workers in parallel) would interleave on it and cross-contaminate
	// responses. Serializing keeps each prompt's stream isolated.
	promptMu sync.Mutex
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

func (p *Acp) Name() string       { return p.providerName }
func (p *Acp) IsConfigured() bool { return p.command != "" }

// Chat sends messages to the ACP server and returns the assembled text reply.
func (p *Acp) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	start := time.Now()

	conn, err := p.ensureConn(ctx)
	if err != nil {
		return "", fmt.Errorf("acp(%s): connect: %w", p.providerName, err)
	}

	// Serialize prompts on the shared connection: the single NotifyCh has no
	// per-session demux, so concurrent calls would cross-contaminate streams.
	p.promptMu.Lock()
	defer p.promptMu.Unlock()

	cwd := strings.TrimSpace(p.cwd)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// claude-code-acp resolves its file-tool permission boundary from the session
	// cwd. A relative path (e.g. the default "workspace") lands the agent in a
	// directory relative to wherever coden was launched — usually the wrong place,
	// with no write access to the real project. Always pass an absolute path.
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	sessionID, err := conn.NewSession(ctx, cwd)
	if err != nil {
		p.resetConn()
		return "", fmt.Errorf("acp(%s): newSession: %w", p.providerName, err)
	}
	defer conn.CloseSession(context.Background(), sessionID)

	// Select the model for this session (e.g. opus vs sonnet). Best-effort: if the
	// agent doesn't support session/set_model, keep its default model.
	if strings.TrimSpace(model) != "" {
		if err := conn.SetModel(ctx, sessionID, model); err != nil {
			slog.Warn("[acp] set model failed, using agent default",
				"name", p.providerName, "model", model, "error", err)
		}
	}

	acpPrompt := convertToAcpPrompt(messages)

	if err := conn.Prompt(sessionID, acpPrompt); err != nil {
		return "", fmt.Errorf("acp(%s): prompt: %w", p.providerName, err)
	}

	var sb strings.Builder
	var stopReason string
	var usage *acp.Usage
	var msgChunks, thoughtChunks, toolCalls int // DEBUG: chunk breakdown

	for {
		notification, err := conn.ReadNotification(ctx)
		if err != nil {
			// If the caller gave up (timeout/cancel), tell the agent to stop so it
			// doesn't keep generating (and burning tokens) on a dead turn.
			if ctx.Err() != nil {
				conn.Cancel(sessionID)
			}
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
				msgChunks++
				sb.WriteString(notification.SessionUpdate.Text)
			case "agent_thought_chunk":
				thoughtChunks++ // Thinking — skip content, but count it.
			case "tool_call":
				toolCalls++
				slog.Warn("[acp] tool_call in reasoning mode (coden provides no tools here)",
					"name", p.providerName, "session", sessionID, "count", toolCalls)
				// Reasoning-only contract: the agent has no tools in this mode, so
				// coden declines every tool/permission request. A well-behaved
				// completion just answers; an agentic model (Claude via ACP) instead
				// keeps retrying tools, looping until the turn stalls. Bail fast once
				// it's clearly stuck in a tool loop without producing any text, so the
				// broker falls back to a working provider instead of hanging the whole
				// workflow. (The real fix is to stop the agent from attempting tools
				// at all — pending ACP capability/permission support.)
				if toolCalls >= maxAcpReasoningToolCalls && sb.Len() == 0 {
					conn.Cancel(sessionID)
					return "", fmt.Errorf("acp(%s): agent attempted %d tool calls in reasoning-only mode without emitting text — this provider cannot serve reasoning roles over ACP (route the role to an HTTP provider instead)", p.providerName, toolCalls)
				}
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
	replyForLog := reply
	if len(replyForLog) > 800 {
		replyForLog = replyForLog[:800] + "…(truncated)"
	}
	slog.Info("[acp] response received", "name", p.providerName,
		"duration_ms", duration.Milliseconds(),
		"input_tokens", inputToks, "output_tokens", outputToks,
		"reply_len", len(reply),
		// DEBUG: chunk breakdown + raw reply — tells us whether the agent ran
		// agentic (tool_calls coden ignores) and only sent a tiny text chunk, or
		// genuinely replied short.
		"msg_chunks", msgChunks, "thought_chunks", thoughtChunks, "tool_calls", toolCalls,
		"reply", replyForLog)
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
