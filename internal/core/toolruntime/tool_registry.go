package toolruntime

import (
	"strings"
)

// M12-02: ToolRegistry catalogs all available tools with metadata about their
// deferral status, search hints, and concurrency properties.

// ToolMeta describes a single tool's metadata for the registry.
type ToolMeta struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parameters  string   `json:"parameters"`          // compact parameter spec
	Deferred    bool     `json:"deferred"`             // true = not injected into system prompt
	ReadOnly    bool     `json:"read_only"`            // true = no side effects
	Concurrent  bool     `json:"concurrent"`           // true = safe to run in parallel
	SearchHints []string `json:"search_hints"`         // keywords for tool_search matching
	Category    string   `json:"category,omitempty"`   // "core" | "lsp" | "rag" | "search" | "meta"
}

// ToolRegistry holds all known tools and provides queries for core vs deferred.
type ToolRegistry struct {
	tools map[string]ToolMeta
}

// NewToolRegistry creates a registry populated with built-in tool metadata.
func NewToolRegistry() *ToolRegistry {
	r := &ToolRegistry{tools: make(map[string]ToolMeta)}
	r.registerBuiltins()
	return r
}

// Get returns a tool's metadata by name.
func (r *ToolRegistry) Get(name string) (ToolMeta, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// ListCore returns all non-deferred (core) tools that should be injected
// into the system prompt.
func (r *ToolRegistry) ListCore() []ToolMeta {
	var out []ToolMeta
	for _, t := range r.tools {
		if !t.Deferred {
			out = append(out, t)
		}
	}
	return out
}

// ListDeferred returns all deferred tools.
func (r *ToolRegistry) ListDeferred() []ToolMeta {
	var out []ToolMeta
	for _, t := range r.tools {
		if t.Deferred {
			out = append(out, t)
		}
	}
	return out
}

// SearchDeferred searches deferred tools by query string, matching
// against name and search hints (case-insensitive substring match).
func (r *ToolRegistry) SearchDeferred(query string) []ToolMeta {
	q := strings.ToLower(query)
	var out []ToolMeta
	for _, t := range r.tools {
		if !t.Deferred {
			continue
		}
		if matchToolQuery(t, q) {
			out = append(out, t)
		}
	}
	return out
}

// SearchAll searches all tools by query (both core and deferred).
func (r *ToolRegistry) SearchAll(query string) []ToolMeta {
	q := strings.ToLower(query)
	var out []ToolMeta
	for _, t := range r.tools {
		if matchToolQuery(t, q) {
			out = append(out, t)
		}
	}
	return out
}

// All returns all registered tools.
func (r *ToolRegistry) All() []ToolMeta {
	out := make([]ToolMeta, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Register adds a tool to the registry (used for MCP/plugin tools).
func (r *ToolRegistry) Register(meta ToolMeta) {
	r.tools[meta.Name] = meta
}

// matchToolQuery checks if a tool matches a query against name + hints.
func matchToolQuery(t ToolMeta, q string) bool {
	if strings.Contains(strings.ToLower(t.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(t.Description), q) {
		return true
	}
	for _, hint := range t.SearchHints {
		if strings.Contains(strings.ToLower(hint), q) {
			return true
		}
	}
	return false
}
