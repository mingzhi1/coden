package tokenbudget

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{strings.Repeat("x", 100), 25},
		{strings.Repeat("x", 101), 26},
	}
	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("EstimateTokens(%d chars) = %d, want %d", len(tt.input), got, tt.want)
		}
	}
}

func TestBudgetAvailable(t *testing.T) {
	b := Budget{MaxContextTokens: 128000, ReservedOutput: 4096, SystemPrompt: 1000}
	want := 128000 - 4096 - 1000
	if got := b.Available(); got != want {
		t.Errorf("Available() = %d, want %d", got, want)
	}
}

func TestBudgetForModel(t *testing.T) {
	tests := []struct {
		model   string
		wantMax int
	}{
		{"claude-3-5-sonnet-20241022", 200000},
		{"claude-3-5-haiku-20241022", 200000},
		{"gpt-4o-mini", 128000},
		{"gpt-4-turbo", 128000},
		{"gpt-4", 8192},
		{"gpt-3.5-turbo", 16384},
		{"deepseek-coder", 64000},
		{"unknown-model", 128000}, // default
	}
	for _, tt := range tests {
		b := BudgetForModel(tt.model)
		if b.MaxContextTokens != tt.wantMax {
			t.Errorf("BudgetForModel(%q).MaxContextTokens = %d, want %d", tt.model, b.MaxContextTokens, tt.wantMax)
		}
	}
}

func TestTruncateHistory(t *testing.T) {
	msgs := []string{
		strings.Repeat("a", 400), // ~100 tokens
		strings.Repeat("b", 400), // ~100 tokens
		strings.Repeat("c", 400), // ~100 tokens
	}

	// Budget fits all
	got, used := TruncateHistory(msgs, 500)
	if len(got) != 3 {
		t.Errorf("expected 3 messages, got %d (used %d)", len(got), used)
	}

	// Budget fits only 2 most recent
	got, used = TruncateHistory(msgs, 200)
	if len(got) != 2 {
		t.Errorf("expected 2 messages, got %d (used %d)", len(got), used)
	}
	if got[0] != msgs[1] || got[1] != msgs[2] {
		t.Error("expected most recent messages kept")
	}

	// Budget fits only 1
	got, used = TruncateHistory(msgs, 100)
	if len(got) != 1 {
		t.Errorf("expected 1 message, got %d", len(got))
	}

	// Zero budget
	got, _ = TruncateHistory(msgs, 0)
	if got != nil {
		t.Error("expected nil for zero budget")
	}

	// Empty messages
	got, _ = TruncateHistory(nil, 1000)
	if got != nil {
		t.Error("expected nil for empty messages")
	}
}

func TestTruncateFileTree(t *testing.T) {
	files := make([]string, 50)
	for i := range files {
		files[i] = "src/file_" + strings.Repeat("x", 20) + ".go"
	}

	// Large budget: all fit
	got, _ := TruncateFileTree(files, 10000)
	if len(got) != 50 {
		t.Errorf("expected 50 files, got %d", len(got))
	}

	// Small budget: only some fit
	got, _ = TruncateFileTree(files, 50)
	if len(got) == 0 || len(got) >= 50 {
		t.Errorf("expected partial result, got %d", len(got))
	}

	// Zero budget
	got, _ = TruncateFileTree(files, 0)
	if got != nil {
		t.Error("expected nil for zero budget")
	}
}
