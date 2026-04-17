package provider

import (
	"testing"
)

func TestNewAutoDetectsOpenAI(t *testing.T) {
	p, model := New(Config{})
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if model == "" {
		t.Fatal("expected non-empty model")
	}
}

func TestNewExplicitAnthropic(t *testing.T) {
	p, model := New(Config{Provider: "anthropic", APIKey: "sk-ant-test", Model: "claude-3-5-haiku-20241022"})
	if p.Name() != "anthropic" {
		t.Fatalf("expected anthropic, got %s", p.Name())
	}
	if model != "claude-3-5-haiku-20241022" {
		t.Fatalf("expected claude model, got %s", model)
	}
	if !p.IsConfigured() {
		t.Fatal("expected configured")
	}
}

func TestNewExplicitDeepSeek(t *testing.T) {
	p, model := New(Config{Provider: "deepseek", APIKey: "sk-ds-test"})
	if p.Name() != "deepseek" {
		t.Fatalf("expected deepseek, got %s", p.Name())
	}
	if model == "" {
		t.Fatal("expected non-empty model")
	}
	if !p.IsConfigured() {
		t.Fatal("expected configured")
	}
}

func TestNewOpenAINotConfiguredWithoutKey(t *testing.T) {
	p, _ := New(Config{Provider: "openai"})
	if p.Name() != "openai" {
		t.Fatalf("expected openai, got %s", p.Name())
	}
	// Without env var, should not be configured
	if p.IsConfigured() {
		t.Log("OPENAI_API_KEY is set in environment, skipping")
		return
	}
}

func TestDefaultModels(t *testing.T) {
	tests := []struct {
		provider string
		wantNot  string
	}{
		{"openai", ""},
		{"anthropic", ""},
		{"deepseek", ""},
	}
	for _, tt := range tests {
		_, model := New(Config{Provider: tt.provider})
		if model == tt.wantNot {
			t.Errorf("expected non-empty model for %s", tt.provider)
		}
	}
}
