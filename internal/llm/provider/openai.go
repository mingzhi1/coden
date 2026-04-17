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

// OpenAI implements the OpenAI chat completions API (and compatible).
type OpenAI struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func NewOpenAI(apiKey, baseURL string, httpCli *http.Client) *OpenAI {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if baseURL == "" {
		baseURL = envOr("OPENAI_BASE_URL", "https://api.openai.com/v1")
	}
	return &OpenAI{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpCli,
	}
}

func (p *OpenAI) Name() string       { return "openai" }
func (p *OpenAI) IsConfigured() bool { return p.apiKey != "" }

func (p *OpenAI) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	start := time.Now()

	body := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": 0.2,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("openai: marshal: %w", err)
	}

	slog.Debug("[llm:openai] sending request", "model", model, "url", p.baseURL+"/chat/completions", "prompt_len", len(payload))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("openai: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("[llm:openai] error response", "status", resp.StatusCode, "body", trimBody(raw, 300), "duration_ms", time.Since(start).Milliseconds())
		return "", fmt.Errorf("openai: API %d: %s", resp.StatusCode, trimBody(raw, 300))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("openai: parse response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices")
	}
	content := result.Choices[0].Message.Content
	reason := result.Choices[0].FinishReason
	duration := time.Since(start)

	if reason == "length" {
		slog.Warn("[llm:openai] response truncated", "duration_ms", duration.Milliseconds(), "partial_len", len(content))
		return content, &TruncatedError{Content: content, Err: fmt.Errorf("openai: response truncated (finish_reason=length)")}
	}

	slog.Info("[llm:openai] response received", "model", model, "duration_ms", duration.Milliseconds(),
		"prompt_tokens", result.Usage.PromptTokens, "completion_tokens", result.Usage.CompletionTokens,
		"total_tokens", result.Usage.TotalTokens, "reply_len", len(content))
	slog.Debug("[llm:openai] reply content", "model", model, "reply", content)
	return strings.TrimSpace(content), nil
}
