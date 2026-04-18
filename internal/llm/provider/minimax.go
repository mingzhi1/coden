package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// MiniMax uses an OpenAI-compatible API.
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
	return &MiniMax{
		OpenAI: NewOpenAI(apiKey, baseURL, httpCli),
	}
}

func (p *MiniMax) Name() string { return "minimax" }

// Chat overrides OpenAI.Chat to re-wrap errors with the correct provider prefix.
func (p *MiniMax) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	reply, err := p.OpenAI.Chat(ctx, model, messages)
	if err != nil {
		var te *TruncatedError
		if errors.As(err, &te) {
			return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("minimax: %s", strings.TrimPrefix(te.Err.Error(), "openai: "))}
		}
		var pe *ProviderError
		if errors.As(err, &pe) {
			return "", &ProviderError{
				Provider:   "minimax",
				StatusCode: pe.StatusCode,
				Message:    pe.Message,
				Err:        pe.Err,
			}
		}
		return "", fmt.Errorf("minimax: %s", strings.TrimPrefix(err.Error(), "openai: "))
	}
	return reply, nil
}
