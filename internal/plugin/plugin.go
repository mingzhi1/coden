// Package plugin implements the Plugin system for CodeN.
//
// A Plugin is a packaging unit (Git repository + plugin.json manifest) that
// can provide Skills, MCP Servers, and lifecycle Hooks.
//
// Architecture:
//
//	Plugin ──→ Skills    (provides SKILL.md files)
//	Plugin ──→ MCP      (auto-starts MCP Servers)
//	Plugin ──→ Hooks    (PreToolUse / PostToolUse lifecycle callbacks)
//
// Trust level: L4 (plugin) — can only inject Skills into Coder,
// and cannot register mutation MCP tools or run_shell recommendations.
//
// This is the Phase 3 skeleton. Full implementation follows the
// Secretary Agent architecture in docs/design/secretary_agent_permission_model.md.
package plugin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// ---------------------------------------------------------------------------
// Manifest — plugin.json schema
// ---------------------------------------------------------------------------

// Manifest represents the plugin.json file at the root of a plugin directory.
type Manifest struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Version     string                     `json:"version"`
	Skills      []string                   `json:"skills,omitempty"`     // relative paths to skill directories
	Agents      []string                   `json:"agents,omitempty"`     // future
	Hooks       map[string][]HookConfig    `json:"hooks,omitempty"`      // event → commands
	MCPServers  map[string]MCPServerConfig `json:"mcpServers,omitempty"` // name → server config
}

// HookConfig defines a lifecycle hook command.
type HookConfig struct {
	Command string `json:"command"`
	Timeout string `json:"timeout,omitempty"` // e.g. "30s"; default per source
}

// MCPServerConfig mirrors mcp.ServerConfig for plugin-embedded MCP servers.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ---------------------------------------------------------------------------
// Plugin — a loaded plugin instance
// ---------------------------------------------------------------------------

// Scope indicates where a plugin is configured.
type Scope string

const (
	ScopeUser    Scope = "user"    // ~/.coden/plugins/
	ScopeProject Scope = "project" // <workspace>/.coden/plugins/
	ScopeManaged Scope = "managed" // enterprise policy (future)
)

// Plugin represents a loaded and validated plugin.
type Plugin struct {
	Manifest Manifest
	Path     string // absolute path to the plugin directory
	Scope    Scope
	Enabled  bool
}

// SkillDirs returns absolute paths to all skill directories declared by this plugin.
func (p *Plugin) SkillDirs() []string {
	var dirs []string
	for _, rel := range p.Manifest.Skills {
		abs := filepath.Join(p.Path, rel)
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			dirs = append(dirs, abs)
		}
	}
	return dirs
}

// ---------------------------------------------------------------------------
// Registry — manages installed plugins
// ---------------------------------------------------------------------------

// Registry manages loaded plugins with thread-safe access.
type Registry struct {
	mu      sync.RWMutex
	plugins []*Plugin
}

// NewRegistry creates an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// LoadFromDirs scans one or more plugin directories.
// Each subdirectory that contains a plugin.json is loaded as a plugin.
// Errors are logged but do not prevent other plugins from loading.
func (r *Registry) LoadFromDirs(scope Scope, dirs ...string) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Directory doesn't exist or isn't readable — not an error.
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pluginDir := filepath.Join(dir, entry.Name())
			p, loadErr := LoadPlugin(pluginDir, scope)
			if loadErr != nil {
				slog.Warn("[plugin] failed to load plugin",
					"dir", pluginDir,
					"error", loadErr,
				)
				continue
			}
			r.mu.Lock()
			r.plugins = append(r.plugins, p)
			r.mu.Unlock()
			slog.Info("[plugin] loaded",
				"name", p.Manifest.Name,
				"version", p.Manifest.Version,
				"scope", string(scope),
				"skills", len(p.Manifest.Skills),
				"mcp_servers", len(p.Manifest.MCPServers),
				"hooks", len(p.Manifest.Hooks),
			)
		}
	}
}

// All returns all loaded plugins (enabled and disabled).
func (r *Registry) All() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Plugin, len(r.plugins))
	copy(out, r.plugins)
	return out
}

// Enabled returns only enabled plugins.
func (r *Registry) Enabled() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Plugin
	for _, p := range r.plugins {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// SkillDirs returns all skill directories from enabled plugins.
func (r *Registry) SkillDirs() []string {
	var dirs []string
	for _, p := range r.Enabled() {
		dirs = append(dirs, p.SkillDirs()...)
	}
	return dirs
}

// MCPConfigs returns all MCP server configs from enabled plugins.
func (r *Registry) MCPConfigs() map[string]MCPServerConfig {
	configs := make(map[string]MCPServerConfig)
	for _, p := range r.Enabled() {
		for name, cfg := range p.Manifest.MCPServers {
			// Prefix with plugin name to avoid collisions.
			key := p.Manifest.Name + "." + name
			configs[key] = cfg
		}
	}
	return configs
}

// Hooks returns all hook configs for a given lifecycle event from enabled plugins.
func (r *Registry) Hooks(event string) []HookEntry {
	var entries []HookEntry
	for _, p := range r.Enabled() {
		for _, h := range p.Manifest.Hooks[event] {
			entries = append(entries, HookEntry{
				PluginName: p.Manifest.Name,
				Scope:      p.Scope,
				Config:     h,
			})
		}
	}
	return entries
}

// HookEntry pairs a hook config with its plugin metadata.
type HookEntry struct {
	PluginName string
	Scope      Scope
	Config     HookConfig
}

// Count returns the total number of loaded plugins.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.plugins)
}

// ---------------------------------------------------------------------------
// Loader — parse plugin.json
// ---------------------------------------------------------------------------

// LoadPlugin loads a single plugin from a directory containing plugin.json.
func LoadPlugin(dir string, scope Scope) (*Plugin, error) {
	manifestPath := filepath.Join(dir, "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read plugin.json: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse plugin.json in %s: %w", dir, err)
	}

	if manifest.Name == "" {
		manifest.Name = filepath.Base(dir)
	}

	return &Plugin{
		Manifest: manifest,
		Path:     dir,
		Scope:    scope,
		Enabled:  true, // enabled by default; settings.json can override
	}, nil
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

// UserPluginDir returns the user-level plugin directory (~/.coden/plugins/).
func UserPluginDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".coden", "plugins")
}

// ProjectPluginDir returns the project-level plugin directory.
func ProjectPluginDir(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".coden", "plugins")
}
