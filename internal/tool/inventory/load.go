package inventory

import (
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// embeddedCatalogYAML is the compiled-in default tool/language knowledge base.
// It is DATA, not code — adding a tool or a new language's toolchain means editing
// catalog.yaml (or an override file below), never touching Go.
//
//go:embed catalog.yaml
var embeddedCatalogYAML []byte

// LanguageMap is the data-driven language-detection table (layer 3): which project
// files (indicators) and file extensions signal which language(s).
type LanguageMap struct {
	Indicators map[string][]string `yaml:"indicators"`
	Extensions map[string]string   `yaml:"extensions"`
}

// Catalog is the merged tool + language knowledge base: the embedded defaults
// overlaid with user-global (~/.coden/catalog.yaml) and project-local
// (<workspace>/.coden/catalog.yaml) overrides. Because it is loaded as data, a
// brand-new language (nim, zig, crystal…) is supported by dropping a YAML file in,
// with no code change or recompile.
type Catalog struct {
	Languages LanguageMap     `yaml:"languages"`
	Tools     []ToolCandidate `yaml:"tools"`
}

var (
	baseCatalogOnce sync.Once
	baseCatalog     Catalog
	baseCatalogErr  error
)

func loadBaseCatalog() Catalog {
	baseCatalogOnce.Do(func() {
		baseCatalogErr = yaml.Unmarshal(embeddedCatalogYAML, &baseCatalog)
	})
	if baseCatalogErr != nil {
		slog.Error("[inventory] embedded catalog.yaml parse failed", "error", baseCatalogErr)
	}
	return baseCatalog
}

// LoadCatalog returns the embedded catalog merged with the user-global and
// project-local override files (if present). Override semantics: a tool with the
// same Name replaces the base entry; a new Name is appended; language indicator /
// extension keys are added or overridden. Missing or invalid override files are
// skipped (best-effort), so a typo in a user file never breaks discovery.
func LoadCatalog(workspaceRoot string) Catalog {
	merged := cloneCatalog(loadBaseCatalog())
	for _, p := range overridePaths(workspaceRoot) {
		ext, ok := readCatalogFile(p)
		if !ok {
			continue
		}
		mergeCatalog(&merged, ext)
		slog.Info("[inventory] merged catalog override", "path", p,
			"tools", len(ext.Tools), "indicators", len(ext.Languages.Indicators))
	}
	return merged
}

// overridePaths returns the user-global then project-local override file paths,
// in increasing precedence (project wins over user wins over embedded).
func overridePaths(workspaceRoot string) []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".coden", "catalog.yaml"))
	}
	if workspaceRoot != "" {
		paths = append(paths, filepath.Join(workspaceRoot, ".coden", "catalog.yaml"))
	}
	return paths
}

func readCatalogFile(path string) (Catalog, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, false // absent = fine
	}
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		slog.Warn("[inventory] invalid catalog override, ignoring", "path", path, "error", err)
		return Catalog{}, false
	}
	return c, true
}

// mergeCatalog overlays ext onto dst: tools override by Name (else append),
// language keys are added or overridden.
func mergeCatalog(dst *Catalog, ext Catalog) {
	idx := make(map[string]int, len(dst.Tools))
	for i, t := range dst.Tools {
		idx[t.Name] = i
	}
	for _, t := range ext.Tools {
		if i, ok := idx[t.Name]; ok {
			dst.Tools[i] = t
		} else {
			idx[t.Name] = len(dst.Tools)
			dst.Tools = append(dst.Tools, t)
		}
	}
	if len(ext.Languages.Indicators) > 0 {
		if dst.Languages.Indicators == nil {
			dst.Languages.Indicators = map[string][]string{}
		}
		for k, v := range ext.Languages.Indicators {
			dst.Languages.Indicators[k] = v
		}
	}
	if len(ext.Languages.Extensions) > 0 {
		if dst.Languages.Extensions == nil {
			dst.Languages.Extensions = map[string]string{}
		}
		for k, v := range ext.Languages.Extensions {
			dst.Languages.Extensions[k] = v
		}
	}
}

func cloneCatalog(c Catalog) Catalog {
	out := Catalog{
		Tools: append([]ToolCandidate(nil), c.Tools...),
		Languages: LanguageMap{
			Indicators: make(map[string][]string, len(c.Languages.Indicators)),
			Extensions: make(map[string]string, len(c.Languages.Extensions)),
		},
	}
	for k, v := range c.Languages.Indicators {
		out.Languages.Indicators[k] = append([]string(nil), v...)
	}
	for k, v := range c.Languages.Extensions {
		out.Languages.Extensions[k] = v
	}
	return out
}
