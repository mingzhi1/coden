// Package inventory provides tool auto-discovery, project language detection,
// and config generation for the CodeN workspace.
package inventory

// ToolCandidate describes a tool that may exist on the system.
// The catalog contains well-known candidates; the discovery system probes only
// those relevant to the detected project languages.
//
// It is loaded from data (catalog.yaml + user/project overrides; see load.go)
// rather than hardcoded, so adding a tool — or a whole new language's toolchain —
// needs no code change. The yaml tags below are that data contract.
type ToolCandidate struct {
	Name           string   `yaml:"name"`                      // unique key, e.g. "gopls", "pylsp"
	Category       Category `yaml:"category"`                  // "lsp", "formatter", etc.
	Languages      []string `yaml:"languages,omitempty"`       // languages this tool serves
	Command        string   `yaml:"command"`                   // primary executable name
	Args           []string `yaml:"args,omitempty"`            // default args (e.g. LSP startup args)
	VersionCmd     []string `yaml:"version_cmd,omitempty"`     // command to check version, e.g. ["gopls","version"]
	VersionPattern string   `yaml:"version_pattern,omitempty"` // regex to extract version from output; empty = use raw
	InstallHint    string   `yaml:"install_hint,omitempty"`    // human-readable install instruction
	Priority       int      `yaml:"priority,omitempty"`        // higher = preferred when multiple candidates for same role
	Indicators     []string `yaml:"indicators,omitempty"`      // project files that signal relevance, e.g. ["go.mod"]
}

// BuiltinCatalog returns a copy of the embedded (default) tool candidate catalog,
// WITHOUT user/project overrides. Use LoadCatalog(workspaceRoot) for the merged
// set that discovery actually probes.
func BuiltinCatalog() []ToolCandidate {
	base := loadBaseCatalog().Tools
	out := make([]ToolCandidate, len(base))
	copy(out, base)
	return out
}

// FilterByLanguages returns candidates whose Languages are empty (always probe,
// e.g. search tools) or whose Languages overlap with the given set.
func FilterByLanguages(candidates []ToolCandidate, langs []string) []ToolCandidate {
	if len(langs) == 0 {
		return candidates // no filter = probe everything
	}
	langSet := make(map[string]bool, len(langs))
	for _, l := range langs {
		langSet[l] = true
	}

	var filtered []ToolCandidate
	for _, c := range candidates {
		if len(c.Languages) == 0 {
			// Language-agnostic tools (search tools) are always included
			filtered = append(filtered, c)
			continue
		}
		for _, lang := range c.Languages {
			if langSet[lang] {
				filtered = append(filtered, c)
				break
			}
		}
	}
	return filtered
}
