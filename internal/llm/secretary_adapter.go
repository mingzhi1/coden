package llm

import (
	"context"

	"github.com/mingzhi1/coden/internal/secretary"
)

// SecretaryLLMAdapter adapts Chatter to the secretary.LLM interface.
type SecretaryLLMAdapter struct {
	chatter Chatter
}

// NewSecretaryAdapter creates an adapter for the Secretary Agent.
func NewSecretaryAdapter(chatter Chatter) *SecretaryLLMAdapter {
	return &SecretaryLLMAdapter{chatter: chatter}
}

// Chat implements secretary.LLM by delegating to the Chatter.
func (a *SecretaryLLMAdapter) Chat(ctx context.Context, role string, messages []secretary.LLMMessage) (string, error) {
	llmMsgs := make([]Message, len(messages))
	for i, m := range messages {
		llmMsgs[i] = Message{Role: m.Role, Content: m.Content}
	}
	return a.chatter.Chat(ctx, role, llmMsgs)
}
