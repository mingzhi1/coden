package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadConfig loads and merges MCP server configurations.
// Returns the merged config and a source map (serverName → "user"/"project").
func LoadConfig(workspaceRoot string) (MCPConfig, map[string]string) {
	merged := MCPConfig{MCPServers: make(map[string]ServerConfig)}
	sources := make(map[string]string)

	// Load project-level .mcp.json (lower priority)
	projectPath := filepath.Join(workspaceRoot, ".mcp.json")
	if cfg, err := loadConfigFile(projectPath); err == nil {
		for name, srv := range cfg.MCPServers {
			merged.MCPServers[name] = srv
			sources[name] = "project"
		}
	}

	// Load user-level ~/.coden/mcp.json (higher priority, overwrites)
	home, _ := os.UserHomeDir()
	if home != "" {
		userPath := filepath.Join(home, ".coden", "mcp.json")
		if cfg, err := loadConfigFile(userPath); err == nil {
			for name, srv := range cfg.MCPServers {
				merged.MCPServers[name] = srv
				sources[name] = "user"
			}
		}
	}

	return merged, sources
}

func loadConfigFile(path string) (MCPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MCPConfig{}, err
	}
	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return MCPConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]ServerConfig)
	}
	return cfg, nil
}

// ExpandEnv expands ${VAR} in env map values using the process environment.
func ExpandEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return env
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = os.Expand(v, func(key string) string {
			if val, ok := os.LookupEnv(key); ok {
				return val
			}
			return "${" + key + "}"
		})
	}
	return out
}
