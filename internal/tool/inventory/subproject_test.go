package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates parent dirs and writes an (empty) file at ws/rel.
func writeFile(t *testing.T, ws, rel string) {
	t.Helper()
	p := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDetectSubprojects_Monorepo verifies the initial deep walk locates each
// module by its manifest directory across a multi-language monorepo, prunes
// dependency/build dirs, and records the right language per subproject.
func TestDetectSubprojects_Monorepo(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, ws, "go.mod")                            // root: go
	writeFile(t, ws, "services/api/go.mod")               // nested: go
	writeFile(t, ws, "web/package.json")                  // js/ts
	writeFile(t, ws, "ml/pyproject.toml")                 // python
	writeFile(t, ws, "core/Cargo.toml")                   // rust
	writeFile(t, ws, "web/node_modules/dep/package.json") // pruned: must NOT be a subproject
	writeFile(t, ws, "core/target/build/Cargo.toml")      // pruned

	subs := DetectSubprojects(ws, loadBaseCatalog().Languages)

	byDir := map[string]Subproject{}
	for _, s := range subs {
		byDir[s.Dir] = s
	}
	for dir, wantLang := range map[string]string{
		".":            "go",
		"services/api": "go",
		"web":          "javascript",
		"ml":           "python",
		"core":         "rust",
	} {
		sp, ok := byDir[dir]
		if !ok {
			t.Errorf("expected a subproject at %q, got dirs %v", dir, keysOf(byDir))
			continue
		}
		if !strIn(sp.Languages, wantLang) {
			t.Errorf("subproject %q should include %q, got %v", dir, wantLang, sp.Languages)
		}
	}
	// Pruned dependency/build trees must not appear as subprojects.
	for _, s := range subs {
		if strIn(splitSlash(s.Dir), "node_modules") || strIn(splitSlash(s.Dir), "target") {
			t.Errorf("pruned dir leaked as subproject: %q", s.Dir)
		}
	}
}

// TestSubprojectLanguagesForFiles verifies per-task projection: a file maps to the
// languages of its NEAREST enclosing subproject, not the whole repo.
func TestSubprojectLanguagesForFiles(t *testing.T) {
	subs := []Subproject{
		{Dir: ".", Languages: []string{"go"}},
		{Dir: "web", Languages: []string{"javascript", "typescript"}},
		{Dir: "ml", Languages: []string{"python"}},
	}
	if got := SubprojectLanguagesForFiles(subs, []string{"web/src/app.ts"}); !strIn(got, "typescript") || strIn(got, "go") || strIn(got, "python") {
		t.Errorf("web file should map to web's langs only, got %v", got)
	}
	if got := SubprojectLanguagesForFiles(subs, []string{"main.go"}); !strIn(got, "go") || strIn(got, "python") {
		t.Errorf("root file should map to go, got %v", got)
	}
	if got := SubprojectLanguagesForFiles(subs, nil); got != nil {
		t.Errorf("no files → nil (fallback to repo-wide), got %v", got)
	}
}

// TestFormatEnvironmentPromptForLanguages_Scoped verifies the env section is
// scoped to the task's languages, keeps language-agnostic tools, and applies the
// noise cuts (no absolute paths, no install hints, no LSP servers).
func TestFormatEnvironmentPromptForLanguages_Scoped(t *testing.T) {
	inv := New()
	inv.Add(&ToolEntry{Category: CatInterpreter, Name: "go", Command: "go", Languages: []string{"go"}, Path: "/Users/secret/go/bin/go", Status: StatusAvailable})
	inv.Add(&ToolEntry{Category: CatInterpreter, Name: "node", Command: "node", Languages: []string{"javascript", "typescript"}, Status: StatusAvailable})
	inv.Add(&ToolEntry{Category: CatPackageManager, Name: "uv", Command: "uv", Languages: []string{"python"}, Status: StatusAvailable})
	inv.Add(&ToolEntry{Category: CatSearch, Name: "ripgrep", Command: "rg", Status: StatusAvailable})
	inv.Add(&ToolEntry{Category: CatLSP, Name: "gopls", Command: "gopls", Languages: []string{"go"}, Status: StatusAvailable})

	out := FormatEnvironmentPromptForLanguages(inv, []string{"typescript"})
	if !strings.Contains(out, "node") {
		t.Error("ts scope should include node")
	}
	if strings.Contains(out, "uv") || strings.Contains(out, "- go") {
		t.Errorf("ts scope must exclude python/go tools, got:\n%s", out)
	}
	if !strings.Contains(out, "rg") {
		t.Error("language-agnostic search tool should always be included")
	}
	// noise cuts
	if strings.Contains(out, "/Users/secret") {
		t.Error("absolute paths must not leak into the prompt")
	}
	if strings.Contains(out, "LSP Servers") || strings.Contains(out, "gopls") {
		t.Error("LSP servers must not appear in the run_shell Environment section")
	}
}

func keysOf(m map[string]Subproject) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func splitSlash(p string) []string { return strings.Split(p, "/") }
