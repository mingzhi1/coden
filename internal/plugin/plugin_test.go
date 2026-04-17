package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), data, 0o644); err != nil {
		t.Fatalf("write plugin.json: %v", err)
	}
}

func TestLoadPlugin_Valid(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, Manifest{
		Name:        "test-plugin",
		Description: "A test plugin",
		Version:     "1.0.0",
	})
	p, err := LoadPlugin(dir, ScopeProject)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if p.Manifest.Name != "test-plugin" {
		t.Errorf("name: got %q", p.Manifest.Name)
	}
	if p.Scope != ScopeProject {
		t.Errorf("scope: got %q", p.Scope)
	}
	if !p.Enabled {
		t.Error("expected enabled by default")
	}
}

func TestLoadPlugin_NoManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadPlugin(dir, ScopeUser)
	if err == nil {
		t.Error("expected error for missing plugin.json")
	}
}

func TestLoadPlugin_EmptyName_UsesDir(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, Manifest{Name: "", Version: "1.0"})
	p, err := LoadPlugin(dir, ScopeUser)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if p.Manifest.Name == "" {
		t.Error("expected name to be derived from dir")
	}
}

func TestRegistry_LoadFromDirs(t *testing.T) {
	root := t.TempDir()
	// Create two plugins in subdirectories.
	for _, name := range []string{"plugin-a", "plugin-b"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeManifest(t, dir, Manifest{Name: name, Version: "0.1"})
	}
	// Create a non-plugin dir (no plugin.json) — should be ignored.
	if err := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	reg.LoadFromDirs(ScopeProject, root)

	if reg.Count() != 2 {
		t.Errorf("expected 2 plugins, got %d", reg.Count())
	}
}

func TestRegistry_SkillDirs(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "myplugin")
	skillDir := filepath.Join(pluginDir, "skills")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, pluginDir, Manifest{
		Name:   "myplugin",
		Skills: []string{"skills"},
	})

	reg := NewRegistry()
	reg.LoadFromDirs(ScopeProject, root)

	dirs := reg.SkillDirs()
	if len(dirs) != 1 {
		t.Errorf("expected 1 skill dir, got %d", len(dirs))
	}
	if dirs[0] != skillDir {
		t.Errorf("skill dir: got %q want %q", dirs[0], skillDir)
	}
}

func TestRegistry_MCPConfigs(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "mcp-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, pluginDir, Manifest{
		Name: "mcp-plugin",
		MCPServers: map[string]MCPServerConfig{
			"myserver": {Command: "npx", Args: []string{"my-mcp-server"}},
		},
	})

	reg := NewRegistry()
	reg.LoadFromDirs(ScopeProject, root)

	configs := reg.MCPConfigs()
	if len(configs) != 1 {
		t.Errorf("expected 1 MCP config, got %d", len(configs))
	}
	key := "mcp-plugin.myserver"
	if _, ok := configs[key]; !ok {
		t.Errorf("expected key %q in MCP configs", key)
	}
}

func TestRegistry_Hooks(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "hook-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, pluginDir, Manifest{
		Name: "hook-plugin",
		Hooks: map[string][]HookConfig{
			"post_code": {{Command: "echo done", Timeout: "5s"}},
		},
	})

	reg := NewRegistry()
	reg.LoadFromDirs(ScopeProject, root)

	hooks := reg.Hooks("post_code")
	if len(hooks) != 1 {
		t.Errorf("expected 1 hook, got %d", len(hooks))
	}
	if hooks[0].Config.Command != "echo done" {
		t.Errorf("hook command: got %q", hooks[0].Config.Command)
	}
}

func TestPlugin_SkillDirs_NonexistentDir(t *testing.T) {
	p := &Plugin{
		Manifest: Manifest{Skills: []string{"does-not-exist"}},
		Path:     t.TempDir(),
		Enabled:  true,
	}
	dirs := p.SkillDirs()
	if len(dirs) != 0 {
		t.Errorf("expected 0 skill dirs for nonexistent path, got %d", len(dirs))
	}
}
