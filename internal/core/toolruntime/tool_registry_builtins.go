package toolruntime

import "maps"

// registerBuiltins populates the registry from the static builtinTools catalog
// (see catalog.go — the single source of truth for built-in tool metadata).
// Core tools (Deferred=false) are always injected into the system prompt;
// deferred tools are only discovered via tool_search.
func (r *ToolRegistry) registerBuiltins() {
	maps.Copy(r.tools, builtinTools)
}
