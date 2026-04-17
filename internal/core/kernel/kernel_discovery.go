package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// runDiscovery executes a lightweight exploration step after planning.
// For each task, it searches the workspace for relevant files based on
// the task title/goal, reads key file snippets, and populates
// DiscoveryContext so the Coder starts with real code understanding.
//
// This bridges the gap between Plan (high-level direction) and Code
// (concrete implementation): Plan says WHAT, Discovery finds WHERE.
func (k *Kernel) runDiscovery(ctx context.Context, sessionID, workflowID string, intent model.IntentSpec, tasks []model.Task) []model.FileSnippet {
	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "discovery",
		Status:     "running",
	})

	var snippets []model.FileSnippet
	seen := make(map[string]bool)

	// Strategy 1: Search for keywords from task titles in the codebase.
	for _, task := range tasks {
		keywords := extractSearchTerms(task.Title, intent.Goal)
		for _, kw := range keywords {
			results := k.searchWorkspace(ctx, kw)
			for _, r := range results {
				if seen[r] {
					continue
				}
				seen[r] = true
				snippet := k.readFileSnippet(ctx, r)
				snippets = append(snippets, snippet)
			}
		}
	}

	// Strategy 2: If tasks declared files (from Planner guess), validate them.
	for _, task := range tasks {
		for _, f := range task.Files {
			if seen[f] {
				continue
			}
			seen[f] = true
			snippet := k.readFileSnippet(ctx, f)
			snippets = append(snippets, snippet)
		}
	}

	// Cap total snippets to avoid token overflow.
	const maxSnippets = 10
	if len(snippets) > maxSnippets {
		snippets = snippets[:maxSnippets]
	}

	k.events.Emit(sessionID, model.EventWorkflowStepUpdate, model.WorkflowStepUpdatedPayload{
		WorkflowID: workflowID,
		Step:       "discovery",
		Status:     "done",
	})

	return snippets
}

// readFileSnippet reads the first maxLines of a file via workspace service.
func (k *Kernel) readFileSnippet(ctx context.Context, path string) model.FileSnippet {
	const maxSnippetBytes = 3000

	content, err := k.workspace.Read(path)
	if err != nil {
		return model.FileSnippet{Path: path, Exists: false}
	}

	lines := strings.Count(string(content), "\n") + 1
	text := string(content)
	if len(text) > maxSnippetBytes {
		text = text[:maxSnippetBytes] + fmt.Sprintf("\n... (truncated, %d lines total)", lines)
	}

	return model.FileSnippet{
		Path:    path,
		Content: text,
		Exists:  true,
		Lines:   lines,
	}
}

// searchWorkspace does a quick grep for a keyword using the tool runtime.
func (k *Kernel) searchWorkspace(ctx context.Context, keyword string) []string {
	if keyword == "" {
		return nil
	}

	result, err := k.tools.Execute(ctx, toolruntime.Request{
		Kind:    "search",
		Content: keyword,
	})
	if err != nil {
		return nil
	}

	// Parse search results: each line starts with "path:line:content".
	var paths []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(result.Output, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 2 {
			p := strings.TrimSpace(parts[0])
			if p != "" && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		if len(paths) >= 5 { // max 5 files per keyword
			break
		}
	}
	return paths
}

// extractSearchTerms pulls 1-3 meaningful keywords from the task title and goal.
func extractSearchTerms(title, goal string) []string {
	// Combine and deduplicate meaningful terms.
	combined := title + " " + goal
	combined = strings.ToLower(combined)

	// Filter out noise words.
	noise := map[string]bool{
		"the": true, "a": true, "an": true, "in": true, "to": true,
		"for": true, "of": true, "and": true, "or": true, "is": true,
		"it": true, "this": true, "that": true, "with": true, "from": true,
		"be": true, "on": true, "at": true, "as": true, "by": true,
		"add": true, "fix": true, "update": true, "implement": true,
		"create": true, "modify": true, "change": true, "make": true,
		"use": true, "ensure": true, "should": true, "will": true,
	}

	seen := make(map[string]bool)
	var terms []string
	for _, word := range strings.Fields(combined) {
		// Clean punctuation.
		word = strings.Trim(word, ".,;:!?()[]{}\"'`")
		if len(word) < 3 || noise[word] || seen[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, word)
		if len(terms) >= 3 {
			break
		}
	}
	return terms
}
