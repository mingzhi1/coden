package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateResult holds the outcome of a migration attempt.
type MigrateResult struct {
	Migrated   bool
	SourcePath string
	DestPath   string
	BackupPath string
}

// MigrateToolsYaml converts a legacy tools.yaml into the unified
// .coden/config.yaml format. It will NOT overwrite an existing
// config.yaml — the user must resolve conflicts manually.
func MigrateToolsYaml(workspaceRoot string) (*MigrateResult, error) {
	legacyPath := filepath.Join(workspaceRoot, legacyConfigFileName)
	destDir := filepath.Join(workspaceRoot, codenDirName)
	destPath := filepath.Join(destDir, configFileName)

	// Check legacy file exists.
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		return &MigrateResult{Migrated: false}, nil
	}

	// Refuse to overwrite existing config.
	if _, err := os.Stat(destPath); err == nil {
		return nil, fmt.Errorf(
			"%s already exists; merge manually or remove it first",
			destPath,
		)
	}

	// Load legacy config.
	oldCfg, err := loadYAMLConfig(legacyPath)
	if err != nil {
		return nil, fmt.Errorf("read legacy %s: %w", legacyPath, err)
	}

	// Wrap in unified Config (other sections use defaults).
	unified := DefaultConfig()
	unified.Tools = *oldCfg

	// Ensure destination directory.
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("create %s: %w", destDir, err)
	}

	// Marshal.
	data, err := yaml.Marshal(unified)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	header := fmt.Sprintf(
		"# Migrated from %s on %s\n# Delete that file after verifying this config.\n\n",
		legacyConfigFileName,
		time.Now().Format("2006-01-02 15:04"),
	)

	if err := os.WriteFile(destPath, []byte(header+string(data)), 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", destPath, err)
	}

	// Backup legacy file.
	backupPath := legacyPath + ".bak"
	if err := os.Rename(legacyPath, backupPath); err != nil {
		// Non-fatal — config was already written.
		backupPath = ""
	}

	return &MigrateResult{
		Migrated:   true,
		SourcePath: legacyPath,
		DestPath:   destPath,
		BackupPath: backupPath,
	}, nil
}

// ConfigStatus describes the state of config files for a workspace.
type ConfigStatus struct {
	UserConfig      string // path or ""
	UserExists      bool
	WorkspaceConfig string // path or ""
	WorkspaceExists bool
	LegacyConfig    string // path or ""
	LegacyExists    bool
	NeedsMigration  bool
}

// CheckStatus inspects the filesystem and returns config status.
func CheckStatus(workspaceRoot string) *ConfigStatus {
	s := &ConfigStatus{}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		s.UserConfig = filepath.Join(home, codenDirName, configFileName)
		_, e := os.Stat(s.UserConfig)
		s.UserExists = e == nil
	}

	if workspaceRoot != "" {
		s.WorkspaceConfig = filepath.Join(workspaceRoot, codenDirName, configFileName)
		_, e := os.Stat(s.WorkspaceConfig)
		s.WorkspaceExists = e == nil

		s.LegacyConfig = filepath.Join(workspaceRoot, legacyConfigFileName)
		_, e = os.Stat(s.LegacyConfig)
		s.LegacyExists = e == nil
	}

	s.NeedsMigration = s.LegacyExists && !s.WorkspaceExists
	return s
}
