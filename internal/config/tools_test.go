package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- helpers ----------

// writeYAML creates a YAML file at the given path with the given content.
// Parent directories are created automatically.
func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tempDir returns a fresh temporary directory cleaned up after the test.
func tempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	// Resolve symlinks so comparisons with filepath.Join are stable on macOS (/var → /private/var).
	resolved, err := filepath.EvalSymlinks(d)
	if err != nil {
		return d
	}
	return resolved
}

// ---------- loadYAMLConfig ----------

func TestLoadYAMLConfig_NotFound(t *testing.T) {
	_, err := loadYAMLConfig("/does/not/exist/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadYAMLConfig_ValidFile(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "config.yaml")
	writeYAML(t, path, `
lsp:
  rust:
    enabled: true
    command: rust-analyzer
    args: []
    languages: ["rust"]
    timeout: 10s
    max_restarts: 5
    auto_start: false
`)
	cfg, err := loadYAMLConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	rust, ok := cfg.LSP["rust"]
	if !ok {
		t.Fatal("expected 'rust' LSP entry")
	}
	if !rust.Enabled {
		t.Error("expected rust.Enabled == true")
	}
	if rust.Command != "rust-analyzer" {
		t.Errorf("expected command 'rust-analyzer', got %q", rust.Command)
	}
	if rust.MaxRestarts != 5 {
		t.Errorf("expected MaxRestarts 5, got %d", rust.MaxRestarts)
	}
}

func TestLoadYAMLConfig_MalformedYAML(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "bad.yaml")
	writeYAML(t, path, `lsp: [this is not valid:: yaml`)
	_, err := loadYAMLConfig(path)
	if err == nil {
		t.Fatal("expected parse error for malformed YAML")
	}
}

// ---------- MergeWith ----------

func TestMergeWith_Nil(t *testing.T) {
	cfg := DefaultToolsConfig()
	cfg.MergeWith(nil) // must not panic
}

func TestMergeWith_LSP(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		LSP: map[string]LSPConfig{
			"python": {
				Enabled: true,
				Command: "pylsp",
				Args:    []string{},
			},
			// Override the default go entry.
			"go": {
				Enabled:     true,
				Command:     "gopls",
				Args:        []string{"serve", "-rpc.trace"},
				MaxRestarts: 10,
			},
		},
	}
	base.MergeWith(overlay)

	if _, ok := base.LSP["python"]; !ok {
		t.Error("expected python LSP entry after merge")
	}
	goLSP := base.LSP["go"]
	if goLSP.MaxRestarts != 10 {
		t.Errorf("expected go MaxRestarts 10, got %d", goLSP.MaxRestarts)
	}
	if len(goLSP.Args) != 2 || goLSP.Args[1] != "-rpc.trace" {
		t.Errorf("expected merged go args, got %v", goLSP.Args)
	}
}

func TestMergeWith_Search(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		Search: SearchConfig{
			Ripgrep: RipgrepConfig{
				Enabled:     true,
				Command:     "rg",
				DefaultArgs: []string{"--json"},
				Fallback:    "grep",
			},
			Fd: ToolConfig{
				Enabled: true,
				Command: "fd",
			},
		},
	}
	base.MergeWith(overlay)

	if base.Search.Ripgrep.Fallback != "grep" {
		t.Errorf("expected Ripgrep.Fallback 'grep', got %q", base.Search.Ripgrep.Fallback)
	}
	if !base.Search.Fd.Enabled {
		t.Error("expected Fd.Enabled after merge")
	}
}

func TestMergeWith_RAG(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		RAG: RAGConfig{
			Enabled: true,
			Indexing: RAGIndexingConfig{
				ChunkSize:    100,
				ChunkOverlap: 20,
				MaxFileSize:  2097152,
			},
			Search: RAGSearchConfig{
				DefaultTopK: 20,
				MaxTopK:     200,
				MinScore:    0.2,
				K1:          1.2,
				B:           0.8,
			},
		},
	}
	base.MergeWith(overlay)

	if base.RAG.Indexing.ChunkSize != 100 {
		t.Errorf("expected ChunkSize 100, got %d", base.RAG.Indexing.ChunkSize)
	}
	if base.RAG.Search.DefaultTopK != 20 {
		t.Errorf("expected DefaultTopK 20, got %d", base.RAG.Search.DefaultTopK)
	}
}

func TestMergeWith_Hooks(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		Hooks: HooksConfig{
			PostCode: []HookEntry{
				{Name: "lint", Command: "golangci-lint run", Blocking: true, Timeout: "60s"},
			},
		},
	}
	base.MergeWith(overlay)

	if len(base.Hooks.PostCode) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(base.Hooks.PostCode))
	}
	if base.Hooks.PostCode[0].Name != "lint" {
		t.Errorf("expected hook name 'lint', got %q", base.Hooks.PostCode[0].Name)
	}
}

func TestMergeWith_PackageManagers(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		PackageManagers: map[string]PackageManager{
			"cargo": {Enabled: true, Command: "cargo"},
		},
	}
	base.MergeWith(overlay)

	if _, ok := base.PackageManagers["cargo"]; !ok {
		t.Error("expected cargo entry after merge")
	}
}

func TestMergeWith_Interpreters(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		Interpreters: map[string]Interpreter{
			"node": {Enabled: true, Command: "node"},
		},
	}
	base.MergeWith(overlay)

	if _, ok := base.Interpreters["node"]; !ok {
		t.Error("expected node interpreter after merge")
	}
}

func TestMergeWith_Formatters(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		Formatters: map[string]Formatter{
			"black": {Enabled: true, Command: "black", Languages: []string{"python"}},
		},
	}
	base.MergeWith(overlay)

	if _, ok := base.Formatters["black"]; !ok {
		t.Error("expected black formatter after merge")
	}
}

func TestMergeWith_Linters(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		Linters: map[string]Linter{
			"eslint": {Enabled: true, Command: "eslint", Languages: []string{"javascript"}},
		},
	}
	base.MergeWith(overlay)

	if _, ok := base.Linters["eslint"]; !ok {
		t.Error("expected eslint linter after merge")
	}
}

func TestMergeWith_Platforms(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		Platforms: map[string]PlatformConfig{
			"windows": {Suffix: ".exe", WhichCommand: "where"},
		},
	}
	base.MergeWith(overlay)

	if _, ok := base.Platforms["windows"]; !ok {
		t.Error("expected windows platform after merge")
	}
}

func TestMergeWith_Discovery(t *testing.T) {
	base := DefaultToolsConfig()
	overlay := &ToolsConfig{
		Discovery: DiscoveryConfig{
			CheckTimeout: "10s",
			AutoCheck:    false,
			CacheTTL:     7200,
			OnMissing:    "error",
		},
	}
	base.MergeWith(overlay)

	if base.Discovery.CheckTimeout != "10s" {
		t.Errorf("expected CheckTimeout '10s', got %q", base.Discovery.CheckTimeout)
	}
	if base.Discovery.OnMissing != "error" {
		t.Errorf("expected OnMissing 'error', got %q", base.Discovery.OnMissing)
	}
}

// ---------- LoadConfig ----------

func TestLoadConfig_DefaultsWhenNoFiles(t *testing.T) {
	dir := tempDir(t)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should match DefaultToolsConfig.
	def := DefaultToolsConfig()
	if cfg.RAG.Indexing.ChunkSize != def.RAG.Indexing.ChunkSize {
		t.Errorf("expected default ChunkSize %d, got %d", def.RAG.Indexing.ChunkSize, cfg.RAG.Indexing.ChunkSize)
	}
	if len(cfg.LSP) != len(def.LSP) {
		t.Errorf("expected %d LSP entries, got %d", len(def.LSP), len(cfg.LSP))
	}
}

func TestLoadConfig_LegacyFallback(t *testing.T) {
	dir := tempDir(t)
	writeYAML(t, filepath.Join(dir, "tools.yaml"), `
hooks:
  post_code:
    - name: legacy_hook
      command: "echo legacy"
      blocking: false
      timeout: 5s
`)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Hooks.PostCode) != 1 || cfg.Hooks.PostCode[0].Name != "legacy_hook" {
		t.Errorf("expected legacy hook, got %+v", cfg.Hooks.PostCode)
	}
}

func TestLoadConfig_LegacyIgnoredWhenNewConfigExists(t *testing.T) {
	dir := tempDir(t)
	// Write legacy tools.yaml with a hook.
	writeYAML(t, filepath.Join(dir, "tools.yaml"), `
hooks:
  post_code:
    - name: legacy_hook
      command: "echo legacy"
      blocking: false
      timeout: 5s
`)
	// Write new workspace config without any hooks.
	writeYAML(t, filepath.Join(dir, ".coden", "config.yaml"), `
search:
  ripgrep:
    enabled: true
    command: rg
    default_args: ["--json"]
    fallback: builtin
`)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The legacy hook should NOT be loaded because .coden/config.yaml exists.
	if len(cfg.Hooks.PostCode) != 0 {
		t.Errorf("expected no hooks (legacy should be ignored), got %d", len(cfg.Hooks.PostCode))
	}
	// But the workspace search config should be applied.
	if cfg.Search.Ripgrep.Fallback != "builtin" {
		t.Errorf("expected fallback 'builtin', got %q", cfg.Search.Ripgrep.Fallback)
	}
}

func TestLoadConfig_WorkspaceOverridesDefaults(t *testing.T) {
	dir := tempDir(t)
	writeYAML(t, filepath.Join(dir, ".coden", "config.yaml"), `
rag:
  enabled: true
  indexing:
    chunk_size: 200
    chunk_overlap: 40
    max_file_size: 4194304
  search:
    k1: 2.0
    b: 0.5
    default_top_k: 25
    max_top_k: 250
    min_score: 0.05
`)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RAG.Indexing.ChunkSize != 200 {
		t.Errorf("expected ChunkSize 200, got %d", cfg.RAG.Indexing.ChunkSize)
	}
	if cfg.RAG.Search.K1 != 2.0 {
		t.Errorf("expected K1 2.0, got %f", cfg.RAG.Search.K1)
	}
	// LSP should still have the defaults since workspace config doesn't override it.
	goLSP, ok := cfg.LSP["go"]
	if !ok || goLSP.Command != "gopls" {
		t.Error("expected default go LSP to be preserved")
	}
}

func TestLoadConfig_ValidationError(t *testing.T) {
	dir := tempDir(t)
	writeYAML(t, filepath.Join(dir, ".coden", "config.yaml"), `
rag:
  enabled: true
  indexing:
    chunk_size: 10
    chunk_overlap: 20
    max_file_size: 100
  search:
    k1: 1.5
    b: 0.75
    default_top_k: 5
    max_top_k: 50
    min_score: 0.1
`)
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected validation error (chunk_overlap >= chunk_size)")
	}
	if !strings.Contains(err.Error(), "chunk_overlap") {
		t.Errorf("expected error about chunk_overlap, got: %v", err)
	}
}

func TestLoadConfig_EmptyWorkspaceRoot(t *testing.T) {
	// Empty workspace root should still return defaults without error.
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoadConfig_TwoLevelMerge(t *testing.T) {
	// Simulate user home and workspace with separate config files.
	// We can't easily override os.UserHomeDir(), so we test the merge
	// logic by exercising loadYAMLConfig + MergeWith directly, which
	// is the same code path LoadConfig follows internally.

	dir := tempDir(t)
	userDir := filepath.Join(dir, "home", ".coden")
	wsDir := filepath.Join(dir, "project", ".coden")

	// User config: sets chunk_size and a hook.
	writeYAML(t, filepath.Join(userDir, "config.yaml"), `
rag:
  enabled: true
  indexing:
    chunk_size: 80
    chunk_overlap: 15
    max_file_size: 2097152
  search:
    k1: 1.5
    b: 0.75
    default_top_k: 15
    max_top_k: 150
    min_score: 0.1
hooks:
  post_code:
    - name: user_hook
      command: "echo user"
      blocking: false
      timeout: 5s
`)

	// Workspace config: overrides chunk_size, adds LSP entry.
	writeYAML(t, filepath.Join(wsDir, "config.yaml"), `
rag:
  enabled: true
  indexing:
    chunk_size: 120
    chunk_overlap: 25
    max_file_size: 3145728
  search:
    k1: 1.8
    b: 0.6
    default_top_k: 20
    max_top_k: 200
    min_score: 0.05
lsp:
  rust:
    enabled: true
    command: rust-analyzer
    args: []
    languages: ["rust"]
    timeout: 30s
    max_restarts: 3
    auto_start: false
`)

	// Manually reproduce the merge logic.
	cfg := DefaultToolsConfig()

	userCfg, err := loadYAMLConfig(filepath.Join(userDir, "config.yaml"))
	if err != nil {
		t.Fatalf("load user config: %v", err)
	}
	cfg.MergeWith(userCfg)

	// After user merge: chunk_size=80, hook present.
	if cfg.RAG.Indexing.ChunkSize != 80 {
		t.Errorf("after user merge: expected ChunkSize 80, got %d", cfg.RAG.Indexing.ChunkSize)
	}
	if len(cfg.Hooks.PostCode) != 1 {
		t.Fatalf("after user merge: expected 1 hook, got %d", len(cfg.Hooks.PostCode))
	}

	wsCfg, err := loadYAMLConfig(filepath.Join(wsDir, "config.yaml"))
	if err != nil {
		t.Fatalf("load workspace config: %v", err)
	}
	cfg.MergeWith(wsCfg)

	// After workspace merge: chunk_size=120, hook unchanged (workspace didn't set hooks).
	if cfg.RAG.Indexing.ChunkSize != 120 {
		t.Errorf("after ws merge: expected ChunkSize 120, got %d", cfg.RAG.Indexing.ChunkSize)
	}
	if len(cfg.Hooks.PostCode) != 1 || cfg.Hooks.PostCode[0].Name != "user_hook" {
		t.Errorf("after ws merge: expected user_hook preserved, got %+v", cfg.Hooks.PostCode)
	}

	// Workspace added rust LSP — default go should still be present.
	if _, ok := cfg.LSP["go"]; !ok {
		t.Error("expected go LSP preserved after workspace merge")
	}
	if _, ok := cfg.LSP["rust"]; !ok {
		t.Error("expected rust LSP added by workspace merge")
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("merged config failed validation: %v", err)
	}
}

// ---------- ConfigSearchPaths ----------

func TestConfigSearchPaths_WithWorkspace(t *testing.T) {
	paths := ConfigSearchPaths("/my/project")
	if len(paths) < 2 {
		t.Fatalf("expected at least 2 paths, got %d", len(paths))
	}
	// Should contain the workspace .coden/config.yaml.
	found := false
	for _, p := range paths {
		if strings.Contains(p, filepath.Join("my", "project", ".coden", "config.yaml")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected workspace config path in search paths, got %v", paths)
	}
	// Last entry should be the legacy fallback.
	last := paths[len(paths)-1]
	if !strings.Contains(last, "legacy") {
		t.Errorf("expected last path to be legacy fallback, got %q", last)
	}
}

func TestConfigSearchPaths_EmptyWorkspace(t *testing.T) {
	paths := ConfigSearchPaths("")
	// Should still include user-level path at minimum (if home is resolvable).
	for _, p := range paths {
		if strings.Contains(p, "tools.yaml") {
			t.Errorf("empty workspace should not include legacy path, got %q", p)
		}
	}
}

// ---------- ShowConfig ----------

func TestShowConfig_ContainsHeader(t *testing.T) {
	dir := tempDir(t)
	out, err := ShowConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Effective configuration") {
		t.Error("expected 'Effective configuration' header in output")
	}
	if !strings.Contains(out, "Search paths") {
		t.Error("expected 'Search paths' in output")
	}
	// Should contain some YAML content.
	if !strings.Contains(out, "lsp:") {
		t.Error("expected 'lsp:' in YAML output")
	}
}

func TestShowConfig_WithWorkspaceConfig(t *testing.T) {
	dir := tempDir(t)
	writeYAML(t, filepath.Join(dir, ".coden", "config.yaml"), `
search:
  ripgrep:
    enabled: true
    command: rg
    default_args: ["--json", "--max-count=100"]
    fallback: builtin
`)
	out, err := ShowConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "max-count=100") {
		t.Error("expected workspace search override in output")
	}
}

// ---------- mergeRAG ----------

func TestMergeRAG_EmptySrcIsNoOp(t *testing.T) {
	dst := DefaultToolsConfig().RAG
	src := RAGConfig{} // all zero values
	mergeRAG(&dst, &src)

	def := DefaultToolsConfig().RAG
	if dst.Indexing.ChunkSize != def.Indexing.ChunkSize {
		t.Errorf("expected ChunkSize unchanged, got %d", dst.Indexing.ChunkSize)
	}
	if dst.Search.DefaultTopK != def.Search.DefaultTopK {
		t.Errorf("expected DefaultTopK unchanged, got %d", dst.Search.DefaultTopK)
	}
}

func TestMergeRAG_DisableRAG(t *testing.T) {
	dst := DefaultToolsConfig().RAG
	// To disable RAG, src.Enabled=false but a sentinel field must be set
	// so the merge detects the section as present.
	src := RAGConfig{
		Enabled: false,
		Indexing: RAGIndexingConfig{
			ChunkSize: 1, // sentinel to trigger merge
		},
	}
	mergeRAG(&dst, &src)

	if dst.Enabled {
		t.Error("expected RAG to be disabled after merge")
	}
}

// ---------- Deprecated LoadToolsConfig ----------

func TestLoadToolsConfig_StillWorks(t *testing.T) {
	dir := tempDir(t)
	writeYAML(t, filepath.Join(dir, "tools.yaml"), `
lsp:
  go:
    enabled: true
    command: gopls
    args: ["serve"]
    languages: ["go"]
    timeout: 30s
    max_restarts: 3
    auto_start: true
search:
  ripgrep:
    enabled: true
    command: rg
    default_args: ["--json"]
    fallback: builtin
`)
	cfg, err := LoadToolsConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LSP["go"].Command != "gopls" {
		t.Errorf("expected gopls, got %q", cfg.LSP["go"].Command)
	}
}

func TestLoadToolsConfig_MissingFileReturnsDefaults(t *testing.T) {
	dir := tempDir(t)
	cfg, err := LoadToolsConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil default config")
	}
	if cfg.LSP["go"].Command != "gopls" {
		t.Errorf("expected default gopls, got %q", cfg.LSP["go"].Command)
	}
}

// ---------- Edge cases ----------

func TestMergeWith_NilMaps(t *testing.T) {
	// Start with a config that has nil maps (as from an empty YAML file).
	cfg := &ToolsConfig{}
	overlay := &ToolsConfig{
		LSP: map[string]LSPConfig{
			"go": {Enabled: true, Command: "gopls"},
		},
		Formatters: map[string]Formatter{
			"gofmt": {Enabled: true, Command: "gofmt"},
		},
	}
	cfg.MergeWith(overlay)

	if _, ok := cfg.LSP["go"]; !ok {
		t.Error("expected go LSP after merging into nil map")
	}
	if _, ok := cfg.Formatters["gofmt"]; !ok {
		t.Error("expected gofmt formatter after merging into nil map")
	}
}

func TestLoadConfig_InvalidYAMLInWorkspace(t *testing.T) {
	dir := tempDir(t)
	writeYAML(t, filepath.Join(dir, ".coden", "config.yaml"), `
lsp: [invalid:: yaml content
`)
	_, err := LoadConfig(dir)
	// Should still succeed using defaults since the parse error means the
	// workspace config is skipped. Actually, loadYAMLConfig returns an error
	// on bad YAML, and LoadConfig ignores files that error — so this should
	// fall through to defaults.
	// Wait, let me re-check the LoadConfig logic… it only applies configs
	// where loadYAMLConfig succeeds, so malformed YAML is silently skipped.
	if err != nil {
		t.Fatalf("expected LoadConfig to skip malformed workspace config, got error: %v", err)
	}
}

func TestLoadYAMLConfig_EmptyFile(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "empty.yaml")
	writeYAML(t, path, "")
	cfg, err := loadYAMLConfig(path)
	if err != nil {
		t.Fatalf("unexpected error for empty file: %v", err)
	}
	// An empty YAML file yields a zero-value ToolsConfig.
	if cfg == nil {
		t.Fatal("expected non-nil config for empty file")
	}
}

func TestLoadYAMLConfig_OnlyComments(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "comments.yaml")
	writeYAML(t, path, "# This file has only comments\n# Nothing else\n")
	cfg, err := loadYAMLConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}
