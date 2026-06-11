package inventory

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Subproject is one module within the workspace: a directory holding a build
// manifest (go.mod, package.json, Cargo.toml, …) and the language(s) it implies.
// A monorepo has several; a single-language repo has one (at "."). The map of
// subprojects is what lets a task that only touches web/ get web/'s toolchain,
// not the whole repo's (per-task projection).
type Subproject struct {
	Dir       string   `json:"dir"`       // workspace-relative dir ("." for root)
	Languages []string `json:"languages"` // languages implied by this dir's manifests
	Manifests []string `json:"manifests"` // indicator files found here, e.g. ["go.mod"]
}

// scanSkipDirs are directories never descended into during the initial workspace
// walk: dependency/build/cache trees that are huge and never the project's own
// source. Pruning them keeps the walk fast and the subproject map clean.
var scanSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "target": true, "dist": true,
	"build": true, "out": true, "bin": true, "obj": true,
	".venv": true, "venv": true, "__pycache__": true, ".tox": true,
	".next": true, ".nuxt": true, ".cache": true, ".gradle": true,
}

// maxScanDepth bounds how deep the initial walk descends, so a pathologically
// nested tree can't stall startup. 6 levels covers realistic monorepo layouts
// (services/api/internal/...), and deeper subprojects still contribute their
// language via the root-level extension fallback.
const maxScanDepth = 6

// DetectSubprojects walks the workspace tree (pruning dependency/build dirs and
// hidden dirs, bounded by maxScanDepth) and returns one Subproject per directory
// that holds a known build manifest. This is the initial, repo-wide map a
// monorepo needs: each module's location + language, so later steps project the
// right toolchain onto a task by its file paths instead of dumping every
// language's tools on every task.
func DetectSubprojects(workspaceRoot string, lm LanguageMap) []Subproject {
	byDir := map[string]*Subproject{}
	_ = filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, never abort the whole walk
		}
		if d.IsDir() {
			if path == workspaceRoot {
				return nil
			}
			base := d.Name()
			if scanSkipDirs[base] || strings.HasPrefix(base, ".") {
				return fs.SkipDir
			}
			if relDepth(workspaceRoot, path) > maxScanDepth {
				return fs.SkipDir
			}
			return nil
		}
		// A file: record it against its directory if it's a known manifest.
		langs, ok := lm.Indicators[d.Name()]
		if !ok {
			return nil
		}
		dir := filepath.Dir(path)
		sp := byDir[dir]
		if sp == nil {
			rel, _ := filepath.Rel(workspaceRoot, dir)
			sp = &Subproject{Dir: filepath.ToSlash(rel)}
			byDir[dir] = sp
		}
		sp.Manifests = append(sp.Manifests, d.Name())
		for _, l := range langs {
			if !strIn(sp.Languages, l) {
				sp.Languages = append(sp.Languages, l)
			}
		}
		return nil
	})

	subs := make([]Subproject, 0, len(byDir))
	for _, sp := range byDir {
		sort.Strings(sp.Languages)
		sort.Strings(sp.Manifests)
		subs = append(subs, *sp)
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].Dir < subs[j].Dir })
	return subs
}

// SubprojectLanguagesForFiles projects a task onto the subproject map: for each
// file it finds the NEAREST enclosing subproject (longest matching Dir) and unions
// their languages. This is what lets a task that only touches web/ get just the JS
// toolchain instead of the whole monorepo's. Returns nil (→ caller falls back to
// the full repo view) when files is empty or none map to a known subproject.
func SubprojectLanguagesForFiles(subs []Subproject, files []string) []string {
	if len(files) == 0 || len(subs) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	for _, f := range files {
		f = filepath.ToSlash(f)
		best, bestLen := -1, -1
		for i, sp := range subs {
			if isAncestorDir(sp.Dir, f) && len(sp.Dir) > bestLen {
				best, bestLen = i, len(sp.Dir)
			}
		}
		if best >= 0 {
			for _, l := range subs[best].Languages {
				seen[l] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// isAncestorDir reports whether dir encloses file (both forward-slash, workspace
// relative). "." (the repo root subproject) encloses everything.
func isAncestorDir(dir, file string) bool {
	if dir == "." || dir == "" {
		return true
	}
	return file == dir || strings.HasPrefix(file, dir+"/")
}

// relDepth returns how many directory levels path is below root.
func relDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// strIn reports membership (small, local helper).
func strIn(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// DetectProjectLanguages returns the deduplicated, sorted set of languages used
// anywhere in the workspace — the union of every subproject's manifest languages
// (deep tree walk) plus a root extension scan for manifest-less languages.
func DetectProjectLanguages(workspaceRoot string) []string {
	langs, _ := detectProject(workspaceRoot, LoadCatalog(workspaceRoot).Languages)
	return langs
}

// detectProject performs the initial repo-wide detection in one pass: walk the
// tree for subprojects (manifest dirs → languages), then add any manifest-less
// languages seen by extension at the root. Returns both the language set and the
// subproject map so the caller can store the latter for per-task projection.
func detectProject(root string, lm LanguageMap) (langs []string, subs []Subproject) {
	subs = DetectSubprojects(root, lm)
	seen := make(map[string]bool)
	for _, sp := range subs {
		for _, l := range sp.Languages {
			seen[l] = true
		}
	}
	// Extension fallback: languages with source files but no manifest (e.g. a repo
	// of loose .py scripts, or a long-tail language). Reuses the root + common-dir
	// scan so behavior for simple repos is unchanged.
	if entries, err := os.ReadDir(root); err == nil {
		scanExtensions(root, entries, lm.Extensions, seen)
		for _, e := range entries {
			switch filepath.Ext(e.Name()) {
			case ".csproj", ".fsproj", ".sln":
				seen["csharp"] = true
			}
		}
	}
	for l := range seen {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs, subs
}

// detectLanguages is the data-driven core: it consults the given LanguageMap
// (loaded from catalog.yaml + overrides) instead of any hardcoded table, so a new
// language is recognized by adding YAML, not editing Go.
func detectLanguages(workspaceRoot string, lm LanguageMap) []string {
	seen := make(map[string]bool)

	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil
	}
	// Phase 1: well-known indicator files in root.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if langs, ok := lm.Indicators[e.Name()]; ok {
			for _, l := range langs {
				seen[l] = true
			}
		}
	}

	// Phase 2: extension scan (root + one level of common source dirs).
	scanExtensions(workspaceRoot, entries, lm.Extensions, seen)

	// Phase 3: .NET project/solution files (glob-ish, kept in code).
	for _, e := range entries {
		switch filepath.Ext(e.Name()) {
		case ".csproj", ".fsproj", ".sln":
			seen["csharp"] = true
		}
	}

	langs := make([]string, 0, len(seen))
	for l := range seen {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

// commonSourceDirs are the one-level-deep directories scanned for extensions.
var commonSourceDirs = []string{"src", "lib", "cmd", "pkg", "app", "internal", "test", "tests"}

// scanExtensions checks file extensions in the workspace root and one level of
// common source directories against the extension→language table, in place.
func scanExtensions(root string, rootEntries []os.DirEntry, extMap map[string]string, seen map[string]bool) {
	for _, e := range rootEntries {
		if e.IsDir() {
			continue
		}
		if lang, ok := extMap[filepath.Ext(e.Name())]; ok {
			seen[lang] = true
		}
	}
	for _, dir := range commonSourceDirs {
		subEntries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue
		}
		for _, e := range subEntries {
			if e.IsDir() {
				continue
			}
			if lang, ok := extMap[filepath.Ext(e.Name())]; ok {
				seen[lang] = true
			}
		}
	}
}

// nonSourceExtensions are extensions that look like source but aren't, so the
// long-tail scan (layer 4) doesn't surface config/doc/data/build noise as an
// "unknown language" signal.
var nonSourceExtensions = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".rst": true, ".adoc": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".cfg": true, ".conf": true, ".xml": true, ".csv": true, ".tsv": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".less": true,
	".lock": true, ".sum": true, ".mod": true, ".work": true, ".sql": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".pdf": true, ".zip": true, ".tar": true, ".gz": true, ".ico": true,
	".log": true, ".env": true, ".gitignore": true, ".dockerignore": true,
	".sh": true, ".bash": true, ".zsh": true, ".bat": true, ".ps1": true,
	".example": true, ".sample": true, ".tmpl": true, ".tpl": true, ".in": true,
}

// ScanUnknownExtensions returns source-like file extensions present in the
// workspace that the catalog does NOT map to any language (layer 4 long-tail
// signal). It is how a project in an unconfigured language (nim, crystal, …) is
// still surfaced to the LLM: "I see .nim files, no tooling configured — use
// run_shell with your knowledge." Capped and sorted for determinism.
func ScanUnknownExtensions(workspaceRoot string, extMap map[string]string) []string {
	seen := map[string]bool{}
	collect := func(entries []os.DirEntry) {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == "" || len(ext) > 7 {
				continue
			}
			if _, known := extMap[ext]; known {
				continue
			}
			if nonSourceExtensions[ext] {
				continue
			}
			seen[ext] = true
		}
	}
	rootEntries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil
	}
	collect(rootEntries)
	for _, dir := range commonSourceDirs {
		if subEntries, err := os.ReadDir(filepath.Join(workspaceRoot, dir)); err == nil {
			collect(subEntries)
		}
	}

	out := make([]string, 0, len(seen))
	for ext := range seen {
		out = append(out, ext)
	}
	sort.Strings(out)
	const maxExts = 8 // bound the signal; a handful is enough to inform the LLM
	if len(out) > maxExts {
		out = out[:maxExts]
	}
	return out
}
