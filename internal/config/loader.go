package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader handles 4-layer configuration loading and merging.
type Loader struct {
	workspaceRoot string
	envPrefix     string
}

// NewLoader creates a config loader for the given workspace.
func NewLoader(workspaceRoot string) *Loader {
	return &Loader{
		workspaceRoot: workspaceRoot,
		envPrefix:     "CODEN_",
	}
}

// Load loads the full unified Config through all 4 layers:
//  1. Built-in defaults
//  2. User-level  ~/.coden/config.yaml
//  3. Workspace-level <workspace>/.coden/config.yaml
//  4. Environment variable overrides (CODEN_*)
func (l *Loader) Load() (*Config, error) {
	cfg := DefaultConfig()

	// Layer 1: user-level
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		userPath := filepath.Join(home, codenDirName, configFileName)
		if partial, e := loadFullYAML(userPath); e == nil {
			mergeConfigs(cfg, partial)
			slog.Info("[config] loaded user config", "path", userPath)
		}
	}

	// Layer 2: workspace-level
	if l.workspaceRoot != "" {
		wsPath := filepath.Join(l.workspaceRoot, codenDirName, configFileName)
		if partial, e := loadFullYAML(wsPath); e == nil {
			mergeConfigs(cfg, partial)
			slog.Info("[config] loaded workspace config", "path", wsPath)
		}
	}

	// Layer 3: environment variable overrides
	l.applyEnvOverrides(cfg)

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate unified config: %w", err)
	}
	return cfg, nil
}

// LoadToolsOnly loads the full config and returns just the Tools section.
// Backward compatible helper for callers that only need ToolsConfig.
func (l *Loader) LoadToolsOnly() (*ToolsConfig, error) {
	cfg, err := l.Load()
	if err != nil {
		return nil, err
	}
	return &cfg.Tools, nil
}

// SearchPaths returns the config file paths in priority order.
func (l *Loader) SearchPaths() []string {
	var paths []string
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		paths = append(paths, filepath.Join(home, codenDirName, configFileName))
	}
	if l.workspaceRoot != "" {
		paths = append(paths, filepath.Join(l.workspaceRoot, codenDirName, configFileName))
	}
	return paths
}

// loadFullYAML loads a YAML file into a partial Config struct.
func loadFullYAML(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// applyEnvOverrides checks for CODEN_* environment variables and applies them.
func (l *Loader) applyEnvOverrides(cfg *Config) {
	// Core overrides
	if v := os.Getenv(l.envPrefix + "ALLOW_SHELL"); v != "" {
		cfg.Core.Security.AllowShell = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv(l.envPrefix + "LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}

	// Provider API keys from env (convention: CODEN_OPENAI_API_KEY, etc.)
	for name, prov := range cfg.LLM.Providers {
		envKey := l.envPrefix + strings.ToUpper(name) + "_API_KEY"
		if v := os.Getenv(envKey); v != "" {
			prov.APIKey = v
			cfg.LLM.Providers[name] = prov
		}
	}

	// ACP provider from env: CODEN_ACP_COMMAND adds/overrides a "claude-code" ACP entry.
	if acpCmd := os.Getenv(l.envPrefix + "ACP_COMMAND"); acpCmd != "" {
		if cfg.LLM.Providers == nil {
			cfg.LLM.Providers = make(map[string]ProviderEntry)
		}
		acpName := os.Getenv(l.envPrefix + "ACP_NAME")
		if acpName == "" {
			acpName = "claude-code"
		}
		cfg.LLM.Providers[acpName] = ProviderEntry{
			Type:    "acp",
			Command: acpCmd,
		}
		// Prepend to primary pool if not already there.
		if !containsString(cfg.LLM.Pool.Primary, acpName) {
			cfg.LLM.Pool.Primary = append([]string{acpName}, cfg.LLM.Pool.Primary...)
		}
	}
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
