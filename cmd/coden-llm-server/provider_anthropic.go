package main

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
		slog.Warn("[llm:anthropic] error response", "status", resp.StatusCode)
		return "", fmt.Errorf("anthropic: API %d: %s", resp.StatusCode, trimBody(raw, 300))
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

	if reply == "" {
		return "", fmt.Errorf("anthropic: empty response")
	}
	if result.StopReason == "max_tokens" {
		return reply, &TruncatedError{Content: reply, Err: fmt.Errorf("anthropic: truncated")}
	}

	slog.Info("[llm:anthropic] response received", "model", model, "reply_len", len(reply),
		"tokens_in", result.Usage.InputTokens, "tokens_out", result.Usage.OutputTokens)
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
