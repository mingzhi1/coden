// Package provider defines the chat completion interface and implementations
// for multiple LLM providers: OpenAI (+ compatible), Anthropic, DeepSeek,
// GitHub Copilot.
package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	clog "github.com/mingzhi1/coden/internal/log"
)

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TruncatedError is returned when an LLM response is cut short by the
// output-token limit.  It carries the partial content so callers can
// implement continuation/recovery logic.
type TruncatedError struct {
	Content string // partial response text produced before the cut
	Err     error  // underlying provider error
}

func (e *TruncatedError) Error() string { return e.Err.Error() }
func (e *TruncatedError) Unwrap() error { return e.Err }

// ProviderError is a structured error from an LLM provider HTTP call.
// It replaces string-based classification with machine-readable fields,
// enabling errors.As/Is throughout the error chain.
type ProviderError struct {
	Provider   string // "openai", "anthropic", "copilot", etc.
	StatusCode int    // HTTP status code (0 if not an HTTP error)
	Message    string // human-readable description
	Err        error  // underlying error (may be nil)
}

func (e *ProviderError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s: API %d: %s", e.Provider, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Provider, e.Message)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// IsRateLimit returns true if this is a 429 rate-limit error.
func (e *ProviderError) IsRateLimit() bool { return e.StatusCode == 429 }

// IsOverloaded returns true if this is a 529 overloaded error.
func (e *ProviderError) IsOverloaded() bool { return e.StatusCode == 529 }

// IsServerError returns true if the status code indicates a retryable server error.
func (e *ProviderError) IsServerError() bool {
	return e.StatusCode >= 500 && e.StatusCode <= 599
}

// IsAuthError returns true if this is a 401/403 authentication error.
func (e *ProviderError) IsAuthError() bool {
	return e.StatusCode == 401 || e.StatusCode == 403
}

// IsPromptTooLong returns true if this is a 413 payload too large error.
func (e *ProviderError) IsPromptTooLong() bool { return e.StatusCode == 413 }

// IsRetryable returns true if the error is transient and the request should be retried.
func (e *ProviderError) IsRetryable() bool {
	return e.IsRateLimit() || e.IsOverloaded() || e.IsServerError()
}

// ChatProvider is the interface every LLM backend must implement.
type ChatProvider interface {
	// Chat sends messages and returns the assistant reply.
	Chat(ctx context.Context, model string, messages []Message) (string, error)
	// IsConfigured returns true when the provider has a usable API key.
	IsConfigured() bool
	// Name returns the provider identifier (e.g. "openai", "anthropic").
	Name() string
}

// Config holds the provider configuration.
type Config struct {
	Provider string // "openai" | "anthropic" | "deepseek" | "minimax" | "copilot" | "acp" | ""=auto
	APIKey   string
	BaseURL  string // OpenAI-compatible base URL override (also used by anthropic)
	Model    string
	// ACP-specific fields (only used when Provider == "acp").
	AcpName    string            // display name for the ACP provider
	AcpCommand string            // executable command (e.g. "npx @agentclientprotocol/claude-agent-acp")
	AcpArgs    []string          // additional CLI arguments
	AcpEnv     map[string]string // environment variables
	AcpCwd     string            // working directory for ACP sessions
}

// New creates a ChatProvider from config. Auto-detects if Provider is empty.
func New(cfg Config) (ChatProvider, string) {
	timeout := 120 * time.Second
	if ms := os.Getenv("API_TIMEOUT_MS"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 {
			timeout = time.Duration(v) * time.Millisecond
		}
	}
	httpCli := clog.NewHTTPClient(timeout)
	name := strings.ToLower(cfg.Provider)
	if name == "" {
		name = autoDetect()
	}

	model := cfg.Model
	if model == "" {
		model = defaultModel(name)
	}

	switch name {
	case "acp":
		return NewAcp(AcpConfig{
			Name:    cfg.AcpName,
			Command: cfg.AcpCommand,
			Args:    cfg.AcpArgs,
			Env:     cfg.AcpEnv,
			CWD:     cfg.AcpCwd,
		}), model
	case "anthropic":
		return NewAnthropic(cfg.APIKey, cfg.BaseURL, httpCli), model
	case "deepseek":
		return NewDeepSeek(cfg.APIKey, httpCli), model
	case "minimax":
		return NewMiniMax(cfg.APIKey, cfg.BaseURL, httpCli), model
	case "copilot":
		return NewCopilot(cfg.APIKey, httpCli), model
	default:
		return NewOpenAI(cfg.APIKey, cfg.BaseURL, httpCli), model
	}
}

func autoDetect() string {
	// ACP providers take priority when configured.
	if os.Getenv("CODEN_ACP_COMMAND") != "" {
		return "acp"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "anthropic"
	}
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		return "deepseek"
	}
	if os.Getenv("MINIMAX_API_KEY") != "" {
		return "minimax"
	}
	if os.Getenv("GITHUB_COPILOT_TOKEN") != "" {
		return "copilot"
	}
	return "openai"
}

func defaultModel(providerName string) string {
	switch providerName {
	case "anthropic":
		return envOr("ANTHROPIC_MODEL", "claude-3-5-haiku-20241022")
	case "deepseek":
		return envOr("DEEPSEEK_MODEL", "deepseek-chat")
	case "minimax":
		return envOr("MINIMAX_MODEL", "MiniMax-M2.7")
	case "copilot":
		return envOr("COPILOT_MODEL", "gpt-4o")
	default:
		return envOr("OPENAI_MODEL", "gpt-4o-mini")
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func trimBody(b []byte, max int) string {
	s := string(b)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
