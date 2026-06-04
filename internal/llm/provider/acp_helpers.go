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

// convertToAcpPrompt converts CodeN's chat-style messages into ACP's
// session/prompt content blocks. ACP prompt turns are user-message shaped, so
// we preserve roles inline inside a single text block.
func convertToAcpPrompt(messages []Message) []acp.ContentBlock {
	var text strings.Builder
	for _, m := range messages {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		if text.Len() > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(strings.ToUpper(role))
		text.WriteString(":\n")
		text.WriteString(content)
	}
	return []acp.ContentBlock{{Type: "text", Text: text.String()}}
}
