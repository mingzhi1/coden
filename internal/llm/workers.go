// Package llm provides LLM-backed workflow worker implementations.
// This file contains shared utilities and JSON parsing helpers used across all workers.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/tokenbudget"
)

// contextSummary formats the WorkflowContext as a compact string for LLM prompts.
// Budget allocation: FileTree 30%, History 60%, Retry 10%.
func contextSummary(ctx context.Context) string {
	wc := model.WorkflowContextFrom(ctx)
	var sb strings.Builder

	const contextBudget = 30000
	fileTreeBudget := contextBudget * 30 / 100
	historyBudget := contextBudget * 60 / 100
	retryBudget := contextBudget * 10 / 100

	if len(wc.FileTree) > 0 {
		kept, _ := tokenbudget.TruncateFileTree(wc.FileTree, fileTreeBudget)
		if len(kept) > 0 {
			sb.WriteString("## Workspace files\n")
			for _, f := range kept {
				sb.WriteString("- ")
				sb.WriteString(f)
				sb.WriteString("\n")
			}
			if len(kept) < len(wc.FileTree) {
				sb.WriteString(fmt.Sprintf("... (%d more files)\n", len(wc.FileTree)-len(kept)))
			}
		}
	}
	// M8-04: Inject git state (branch, uncommitted changes, diff stat, recent commits).
	if wc.GitStatus != "" {
		sb.WriteString("\n")
		sb.WriteString(wc.GitStatus)
	}
	// M8-11: Inject pre-formatted top insights from InsightStore.
	if wc.TopInsights != "" {
		sb.WriteString("\n")
		sb.WriteString(wc.TopInsights)
	}
	// M8-08: Inject structured previous-turn summaries.
	if len(wc.PreviousTurns) > 0 {
		sb.WriteString("\n## Previous turns\n")
		for i, t := range wc.PreviousTurns {
			sb.WriteString(fmt.Sprintf("### Turn %d\n", i+1))
			sb.WriteString(fmt.Sprintf("Intent: %s\n", t.Intent.Goal))
			if len(t.TaskResults) > 0 {
				sb.WriteString("Tasks:\n")
				for _, tr := range t.TaskResults {
					sb.WriteString(fmt.Sprintf("  - [%s] %s\n", tr.Status, tr.Title))
				}
			}
			if len(t.ChangedFiles) > 0 {
				sb.WriteString("Files changed:\n")
				for _, fc := range t.ChangedFiles {
					sb.WriteString(fmt.Sprintf("  - %s (%s)\n", fc.Path, fc.Op))
				}
			}
			sb.WriteString(fmt.Sprintf("Outcome: %s\n", t.Checkpoint.Status))
		}
	}
	// M8-08: Accumulated file changes across all known turns.
	if len(wc.AccumChanges) > 0 {
		sb.WriteString("\n## Previously modified files\n")
		for _, fc := range wc.AccumChanges {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", fc.Path, fc.Op))
		}
	}
	// M10-04: Pre-fetched code snippets from the discovery step.
	// Budget: 30% of contextBudget (9000 tokens) shared across all snippets.
	// Individual snippets are already capped at ~3000 bytes by readFileSnippet,
	// so we only need the aggregate guard here.
	if len(wc.DiscoveryContext) > 0 {
		const discoveryBudget = contextBudget * 30 / 100 // 9000 tokens total
		discoveryUsed := 0
		sb.WriteString("\n## Discovered code (pre-read)\n")
		for _, s := range wc.DiscoveryContext {
			if !s.Exists {
				sb.WriteString(fmt.Sprintf("### %s (not found)\n", s.Path))
				continue
			}
			snippet := fmt.Sprintf("### %s (%d lines)\n```\n%s\n```\n", s.Path, s.Lines, s.Content)
			snippetTokens := tokenbudget.EstimateTokens(snippet)
			if discoveryUsed+snippetTokens > discoveryBudget {
				break
			}
			sb.WriteString(snippet)
			discoveryUsed += snippetTokens
		}
	}
	if len(wc.Discovery.Evidence) > 0 {
		const evidenceBudget = contextBudget * 10 / 100 // 3000 tokens total
		evidenceUsed := 0
		sb.WriteString("\n## Discovery metadata\n")
		if wc.Discovery.Query != "" {
			sb.WriteString("- query: " + wc.Discovery.Query + "\n")
		}
		if wc.Discovery.QueryID != "" {
			sb.WriteString("- query_id: " + wc.Discovery.QueryID + "\n")
		}
		if wc.Discovery.Confidence > 0 {
			sb.WriteString(fmt.Sprintf("- confidence: %.2f\n", wc.Discovery.Confidence))
		}
		if len(wc.DirtyPaths) > 0 {
			sb.WriteString(fmt.Sprintf("- dirty_paths: %d\n", len(wc.DirtyPaths)))
		}
		sb.WriteString("\n## Discovery evidence\n")
		for _, e := range wc.Discovery.Evidence {
			line := "- [" + e.Source + "] " + e.Path
			if e.Line > 0 {
				line += fmt.Sprintf(":%d", e.Line)
			}
			if e.Symbol != "" {
				line += " symbol=" + e.Symbol
			}
			if e.Explanation != "" {
				line += " — " + e.Explanation
			}
			line += "\n"
			lineTokens := tokenbudget.EstimateTokens(line)
			if evidenceUsed+lineTokens > evidenceBudget {
				break
			}
			sb.WriteString(line)
			evidenceUsed += lineTokens
		}
	}
	if len(wc.DirtyPaths) > 0 {
		sb.WriteString("\n## Dirty workspace paths\n")
		for _, path := range wc.DirtyPaths {
			sb.WriteString("- ")
			sb.WriteString(path)
			sb.WriteString("\n")
		}
	}
	if len(wc.History) > 0 {
		histStrs := make([]string, len(wc.History))
		for i, msg := range wc.History {
			histStrs[i] = msg.Role + ": " + msg.Content
		}
		kept, _ := tokenbudget.TruncateHistory(histStrs, historyBudget)
		if len(kept) > 0 {
			sb.WriteString("\n## Recent conversation\n")
			if len(kept) < len(wc.History) {
				sb.WriteString(fmt.Sprintf("(showing %d of %d messages)\n", len(kept), len(wc.History)))
			}
			for _, s := range kept {
				sb.WriteString(s)
				sb.WriteString("\n")
			}
		}
	}
	if wc.RetryFeedback != "" {
		feedback := wc.RetryFeedback
		if tokenbudget.EstimateTokens(feedback) > retryBudget {
			maxChars := retryBudget * 4
			if maxChars < len(feedback) {
				feedback = feedback[:maxChars] + "\n... (truncated)"
			}
		}
		sb.WriteString("\n## ⚠️ RETRY: Previous attempt was rejected\n")
		sb.WriteString(feedback)
		sb.WriteString("\n")
	}
	// Secretary Agent: inject authorized extension context (Skills, RULES.md, etc.)
	if wc.SecretaryContext != "" {
		sb.WriteString("\n")
		sb.WriteString(wc.SecretaryContext)
	}
	// MCP tool descriptions: inject pre-formatted MCP tool definitions for Coder.
	if wc.MCPToolDescriptions != "" {
		sb.WriteString("\n")
		sb.WriteString(wc.MCPToolDescriptions)
	}
	// Environment info from inventory discovery (interpreters, formatters, etc.)
	if wc.EnvironmentPrompt != "" {
		sb.WriteString("\n")
		sb.WriteString(wc.EnvironmentPrompt)
	}
	return sb.String()
}

// --- shared message buffer ---

type msgBuffer struct {
	mu       sync.Mutex
	messages []model.WorkerMessage
}

func (b *msgBuffer) push(kind, role, content string) {
	b.mu.Lock()
	b.messages = append(b.messages, model.WorkerMessage{
		Kind:    kind,
		Role:    role,
		Content: content,
	})
	b.mu.Unlock()
}

func (b *msgBuffer) TakeMessages() []model.WorkerMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := append([]model.WorkerMessage(nil), b.messages...)
	b.messages = nil
	return out
}

// --- text helpers ---

// truncateToLines returns text capped at n lines. Lines beyond n are dropped.
func truncateToLines(text string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.SplitN(text, "\n", n+1)
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n")
}

func bulletList(items []string) string {
	var b strings.Builder
	for _, s := range items {
		b.WriteString("- ")
		b.WriteString(s)
		b.WriteString("\n")
	}
	return b.String()
}

// --- JSON / plan parsing ---

type codePlanReply struct {
	Files     []codePlanFile     `json:"files"`
	ToolCalls []codePlanToolCall `json:"tool_calls"`
}

type codePlanFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type codePlanToolCall struct {
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	Content    string `json:"content,omitempty"`
	Dir        string `json:"dir,omitempty"`
	Query      string `json:"query,omitempty"`
	Command    string `json:"command,omitempty"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
	// Search options (R-01, R-02, R-03)
	IsRegex      bool `json:"is_regex,omitempty"`
	Line         int  `json:"line,omitempty"`
	ContextLines int  `json:"context_lines,omitempty"`
	// LSP options (R-06)
	Column int `json:"column,omitempty"`
	// RAG options (R-10)
	TopK int `json:"top_k,omitempty"`
	// Shell options (C-02)
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

func parseCodePlanReply(workflowID, intentID, goal, reply string) workflow.CodePlan {
	toolCalls := parsePlanToolCalls(workflowID, reply)
	if len(toolCalls) > 0 {
		first := toolCalls[0]
		return workflow.CodePlan{
			ToolCalls:  toolCalls,
			ToolCallID: first.ToolCallID,
			Request:    first.Request,
		}
	}

	files := parsePlanFiles(reply)
	if len(files) == 0 {
		content := strings.TrimSpace(reply)
		if content == "" {
			content = fmt.Sprintf("# %s\n\nNo content generated.", goal)
		}
		files = []codePlanFile{{
			Path:    fmt.Sprintf("artifacts/%s.md", intentID),
			Content: content,
		}}
	}

	toolCalls = make([]workflow.ToolCall, 0, len(files))
	for i, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			path = defaultFilePath(intentID, i)
		}
		toolCalls = append(toolCalls, workflow.ToolCall{
			ToolCallID: fmt.Sprintf("tool-%s-write-file-%d", workflowID, i+1),
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    path,
				Content: file.Content,
			},
		})
	}

	first := toolCalls[0]
	return workflow.CodePlan{
		ToolCalls:  toolCalls,
		ToolCallID: first.ToolCallID,
		Request:    first.Request,
	}
}

func refineCodePlanWithContext(ctx context.Context, workflowID string, plan workflow.CodePlan) workflow.CodePlan {
	calls := plan.Calls()
	if len(calls) == 0 || hasDiscoveryBeforeFirstWrite(calls) {
		return plan
	}

	firstWrite := firstNonArtifactWrite(calls)
	if firstWrite == nil {
		return plan
	}

	wc := model.WorkflowContextFrom(ctx)
	discovery := workflow.ToolCall{
		ToolCallID: fmt.Sprintf("tool-%s-discover-1", workflowID),
		Request: toolruntime.Request{
			Kind: "list_dir",
			Dir:  "",
		},
	}

	target := strings.TrimSpace(firstWrite.Request.Path)
	if target != "" && fileExistsInContext(wc.FileTree, target) {
		discovery.Request = toolruntime.Request{
			Kind: "read_file",
			Path: target,
		}
	}

	refined := append([]workflow.ToolCall{discovery}, calls...)
	return workflow.CodePlan{
		ToolCalls:  refined,
		ToolCallID: refined[0].ToolCallID,
		Request:    refined[0].Request,
	}
}

func firstNonArtifactWrite(calls []workflow.ToolCall) *workflow.ToolCall {
	for i := range calls {
		call := &calls[i]
		if call.Request.Kind != "write_file" {
			continue
		}
		path := strings.TrimSpace(call.Request.Path)
		if path == "" || strings.HasPrefix(path, "artifacts/") {
			continue
		}
		return call
	}
	return nil
}

func hasDiscoveryBeforeFirstWrite(calls []workflow.ToolCall) bool {
	for _, call := range calls {
		switch call.Request.Kind {
		case "read_file", "search", "list_dir", "grep_context",
			"lsp_symbols", "lsp_definition", "lsp_references",
			"rag_search":
			return true
		case "write_file":
			path := strings.TrimSpace(call.Request.Path)
			if path == "" || strings.HasPrefix(path, "artifacts/") {
				continue
			}
			return false
		}
	}
	return false
}

func fileExistsInContext(files []string, path string) bool {
	path = strings.TrimSpace(path)
	for _, file := range files {
		if strings.TrimSpace(file) == path {
			return true
		}
	}
	return false
}

func parsePlanToolCalls(workflowID, reply string) []workflow.ToolCall {
	raw := []byte(extractJSON(reply))

	var object codePlanReply
	if err := json.Unmarshal(raw, &object); err == nil && len(object.ToolCalls) > 0 {
		return normalizePlanToolCalls(workflowID, object.ToolCalls)
	}

	var array []codePlanToolCall
	if err := json.Unmarshal(raw, &array); err == nil && len(array) > 0 {
		return normalizePlanToolCalls(workflowID, array)
	}

	return nil
}

func parsePlanFiles(reply string) []codePlanFile {
	raw := []byte(extractJSON(reply))

	var object codePlanReply
	if err := json.Unmarshal(raw, &object); err == nil && len(object.Files) > 0 {
		return normalizePlanFiles(object.Files)
	}

	var array []codePlanFile
	if err := json.Unmarshal(raw, &array); err == nil && len(array) > 0 {
		return normalizePlanFiles(array)
	}

	return nil
}

func normalizePlanFiles(files []codePlanFile) []codePlanFile {
	out := make([]codePlanFile, 0, len(files))
	for _, file := range files {
		content := strings.TrimSpace(file.Content)
		if content == "" {
			continue
		}
		out = append(out, codePlanFile{
			Path:    strings.TrimSpace(file.Path),
			Content: content,
		})
	}
	return out
}

func normalizePlanToolCalls(workflowID string, calls []codePlanToolCall) []workflow.ToolCall {
	out := make([]workflow.ToolCall, 0, len(calls))
	for i, call := range calls {
		kind := strings.TrimSpace(call.Kind)
		switch kind {
		case "write_file", "read_file", "search", "list_dir", "edit_file", "run_shell":
		case "grep_context": // R-02
		case "lsp_symbols", "lsp_definition", "lsp_references": // R-06
		case "rag_search":    // R-10
		case "tool_search":   // M12-02: meta-tool for discovering deferred tools
		case "web_fetch":     // web content fetcher
		default:
			continue
		}

		req := toolruntime.Request{
			Kind:         kind,
			Path:         strings.TrimSpace(call.Path),
			Content:      call.Content,
			Dir:          strings.TrimSpace(call.Dir),
			Query:        strings.TrimSpace(call.Query),
			Command:      strings.TrimSpace(call.Command),
			OldContent:   call.OldContent,
			NewContent:   call.NewContent,
			IsRegex:      call.IsRegex,      // R-03: regex support
			Line:         call.Line,         // R-02: line for context
			ContextLines: call.ContextLines, // R-02: context lines
			Column:       call.Column,       // R-06: LSP column
			TopK:         call.TopK,         // R-10: RAG top_k
			TimeoutSec:   call.TimeoutSec,   // C-02: shell timeout
		}
		if kind == "write_file" && strings.TrimSpace(req.Content) == "" {
			continue
		}
		if (kind == "write_file" || kind == "read_file" || kind == "edit_file") && req.Path == "" {
			continue
		}
		if kind == "edit_file" && req.OldContent == "" {
			continue
		}
		if kind == "search" && req.Query == "" && strings.TrimSpace(req.Content) == "" {
			continue
		}
		if kind == "run_shell" && req.Command == "" && strings.TrimSpace(req.Content) == "" {
			continue
		}
		// R-06: LSP tools require path; definition/references also need line.
		if (kind == "lsp_symbols" || kind == "lsp_definition" || kind == "lsp_references") && req.Path == "" {
			continue
		}
		if (kind == "lsp_definition" || kind == "lsp_references") && req.Line == 0 {
			continue
		}
		// R-10: rag_search requires query.
		if kind == "rag_search" && req.Query == "" {
			continue
		}
		// M12-02: tool_search requires query.
		if kind == "tool_search" && req.Query == "" {
			continue
		}
		// web_fetch requires path (URL).
		if kind == "web_fetch" && req.Path == "" && req.Query == "" {
			continue
		}

		out = append(out, workflow.ToolCall{
			ToolCallID: fmt.Sprintf("tool-%s-%s-%d", workflowID, kind, i+1),
			Request:    req,
		})
	}
	return out
}

func defaultFilePath(intentID string, index int) string {
	if index == 0 {
		return fmt.Sprintf("artifacts/%s.md", intentID)
	}
	return fmt.Sprintf("artifacts/%s_%d.md", intentID, index+1)
}

// extractJSON finds the first JSON object or array in s and returns it exactly,
// stripping any surrounding prose, markdown fences, or trailing content.
// It uses json.Decoder so that only the valid JSON span is captured, which
// prevents json.Unmarshal from failing on trailing backticks or explanatory text.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip a leading code fence (```json … ``` or ``` … ```)
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = strings.TrimSpace(lines[1])
		}
		// Remove a trailing fence that may or may not have trailing whitespace
		if idx := strings.LastIndex(s, "```"); idx > 0 {
			s = strings.TrimSpace(s[:idx])
		}
	}
	// Walk forward to the first { or [, then use json.Decoder to consume
	// exactly one well-formed JSON value — this discards any trailing prose.
	for i, r := range s {
		if r == '{' || r == '[' {
			dec := json.NewDecoder(strings.NewReader(s[i:]))
			var raw json.RawMessage
			if err := dec.Decode(&raw); err == nil {
				return string(raw)
			}
			// Fallback: return from the opening brace even if malformed,
			// so downstream parsers can produce a better error message.
			return s[i:]
		}
	}
	return s
}
