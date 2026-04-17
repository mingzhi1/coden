package inventory

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/config"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("expected %v to contain %q", slice, want)
}

func assertNotContains(t *testing.T, slice []string, unwanted string) {
	t.Helper()
	for _, s := range slice {
		if s == unwanted {
			t.Errorf("expected %v NOT to contain %q", slice, unwanted)
			return
		}
	}
}

func assertEntryNames(t *testing.T, entries []*ToolEntry, wantNames ...string) {
	t.Helper()
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name
	}
	if len(got) != len(wantNames) {
		t.Errorf("expected names %v, got %v", wantNames, got)
		return
	}
	for i := range wantNames {
		if got[i] != wantNames[i] {
			t.Errorf("entry[%d]: expected name %q, got %q", i, wantNames[i], got[i])
		}
	}
}

// ─── TestDetectProjectLanguages ─────────────────────────────────────────────

func TestDetectProjectLanguages(t *testing.T) {
	t.Run("go project", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
		os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "go")
	})

	t.Run("node project detects js and ts", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "javascript")
		assertContains(t, langs, "typescript")
	})

	t.Run("rust project", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "rust")
	})

	t.Run("python project via indicator", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "python")
	})

	t.Run("multi-language project", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
		os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0644)
		os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "go")
		assertContains(t, langs, "javascript")
		assertContains(t, langs, "typescript")
		assertContains(t, langs, "rust")
	})

	t.Run("empty dir returns empty", func(t *testing.T) {
		dir := t.TempDir()
		langs := DetectProjectLanguages(dir)
		if len(langs) != 0 {
			t.Errorf("expected empty slice, got %v", langs)
		}
	})

	t.Run("nonexistent dir returns nil", func(t *testing.T) {
		langs := DetectProjectLanguages("/nonexistent/path/that/does/not/exist")
		if langs != nil {
			t.Errorf("expected nil, got %v", langs)
		}
	})

	t.Run("extension-only detection root level", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('hi')\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "python")
	})

	t.Run("extension detection in src subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		srcDir := filepath.Join(dir, "src")
		os.MkdirAll(srcDir, 0755)
		os.WriteFile(filepath.Join(srcDir, "main.rs"), []byte("fn main() {}\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "rust")
	})

	t.Run("extension detection in lib subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		libDir := filepath.Join(dir, "lib")
		os.MkdirAll(libDir, 0755)
		os.WriteFile(filepath.Join(libDir, "helper.rb"), []byte("puts 'hi'\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "ruby")
	})

	t.Run("csharp via csproj extension", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "MyApp.csproj"), []byte("<Project/>\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "csharp")
	})

	t.Run("results are sorted", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0644) // rust
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)   // go
		os.WriteFile(filepath.Join(dir, "script.py"), []byte("pass\n"), 0644)       // python

		langs := DetectProjectLanguages(dir)
		for i := 1; i < len(langs); i++ {
			if langs[i-1] > langs[i] {
				t.Errorf("languages not sorted: %v", langs)
				break
			}
		}
	})

	t.Run("java via pom.xml", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "java")
	})

	t.Run("c cpp via CMakeLists", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "CMakeLists.txt"), []byte("cmake_minimum_required(VERSION 3.0)\n"), 0644)

		langs := DetectProjectLanguages(dir)
		assertContains(t, langs, "c")
		assertContains(t, langs, "cpp")
	})
}

// ─── TestFilterByLanguages ──────────────────────────────────────────────────

func TestFilterByLanguages(t *testing.T) {
	catalog := BuiltinCatalog()

	t.Run("empty langs returns all candidates", func(t *testing.T) {
		filtered := FilterByLanguages(catalog, nil)
		if len(filtered) != len(catalog) {
			t.Errorf("expected %d candidates, got %d", len(catalog), len(filtered))
		}

		filtered2 := FilterByLanguages(catalog, []string{})
		if len(filtered2) != len(catalog) {
			t.Errorf("expected %d candidates with empty slice, got %d", len(catalog), len(filtered2))
		}
	})

	t.Run("filter by go", func(t *testing.T) {
		filtered := FilterByLanguages(catalog, []string{"go"})
		if len(filtered) == 0 {
			t.Fatal("expected at least one go tool")
		}

		// All returned candidates should either have "go" in Languages or be language-agnostic
		for _, c := range filtered {
			if len(c.Languages) == 0 {
				continue // language-agnostic (search tools), always included
			}
			found := false
			for _, l := range c.Languages {
				if l == "go" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("candidate %q has languages %v, expected 'go' or language-agnostic", c.Name, c.Languages)
			}
		}
	})

	t.Run("filter by rust", func(t *testing.T) {
		filtered := FilterByLanguages(catalog, []string{"rust"})
		hasRustAnalyzer := false
		hasCargo := false
		for _, c := range filtered {
			if c.Name == "rust-analyzer" {
				hasRustAnalyzer = true
			}
			if c.Name == "cargo" {
				hasCargo = true
			}
		}
		if !hasRustAnalyzer {
			t.Error("expected rust-analyzer in filtered results")
		}
		if !hasCargo {
			t.Error("expected cargo in filtered results")
		}
	})

	t.Run("language-agnostic tools always included", func(t *testing.T) {
		filtered := FilterByLanguages(catalog, []string{"go"})
		hasRipgrep := false
		for _, c := range filtered {
			if c.Name == "ripgrep" {
				hasRipgrep = true
				break
			}
		}
		if !hasRipgrep {
			t.Error("expected language-agnostic tool ripgrep to be included")
		}
	})

	t.Run("unrecognized language returns only agnostic tools", func(t *testing.T) {
		filtered := FilterByLanguages(catalog, []string{"brainfuck"})
		for _, c := range filtered {
			if len(c.Languages) > 0 {
				t.Errorf("candidate %q with languages %v should not be included for 'brainfuck'", c.Name, c.Languages)
			}
		}
		// Should still have language-agnostic tools
		if len(filtered) == 0 {
			t.Error("expected at least language-agnostic tools for unrecognized language")
		}
	})

	t.Run("multiple languages", func(t *testing.T) {
		filtered := FilterByLanguages(catalog, []string{"go", "python"})
		hasGo := false
		hasPython := false
		for _, c := range filtered {
			for _, l := range c.Languages {
				if l == "go" {
					hasGo = true
				}
				if l == "python" {
					hasPython = true
				}
			}
		}
		if !hasGo {
			t.Error("expected go tools in multi-language filter")
		}
		if !hasPython {
			t.Error("expected python tools in multi-language filter")
		}
	})
}

// ─── TestInventoryAddAndGet ─────────────────────────────────────────────────

func TestInventoryAddAndGet(t *testing.T) {
	t.Run("add and get entry", func(t *testing.T) {
		inv := New()

		entry := &ToolEntry{
			Category: CatLSP,
			Name:     "gopls",
			Command:  "gopls",
			Status:   StatusAvailable,
			Version:  "0.15.3",
		}
		inv.Add(entry)

		got := inv.Get(CatLSP, "gopls")
		if got == nil {
			t.Fatal("expected to get gopls entry")
		}
		if got.Command != "gopls" {
			t.Errorf("expected command 'gopls', got %q", got.Command)
		}
		if got.Version != "0.15.3" {
			t.Errorf("expected version '0.15.3', got %q", got.Version)
		}
		if got.Status != StatusAvailable {
			t.Errorf("expected status available, got %q", got.Status)
		}
	})

	t.Run("get nonexistent returns nil", func(t *testing.T) {
		inv := New()
		got := inv.Get(CatLSP, "nonexistent")
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("overwrite existing entry", func(t *testing.T) {
		inv := New()

		inv.Add(&ToolEntry{
			Category: CatLSP,
			Name:     "gopls",
			Command:  "gopls",
			Status:   StatusUnavailable,
			Version:  "0.14.0",
		})
		inv.Add(&ToolEntry{
			Category: CatLSP,
			Name:     "gopls",
			Command:  "gopls",
			Status:   StatusAvailable,
			Version:  "0.15.3",
		})

		got := inv.Get(CatLSP, "gopls")
		if got.Status != StatusAvailable {
			t.Errorf("expected overwritten entry to be available, got %q", got.Status)
		}
		if got.Version != "0.15.3" {
			t.Errorf("expected overwritten version '0.15.3', got %q", got.Version)
		}
	})

	t.Run("Available filter", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Command: "gopls", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatLSP, Name: "pylsp", Command: "pylsp", Status: StatusUnavailable})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "gofmt", Command: "gofmt", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatLinter, Name: "eslint", Command: "eslint", Status: StatusUnavailable})

		avail := inv.Available()
		if len(avail) != 2 {
			t.Fatalf("expected 2 available, got %d", len(avail))
		}
		for _, e := range avail {
			if e.Status != StatusAvailable {
				t.Errorf("Available() returned entry with status %q", e.Status)
			}
		}
	})

	t.Run("Unavailable filter", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Command: "gopls", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatLSP, Name: "pylsp", Command: "pylsp", Status: StatusUnavailable})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "gofmt", Command: "gofmt", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatLinter, Name: "eslint", Command: "eslint", Status: StatusUnavailable})

		unavail := inv.Unavailable()
		if len(unavail) != 2 {
			t.Fatalf("expected 2 unavailable, got %d", len(unavail))
		}
		for _, e := range unavail {
			if e.Status != StatusUnavailable {
				t.Errorf("Unavailable() returned entry with status %q", e.Status)
			}
		}
	})

	t.Run("All returns sorted by category then name", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLinter, Name: "eslint", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "prettier", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "black", Status: StatusUnavailable})
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Status: StatusAvailable})

		all := inv.All()
		if len(all) != 4 {
			t.Fatalf("expected 4, got %d", len(all))
		}
		// Should be sorted by category then name
		for i := 1; i < len(all); i++ {
			prev := string(all[i-1].Category) + ":" + all[i-1].Name
			curr := string(all[i].Category) + ":" + all[i].Name
			if prev > curr {
				t.Errorf("All() not sorted: %q > %q", prev, curr)
			}
		}
	})

	t.Run("empty inventory", func(t *testing.T) {
		inv := New()
		if len(inv.All()) != 0 {
			t.Error("expected empty All()")
		}
		if len(inv.Available()) != 0 {
			t.Error("expected empty Available()")
		}
		if len(inv.Unavailable()) != 0 {
			t.Error("expected empty Unavailable()")
		}
	})

	t.Run("Summary", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatLSP, Name: "pylsp", Status: StatusUnavailable})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "gofmt", Status: StatusAvailable})

		summary := inv.Summary()
		if !strings.Contains(summary, "3 tools discovered") {
			t.Errorf("unexpected summary: %s", summary)
		}
		if !strings.Contains(summary, "2 available") {
			t.Errorf("unexpected summary: %s", summary)
		}
		if !strings.Contains(summary, "1 unavailable") {
			t.Errorf("unexpected summary: %s", summary)
		}
	})

	t.Run("HasCategory", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatLinter, Name: "eslint", Status: StatusUnavailable})

		if !inv.HasCategory(CatLSP) {
			t.Error("expected HasCategory(CatLSP) to be true")
		}
		// eslint is unavailable so CatLinter should not be "has"
		if inv.HasCategory(CatLinter) {
			t.Error("expected HasCategory(CatLinter) to be false (only unavailable entries)")
		}
		if inv.HasCategory(CatSearch) {
			t.Error("expected HasCategory(CatSearch) to be false (no entries)")
		}
	})

	t.Run("AvailableLanguages", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Status: StatusAvailable, Languages: []string{"go"}})
		inv.Add(&ToolEntry{Category: CatLSP, Name: "pylsp", Status: StatusAvailable, Languages: []string{"python"}})
		inv.Add(&ToolEntry{Category: CatLSP, Name: "tsserver", Status: StatusUnavailable, Languages: []string{"typescript"}})
		// Formatter should not count for AvailableLanguages
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "black", Status: StatusAvailable, Languages: []string{"python"}})

		langs := inv.AvailableLanguages()
		assertContains(t, langs, "go")
		assertContains(t, langs, "python")
		assertNotContains(t, langs, "typescript") // unavailable LSP
	})
}

// ─── TestInventoryByCategory ────────────────────────────────────────────────

func TestInventoryByCategory(t *testing.T) {
	t.Run("sorted by priority descending", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "gofmt", Status: StatusAvailable, Priority: 90})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "goimports", Status: StatusAvailable, Priority: 100})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "gofumpt", Status: StatusAvailable, Priority: 95})

		formatters := inv.ByCategory(CatFormatter)
		if len(formatters) != 3 {
			t.Fatalf("expected 3 formatters, got %d", len(formatters))
		}
		// Priority order: goimports(100) > gofumpt(95) > gofmt(90)
		assertEntryNames(t, formatters, "goimports", "gofumpt", "gofmt")
	})

	t.Run("excludes unavailable", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLinter, Name: "golangci-lint", Status: StatusAvailable, Priority: 100})
		inv.Add(&ToolEntry{Category: CatLinter, Name: "staticcheck", Status: StatusUnavailable, Priority: 90})

		linters := inv.ByCategory(CatLinter)
		if len(linters) != 1 {
			t.Fatalf("expected 1 available linter, got %d", len(linters))
		}
		if linters[0].Name != "golangci-lint" {
			t.Errorf("expected golangci-lint, got %q", linters[0].Name)
		}
	})

	t.Run("empty category returns empty", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Status: StatusAvailable, Priority: 100})

		results := inv.ByCategory(CatSearch)
		if len(results) != 0 {
			t.Errorf("expected empty, got %d entries", len(results))
		}
	})

	t.Run("same priority preserves stable result", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatPackageManager, Name: "npm", Status: StatusAvailable, Priority: 80})
		inv.Add(&ToolEntry{Category: CatPackageManager, Name: "pnpm", Status: StatusAvailable, Priority: 80})

		pms := inv.ByCategory(CatPackageManager)
		if len(pms) != 2 {
			t.Fatalf("expected 2, got %d", len(pms))
		}
		// Both have same priority; just check we get both
		names := map[string]bool{}
		for _, p := range pms {
			names[p.Name] = true
		}
		if !names["npm"] || !names["pnpm"] {
			t.Errorf("expected both npm and pnpm, got %v", pms)
		}
	})
}

// ─── TestExtractVersion ─────────────────────────────────────────────────────

func TestExtractVersion(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		pattern string
		want    string
	}{
		{
			name:    "go version string",
			raw:     "go version go1.22.0 linux/amd64",
			pattern: "",
			want:    "1.22.0",
		},
		{
			name:    "v-prefixed semver",
			raw:     "v0.15.3",
			pattern: "",
			want:    "0.15.3",
		},
		{
			name:    "bare semver",
			raw:     "1.2.3",
			pattern: "",
			want:    "1.2.3",
		},
		{
			name:    "semver with prerelease",
			raw:     "rustfmt 1.7.0-nightly",
			pattern: "",
			want:    "1.7.0-nightly",
		},
		{
			name:    "npm version output",
			raw:     "10.2.4",
			pattern: "",
			want:    "10.2.4",
		},
		{
			name:    "golangci-lint version",
			raw:     "golangci-lint has version 1.56.2 built with go1.22.0 from abc123",
			pattern: "",
			want:    "1.56.2",
		},
		{
			name:    "custom pattern with capture group",
			raw:     "MyTool release-42 (stable)",
			pattern: `release-(\d+)`,
			want:    "42",
		},
		{
			name:    "custom pattern takes precedence",
			raw:     "v1.2.3 but custom says release-99",
			pattern: `release-(\d+)`,
			want:    "99",
		},
		{
			name:    "empty string returns empty",
			raw:     "",
			pattern: "",
			want:    "",
		},
		{
			name:    "version keyword matches even in prose",
			raw:     "no version here at all just some text",
			pattern: "",
			want:    "here",
		},
		{
			name:    "no version found returns first line capped",
			raw:     "some random tool output with no recognizable pattern",
			pattern: "",
			want:    "some random tool output with no recognizable pattern",
		},
		{
			name:    "multiline takes first match",
			raw:     "tool info\nversion 2.3.4\nmore stuff",
			pattern: "",
			want:    "2.3.4",
		},
		{
			name:    "invalid custom pattern falls back to common",
			raw:     "v3.4.5",
			pattern: `[invalid`,
			want:    "3.4.5",
		},
		{
			name:    "version keyword",
			raw:     "clangd version 17.0.6",
			pattern: "",
			want:    "17.0.6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersion(tt.raw, tt.pattern)
			if got != tt.want {
				t.Errorf("extractVersion(%q, %q) = %q, want %q", tt.raw, tt.pattern, got, tt.want)
			}
		})
	}
}

// ─── TestHasNewFindings ─────────────────────────────────────────────────────

func TestHasNewFindings(t *testing.T) {
	t.Run("new LSP tool found", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Command: "gopls", Status: StatusAvailable})

		cfg := &config.ToolsConfig{
			LSP: map[string]config.LSPConfig{}, // empty — gopls is new
		}

		if !HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be true when gopls is new")
		}
	})

	t.Run("all tools already in config", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Command: "gopls", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "gofmt", Command: "gofmt", Status: StatusAvailable})

		cfg := &config.ToolsConfig{
			LSP: map[string]config.LSPConfig{
				"gopls": {Enabled: true, Command: "gopls"},
			},
			Formatters: map[string]config.Formatter{
				"gofmt": {Enabled: true, Command: "gofmt"},
			},
		}

		if HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be false when all tools in config")
		}
	})

	t.Run("unavailable tools are not new findings", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Command: "gopls", Status: StatusUnavailable})

		cfg := &config.ToolsConfig{
			LSP: map[string]config.LSPConfig{},
		}

		if HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be false for unavailable tools")
		}
	})

	t.Run("new formatter found", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatFormatter, Name: "black", Command: "black", Status: StatusAvailable})

		cfg := &config.ToolsConfig{
			Formatters: map[string]config.Formatter{},
		}

		if !HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be true for new formatter")
		}
	})

	t.Run("new linter found", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLinter, Name: "ruff", Command: "ruff", Status: StatusAvailable})

		cfg := &config.ToolsConfig{
			Linters: map[string]config.Linter{},
		}

		if !HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be true for new linter")
		}
	})

	t.Run("new package manager found", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatPackageManager, Name: "cargo", Command: "cargo", Status: StatusAvailable})

		cfg := &config.ToolsConfig{
			PackageManagers: map[string]config.PackageManager{},
		}

		if !HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be true for new package manager")
		}
	})

	t.Run("new interpreter found", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatInterpreter, Name: "python3", Command: "python3", Status: StatusAvailable})

		cfg := &config.ToolsConfig{
			Interpreters: map[string]config.Interpreter{},
		}

		if !HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be true for new interpreter")
		}
	})

	t.Run("nil maps in config treated as missing", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Command: "gopls", Status: StatusAvailable})

		cfg := &config.ToolsConfig{} // all maps are nil

		if !HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be true when config maps are nil")
		}
	})

	t.Run("empty inventory has no new findings", func(t *testing.T) {
		inv := New()
		cfg := &config.ToolsConfig{}

		if HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be false for empty inventory")
		}
	})

	t.Run("search and builtin categories are not checked", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatSearch, Name: "ripgrep", Command: "rg", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatBuiltin, Name: "internal", Command: "internal", Status: StatusAvailable})

		cfg := &config.ToolsConfig{}

		if HasNewFindings(inv, cfg) {
			t.Error("expected HasNewFindings to be false for search/builtin-only tools")
		}
	})
}

// ─── TestGenerateConfig ─────────────────────────────────────────────────────

func TestGenerateConfig(t *testing.T) {
	t.Run("generates config from available tools", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{
			Category:  CatLSP,
			Name:      "gopls",
			Command:   "gopls",
			Args:      []string{"serve"},
			Status:    StatusAvailable,
			Version:   "0.15.3",
			Languages: []string{"go"},
		})
		inv.Add(&ToolEntry{
			Category: CatPackageManager,
			Name:     "npm",
			Command:  "npm",
			Status:   StatusAvailable,
		})
		inv.Add(&ToolEntry{
			Category: CatInterpreter,
			Name:     "python3",
			Command:  "python3",
			Status:   StatusAvailable,
		})
		inv.Add(&ToolEntry{
			Category:  CatFormatter,
			Name:      "prettier",
			Command:   "prettier",
			Args:      []string{"--write"},
			Status:    StatusAvailable,
			Languages: []string{"javascript", "typescript"},
		})
		inv.Add(&ToolEntry{
			Category:  CatLinter,
			Name:      "eslint",
			Command:   "eslint",
			Args:      []string{"--fix"},
			Status:    StatusAvailable,
			Languages: []string{"javascript"},
		})

		cfg := GenerateConfig(inv)

		// Check LSP
		lsp, ok := cfg.LSP["gopls"]
		if !ok {
			t.Fatal("expected gopls in LSP config")
		}
		if !lsp.Enabled {
			t.Error("expected gopls to be enabled")
		}
		if lsp.Command != "gopls" {
			t.Errorf("expected command 'gopls', got %q", lsp.Command)
		}
		if len(lsp.Args) != 1 || lsp.Args[0] != "serve" {
			t.Errorf("expected args [serve], got %v", lsp.Args)
		}
		if lsp.Timeout != "30s" {
			t.Errorf("expected timeout '30s', got %q", lsp.Timeout)
		}
		if lsp.MaxRestarts != 3 {
			t.Errorf("expected max_restarts 3, got %d", lsp.MaxRestarts)
		}
		if !lsp.AutoStart {
			t.Error("expected auto_start true")
		}
		assertContains(t, lsp.Languages, "go")

		// Check PackageManager
		pm, ok := cfg.PackageManagers["npm"]
		if !ok {
			t.Fatal("expected npm in PackageManagers config")
		}
		if !pm.Enabled {
			t.Error("expected npm to be enabled")
		}
		if pm.Command != "npm" {
			t.Errorf("expected command 'npm', got %q", pm.Command)
		}

		// Check Interpreter
		interp, ok := cfg.Interpreters["python3"]
		if !ok {
			t.Fatal("expected python3 in Interpreters config")
		}
		if !interp.Enabled {
			t.Error("expected python3 to be enabled")
		}
		if interp.Command != "python3" {
			t.Errorf("expected command 'python3', got %q", interp.Command)
		}

		// Check Formatter
		fmt, ok := cfg.Formatters["prettier"]
		if !ok {
			t.Fatal("expected prettier in Formatters config")
		}
		if !fmt.Enabled {
			t.Error("expected prettier to be enabled")
		}
		assertContains(t, fmt.Languages, "javascript")
		assertContains(t, fmt.Languages, "typescript")
		if len(fmt.Args) != 1 || fmt.Args[0] != "--write" {
			t.Errorf("expected args [--write], got %v", fmt.Args)
		}

		// Check Linter
		lint, ok := cfg.Linters["eslint"]
		if !ok {
			t.Fatal("expected eslint in Linters config")
		}
		if !lint.Enabled {
			t.Error("expected eslint to be enabled")
		}
		assertContains(t, lint.Languages, "javascript")
	})

	t.Run("excludes unavailable tools", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{
			Category: CatLSP,
			Name:     "gopls",
			Command:  "gopls",
			Status:   StatusUnavailable,
		})
		inv.Add(&ToolEntry{
			Category: CatLSP,
			Name:     "pylsp",
			Command:  "pylsp",
			Status:   StatusAvailable,
		})

		cfg := GenerateConfig(inv)
		if _, ok := cfg.LSP["gopls"]; ok {
			t.Error("unavailable gopls should not be in generated config")
		}
		if _, ok := cfg.LSP["pylsp"]; !ok {
			t.Error("available pylsp should be in generated config")
		}
	})

	t.Run("search and builtin categories not in config maps", func(t *testing.T) {
		inv := New()
		inv.Add(&ToolEntry{Category: CatSearch, Name: "ripgrep", Command: "rg", Status: StatusAvailable})
		inv.Add(&ToolEntry{Category: CatBuiltin, Name: "internal", Command: "internal", Status: StatusAvailable})

		cfg := GenerateConfig(inv)
		if len(cfg.LSP) != 0 {
			t.Error("expected empty LSP")
		}
		if len(cfg.Formatters) != 0 {
			t.Error("expected empty Formatters")
		}
	})

	t.Run("empty inventory generates empty config maps", func(t *testing.T) {
		inv := New()
		cfg := GenerateConfig(inv)

		if cfg.LSP == nil {
			t.Error("expected non-nil LSP map")
		}
		if len(cfg.LSP) != 0 {
			t.Errorf("expected empty LSP map, got %d entries", len(cfg.LSP))
		}
		if cfg.PackageManagers == nil {
			t.Error("expected non-nil PackageManagers map")
		}
		if cfg.Interpreters == nil {
			t.Error("expected non-nil Interpreters map")
		}
		if cfg.Formatters == nil {
			t.Error("expected non-nil Formatters map")
		}
		if cfg.Linters == nil {
			t.Error("expected non-nil Linters map")
		}
	})
}

// ─── TestWriteWorkspaceConfig ───────────────────────────────────────────────

func TestWriteWorkspaceConfig(t *testing.T) {
	makeConfig := func() *config.ToolsConfig {
		return &config.ToolsConfig{
			LSP: map[string]config.LSPConfig{
				"gopls": {
					Enabled:     true,
					Command:     "gopls",
					Args:        []string{"serve"},
					Languages:   []string{"go"},
					Timeout:     "30s",
					MaxRestarts: 3,
					AutoStart:   true,
				},
			},
			PackageManagers: make(map[string]config.PackageManager),
			Interpreters:    make(map[string]config.Interpreter),
			Formatters:      make(map[string]config.Formatter),
			Linters:         make(map[string]config.Linter),
		}
	}

	t.Run("create mode creates new file", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeConfig()

		err := WriteWorkspaceConfig(dir, cfg, "create")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		configPath := filepath.Join(dir, ".coden", "config.yaml")
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read config: %v", err)
		}

		content := string(data)
		if !strings.Contains(content, "gopls") {
			t.Error("expected config to contain 'gopls'")
		}
		if !strings.Contains(content, "# CodeN workspace configuration") {
			t.Error("expected config to contain header comment")
		}
	})

	t.Run("create mode does not overwrite existing", func(t *testing.T) {
		dir := t.TempDir()

		// Create existing config
		configDir := filepath.Join(dir, ".coden")
		os.MkdirAll(configDir, 0755)
		configPath := filepath.Join(configDir, "config.yaml")
		existingContent := "# my custom config\nlsp:\n  custom-lsp:\n    enabled: true\n"
		os.WriteFile(configPath, []byte(existingContent), 0644)

		cfg := makeConfig()
		err := WriteWorkspaceConfig(dir, cfg, "create")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should still have old content
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}
		if string(data) != existingContent {
			t.Error("create mode should not have overwritten existing file")
		}
	})

	t.Run("overwrite mode replaces file", func(t *testing.T) {
		dir := t.TempDir()

		// Create existing config
		configDir := filepath.Join(dir, ".coden")
		os.MkdirAll(configDir, 0755)
		configPath := filepath.Join(configDir, "config.yaml")
		os.WriteFile(configPath, []byte("# old config\n"), 0644)

		cfg := makeConfig()
		err := WriteWorkspaceConfig(dir, cfg, "overwrite")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}
		content := string(data)
		if strings.Contains(content, "# old config") {
			t.Error("overwrite mode should have replaced old content")
		}
		if !strings.Contains(content, "gopls") {
			t.Error("expected overwritten config to contain 'gopls'")
		}
	})

	t.Run("merge mode preserves existing and adds new", func(t *testing.T) {
		dir := t.TempDir()

		// Write an existing config with pylsp
		existingCfg := &config.ToolsConfig{
			LSP: map[string]config.LSPConfig{
				"pylsp": {
					Enabled: true,
					Command: "pylsp",
					Timeout: "60s",
				},
			},
			PackageManagers: make(map[string]config.PackageManager),
			Interpreters:    make(map[string]config.Interpreter),
			Formatters:      make(map[string]config.Formatter),
			Linters:         make(map[string]config.Linter),
		}

		// First write existing config
		err := WriteWorkspaceConfig(dir, existingCfg, "create")
		if err != nil {
			t.Fatalf("unexpected error creating existing: %v", err)
		}

		// Now merge in new config with gopls
		newCfg := makeConfig()
		err = WriteWorkspaceConfig(dir, newCfg, "merge")
		if err != nil {
			t.Fatalf("unexpected error merging: %v", err)
		}

		configPath := filepath.Join(dir, ".coden", "config.yaml")
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}
		content := string(data)

		// Both pylsp and gopls should be present
		if !strings.Contains(content, "pylsp") {
			t.Error("merge should preserve existing pylsp")
		}
		if !strings.Contains(content, "gopls") {
			t.Error("merge should add new gopls")
		}
	})

	t.Run("merge mode does not overwrite existing keys", func(t *testing.T) {
		dir := t.TempDir()

		// Existing config has gopls with custom timeout
		existingCfg := &config.ToolsConfig{
			LSP: map[string]config.LSPConfig{
				"gopls": {
					Enabled: true,
					Command: "gopls",
					Timeout: "120s", // user-customized
				},
			},
			PackageManagers: make(map[string]config.PackageManager),
			Interpreters:    make(map[string]config.Interpreter),
			Formatters:      make(map[string]config.Formatter),
			Linters:         make(map[string]config.Linter),
		}

		err := WriteWorkspaceConfig(dir, existingCfg, "create")
		if err != nil {
			t.Fatalf("create error: %v", err)
		}

		// Merge in generated config with different gopls settings
		newCfg := makeConfig() // gopls with timeout "30s"
		err = WriteWorkspaceConfig(dir, newCfg, "merge")
		if err != nil {
			t.Fatalf("merge error: %v", err)
		}

		configPath := filepath.Join(dir, ".coden", "config.yaml")
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}
		content := string(data)

		// Should still have the user's "120s" not the generated "30s"
		if !strings.Contains(content, "120s") {
			t.Error("merge should preserve existing gopls timeout of 120s")
		}
	})

	t.Run("merge on nonexistent file acts as create", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeConfig()

		err := WriteWorkspaceConfig(dir, cfg, "merge")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		configPath := filepath.Join(dir, ".coden", "config.yaml")
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("failed to read: %v", err)
		}
		if !strings.Contains(string(data), "gopls") {
			t.Error("expected config to contain 'gopls'")
		}
	})

	t.Run("unknown mode returns error", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeConfig()

		err := WriteWorkspaceConfig(dir, cfg, "invalid")
		if err == nil {
			t.Error("expected error for unknown mode")
		}
		if !strings.Contains(err.Error(), "unknown write mode") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("creates .coden directory if missing", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeConfig()

		err := WriteWorkspaceConfig(dir, cfg, "create")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		codenDir := filepath.Join(dir, ".coden")
		info, err := os.Stat(codenDir)
		if err != nil {
			t.Fatalf(".coden dir not created: %v", err)
		}
		if !info.IsDir() {
			t.Error("expected .coden to be a directory")
		}
	})
}

// ─── TestPlatformResolveCommand ─────────────────────────────────────────────

func TestPlatformResolveCommand(t *testing.T) {
	t.Run("windows adds .exe suffix", func(t *testing.T) {
		p := Platform{Suffix: ".exe", WhichCommand: "where"}

		result := p.ResolveCommand("gopls")
		if result != "gopls.exe" {
			t.Errorf("expected 'gopls.exe', got %q", result)
		}
	})

	t.Run("windows does not double .exe", func(t *testing.T) {
		p := Platform{Suffix: ".exe", WhichCommand: "where"}

		result := p.ResolveCommand("gopls.exe")
		if result != "gopls.exe" {
			t.Errorf("expected 'gopls.exe' (no double suffix), got %q", result)
		}
	})

	t.Run("linux no suffix", func(t *testing.T) {
		p := Platform{Suffix: "", WhichCommand: "which"}

		result := p.ResolveCommand("gopls")
		if result != "gopls" {
			t.Errorf("expected 'gopls', got %q", result)
		}
	})

	t.Run("darwin no suffix", func(t *testing.T) {
		p := Platform{Suffix: "", WhichCommand: "which"}

		result := p.ResolveCommand("rust-analyzer")
		if result != "rust-analyzer" {
			t.Errorf("expected 'rust-analyzer', got %q", result)
		}
	})

	t.Run("CurrentPlatform returns valid platform", func(t *testing.T) {
		p := CurrentPlatform()
		if runtime.GOOS == "windows" {
			if p.Suffix != ".exe" {
				t.Errorf("expected .exe suffix on Windows, got %q", p.Suffix)
			}
			if p.WhichCommand != "where" {
				t.Errorf("expected 'where' on Windows, got %q", p.WhichCommand)
			}
		} else {
			if p.Suffix != "" {
				t.Errorf("expected empty suffix on %s, got %q", runtime.GOOS, p.Suffix)
			}
			if p.WhichCommand != "which" {
				t.Errorf("expected 'which' on %s, got %q", runtime.GOOS, p.WhichCommand)
			}
		}
	})

	t.Run("empty command with suffix", func(t *testing.T) {
		p := Platform{Suffix: ".exe", WhichCommand: "where"}

		// Edge case: empty command
		result := p.ResolveCommand("")
		if result != ".exe" {
			t.Errorf("expected '.exe' for empty command, got %q", result)
		}
	})
}

// ─── TestEntryKey ───────────────────────────────────────────────────────────

func TestEntryKey(t *testing.T) {
	tests := []struct {
		cat  Category
		name string
		want string
	}{
		{CatLSP, "gopls", "lsp:gopls"},
		{CatFormatter, "black", "formatter:black"},
		{CatLinter, "eslint", "linter:eslint"},
		{CatPackageManager, "npm", "package_manager:npm"},
		{CatInterpreter, "python3", "interpreter:python3"},
		{CatSearch, "ripgrep", "search:ripgrep"},
		{CatBuiltin, "internal", "builtin:internal"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := entryKey(tt.cat, tt.name)
			if got != tt.want {
				t.Errorf("entryKey(%q, %q) = %q, want %q", tt.cat, tt.name, got, tt.want)
			}
		})
	}
}

// ─── TestBuiltinCatalog ─────────────────────────────────────────────────────

func TestBuiltinCatalog(t *testing.T) {
	t.Run("returns a copy", func(t *testing.T) {
		c1 := BuiltinCatalog()
		c2 := BuiltinCatalog()

		if len(c1) == 0 {
			t.Fatal("expected non-empty catalog")
		}
		if len(c1) != len(c2) {
			t.Fatal("expected same length")
		}

		// Modify c1 and verify c2 is unaffected
		c1[0].Name = "modified"
		if c2[0].Name == "modified" {
			t.Error("BuiltinCatalog should return a copy, not a reference")
		}
	})

	t.Run("has known tools", func(t *testing.T) {
		catalog := BuiltinCatalog()
		names := make(map[string]bool)
		for _, c := range catalog {
			names[c.Name] = true
		}

		expectedTools := []string{"gopls", "pylsp", "npm", "cargo", "ripgrep", "prettier", "eslint"}
		for _, name := range expectedTools {
			if !names[name] {
				t.Errorf("expected catalog to contain %q", name)
			}
		}
	})
}

// ─── TestInventoryConcurrency ───────────────────────────────────────────────

func TestInventoryConcurrency(t *testing.T) {
	inv := New()
	const n = 100
	done := make(chan struct{})

	// Concurrent writes
	go func() {
		for i := 0; i < n; i++ {
			inv.Add(&ToolEntry{
				Category: CatLSP,
				Name:     "tool",
				Command:  "tool",
				Status:   StatusAvailable,
				Version:  "1.0.0",
			})
		}
		done <- struct{}{}
	}()

	// Concurrent reads
	go func() {
		for i := 0; i < n; i++ {
			_ = inv.Get(CatLSP, "tool")
			_ = inv.All()
			_ = inv.Available()
			_ = inv.ByCategory(CatLSP)
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	// If we get here without a race condition, the test passes
	got := inv.Get(CatLSP, "tool")
	if got == nil {
		t.Error("expected tool to exist after concurrent operations")
	}
}

// ─── TestToolEntryFields ────────────────────────────────────────────────────

func TestToolEntryFields(t *testing.T) {
	now := time.Now()
	entry := &ToolEntry{
		Category:    CatLSP,
		Name:        "gopls",
		Command:     "gopls",
		Args:        []string{"serve"},
		Status:      StatusAvailable,
		Version:     "0.15.3",
		Languages:   []string{"go"},
		Path:        "/usr/local/bin/gopls",
		CheckedAt:   now,
		Error:       "",
		InstallHint: "go install golang.org/x/tools/gopls@latest",
		Priority:    100,
	}

	if entry.Category != CatLSP {
		t.Errorf("Category: got %q", entry.Category)
	}
	if entry.Name != "gopls" {
		t.Errorf("Name: got %q", entry.Name)
	}
	if entry.Command != "gopls" {
		t.Errorf("Command: got %q", entry.Command)
	}
	if len(entry.Args) != 1 || entry.Args[0] != "serve" {
		t.Errorf("Args: got %v", entry.Args)
	}
	if entry.Status != StatusAvailable {
		t.Errorf("Status: got %q", entry.Status)
	}
	if entry.Version != "0.15.3" {
		t.Errorf("Version: got %q", entry.Version)
	}
	if entry.Path != "/usr/local/bin/gopls" {
		t.Errorf("Path: got %q", entry.Path)
	}
	if !entry.CheckedAt.Equal(now) {
		t.Errorf("CheckedAt: got %v", entry.CheckedAt)
	}
	if entry.Priority != 100 {
		t.Errorf("Priority: got %d", entry.Priority)
	}
}

// ─── TestOptionsFromConfig ──────────────────────────────────────────────────

func TestOptionsFromConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		dc := config.DiscoveryConfig{}
		opts := OptionsFromConfig(dc)
		if opts.CheckTimeout != 5*time.Second {
			t.Errorf("expected 5s default timeout, got %v", opts.CheckTimeout)
		}
		if opts.OnMissing != "warn" {
			t.Errorf("expected 'warn' default OnMissing, got %q", opts.OnMissing)
		}
	})

	t.Run("custom values", func(t *testing.T) {
		dc := config.DiscoveryConfig{
			CheckTimeout: "10s",
			OnMissing:    "error",
		}
		opts := OptionsFromConfig(dc)
		if opts.CheckTimeout != 10*time.Second {
			t.Errorf("expected 10s timeout, got %v", opts.CheckTimeout)
		}
		if opts.OnMissing != "error" {
			t.Errorf("expected 'error', got %q", opts.OnMissing)
		}
	})

	t.Run("invalid duration uses default", func(t *testing.T) {
		dc := config.DiscoveryConfig{
			CheckTimeout: "not-a-duration",
		}
		opts := OptionsFromConfig(dc)
		if opts.CheckTimeout != 5*time.Second {
			t.Errorf("expected fallback to 5s, got %v", opts.CheckTimeout)
		}
	})
}

// ─── TestStatusConstants ────────────────────────────────────────────────────

func TestStatusConstants(t *testing.T) {
	if StatusAvailable != "available" {
		t.Errorf("StatusAvailable: got %q", StatusAvailable)
	}
	if StatusUnavailable != "unavailable" {
		t.Errorf("StatusUnavailable: got %q", StatusUnavailable)
	}
	if StatusUnknown != "unknown" {
		t.Errorf("StatusUnknown: got %q", StatusUnknown)
	}
}

// ─── TestCategoryConstants ──────────────────────────────────────────────────

func TestCategoryConstants(t *testing.T) {
	if CatLSP != "lsp" {
		t.Errorf("CatLSP: got %q", CatLSP)
	}
	if CatFormatter != "formatter" {
		t.Errorf("CatFormatter: got %q", CatFormatter)
	}
	if CatLinter != "linter" {
		t.Errorf("CatLinter: got %q", CatLinter)
	}
	if CatInterpreter != "interpreter" {
		t.Errorf("CatInterpreter: got %q", CatInterpreter)
	}
	if CatPackageManager != "package_manager" {
		t.Errorf("CatPackageManager: got %q", CatPackageManager)
	}
	if CatSearch != "search" {
		t.Errorf("CatSearch: got %q", CatSearch)
	}
	if CatBuiltin != "builtin" {
		t.Errorf("CatBuiltin: got %q", CatBuiltin)
	}
}
