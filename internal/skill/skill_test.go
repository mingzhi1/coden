package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFrontmatter(t *testing.T) {
	input := []byte(`---
name: test-skill
description: A test skill
when_to_use: When testing
allowed_tools:
  - read_file
  - grep
paths:
  - "**/*.go"
user_invocable: false
arguments:
  - file_path
context: full
effort: high
---
# Test Skill

This is the body content.
`)

	fm, body, err := parseFrontmatter(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fm.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", fm.Name, "test-skill")
	}
	if fm.Description != "A test skill" {
		t.Errorf("Description = %q, want %q", fm.Description, "A test skill")
	}
	if fm.WhenToUse != "When testing" {
		t.Errorf("WhenToUse = %q, want %q", fm.WhenToUse, "When testing")
	}
	if len(fm.AllowedTools) != 2 || fm.AllowedTools[0] != "read_file" || fm.AllowedTools[1] != "grep" {
		t.Errorf("AllowedTools = %v, want [read_file grep]", fm.AllowedTools)
	}
	if len(fm.Paths) != 1 || fm.Paths[0] != "**/*.go" {
		t.Errorf("Paths = %v, want [**/*.go]", fm.Paths)
	}
	if fm.UserInvocable == nil || *fm.UserInvocable != false {
		t.Errorf("UserInvocable = %v, want false", fm.UserInvocable)
	}
	if len(fm.Arguments) != 1 || fm.Arguments[0] != "file_path" {
		t.Errorf("Arguments = %v, want [file_path]", fm.Arguments)
	}
	if fm.Context != "full" {
		t.Errorf("Context = %q, want %q", fm.Context, "full")
	}
	if fm.Effort != "high" {
		t.Errorf("Effort = %q, want %q", fm.Effort, "high")
	}
	if !strings.Contains(body, "# Test Skill") {
		t.Errorf("body should contain '# Test Skill', got %q", body)
	}
	if !strings.Contains(body, "This is the body content.") {
		t.Errorf("body should contain 'This is the body content.', got %q", body)
	}
}

func TestParseFrontmatterEmpty(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"no frontmatter", "# Just Markdown\n\nSome content here."},
		{"empty string", ""},
		{"only content", "Hello world"},
		{"dashes but not frontmatter", "--- some text ---\nmore text"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fm, body, err := parseFrontmatter([]byte(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fm.Name != "" {
				t.Errorf("expected empty Name, got %q", fm.Name)
			}
			if tc.input != "" && body == "" {
				t.Errorf("expected non-empty body for non-empty input")
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// ** patterns
		{"**/*.go", "main.go", true},
		{"**/*.go", "src/main.go", true},
		{"**/*.go", "src/pkg/util.go", true},
		{"**/*.go", "README.md", false},
		{"**/*.go", "src/main.ts", false},

		// Simple extension patterns
		{"*.ts", "app.ts", true},
		{"*.ts", "src/app.ts", true}, // matches against basename
		{"*.ts", "app.go", false},

		// Directory prefix with **
		{"src/**", "src/main.go", true},
		{"src/**", "src/deep/nested/file.go", true},
		{"src/**", "pkg/main.go", false},

		// Exact match
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", true}, // matches basename

		// Directory + extension
		{"internal/**/*.go", "internal/skill/types.go", true},
		{"internal/**/*.go", "internal/types.go", true},
		{"internal/**/*.go", "external/skill/types.go", false},

		// Question mark wildcard
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
	}

	for _, tc := range tests {
		t.Run(tc.pattern+"_vs_"+tc.path, func(t *testing.T) {
			got := matchGlob(tc.pattern, tc.path)
			if got != tc.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestSkillMatchesPath(t *testing.T) {
	t.Run("no paths constraint matches everything", func(t *testing.T) {
		s := &Skill{
			Frontmatter: Frontmatter{Name: "universal"},
		}
		if !s.MatchesPath("anything.go") {
			t.Error("expected skill with no paths to match any file")
		}
		if !s.MatchesPath("deep/nested/file.ts") {
			t.Error("expected skill with no paths to match any nested file")
		}
	})

	t.Run("paths constraint filters correctly", func(t *testing.T) {
		s := &Skill{
			Frontmatter: Frontmatter{
				Name:  "go-only",
				Paths: []string{"**/*.go"},
			},
		}
		if !s.MatchesPath("main.go") {
			t.Error("expected match for main.go")
		}
		if !s.MatchesPath("internal/skill/types.go") {
			t.Error("expected match for internal/skill/types.go")
		}
		if s.MatchesPath("styles.css") {
			t.Error("expected no match for styles.css")
		}
	})

	t.Run("multiple paths patterns", func(t *testing.T) {
		s := &Skill{
			Frontmatter: Frontmatter{
				Name:  "web-skills",
				Paths: []string{"**/*.ts", "**/*.tsx", "**/*.css"},
			},
		}
		if !s.MatchesPath("app.ts") {
			t.Error("expected match for app.ts")
		}
		if !s.MatchesPath("component.tsx") {
			t.Error("expected match for component.tsx")
		}
		if !s.MatchesPath("styles.css") {
			t.Error("expected match for styles.css")
		}
		if s.MatchesPath("main.go") {
			t.Error("expected no match for main.go")
		}
	})
}

func TestSkillIsUserInvocable(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		s := &Skill{Frontmatter: Frontmatter{Name: "test"}}
		if !s.IsUserInvocable() {
			t.Error("expected nil UserInvocable to default to true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		b := true
		s := &Skill{Frontmatter: Frontmatter{Name: "test", UserInvocable: &b}}
		if !s.IsUserInvocable() {
			t.Error("expected true")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		b := false
		s := &Skill{Frontmatter: Frontmatter{Name: "test", UserInvocable: &b}}
		if s.IsUserInvocable() {
			t.Error("expected false")
		}
	})
}

func TestSkillMatchesAnyPath(t *testing.T) {
	s := &Skill{
		Frontmatter: Frontmatter{
			Name:  "go-only",
			Paths: []string{"**/*.go"},
		},
	}

	if !s.MatchesAnyPath([]string{"readme.md", "main.go"}) {
		t.Error("expected match when at least one path matches")
	}
	if s.MatchesAnyPath([]string{"readme.md", "styles.css"}) {
		t.Error("expected no match when no paths match")
	}
	if s.MatchesAnyPath(nil) {
		t.Error("expected no match when touched paths is nil and skill has paths constraint")
	}

	// Skill with no constraint
	sAll := &Skill{Frontmatter: Frontmatter{Name: "all"}}
	if !sAll.MatchesAnyPath([]string{"anything.txt"}) {
		t.Error("expected skill with no paths to match anything")
	}
}

func TestRegistryLoadAndGet(t *testing.T) {
	// Create a temp directory with skill subdirectories
	tmpDir := t.TempDir()

	// Create skill: my-skill
	skillDir := filepath.Join(tmpDir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := `---
name: my-skill
description: A test skill for registry
when_to_use: During tests
paths:
  - "**/*.go"
---
# My Skill

Follow these guidelines when writing Go code.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create skill: another-skill
	anotherDir := filepath.Join(tmpDir, "another-skill")
	if err := os.MkdirAll(anotherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	anotherContent := `---
name: another-skill
description: Another test skill
---
# Another Skill

More guidelines here.
`
	if err := os.WriteFile(filepath.Join(anotherDir, "SKILL.md"), []byte(anotherContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load into registry
	r := NewRegistry()
	err := r.LoadFromDir(tmpDir, SourceProject)
	if err != nil {
		t.Fatalf("LoadFromDir failed: %v", err)
	}

	if r.Count() != 2 {
		t.Errorf("Count() = %d, want 2", r.Count())
	}

	// Test Get
	s := r.Get("my-skill")
	if s == nil {
		t.Fatal("Get(my-skill) returned nil")
	}
	if s.Frontmatter.Name != "my-skill" {
		t.Errorf("Name = %q, want %q", s.Frontmatter.Name, "my-skill")
	}
	if s.Frontmatter.Description != "A test skill for registry" {
		t.Errorf("Description = %q, want %q", s.Frontmatter.Description, "A test skill for registry")
	}
	if s.LoadedFrom != SourceProject {
		t.Errorf("LoadedFrom = %q, want %q", s.LoadedFrom, SourceProject)
	}
	if !strings.Contains(s.Content, "# My Skill") {
		t.Errorf("Content should contain '# My Skill'")
	}

	// Test Get for another-skill
	a := r.Get("another-skill")
	if a == nil {
		t.Fatal("Get(another-skill) returned nil")
	}

	// Test Get for non-existent
	if r.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent skill")
	}
}

func TestRegistryLoadNonexistentDir(t *testing.T) {
	r := NewRegistry()
	err := r.LoadFromDir(filepath.Join(t.TempDir(), "does-not-exist"), SourceProject)
	if err != nil {
		t.Fatalf("expected nil error for nonexistent dir, got: %v", err)
	}
	if r.Count() != 0 {
		t.Errorf("Count() = %d, want 0", r.Count())
	}
}

func TestRegistryFormatForPrompt(t *testing.T) {
	r := NewRegistry()

	// Register two skills — one with paths constraint, one without
	r.Register(&Skill{
		Frontmatter: Frontmatter{
			Name:        "always-active",
			Description: "Always on",
		},
		Content:    "Always be helpful.",
		LoadedFrom: SourceBuiltin,
		LoadedAt:   time.Now(),
	})
	r.Register(&Skill{
		Frontmatter: Frontmatter{
			Name:        "go-skill",
			Description: "Go guidelines",
			Paths:       []string{"**/*.go"},
		},
		Content:    "Write idiomatic Go.",
		LoadedFrom: SourceProject,
		LoadedAt:   time.Now(),
	})

	// With a .go file touched — both should appear
	prompt := r.FormatForPrompt([]string{"main.go"})
	if !strings.Contains(prompt, "### Skill: always-active") {
		t.Error("prompt should contain always-active skill")
	}
	if !strings.Contains(prompt, "### Skill: go-skill") {
		t.Error("prompt should contain go-skill when .go file is touched")
	}
	if !strings.Contains(prompt, "Always be helpful.") {
		t.Error("prompt should contain always-active content")
	}
	if !strings.Contains(prompt, "Write idiomatic Go.") {
		t.Error("prompt should contain go-skill content")
	}
	if !strings.Contains(prompt, "_Go guidelines_") {
		t.Error("prompt should contain go-skill description in italics")
	}

	// With no .go files — only always-active
	promptNoGo := r.FormatForPrompt([]string{"styles.css"})
	if !strings.Contains(promptNoGo, "### Skill: always-active") {
		t.Error("prompt should contain always-active skill")
	}
	if strings.Contains(promptNoGo, "### Skill: go-skill") {
		t.Error("prompt should NOT contain go-skill when no .go file is touched")
	}
}

func TestRegistryFormatForPromptEmpty(t *testing.T) {
	r := NewRegistry()
	prompt := r.FormatForPrompt(nil)
	if prompt != "" {
		t.Errorf("expected empty prompt for empty registry, got %q", prompt)
	}
}

func TestRegistryActiveSkillsFiltering(t *testing.T) {
	r := NewRegistry()

	r.Register(&Skill{
		Frontmatter: Frontmatter{
			Name: "universal",
		},
		Content:    "Universal skill",
		LoadedFrom: SourceBuiltin,
		LoadedAt:   time.Now(),
	})
	r.Register(&Skill{
		Frontmatter: Frontmatter{
			Name:  "go-only",
			Paths: []string{"**/*.go"},
		},
		Content:    "Go skill",
		LoadedFrom: SourceProject,
		LoadedAt:   time.Now(),
	})
	r.Register(&Skill{
		Frontmatter: Frontmatter{
			Name:  "ts-only",
			Paths: []string{"**/*.ts", "**/*.tsx"},
		},
		Content:    "TypeScript skill",
		LoadedFrom: SourceProject,
		LoadedAt:   time.Now(),
	})
	r.Register(&Skill{
		Frontmatter: Frontmatter{
			Name:  "frontend",
			Paths: []string{"src/ui/**"},
		},
		Content:    "Frontend skill",
		LoadedFrom: SourceProject,
		LoadedAt:   time.Now(),
	})

	tests := []struct {
		name          string
		touchedPaths  []string
		wantSkills    []string
		notWantSkills []string
	}{
		{
			name:          "go files",
			touchedPaths:  []string{"internal/core/main.go"},
			wantSkills:    []string{"universal", "go-only"},
			notWantSkills: []string{"ts-only", "frontend"},
		},
		{
			name:          "ts files",
			touchedPaths:  []string{"app.ts"},
			wantSkills:    []string{"universal", "ts-only"},
			notWantSkills: []string{"go-only", "frontend"},
		},
		{
			name:          "mixed go and ts",
			touchedPaths:  []string{"main.go", "app.ts"},
			wantSkills:    []string{"universal", "go-only", "ts-only"},
			notWantSkills: []string{"frontend"},
		},
		{
			name:          "frontend tsx",
			touchedPaths:  []string{"src/ui/Button.tsx"},
			wantSkills:    []string{"universal", "ts-only", "frontend"},
			notWantSkills: []string{"go-only"},
		},
		{
			name:          "no touched paths",
			touchedPaths:  nil,
			wantSkills:    []string{"universal"},
			notWantSkills: []string{"go-only", "ts-only", "frontend"},
		},
		{
			name:          "unmatched file type",
			touchedPaths:  []string{"readme.md"},
			wantSkills:    []string{"universal"},
			notWantSkills: []string{"go-only", "ts-only", "frontend"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			active := r.GetSkillsForWorker(TargetCoder, tc.touchedPaths)
			activeNames := make(map[string]bool)
			for _, s := range active {
				activeNames[s.Frontmatter.Name] = true
			}

			for _, want := range tc.wantSkills {
				if !activeNames[want] {
					t.Errorf("expected skill %q to be active, but it wasn't. Active: %v", want, namesList(active))
				}
			}
			for _, notWant := range tc.notWantSkills {
				if activeNames[notWant] {
					t.Errorf("expected skill %q to NOT be active, but it was. Active: %v", notWant, namesList(active))
				}
			}
		})
	}
}

func TestRegistryRegisterAndListAll(t *testing.T) {
	r := NewRegistry()

	r.Register(&Skill{
		Frontmatter: Frontmatter{Name: "skill-a"},
		LoadedFrom:  SourceBuiltin,
		LoadedAt:    time.Now(),
	})
	r.Register(&Skill{
		Frontmatter: Frontmatter{Name: "skill-b"},
		LoadedFrom:  SourceProject,
		LoadedAt:    time.Now(),
	})

	all := r.ListAll()
	if len(all) != 2 {
		t.Fatalf("ListAll() returned %d skills, want 2", len(all))
	}

	names := make(map[string]bool)
	for _, s := range all {
		names[s.Frontmatter.Name] = true
	}
	if !names["skill-a"] || !names["skill-b"] {
		t.Errorf("expected both skill-a and skill-b in ListAll(), got %v", names)
	}
}

func TestRegistryRegisterOverwrite(t *testing.T) {
	r := NewRegistry()

	r.Register(&Skill{
		Frontmatter: Frontmatter{Name: "dup", Description: "first"},
		LoadedFrom:  SourceBuiltin,
		LoadedAt:    time.Now(),
	})
	r.Register(&Skill{
		Frontmatter: Frontmatter{Name: "dup", Description: "second"},
		LoadedFrom:  SourceProject,
		LoadedAt:    time.Now(),
	})

	if r.Count() != 1 {
		t.Errorf("Count() = %d, want 1 (duplicate should overwrite)", r.Count())
	}
	s := r.Get("dup")
	if s.Frontmatter.Description != "second" {
		t.Errorf("Description = %q, want %q (last registration wins)", s.Frontmatter.Description, "second")
	}
}

func TestLoadRulesFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Rules file without frontmatter
	rulesPath := filepath.Join(tmpDir, "RULES.md")
	rulesContent := `# Project Rules

- Always write tests
- Use descriptive variable names
`
	if err := os.WriteFile(rulesPath, []byte(rulesContent), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadRulesFile(rulesPath, SourceProject)
	if err != nil {
		t.Fatalf("LoadRulesFile failed: %v", err)
	}
	if s.Frontmatter.Name != "project-rules" {
		t.Errorf("Name = %q, want %q", s.Frontmatter.Name, "project-rules")
	}
	if !strings.Contains(s.Content, "Always write tests") {
		t.Error("content should contain rules text")
	}
	if s.LoadedFrom != SourceProject {
		t.Errorf("LoadedFrom = %q, want %q", s.LoadedFrom, SourceProject)
	}
}

func TestLoadRulesFileEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	rulesPath := filepath.Join(tmpDir, "RULES.md")
	if err := os.WriteFile(rulesPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRulesFile(rulesPath, SourceProject)
	if err == nil {
		t.Error("expected error for empty rules file")
	}
}

func TestParseSkillFileDeriveNameFromDir(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my-derived-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Skill file without name in frontmatter
	content := `---
description: Skill without explicit name
---
# Content

Body here.
`
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ParseSkillFile(skillPath, SourceProject)
	if err != nil {
		t.Fatalf("ParseSkillFile failed: %v", err)
	}
	if s.Frontmatter.Name != "my-derived-skill" {
		t.Errorf("Name = %q, want %q (derived from directory)", s.Frontmatter.Name, "my-derived-skill")
	}
}

func TestRegistryLoadRules(t *testing.T) {
	tmpDir := t.TempDir()
	rulesPath := filepath.Join(tmpDir, "RULES.md")
	if err := os.WriteFile(rulesPath, []byte("# Rules\nBe nice."), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	if err := r.LoadRules(rulesPath, SourceProject); err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}

	s := r.Get("project-rules")
	if s == nil {
		t.Fatal("expected project-rules skill to be registered")
	}
	if !strings.Contains(s.Content, "Be nice.") {
		t.Error("content should contain rules text")
	}
}

func TestBuiltinsRegistered(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	s := r.Get("coden-defaults")
	if s == nil {
		t.Fatal("expected coden-defaults builtin to be registered")
	}
	if s.LoadedFrom != SourceBuiltin {
		t.Errorf("LoadedFrom = %q, want %q", s.LoadedFrom, SourceBuiltin)
	}
	if !strings.Contains(s.Content, "Preserve existing style") {
		t.Error("content should contain default rules")
	}
}

// namesList is a helper for test output.
func namesList(skills []*Skill) []string {
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Frontmatter.Name
	}
	return names
}
