package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const copilotBaseURL = "https://api.githubcopilot.com"

// Copilot implements the GitHub Copilot chat completions API.
// Copilot is OpenAI-compatible, so it embeds *OpenAI and re-wraps errors.
type Copilot struct {
	*OpenAI
}

func NewCopilot(apiKey string, httpCli *http.Client) *Copilot {
	if apiKey == "" {
		apiKey = os.Getenv("GITHUB_COPILOT_TOKEN")
	}
	baseURL := envOr("COPILOT_BASE_URL", copilotBaseURL)
	return &Copilot{
		OpenAI: NewOpenAI(apiKey, baseURL, httpCli),
	}
}

func (p *Copilot) Name() string { return "copilot" }

// Chat overrides OpenAI.Chat to re-wrap errors with the correct provider prefix.
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
