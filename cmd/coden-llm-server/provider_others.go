package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// --- DeepSeek ---

type DeepSeek struct {
	*OpenAI
}

func NewDeepSeek(apiKey string, httpCli *http.Client) *DeepSeek {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	baseURL := envOr("DEEPSEEK_BASE_URL", "https://api.deepseek.com/v1")
	return &DeepSeek{OpenAI: NewOpenAI(apiKey, baseURL, httpCli)}
}
func (p *DeepSeek) Name() string { return "deepseek" }
func (p *DeepSeek) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	reply, err := p.OpenAI.Chat(ctx, model, messages)
	if err != nil {
		var te *TruncatedError
		if errors.As(err, &te) {
			return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("deepseek: %s", strings.TrimPrefix(te.Err.Error(), "openai: "))}
		}
		return "", fmt.Errorf("deepseek: %s", strings.TrimPrefix(err.Error(), "openai: "))
	}
	return reply, nil
}

// --- MiniMax ---

type MiniMax struct {
	*OpenAI
}

func NewMiniMax(apiKey, baseURL string, httpCli *http.Client) *MiniMax {
	if apiKey == "" {
		apiKey = os.Getenv("MINIMAX_API_KEY")
	}
	if baseURL == "" {
		baseURL = envOr("MINIMAX_BASE_URL", "https://api.minimax.chat/v1")
	}
	return &MiniMax{OpenAI: NewOpenAI(apiKey, baseURL, httpCli)}
}
func (p *MiniMax) Name() string { return "minimax" }
func (p *MiniMax) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	reply, err := p.OpenAI.Chat(ctx, model, messages)
	if err != nil {
		var te *TruncatedError
		if errors.As(err, &te) {
			return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("minimax: %s", strings.TrimPrefix(te.Err.Error(), "openai: "))}
		}
		return "", fmt.Errorf("minimax: %s", strings.TrimPrefix(err.Error(), "openai: "))
	}
	return reply, nil
}

// --- Copilot ---

const copilotBaseURL = "https://api.githubcopilot.com"

type Copilot struct {
	*OpenAI
}

func NewCopilot(apiKey string, httpCli *http.Client) *Copilot {
	if apiKey == "" {
		apiKey = os.Getenv("GITHUB_COPILOT_TOKEN")
	}
	baseURL := envOr("COPILOT_BASE_URL", copilotBaseURL)
	return &Copilot{OpenAI: NewOpenAI(apiKey, baseURL, httpCli)}
}
func (p *Copilot) Name() string { return "copilot" }
func (p *Copilot) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	reply, err := p.OpenAI.Chat(ctx, model, messages)
	if err != nil {
		var te *TruncatedError
		if errors.As(err, &te) {
			return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("copilot: %s", strings.TrimPrefix(te.Err.Error(), "openai: "))}
		}
		return "", fmt.Errorf("copilot: %s", strings.TrimPrefix(err.Error(), "openai: "))
	}
	return reply, nil
}
