package provider

import (
	"context"
	"strings"

	"github.com/mingzhi1/coden/internal/acp"
)

func (p *Acp) ensureConn(ctx context.Context) (*acp.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		return p.conn, nil
	}
	conn, err := acp.Dial(ctx, acp.DialConfig{
		Name:          p.providerName,
		Command:       p.command,
		Args:          p.args,
		Env:           p.env,
		ClientName:    "coden",
		ClientVersion: "0.1.0",
	})
	if err != nil {
		return nil, err
	}
	p.conn = conn
	return conn, nil
}

func (p *Acp) resetConn() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

// convertToAcpMessages converts CodeN provider.Message slice to ACP format.
// System messages are merged into the first user message as a prefix,
// since ACP protocol uses the standard user/assistant role pattern.
func convertToAcpMessages(messages []Message) []acp.PromptMessage {
	var system strings.Builder
	var out []acp.PromptMessage

	for _, m := range messages {
		if m.Role == "system" {
			if system.Len() > 0 {
				system.WriteString("\n")
			}
			system.WriteString(m.Content)
			continue
		}

		content := m.Content
		if m.Role == "user" && system.Len() > 0 {
			content = system.String() + "\n\n" + content
			system.Reset()
		}

		out = append(out, acp.PromptMessage{
			Role:    m.Role,
			Content: []acp.ContentBlock{{Type: "text", Text: content}},
		})
	}
	return out
}
