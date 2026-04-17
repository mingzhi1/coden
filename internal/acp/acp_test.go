package acp

import (
	"encoding/json"
	"testing"
)

func TestTypesRoundTrip(t *testing.T) {
	// Verify Request serialization
	req := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: InitParams{
			ClientInfo: ClientInfo{Name: "test", Version: "0.1.0"},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if decoded.Method != "initialize" {
		t.Errorf("expected method=initialize, got %s", decoded.Method)
	}
	if decoded.ID != 1 {
		t.Errorf("expected id=1, got %d", decoded.ID)
	}
}

func TestResponseWithError(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":5,"error":{"code":-32600,"message":"bad request"}}`
	var resp Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("expected code=-32600, got %d", resp.Error.Code)
	}
}

func TestNotificationParsing(t *testing.T) {
	// Simulate a sessionUpdate notification
	raw := `{"jsonrpc":"2.0","method":"notifications/sessionUpdate","params":{"sessionId":"s1","update":{"type":"agent_message_chunk","text":"hello"}}}`
	var msg Response
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Method != "notifications/sessionUpdate" {
		t.Errorf("expected sessionUpdate method, got %s", msg.Method)
	}
	if msg.ID != nil {
		t.Errorf("notification should have nil ID")
	}
}

func TestPromptMessageSerialization(t *testing.T) {
	msg := PromptMessage{
		Role: "user",
		Content: []ContentBlock{
			{Type: "text", Text: "hello world"},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PromptMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Role != "user" {
		t.Errorf("expected role=user, got %s", decoded.Role)
	}
	if len(decoded.Content) != 1 || decoded.Content[0].Text != "hello world" {
		t.Errorf("unexpected content: %+v", decoded.Content)
	}
}
