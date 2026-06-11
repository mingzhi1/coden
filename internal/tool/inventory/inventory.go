package inventory

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Status represents tool availability.
type Status string

const (
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable"
	StatusUnknown     Status = "unknown"
)

// Category classifies a tool's role.
type Category string

const (
	CatLSP            Category = "lsp"
	CatFormatter      Category = "formatter"
	CatLinter         Category = "linter"
	CatInterpreter    Category = "interpreter"
	CatPackageManager Category = "package_manager"
	CatSearch         Category = "search"
	CatBuiltin        Category = "builtin"
)

// ToolEntry is the discovered state of a single tool.
type ToolEntry struct {
	Category    Category  `json:"category"`
	Name        string    `json:"name"`
	Command     string    `json:"command"`
	Args        []string  `json:"args,omitempty"`
	Status      Status    `json:"status"`
	Version     string    `json:"version,omitempty"`
	Languages   []string  `json:"languages,omitempty"`
	Path        string    `json:"path,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	Error       string    `json:"error,omitempty"`
	InstallHint string    `json:"install_hint,omitempty"`
	Priority    int       `json:"priority,omitempty"`
}

// Inventory holds tool discovery results. It is thread-safe.
type Inventory struct {
	mu      sync.RWMutex
	entries map[string]*ToolEntry // key = "category:name"

	// Layer-4 long-tail signals: what the project uses that the catalog could NOT
	// fully serve, so the LLM can fall back to run_shell with its own knowledge.
	detectedLangs []string // all languages detected in the workspace
	unservedLangs []string // detected languages with zero available tools
	unknownExts   []string // source-like file extensions the catalog doesn't map

	// subprojects is the repo-wide module map from the initial tree walk (one entry
	// per manifest dir). It lets later steps project a task's toolchain by the
	// directory its files live in, instead of dumping every language's tools on
	// every task — the fix for multi-language monorepos.
	subprojects []Subproject
}

// New creates an empty Inventory.
func New() *Inventory {
	return &Inventory{
		entries: make(map[string]*ToolEntry),
	}
}

// SetLongTail records the layer-4 signals computed by Discover.
func (inv *Inventory) SetLongTail(detected, unserved, unknownExts []string) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.detectedLangs = detected
	inv.unservedLangs = unserved
	inv.unknownExts = unknownExts
}

// SetSubprojects records the repo-wide module map from the initial walk.
func (inv *Inventory) SetSubprojects(subs []Subproject) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.subprojects = subs
}

// Subprojects returns the repo-wide module map (one entry per manifest dir).
func (inv *Inventory) Subprojects() []Subproject {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return append([]Subproject(nil), inv.subprojects...)
}

// DetectedLanguages returns all languages detected in the workspace.
func (inv *Inventory) DetectedLanguages() []string {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return append([]string(nil), inv.detectedLangs...)
}

// UnservedLanguages returns detected languages with no available tooling.
func (inv *Inventory) UnservedLanguages() []string {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return append([]string(nil), inv.unservedLangs...)
}

// UnknownExtensions returns source-like extensions the catalog does not map.
func (inv *Inventory) UnknownExtensions() []string {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return append([]string(nil), inv.unknownExts...)
}

// entryKey builds the map key for a tool entry.
func entryKey(cat Category, name string) string {
	return string(cat) + ":" + name
}

// Add registers a tool entry. Overwrites any existing entry with the same key.
func (inv *Inventory) Add(entry *ToolEntry) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.entries[entryKey(entry.Category, entry.Name)] = entry
}

// Get returns a specific tool entry, or nil if not found.
func (inv *Inventory) Get(cat Category, name string) *ToolEntry {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return inv.entries[entryKey(cat, name)]
}

// All returns all entries sorted by category then name.
func (inv *Inventory) All() []*ToolEntry {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	out := make([]*ToolEntry, 0, len(inv.entries))
	for _, e := range inv.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Available returns only entries with StatusAvailable, sorted.
func (inv *Inventory) Available() []*ToolEntry {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	var out []*ToolEntry
	for _, e := range inv.entries {
		if e.Status == StatusAvailable {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Unavailable returns only entries with StatusUnavailable, sorted.
func (inv *Inventory) Unavailable() []*ToolEntry {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	var out []*ToolEntry
	for _, e := range inv.entries {
		if e.Status == StatusUnavailable {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ByCategory returns available entries of the given category, sorted by priority descending.
func (inv *Inventory) ByCategory(cat Category) []*ToolEntry {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	var out []*ToolEntry
	for _, e := range inv.entries {
		if e.Category == cat && e.Status == StatusAvailable {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Priority > out[j].Priority
	})
	return out
}

// HasCategory returns true if at least one available tool of the given category exists.
func (inv *Inventory) HasCategory(cat Category) bool {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	for _, e := range inv.entries {
		if e.Category == cat && e.Status == StatusAvailable {
			return true
		}
	}
	return false
}

// AvailableLanguages returns the set of languages that have at least one available LSP server.
func (inv *Inventory) AvailableLanguages() []string {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	langSet := make(map[string]bool)
	for _, e := range inv.entries {
		if e.Category == CatLSP && e.Status == StatusAvailable {
			for _, l := range e.Languages {
				langSet[l] = true
			}
		}
	}
	var langs []string
	for l := range langSet {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	return langs
}

// Summary returns a human-readable one-line summary.
func (inv *Inventory) Summary() string {
	total := len(inv.entries)
	avail := 0
	for _, e := range inv.entries {
		if e.Status == StatusAvailable {
			avail++
		}
	}
	return fmt.Sprintf("%d tools discovered, %d available, %d unavailable", total, avail, total-avail)
}
