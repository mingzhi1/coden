package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catHasTool(tools []ToolCandidate, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// TestEmbeddedCatalog_LoadsAllData verifies layer 1: the catalog is loaded from
// the embedded catalog.yaml (data) with all tools + language tables intact, and
// that an unconfigured language (nim) is deliberately ABSENT — the long-tail case.
func TestEmbeddedCatalog_LoadsAllData(t *testing.T) {
	cat := loadBaseCatalog()
	if len(cat.Tools) < 30 {
		t.Fatalf("embedded catalog should carry the full toolset, got %d tools", len(cat.Tools))
	}
	for _, name := range []string{"gopls", "uv", "pyright", "poetry", "mypy", "isort", "ripgrep"} {
		if !catHasTool(cat.Tools, name) {
			t.Errorf("embedded catalog missing tool %q (YAML migration dropped it?)", name)
		}
	}
	if cat.Languages.Indicators["go.mod"] == nil {
		t.Error("language indicator go.mod missing from embedded catalog")
	}
	if cat.Languages.Extensions[".py"] != "python" {
		t.Errorf("extension .py should map to python, got %q", cat.Languages.Extensions[".py"])
	}
	if _, ok := cat.Languages.Extensions[".nim"]; ok {
		t.Error(".nim must NOT be preconfigured (it is the long-tail demo)")
	}
}

// TestLoadCatalog_ProjectOverride verifies layer 2: a project-local
// .coden/catalog.yaml adds a brand-new language's toolchain (nim) WITHOUT touching
// code, and the merge is additive (base tools survive).
func TestLoadCatalog_ProjectOverride(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".coden"), 0o755); err != nil {
		t.Fatal(err)
	}
	override := `
languages:
  extensions:
    ".nim": nim
  indicators:
    "nim.cfg": [nim]
tools:
  - name: nim
    category: interpreter
    languages: [nim]
    command: nim
    version_cmd: [nim, --version]
`
	if err := os.WriteFile(filepath.Join(ws, ".coden", "catalog.yaml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	cat := LoadCatalog(ws)
	if cat.Languages.Extensions[".nim"] != "nim" {
		t.Error("project override did not add .nim → nim")
	}
	if !catHasTool(cat.Tools, "nim") {
		t.Error("project override did not add the nim tool")
	}
	if !catHasTool(cat.Tools, "gopls") {
		t.Error("merge must be additive — base tools were dropped")
	}
}

// TestDetectLanguages_DataDriven verifies layer 3: language detection consults the
// data map, so nim is invisible with the base catalog but detected once a map adds
// its extension — no code change.
func TestDetectLanguages_DataDriven(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "main.nim"), []byte("echo 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	if langs := detectLanguages(ws, loadBaseCatalog().Languages); strIn(langs, "nim") {
		t.Error("nim must not be detected with the base catalog")
	}
	lm := LanguageMap{Extensions: map[string]string{".nim": "nim"}}
	if langs := detectLanguages(ws, lm); !strIn(langs, "nim") {
		t.Errorf("nim should be detected via the data map, got %v", langs)
	}
}

// TestScanUnknownExtensions verifies layer 4: source-like files in an unconfigured
// language are surfaced, while known and non-source files are not.
func TestScanUnknownExtensions(t *testing.T) {
	ws := t.TempDir()
	for _, f := range []string{"main.nim", "README.md", "go.mod", "util.go"} {
		if err := os.WriteFile(filepath.Join(ws, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exts := ScanUnknownExtensions(ws, loadBaseCatalog().Languages.Extensions)
	if !strIn(exts, ".nim") {
		t.Errorf("expected .nim surfaced as unknown source, got %v", exts)
	}
	if strIn(exts, ".go") || strIn(exts, ".md") {
		t.Errorf("known/non-source extensions must not be surfaced, got %v", exts)
	}
}

// TestFormatEnvironmentPrompt_LongTail verifies layer 4 reaches the prompt EVEN
// when zero catalog tools are available (a pure unconfigured-language project),
// so the model is told to drive the toolchain via run_shell.
func TestFormatEnvironmentPrompt_LongTail(t *testing.T) {
	inv := New() // no available tools at all
	inv.SetLongTail([]string{"nim"}, []string{"nim"}, []string{".nim"})

	out := FormatEnvironmentPrompt(inv)
	if !strings.Contains(out, "no preconfigured tooling") {
		t.Fatalf("long-tail section missing from prompt:\n%s", out)
	}
	if !strings.Contains(out, "nim") || !strings.Contains(out, ".nim") {
		t.Errorf("long-tail signals not surfaced: %q", out)
	}
}
