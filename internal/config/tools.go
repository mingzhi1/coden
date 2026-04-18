package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ToolsConfig represents the complete tools configuration.
type ToolsConfig struct {
	LSP             map[string]LSPConfig      `yaml:"lsp"`
	PackageManagers map[string]PackageManager `yaml:"package_managers"`
	Interpreters    map[string]Interpreter    `yaml:"interpreters"`
	Search          SearchConfig              `yaml:"search"`
	RAG             RAGConfig                 `yaml:"rag"`
	Formatters      map[string]Formatter      `yaml:"formatters"`
	Linters         map[string]Linter         `yaml:"linters"`
	Discovery       DiscoveryConfig           `yaml:"discovery"`
	Platforms       map[string]PlatformConfig `yaml:"platforms"`
	Hooks           HooksConfig               `yaml:"hooks"`
}

// LSPConfig represents configuration for a language server.
type LSPConfig struct {
	Enabled     bool              `yaml:"enabled"`
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	Languages   []string          `yaml:"languages"`
	Timeout     string            `yaml:"timeout"`
	MaxRestarts int               `yaml:"max_restarts"`
	AutoStart   bool              `yaml:"auto_start"`
	Env         map[string]string `yaml:"env"`
}

// ParseTimeout returns parsed timeout duration.
func (l *LSPConfig) ParseTimeout() (time.Duration, error) {
	if l.Timeout == "" {
		return 30 * time.Second, nil
	}
	return time.ParseDuration(l.Timeout)
}

// PackageManager represents a package manager configuration.
type PackageManager struct {
	Enabled      bool     `yaml:"enabled"`
	Command      string   `yaml:"command"`
	CheckCommand []string `yaml:"check_command"`
	InstallArgs  []string `yaml:"install_args"`
}

// Interpreter represents an interpreter configuration.
type Interpreter struct {
	Enabled            bool     `yaml:"enabled"`
	Command            string   `yaml:"command"`
	Fallback           string   `yaml:"fallback"`
	CheckCommand       []string `yaml:"check_command"`
	VersionRequirement string   `yaml:"version_requirement"`
}

// SearchConfig represents search tools configuration.
type SearchConfig struct {
	Ripgrep RipgrepConfig `yaml:"ripgrep"`
	Fd      ToolConfig    `yaml:"fd"`
	Fzf     ToolConfig    `yaml:"fzf"`
}

// RipgrepConfig represents ripgrep specific configuration.
type RipgrepConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Command      string   `yaml:"command"`
	CheckCommand []string `yaml:"check_command"`
	DefaultArgs  []string `yaml:"default_args"`
	Fallback     string   `yaml:"fallback"`
}

// ToolConfig represents a generic tool configuration.
type ToolConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Command      string   `yaml:"command"`
	CheckCommand []string `yaml:"check_command"`
}

// RAGConfig represents RAG (Retrieval-Augmented Generation) configuration.
type RAGConfig struct {
	Enabled   bool               `yaml:"enabled"`
	Indexing  RAGIndexingConfig  `yaml:"indexing"`
	Search    RAGSearchConfig    `yaml:"search"`
	Embedding RAGEmbeddingConfig `yaml:"embedding"`
}

// RAGIndexingConfig represents RAG indexing configuration.
type RAGIndexingConfig struct {
	AutoBuild           bool     `yaml:"auto_build"`
	IncrementalInterval int      `yaml:"incremental_interval"`
	IndexableExtensions []string `yaml:"indexable_extensions"`
	ExcludeDirs         []string `yaml:"exclude_dirs"`
	ExcludePatterns     []string `yaml:"exclude_patterns"`
	MaxFileSize         int64    `yaml:"max_file_size"`
	ChunkSize           int      `yaml:"chunk_size"`
	ChunkOverlap        int      `yaml:"chunk_overlap"`
}

// RAGSearchConfig represents RAG search configuration.
type RAGSearchConfig struct {
	K1          float64 `yaml:"k1"`
	B           float64 `yaml:"b"`
	DefaultTopK int     `yaml:"default_top_k"`
	MaxTopK     int     `yaml:"max_top_k"`
	MinScore    float64 `yaml:"min_score"`
}

// RAGEmbeddingConfig represents RAG embedding configuration (future).
type RAGEmbeddingConfig struct {
	Enabled   bool   `yaml:"enabled"`
	ModelPath string `yaml:"model_path"`
	Dimension int    `yaml:"dimension"`
}

// Formatter represents a formatter configuration.
type Formatter struct {
	Enabled   bool     `yaml:"enabled"`
	Command   string   `yaml:"command"`
	Args      []string `yaml:"args"`
	Languages []string `yaml:"languages"`
}

// Linter represents a linter configuration.
type Linter struct {
	Enabled   bool     `yaml:"enabled"`
	Command   string   `yaml:"command"`
	Args      []string `yaml:"args"`
	Languages []string `yaml:"languages"`
}

// DiscoveryConfig represents tool discovery configuration.
type DiscoveryConfig struct {
	CheckTimeout string `yaml:"check_timeout"`
	AutoCheck    bool   `yaml:"auto_check"`
	CacheTTL     int    `yaml:"cache_ttl"`
	OnMissing    string `yaml:"on_missing"`
}

// PlatformConfig represents platform-specific configuration.
type PlatformConfig struct {
	Suffix       string `yaml:"suffix"`
	WhichCommand string `yaml:"which_command"`
}

// HookEntry represents a single hook as declared in tools.yaml.
type HookEntry struct {
	Name     string            `yaml:"name"`
	Command  string            `yaml:"command"`
	Blocking bool              `yaml:"blocking"`
	Timeout  string            `yaml:"timeout"` // parsed to time.Duration at conversion time
	Env      map[string]string `yaml:"env,omitempty"`
	Priority int               `yaml:"priority,omitempty"`
}

// HooksConfig groups all hook lists loaded from config.yaml.
type HooksConfig struct {
	PreIntent    []HookEntry `yaml:"pre_intent"`
	PostIntent   []HookEntry `yaml:"post_intent"`
	PostPlan     []HookEntry `yaml:"post_plan"`
	PreCode      []HookEntry `yaml:"pre_code"`
	PostCode     []HookEntry `yaml:"post_code"`
	PreToolUse   []HookEntry `yaml:"pre_tool_use"`
	PostToolUse  []HookEntry `yaml:"post_tool_use"`
	PostAccept   []HookEntry `yaml:"post_accept"`
	PostWorkflow []HookEntry `yaml:"post_workflow"`
}

// configFileName is the name used for the new two-level config files.
const configFileName = "config.yaml"

// legacyConfigFileName is the old single-file config name.
const legacyConfigFileName = "tools.yaml"

// codenDirName is the hidden directory under home or workspace root.
const codenDirName = ".coden"

// LoadConfig loads the effective tool configuration by merging:
//
//  1. Built-in defaults (DefaultToolsConfig)
//  2. User-level config: ~/.coden/config.yaml (if exists)
//  3. Workspace-level config: <workspaceRoot>/.coden/config.yaml (if exists)
//  4. Legacy fallback: <workspaceRoot>/tools.yaml (if exists and no .coden/config.yaml found)
//
// Each layer overrides the previous. Workspace config takes highest priority.
func LoadConfig(workspaceRoot string) (*ToolsConfig, error) {
	cfg := DefaultToolsConfig()

	foundUserConfig := false
	foundWorkspaceConfig := false

	// Layer 1: User-level ~/.coden/config.yaml
	home, homeErr := os.UserHomeDir()
	if homeErr == nil && home != "" {
		userPath := filepath.Join(home, codenDirName, configFileName)
		if userCfg, err := loadYAMLConfig(userPath); err == nil {
			cfg.MergeWith(userCfg)
			foundUserConfig = true
			slog.Info("[config] loaded user config", "path", userPath)
		}
	}

	// Layer 2: Workspace-level <workspace>/.coden/config.yaml
	if workspaceRoot != "" {
		wsPath := filepath.Join(workspaceRoot, codenDirName, configFileName)
		if wsCfg, err := loadYAMLConfig(wsPath); err == nil {
			cfg.MergeWith(wsCfg)
			foundWorkspaceConfig = true
			slog.Info("[config] loaded workspace config", "path", wsPath)
		}
	}

	// Layer 3: Legacy fallback — tools.yaml at workspace root (backward compat).
	// Only used if NO .coden/config.yaml was found at either level.
	if !foundUserConfig && !foundWorkspaceConfig && workspaceRoot != "" {
		legacyPath := filepath.Join(workspaceRoot, legacyConfigFileName)
		if legacyCfg, err := loadYAMLConfig(legacyPath); err == nil {
			cfg.MergeWith(legacyCfg)
			slog.Info("[config] loaded legacy tools.yaml (consider migrating to .coden/config.yaml)", "path", legacyPath)
		}
	}

	if !foundUserConfig && !foundWorkspaceConfig {
		slog.Info("[config] no config.yaml found, using defaults")
	}

	// Layer 4: Split config files (.coden/tools.yaml, rag.yaml, hooks.yaml, settings.yaml).
	// These override unified config.yaml sections when present.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		LoadSplitConfig(home, cfg)
	}
	if workspaceRoot != "" {
		LoadSplitConfig(workspaceRoot, cfg)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// loadYAMLConfig loads a single YAML config file and returns the parsed
// ToolsConfig. Returns a non-nil error if the file does not exist or
// cannot be read/parsed.
func loadYAMLConfig(path string) (*ToolsConfig, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg ToolsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return &cfg, nil
}

// ConfigSearchPaths returns the paths that LoadConfig will check, in
// priority order (lowest to highest). Useful for debugging and
// CLI --show-config commands.
func ConfigSearchPaths(workspaceRoot string) []string {
	var paths []string

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		paths = append(paths, filepath.Join(home, codenDirName, configFileName))
	}

	if workspaceRoot != "" {
		paths = append(paths, filepath.Join(workspaceRoot, codenDirName, configFileName))
		// Legacy fallback path (only used when neither of the above exist).
		paths = append(paths, filepath.Join(workspaceRoot, legacyConfigFileName)+" (legacy fallback)")
	}

	return paths
}

// LoadToolsConfig loads tools configuration from the given directory.
//
// Deprecated: Use LoadConfig instead which supports two-level config
// merging (user defaults + workspace overrides).
func LoadToolsConfig(dir string) (*ToolsConfig, error) {
	path := filepath.Join(dir, legacyConfigFileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Return default config if file doesn't exist
		return DefaultToolsConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tools.yaml: %w", err)
	}

	var config ToolsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse tools.yaml: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate tools.yaml: %w", err)
	}

	return &config, nil
}

// DefaultToolsConfig returns default tools configuration.
func DefaultToolsConfig() *ToolsConfig {
	return &ToolsConfig{
		LSP: map[string]LSPConfig{
			"go": {
				Enabled:     true,
				Command:     "gopls",
				Args:        []string{"serve"},
				Languages:   []string{"go"},
				Timeout:     "30s",
				MaxRestarts: 3,
				AutoStart:   true,
			},
		},
		Search: SearchConfig{
			Ripgrep: RipgrepConfig{
				Enabled:      true,
				Command:      "rg",
				CheckCommand: []string{"rg", "--version"},
				DefaultArgs:  []string{"--json", "--max-count=50", "--max-filesize=1M", "-C=3"},
				Fallback:     "builtin",
			},
		},
		RAG: RAGConfig{
			Enabled: true,
			Indexing: RAGIndexingConfig{
				AutoBuild:           true,
				IncrementalInterval: 300,
				IndexableExtensions: []string{".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java"},
				ExcludeDirs:         []string{".git", "vendor", "node_modules", ".tmp", ".cache"},
				ExcludePatterns:     []string{"*.min.js", "*.min.css", "*.map", "*.lock"},
				MaxFileSize:         1048576, // 1MB
				ChunkSize:           50,
				ChunkOverlap:        10,
			},
			Search: RAGSearchConfig{
				K1:          1.5,
				B:           0.75,
				DefaultTopK: 10,
				MaxTopK:     100,
				MinScore:    0.1,
			},
			Embedding: RAGEmbeddingConfig{
				Enabled:   false,
				ModelPath: "",
				Dimension: 768,
			},
		},
		Discovery: DiscoveryConfig{
			CheckTimeout: "5s",
			AutoCheck:    true,
			CacheTTL:     3600,
			OnMissing:    "warn",
		},
	}
}

// GetLSPConfig returns LSP configuration for a specific language.
func (c *ToolsConfig) GetLSPConfig(lang string) (*LSPConfig, bool) {
	cfg, ok := c.LSP[lang]
	if !ok {
		return nil, false
	}
	return &cfg, true
}

// IsLSPEnabled checks if LSP is enabled for a language.
func (c *ToolsConfig) IsLSPEnabled(lang string) bool {
	cfg, ok := c.GetLSPConfig(lang)
	if !ok {
		return false
	}
	return cfg.Enabled
}

// Validate validates the tools configuration.
func (c *ToolsConfig) Validate() error {
	// Validate LSP configs
	for lang, lspConfig := range c.LSP {
		if lspConfig.Command == "" {
			return fmt.Errorf("LSP config for %s: command is required", lang)
		}
		if lspConfig.Enabled && lspConfig.MaxRestarts < 0 {
			return fmt.Errorf("LSP config for %s: max_restarts must be non-negative", lang)
		}
		if lspConfig.Timeout != "" {
			if _, err := lspConfig.ParseTimeout(); err != nil {
				return fmt.Errorf("LSP config for %s: invalid timeout %q: %w", lang, lspConfig.Timeout, err)
			}
		}
	}

	// Validate search config
	if c.Search.Ripgrep.Enabled && c.Search.Ripgrep.Command == "" {
		return fmt.Errorf("search config: ripgrep command is required")
	}

	// Validate RAG config
	if c.RAG.Enabled {
		if c.RAG.Search.K1 < 0 || c.RAG.Search.K1 > 10 {
			return fmt.Errorf("RAG config: k1 must be in [0, 10], got %f", c.RAG.Search.K1)
		}
		if c.RAG.Search.B < 0 || c.RAG.Search.B > 1 {
			return fmt.Errorf("RAG config: b must be in [0, 1], got %f", c.RAG.Search.B)
		}
		if c.RAG.Indexing.MaxFileSize < 0 {
			return fmt.Errorf("RAG config: max_file_size must be non-negative")
		}
		if c.RAG.Indexing.ChunkSize <= 0 {
			return fmt.Errorf("RAG config: chunk_size must be positive")
		}
		if c.RAG.Indexing.ChunkOverlap < 0 {
			return fmt.Errorf("RAG config: chunk_overlap must be non-negative")
		}
		if c.RAG.Indexing.ChunkOverlap >= c.RAG.Indexing.ChunkSize {
			return fmt.Errorf("RAG config: chunk_overlap (%d) must be less than chunk_size (%d)",
				c.RAG.Indexing.ChunkOverlap, c.RAG.Indexing.ChunkSize)
		}
		if c.RAG.Search.DefaultTopK <= 0 {
			return fmt.Errorf("RAG config: default_top_k must be positive")
		}
		if c.RAG.Search.MaxTopK < c.RAG.Search.DefaultTopK {
			return fmt.Errorf("RAG config: max_top_k (%d) must be >= default_top_k (%d)",
				c.RAG.Search.MaxTopK, c.RAG.Search.DefaultTopK)
		}
		if c.RAG.Search.MinScore < 0 || c.RAG.Search.MinScore > 1 {
			return fmt.Errorf("RAG config: min_score must be in [0, 1], got %f", c.RAG.Search.MinScore)
		}
	}

	// Validate hook entries
	for i, h := range c.Hooks.PostCode {
		if h.Name == "" {
			return fmt.Errorf("hooks.post_code[%d]: name is required", i)
		}
		if h.Command == "" {
			return fmt.Errorf("hooks.post_code[%d] (%s): command is required", i, h.Name)
		}
		if h.Timeout != "" {
			if _, err := time.ParseDuration(h.Timeout); err != nil {
				return fmt.Errorf("hooks.post_code[%d] (%s): invalid timeout %q: %w", i, h.Name, h.Timeout, err)
			}
		}
	}

	return nil
}

// MergeWith merges another config into this one, with the other config taking precedence.
// All sections are merged: maps are merged key-by-key, structs and slices are replaced
// wholesale when the other config provides a non-zero value.
func (c *ToolsConfig) MergeWith(other *ToolsConfig) {
	if other == nil {
		return
	}

	// Merge LSP configs (map merge: per-language override)
	if len(other.LSP) > 0 {
		if c.LSP == nil {
			c.LSP = make(map[string]LSPConfig)
		}
		for lang, lspConfig := range other.LSP {
			c.LSP[lang] = lspConfig
		}
	}

	// Merge PackageManagers (map merge)
	if len(other.PackageManagers) > 0 {
		if c.PackageManagers == nil {
			c.PackageManagers = make(map[string]PackageManager)
		}
		for name, pm := range other.PackageManagers {
			c.PackageManagers[name] = pm
		}
	}

	// Merge Interpreters (map merge)
	if len(other.Interpreters) > 0 {
		if c.Interpreters == nil {
			c.Interpreters = make(map[string]Interpreter)
		}
		for name, interp := range other.Interpreters {
			c.Interpreters[name] = interp
		}
	}

	// Merge search config
	if other.Search.Ripgrep.Command != "" {
		c.Search.Ripgrep = other.Search.Ripgrep
	}
	if other.Search.Fd.Command != "" {
		c.Search.Fd = other.Search.Fd
	}
	if other.Search.Fzf.Command != "" {
		c.Search.Fzf = other.Search.Fzf
	}

	// Merge RAG config — replace sub-structs when the override has meaningful values
	mergeRAG(&c.RAG, &other.RAG)

	// Merge Formatters (map merge)
	if len(other.Formatters) > 0 {
		if c.Formatters == nil {
			c.Formatters = make(map[string]Formatter)
		}
		for name, f := range other.Formatters {
			c.Formatters[name] = f
		}
	}

	// Merge Linters (map merge)
	if len(other.Linters) > 0 {
		if c.Linters == nil {
			c.Linters = make(map[string]Linter)
		}
		for name, l := range other.Linters {
			c.Linters[name] = l
		}
	}

	// Merge discovery config
	if other.Discovery.CheckTimeout != "" {
		c.Discovery = other.Discovery
	}

	// Merge Platforms (map merge)
	if len(other.Platforms) > 0 {
		if c.Platforms == nil {
			c.Platforms = make(map[string]PlatformConfig)
		}
		for name, p := range other.Platforms {
			c.Platforms[name] = p
		}
	}

	// Merge hooks config
	if len(other.Hooks.PostCode) > 0 {
		c.Hooks.PostCode = other.Hooks.PostCode
	}
}

// mergeRAG merges RAG config from src into dst. A non-default src replaces dst sub-sections.
func mergeRAG(dst, src *RAGConfig) {
	// If src explicitly sets enabled, honour it.
	// We detect "was this section present at all?" by checking whether at
	// least one sub-field deviates from the zero value. Checking Enabled
	// alone would make it impossible to disable RAG via an override file,
	// so we also check a few sentinel fields.
	srcPresent := src.Enabled ||
		src.Indexing.ChunkSize > 0 ||
		src.Search.DefaultTopK > 0 ||
		src.Embedding.Dimension > 0

	if !srcPresent {
		return
	}

	// Top-level enabled flag always wins when the section is present.
	dst.Enabled = src.Enabled

	if src.Indexing.ChunkSize > 0 {
		dst.Indexing = src.Indexing
	}
	if src.Search.DefaultTopK > 0 {
		dst.Search = src.Search
	}
	if src.Embedding.Dimension > 0 {
		dst.Embedding = src.Embedding
	}
}

// ShowConfig returns a human-readable YAML representation of the effective
// config, prefixed with the search paths that were considered.
func ShowConfig(workspaceRoot string) (string, error) {
	cfg, err := LoadConfig(workspaceRoot)
	if err != nil {
		return "", err
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	paths := ConfigSearchPaths(workspaceRoot)
	header := "# Effective configuration (merged from all layers)\n# Search paths (lowest → highest priority):\n"
	for _, p := range paths {
		exists := "not found"
		// Strip the " (legacy fallback)" suffix for the stat check.
		clean := p
		if idx := len(p) - len(" (legacy fallback)"); idx > 0 && p[idx:] == " (legacy fallback)" {
			clean = p[:idx]
		}
		if _, statErr := os.Stat(clean); statErr == nil {
			exists = "found"
		}
		header += fmt.Sprintf("#   %s [%s]\n", p, exists)
	}
	header += "#\n"

	return header + string(out), nil
}
