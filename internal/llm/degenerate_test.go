package llm

import "testing"

func TestIsDegenerateReply(t *testing.T) {
	cases := []struct {
		name       string
		reply      string
		degenerate bool
	}{
		{"empty tool_calls JSON", `{"tool_calls": []}`, false},
		{"fenced short JSON", "```json\n{\n  \"tool_calls\": []\n}\n```", false},
		{"short non-JSON garbage", "I'll help.", true},
		{"empty", "", true},
		{"whitespace", "   \n  ", true},
		{"long prose", "A goroutine is a lightweight thread managed by the Go runtime, multiplexed onto OS threads.", false},
		{"long JSON", `{"goal":"do the thing","kind":"chat","success_criteria":["ok"]}`, false},
		{"short malformed json-ish", `{tool_calls`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDegenerateReply(tc.reply); got != tc.degenerate {
				t.Fatalf("IsDegenerateReply(%q) = %v, want %v", tc.reply, got, tc.degenerate)
			}
		})
	}
}

func TestReplyDeclaresDone(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		done  bool
	}{
		{"plain empty array", `{"tool_calls": []}`, true},
		{"fenced empty array", "```json\n{\n  \"tool_calls\": []\n}\n```", true},
		{"prose then empty array", "I'm done, no tools needed.\n```json\n{\"tool_calls\": []}\n```", true},
		{"has tool calls", `{"tool_calls": [{"kind":"write_file"}]}`, false},
		{"no tool_calls key", `{"goal": "x"}`, false},
		{"plain prose", "Hello! How can I help?", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := replyDeclaresDone(tc.reply); got != tc.done {
				t.Fatalf("replyDeclaresDone(%q) = %v, want %v", tc.reply, got, tc.done)
			}
		})
	}
}
