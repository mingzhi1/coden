package inventory

import (
	"os"
	"path/filepath"
	"sort"
)

// languageIndicators maps file names (or glob patterns) found in the project
// root to the programming language(s) they indicate.
var languageIndicators = map[string][]string{
	// Go
	"go.mod": {"go"},
	"go.sum": {"go"},
	// JavaScript / TypeScript
	"package.json":      {"javascript", "typescript"},
	"tsconfig.json":     {"typescript"},
	"jsconfig.json":     {"javascript"},
	"package-lock.json": {"javascript", "typescript"},
	"yarn.lock":         {"javascript", "typescript"},
	"pnpm-lock.yaml":    {"javascript", "typescript"},
	// Python
	"pyproject.toml":   {"python"},
	"setup.py":         {"python"},
	"setup.cfg":        {"python"},
	"requirements.txt": {"python"},
	"Pipfile":          {"python"},
	"uv.lock":          {"python"},
	// Rust
	"Cargo.toml": {"rust"},
	"Cargo.lock": {"rust"},
	// Java / Kotlin
	"pom.xml":          {"java"},
	"build.gradle":     {"java"},
	"build.gradle.kts": {"java", "kotlin"},
	// Ruby
	"Gemfile":  {"ruby"},
	"Rakefile": {"ruby"},
	// C / C++
	"CMakeLists.txt":        {"c", "cpp"},
	"Makefile":              {"c", "cpp"},
	"compile_commands.json": {"c", "cpp"},
	// PHP
	"composer.json": {"php"},
	// .NET / C#
	// (*.csproj is handled via extension scanning below)
}

// extensionLanguages maps file extensions to languages. Used as a secondary
// signal when indicator files are not sufficient (e.g. no go.mod but .go files exist).
var extensionLanguages = map[string]string{
	".go":    "go",
	".py":    "python",
	".js":    "javascript",
	".jsx":   "javascript",
	".ts":    "typescript",
	".tsx":   "typescript",
	".rs":    "rust",
	".java":  "java",
	".kt":    "kotlin",
	".rb":    "ruby",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".cc":    "cpp",
	".hpp":   "cpp",
	".cs":    "csharp",
	".php":   "php",
	".swift": "swift",
	".lua":   "lua",
	".zig":   "zig",
	".dart":  "dart",
	".ex":    "elixir",
	".exs":   "elixir",
}

// DetectProjectLanguages scans the workspace root for indicator files and
// file extensions to determine which programming languages are used.
// It returns a deduplicated, sorted list of language identifiers.
func DetectProjectLanguages(workspaceRoot string) []string {
	seen := make(map[string]bool)

	// Phase 1: Check for well-known indicator files in root.
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if langs, ok := languageIndicators[e.Name()]; ok {
			for _, l := range langs {
				seen[l] = true
			}
		}
	}

	// Phase 2: If we already detected languages, we can skip extension scanning
	// for those languages. But we still scan top-level + one level deep for
	// any additional languages not covered by indicator files.
	scanExtensions(workspaceRoot, entries, seen)

	// Also check *.csproj, *.fsproj via glob-like matching
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if ext == ".csproj" || ext == ".fsproj" || ext == ".sln" {
			seen["csharp"] = true
		}
	}

	var langs []string
	for l := range seen {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

// scanExtensions checks file extensions in the workspace root and one level
// of subdirectories (src/, lib/, cmd/, etc.) for language indicators.
// It modifies the seen map in place.
func scanExtensions(root string, rootEntries []os.DirEntry, seen map[string]bool) {
	// Scan root-level files
	for _, e := range rootEntries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if lang, ok := extensionLanguages[ext]; ok {
			seen[lang] = true
		}
	}

	// Scan common source directories (one level deep only)
	commonDirs := []string{"src", "lib", "cmd", "pkg", "app", "internal", "test", "tests"}
	for _, dir := range commonDirs {
		subDir := filepath.Join(root, dir)
		subEntries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, e := range subEntries {
			if e.IsDir() {
				continue
			}
			ext := filepath.Ext(e.Name())
			if lang, ok := extensionLanguages[ext]; ok {
				seen[lang] = true
			}
		}
	}
}
