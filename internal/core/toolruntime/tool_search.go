package toolruntime

import (
	"fmt"
	"strings"
)

// M12-02c: tool_search is a meta-tool that lets the Coder discover deferred
// tools by keyword query. It is always available (non-deferred) and returns
// matching tool descriptions so the Coder can invoke them.

// executeToolSearch handles the "tool_search" tool kind.
// It searches the registry for deferred tools matching the query and returns
// their full descriptions including parameters.
func (r *LocalExecutor) executeToolSearch(query string) (Result, error) {
	if query == "" {
		return Result{
			Summary: "tool_search: query is required",
			Output:  "Please provide a search query describing what you want to do.",
		}, nil
	}

	reg := r.registry
	if reg == nil {
		reg = NewToolRegistry()
	}

	results := reg.SearchDeferred(query)

	if len(results) == 0 {
		return Result{
			Summary: fmt.Sprintf("tool_search: no matching tools for %q", query),
			Output: fmt.Sprintf("No additional tools found for %q. "+
				"Available core tools are always available: write_file, edit_file, read_file, list_dir, search, run_shell.", query),
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d additional tool(s) matching %q:\n\n", len(results), query))

	for i, tool := range results {
		sb.WriteString(fmt.Sprintf("## %d. %s\n", i+1, tool.Name))
		sb.WriteString(fmt.Sprintf("Description: %s\n", tool.Description))
		sb.WriteString(fmt.Sprintf("Parameters:  %s\n", tool.Parameters))
		if tool.ReadOnly {
			sb.WriteString("Read-only: yes (no side effects)\n")
		}
		if tool.Concurrent {
			sb.WriteString("Concurrent: yes (safe to run in parallel)\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("You can now use these tools by calling them with the parameters shown above.")

	return Result{
		Summary: fmt.Sprintf("found %d tool(s) for %q", len(results), query),
		Output:  sb.String(),
	}, nil
}
