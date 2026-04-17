package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateToolsYaml_NoLegacy(t *testing.T) {
	dir := t.TempDir()
	res, err := MigrateToolsYaml(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Migrated {
		t.Fatal("should not migrate when no tools.yaml")
	}
}

func TestMigrateToolsYaml_Success(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "tools.yaml")
	os.WriteFile(legacy, []byte("rag:\n  enabled: true\n"), 0644)

	res, err := MigrateToolsYaml(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Migrated {
		t.Fatal("expected migration")
	}

	// config.yaml should exist
	dest := filepath.Join(dir, ".coden", "config.yaml")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("config.yaml not created: %v", err)
	}
	if !strings.Contains(string(data), "Migrated from") {
		t.Fatal("missing migration header")
	}

	// legacy should be backed up
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy file should be renamed")
	}
	if _, err := os.Stat(legacy + ".bak"); err != nil {
		t.Fatal("backup file missing")
	}
}

func TestMigrateToolsYaml_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tools.yaml"), []byte("{}"), 0644)
	codenDir := filepath.Join(dir, ".coden")
	os.MkdirAll(codenDir, 0755)
	os.WriteFile(filepath.Join(codenDir, "config.yaml"), []byte("{}"), 0644)

	_, err := MigrateToolsYaml(dir)
	if err == nil {
		t.Fatal("should refuse when config.yaml exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckStatus(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tools.yaml"), []byte("{}"), 0644)

	s := CheckStatus(dir)
	if !s.LegacyExists {
		t.Fatal("legacy should be detected")
	}
	if !s.NeedsMigration {
		t.Fatal("should need migration")
	}

	// After creating config.yaml, migration not needed
	codenDir := filepath.Join(dir, ".coden")
	os.MkdirAll(codenDir, 0755)
	os.WriteFile(filepath.Join(codenDir, "config.yaml"), []byte("{}"), 0644)

	s2 := CheckStatus(dir)
	if s2.NeedsMigration {
		t.Fatal("should not need migration with config.yaml present")
	}
}
