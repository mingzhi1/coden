package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestCodexAppServerChatCollectsAgentDeltas(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	p := NewCodexAppServer(CodexAppServerConfig{
		Name:    "codex-test",
		Command: exe,
		Args:    []string{"-test.run=TestFakeCodexAppServerHelper"},
		Env:     map[string]string{"CODEN_FAKE_CODEX_APP_SERVER": "1"},
		CWD:     t.TempDir(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := p.Chat(ctx, "gpt-test", []Message{
		{Role: "system", Content: "reply briefly"},
		{Role: "user", Content: "say hello"},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("expected collected reply, got %q", got)
	}
}

func TestFakeCodexAppServerHelper(t *testing.T) {
	if os.Getenv("CODEN_FAKE_CODEX_APP_SERVER") != "1" {
		return
	}
	runFakeCodexAppServer()
	os.Exit(0)
}

func runFakeCodexAppServer() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var msg struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			writeFake(map[string]any{"id": *msg.ID, "result": map[string]any{}})
		case "initialized":
		case "thread/start":
			writeFake(map[string]any{"id": *msg.ID, "result": map[string]any{
				"thread": map[string]any{"id": "thread-1"},
			}})
		case "turn/start":
			writeFake(map[string]any{"id": *msg.ID, "result": map[string]any{
				"turn": map[string]any{"id": "turn-1"},
			}})
			writeFake(map[string]any{
				"method": "item/agentMessage/delta",
				"params": map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-1",
					"itemId":   "item-1",
					"delta":    "hello ",
				},
			})
			writeFake(map[string]any{
				"method": "item/agentMessage/delta",
				"params": map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-1",
					"itemId":   "item-1",
					"delta":    "world",
				},
			})
			writeFake(map[string]any{
				"method": "turn/completed",
				"params": map[string]any{
					"threadId": "thread-1",
					"turn": map[string]any{
						"id":     "turn-1",
						"status": "completed",
						"items":  []any{},
					},
				},
			})
		}
	}
}

func writeFake(msg any) {
	data, _ := json.Marshal(msg)
	fmt.Println(string(data))
}
