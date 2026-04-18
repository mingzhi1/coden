package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// DeepSeek uses the OpenAI-compatible API with DeepSeek's endpoint.
type DeepSeek struct {
	*OpenAI
}

func NewDeepSeek(apiKey string, httpCli *http.Client) *DeepSeek {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	baseURL := envOr("DEEPSEEK_BASE_URL", "https://api.deepseek.com/v1")
	return &DeepSeek{
		OpenAI: NewOpenAI(apiKey, baseURL, httpCli),
	}
}

func (p *DeepSeek) Name() string { return "deepseek" }

// Chat overrides OpenAI.Chat to re-wrap errors with the correct provider prefix.
func (p *DeepSeek) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	reply, err := p.OpenAI.Chat(ctx, model, messages)
	if err != nil {
		var te *TruncatedError
		if errors.As(err, &te) {
			return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("deepseek: %s", strings.TrimPrefix(te.Err.Error(), "openai: "))}
		}
		// Re-wrap ProviderError with correct provider name.
		var pe *ProviderError
		if errors.As(err, &pe) {
			return "", &ProviderError{
				Provider:   "deepseek",
				StatusCode: pe.StatusCode,
				Message:    pe.Message,
				Err:        pe.Err,
			}
		}
		return "", fmt.Errorf("deepseek: %s", strings.TrimPrefix(err.Error(), "openai: "))
	}
	return reply, nil
}
