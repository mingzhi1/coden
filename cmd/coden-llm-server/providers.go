package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TruncatedError is returned when an LLM response is cut short.
type TruncatedError struct {
	Content string
	Err     error
}

func (e *TruncatedError) Error() string { return e.Err.Error() }
func (e *TruncatedError) Unwrap() error { return e.Err }

// ChatProvider is the interface every LLM backend must implement.
type ChatProvider interface {
	Chat(ctx context.Context, model string, messages []Message) (string, error)
	IsConfigured() bool
	Name() string
}

// ProviderConfig holds the provider configuration.
type ProviderConfig struct {
	Provider string            // "openai" | "anthropic" | "deepseek" | "minimax" | "copilot" | "acp" | ""=auto
	APIKey   string
	BaseURL  string
	Model    string
	AcpName    string
	AcpCommand string
	AcpArgs    []string
	AcpEnv     map[string]string
	AcpCwd     string
}

// NewChatProvider creates a ChatProvider from config.
func NewChatProvider(cfg ProviderConfig) (ChatProvider, string) {
	timeout := 120 * time.Second
	if ms := os.Getenv("API_TIMEOUT_MS"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil && v > 0 {
			timeout = time.Duration(v) * time.Millisecond
		}
	}
	httpCli := &http.Client{Timeout: timeout}
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
