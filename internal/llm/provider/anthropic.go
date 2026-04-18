package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	anthropicBaseURL = "https://api.anthropic.com/v1"
	anthropicVersion = "2023-06-01"
)

// Anthropic implements the Anthropic Messages API.
type Anthropic struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func NewAnthropic(apiKey, baseURL string, httpCli *http.Client) *Anthropic {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if baseURL == "" {
		baseURL = envOr("ANTHROPIC_BASE_URL", anthropicBaseURL)
	}
	return &Anthropic{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), http: httpCli}
}

func (p *Anthropic) Name() string       { return "anthropic" }
func (p *Anthropic) IsConfigured() bool { return p.apiKey != "" }

func (p *Anthropic) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	start := time.Now()
	var system string
	var turns []anthropicTurn
	for _, m := range messages {
		if m.Role == "system" {
			if system == "" {
				system = m.Content
			} else {
				system += "\n" + m.Content
			}
			continue
		}
		turns = append(turns, anthropicTurn{Role: m.Role, Content: m.Content})
	}

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 4096,
		System:    system,
		Messages:  turns,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshal: %w", err)
	}

	slog.Debug("[llm:anthropic] sending request", "model", model, "url", p.baseURL+"/messages", "prompt_len", len(payload))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("anthropic: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("anthropic: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("[llm:anthropic] error response", "status", resp.StatusCode, "body", trimBody(raw, 300), "duration_ms", time.Since(start).Milliseconds())
		return "", &ProviderError{
			Provider:   "anthropic",
			StatusCode: resp.StatusCode,
			Message:    string(trimBody(raw, 300)),
		}
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("anthropic: parse response: %w", err)
	}

	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	reply := strings.TrimSpace(sb.String())
	duration := time.Since(start)

	if reply == "" {
		slog.Warn("[llm:anthropic] empty response", "duration_ms", duration.Milliseconds())
		return "", fmt.Errorf("anthropic: empty response")
	}
	if result.StopReason == "max_tokens" {
		slog.Warn("[llm:anthropic] response truncated", "duration_ms", duration.Milliseconds(), "partial_len", len(reply))
		return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("anthropic: response truncated (stop_reason=max_tokens)")}
	}

	slog.Info("[llm:anthropic] response received", "model", model, "duration_ms", duration.Milliseconds(),
		"input_tokens", result.Usage.InputTokens, "output_tokens", result.Usage.OutputTokens,
		"reply_len", len(reply))
	slog.Debug("[llm:anthropic] reply content", "model", model, "reply", reply)
	return reply, nil
}

type anthropicTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []anthropicTurn `json:"messages"`
}
