package config

import (
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SplitFileNames maps each split config file to the section it populates.
// When these files exist under <dir>/.coden/, they are loaded and merged
// into the corresponding ToolsConfig section after the unified config.yaml.
var SplitFileNames = []string{
	"tools.yaml",    // LSP, interpreters, formatters, linters, package_managers, search
	"rag.yaml",      // RAG indexing, search, embedding
	"hooks.yaml",    // post_code hooks
	"settings.yaml", // discovery, platforms
}

// LoadSplitConfig loads per-section YAML files from <dir>/.coden/ and merges
// them into the given ToolsConfig. Split files take precedence over the
// unified config.yaml because they are loaded after it.
//
// File mapping:
//   - tools.yaml    → LSP, PackageManagers, Interpreters, Search, Formatters, Linters
//   - rag.yaml      → RAG
//   - hooks.yaml    → Hooks
//   - settings.yaml → Discovery, Platforms
//
// Missing files are silently skipped. Parse errors are logged and skipped.
func LoadSplitConfig(dir string, cfg *ToolsConfig) int {
	if cfg == nil || dir == "" {
		return 0
	}
	codenDir := filepath.Join(dir, codenDirName)
	loaded := 0

	// tools.yaml — tool declarations
	if partial, err := loadSplitFile(filepath.Join(codenDir, "tools.yaml")); err == nil {
		mergeSplitTools(cfg, partial)
		loaded++
	}

	// rag.yaml — RAG configuration
	if partial, err := loadSplitFile(filepath.Join(codenDir, "rag.yaml")); err == nil {
		mergeSplitRAG(cfg, partial)
		loaded++
	}

	// hooks.yaml — lifecycle hooks
	if partial, err := loadSplitFile(filepath.Join(codenDir, "hooks.yaml")); err == nil {
		mergeSplitHooks(cfg, partial)
		loaded++
	}

	// settings.yaml — discovery & platform settings
	if partial, err := loadSplitFile(filepath.Join(codenDir, "settings.yaml")); err == nil {
		mergeSplitSettings(cfg, partial)
		loaded++
	}

	if loaded > 0 {
		slog.Info("[config] loaded split config files", "dir", codenDir, "count", loaded)
	}
	return loaded
}

// splitToolsFile represents the schema for .coden/tools.yaml.
type splitToolsFile struct {
	LSP             map[string]LSPConfig      `yaml:"lsp"`
	PackageManagers map[string]PackageManager `yaml:"package_managers"`
	Interpreters    map[string]Interpreter    `yaml:"interpreters"`
	Search          *SearchConfig             `yaml:"search"`
	Formatters      map[string]Formatter      `yaml:"formatters"`
	Linters         map[string]Linter         `yaml:"linters"`
}

// splitRAGFile represents the schema for .coden/rag.yaml.
type splitRAGFile struct {
	RAG RAGConfig `yaml:"rag"`
}

// splitHooksFile represents the schema for .coden/hooks.yaml.
type splitHooksFile struct {
	Hooks HooksConfig `yaml:"hooks"`
}

// splitSettingsFile represents the schema for .coden/settings.yaml.
type splitSettingsFile struct {
	Discovery *DiscoveryConfig          `yaml:"discovery"`
	Platforms map[string]PlatformConfig `yaml:"platforms"`
}

func loadSplitFile(path string) ([]byte, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("[config] failed to read split config", "path", path, "error", err)
		return nil, err
	}
	slog.Debug("[config] loaded split file", "path", path, "size", len(data))
	return data, nil
}

func mergeSplitTools(cfg *ToolsConfig, data []byte) {
	var f splitToolsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		slog.Warn("[config] failed to parse tools.yaml split file", "error", err)
		return
	}
	if len(f.LSP) > 0 {
		if cfg.LSP == nil {
			cfg.LSP = make(map[string]LSPConfig)
		}
		for k, v := range f.LSP {
			cfg.LSP[k] = v
		}
	}
	if len(f.PackageManagers) > 0 {
		if cfg.PackageManagers == nil {
			cfg.PackageManagers = make(map[string]PackageManager)
		}
		for k, v := range f.PackageManagers {
			cfg.PackageManagers[k] = v
		}
	}
	if len(f.Interpreters) > 0 {
		if cfg.Interpreters == nil {
			cfg.Interpreters = make(map[string]Interpreter)
		}
		for k, v := range f.Interpreters {
			cfg.Interpreters[k] = v
		}
	}
	if f.Search != nil {
		if f.Search.Ripgrep.Command != "" {
			cfg.Search.Ripgrep = f.Search.Ripgrep
		}
		if f.Search.Fd.Command != "" {
			cfg.Search.Fd = f.Search.Fd
		}
		if f.Search.Fzf.Command != "" {
			cfg.Search.Fzf = f.Search.Fzf
		}
	}
	if len(f.Formatters) > 0 {
		if cfg.Formatters == nil {
			cfg.Formatters = make(map[string]Formatter)
		}
		for k, v := range f.Formatters {
			cfg.Formatters[k] = v
		}
	}
	if len(f.Linters) > 0 {
		if cfg.Linters == nil {
			cfg.Linters = make(map[string]Linter)
		}
		for k, v := range f.Linters {
			cfg.Linters[k] = v
		}
	}
}

func mergeSplitRAG(cfg *ToolsConfig, data []byte) {
	var f splitRAGFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		slog.Warn("[config] failed to parse rag.yaml split file", "error", err)
		return
	}
	mergeRAG(&cfg.RAG, &f.RAG)
}

func mergeSplitHooks(cfg *ToolsConfig, data []byte) {
	var f splitHooksFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		slog.Warn("[config] failed to parse hooks.yaml split file", "error", err)
		return
	}
	if len(f.Hooks.PostCode) > 0 {
		cfg.Hooks.PostCode = f.Hooks.PostCode
	}
}

func mergeSplitSettings(cfg *ToolsConfig, data []byte) {
	var f splitSettingsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		slog.Warn("[config] failed to parse settings.yaml split file", "error", err)
		return
	}
	if f.Discovery != nil {
		cfg.Discovery = *f.Discovery
	}
	if len(f.Platforms) > 0 {
		if cfg.Platforms == nil {
			cfg.Platforms = make(map[string]PlatformConfig)
		}
		for k, v := range f.Platforms {
			cfg.Platforms[k] = v
		}
	}
}
