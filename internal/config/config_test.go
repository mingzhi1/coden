package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigValidates(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
}

func TestConfigValidateRejectsInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"negative retries", func(c *Config) { c.Core.Workflow.MaxRetries = -1 }},
		{"bad failure policy", func(c *Config) { c.Core.Workflow.FailurePolicy = "nope" }},
		{"bad log level", func(c *Config) { c.Logging.Level = "trace" }},
		{"bad token ratio", func(c *Config) { c.LLM.TokenBudget.FileTreeRatio = 2.0 }},
		{"negative object size", func(c *Config) { c.Storage.ObjectStore.MaxObjectSize = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMergeConfigs(t *testing.T) {
	dst := DefaultConfig()
	src := &Config{
		Core: CoreConfig{
			Workflow: WorkflowConfig{MaxRetries: 10},
		},
		LLM: LLMConfig{
			Pool: PoolConfig{Primary: []string{"openai", "deepseek"}},
		},
		Logging: LoggingConfig{Level: "debug"},
	}
	mergeConfigs(dst, src)

	if dst.Core.Workflow.MaxRetries != 10 {
		t.Fatalf("expected MaxRetries=10, got %d", dst.Core.Workflow.MaxRetries)
	}
	if len(dst.LLM.Pool.Primary) != 2 || dst.LLM.Pool.Primary[0] != "openai" {
		t.Fatalf("expected Pool.Primary=[openai deepseek], got %v", dst.LLM.Pool.Primary)
	}
	if dst.Logging.Level != "debug" {
		t.Fatalf("expected Level=debug, got %s", dst.Logging.Level)
	}
	// Untouched fields should keep defaults
	if dst.Core.Workflow.Timeout == 0 {
		t.Fatal("timeout should keep default")
	}
}

func TestLoaderLoadFromWorkspace(t *testing.T) {
	dir := t.TempDir()
	codenDir := filepath.Join(dir, ".coden")
	os.MkdirAll(codenDir, 0755)

	cfgYAML := `
core:
  workflow:
    max_retries: 7
logging:
  level: warn
`
	os.WriteFile(filepath.Join(codenDir, "config.yaml"), []byte(cfgYAML), 0644)

	loader := NewLoader(dir)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Core.Workflow.MaxRetries != 7 {
		t.Fatalf("expected 7, got %d", cfg.Core.Workflow.MaxRetries)
	}
	if cfg.Logging.Level != "warn" {
		t.Fatalf("expected warn, got %s", cfg.Logging.Level)
	}
}

func TestLoaderEnvOverride(t *testing.T) {
	t.Setenv("CODEN_ALLOW_SHELL", "true")
	t.Setenv("CODEN_LOG_LEVEL", "debug")
	t.Setenv("CODEN_ACP_COMMAND", "npx @agentclientprotocol/claude-agent-acp")

	loader := NewLoader("")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Core.Security.AllowShell {
		t.Fatal("expected AllowShell=true after env override")
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("expected debug, got %s", cfg.Logging.Level)
	}
	// ACP command should create a "claude-code" provider and prepend to primary.
	entry, ok := cfg.LLM.Providers["claude-code"]
	if !ok {
		t.Fatal("expected claude-code provider from CODEN_ACP_COMMAND")
	}
	if entry.Type != "acp" {
		t.Fatalf("expected type=acp, got %s", entry.Type)
	}
	if cfg.LLM.Pool.Primary[0] != "claude-code" {
		t.Fatalf("expected claude-code first in primary, got %v", cfg.LLM.Pool.Primary)
	}
}

func TestLoaderToolsOnlyBackcompat(t *testing.T) {
	loader := NewLoader("")
	tools, err := loader.LoadToolsOnly()
	if err != nil {
		t.Fatalf("LoadToolsOnly: %v", err)
	}
	if !tools.RAG.Enabled {
		t.Fatal("expected RAG to be enabled by default")
	}
}
