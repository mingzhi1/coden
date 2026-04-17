package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mingzhi1/coden/internal/config"

	"gopkg.in/yaml.v3"
)

// GenerateConfig converts an Inventory's available tools into a ToolsConfig.
// Only tools with StatusAvailable are included.
func GenerateConfig(inv *Inventory) *config.ToolsConfig {
	cfg := &config.ToolsConfig{
		LSP:             make(map[string]config.LSPConfig),
		PackageManagers: make(map[string]config.PackageManager),
		Interpreters:    make(map[string]config.Interpreter),
		Formatters:      make(map[string]config.Formatter),
		Linters:         make(map[string]config.Linter),
	}

	for _, entry := range inv.Available() {
		switch entry.Category {
		case CatLSP:
			cfg.LSP[entry.Name] = config.LSPConfig{
				Enabled:     true,
				Command:     entry.Command,
				Args:        entry.Args,
				Languages:   entry.Languages,
				Timeout:     "30s",
				MaxRestarts: 3,
				AutoStart:   true,
			}
		case CatPackageManager:
			cfg.PackageManagers[entry.Name] = config.PackageManager{
				Enabled: true,
				Command: entry.Command,
			}
		case CatInterpreter:
			cfg.Interpreters[entry.Name] = config.Interpreter{
				Enabled: true,
				Command: entry.Command,
			}
		case CatFormatter:
			cfg.Formatters[entry.Name] = config.Formatter{
				Enabled:   true,
				Command:   entry.Command,
				Args:      entry.Args,
				Languages: entry.Languages,
			}
		case CatLinter:
			cfg.Linters[entry.Name] = config.Linter{
				Enabled:   true,
				Command:   entry.Command,
				Args:      entry.Args,
				Languages: entry.Languages,
			}
			// CatSearch and CatBuiltin are handled by existing config defaults.
		}
	}

	return cfg
}

// WriteWorkspaceConfig writes a generated ToolsConfig to <workspace>/.coden/config.yaml.
//
// Supported modes:
//   - "create":    only write if the file does not exist
//   - "merge":     merge new findings into existing config (doesn't overwrite user entries)
//   - "overwrite": replace the entire file
func WriteWorkspaceConfig(workspaceRoot string, generated *config.ToolsConfig, mode string) error {
	configDir := filepath.Join(workspaceRoot, ".coden")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create .coden dir: %w", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")

	switch mode {
	case "create":
		if _, err := os.Stat(configPath); err == nil {
			// File already exists — do nothing.
			return nil
		}
		return writeConfigFile(configPath, generated)

	case "merge":
		existing, err := loadExistingConfig(configPath)
		if err != nil {
			// No existing file or parse error — treat as fresh create.
			return writeConfigFile(configPath, generated)
		}
		// Merge generated into existing. Generated fills gaps; existing wins on conflicts.
		merged := mergeNewIntoExisting(existing, generated)
		return writeConfigFile(configPath, merged)

	case "overwrite":
		return writeConfigFile(configPath, generated)

	default:
		return fmt.Errorf("unknown write mode: %q", mode)
	}
}

// mergeNewIntoExisting adds new tool entries from generated into existing config.
// Existing entries are never overwritten — only missing keys are filled.
func mergeNewIntoExisting(existing, generated *config.ToolsConfig) *config.ToolsConfig {
	// LSP: add only if key doesn't already exist
	if existing.LSP == nil {
		existing.LSP = make(map[string]config.LSPConfig)
	}
	for name, lsp := range generated.LSP {
		if _, exists := existing.LSP[name]; !exists {
			existing.LSP[name] = lsp
		}
	}

	// PackageManagers
	if existing.PackageManagers == nil {
		existing.PackageManagers = make(map[string]config.PackageManager)
	}
	for name, pm := range generated.PackageManagers {
		if _, exists := existing.PackageManagers[name]; !exists {
			existing.PackageManagers[name] = pm
		}
	}

	// Interpreters
	if existing.Interpreters == nil {
		existing.Interpreters = make(map[string]config.Interpreter)
	}
	for name, interp := range generated.Interpreters {
		if _, exists := existing.Interpreters[name]; !exists {
			existing.Interpreters[name] = interp
		}
	}

	// Formatters
	if existing.Formatters == nil {
		existing.Formatters = make(map[string]config.Formatter)
	}
	for name, f := range generated.Formatters {
		if _, exists := existing.Formatters[name]; !exists {
			existing.Formatters[name] = f
		}
	}

	// Linters
	if existing.Linters == nil {
		existing.Linters = make(map[string]config.Linter)
	}
	for name, l := range generated.Linters {
		if _, exists := existing.Linters[name]; !exists {
			existing.Linters[name] = l
		}
	}

	return existing
}

// loadExistingConfig reads and parses the config file at the given path.
func loadExistingConfig(path string) (*config.ToolsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config.ToolsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// writeConfigFile marshals the config and writes it with an auto-generated header.
func writeConfigFile(path string, cfg *config.ToolsConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	header := fmt.Sprintf(
		"# CodeN workspace configuration\n"+
			"# Auto-generated by tool discovery at %s\n"+
			"# Edit freely — your changes will be preserved on next discovery.\n"+
			"# To re-run discovery: coden tools check\n\n",
		time.Now().Format(time.RFC3339),
	)

	return os.WriteFile(path, []byte(header+string(data)), 0644)
}
