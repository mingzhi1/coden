// Package retrieval provides unified evidence models for grep/LSP/RAG.
package retrieval

import (
	"fmt"
	"strings"
)

// RetrievalEvidence represents a single piece of evidence from any retrieval source.
// This is the unified format for grep, LSP, and RAG results.
type RetrievalEvidence struct {
	Source      string  `json:"source"`      // "grep" | "lsp" | "rag"
	Path        string  `json:"path"`        // File path (workspace-relative)
	Line        int     `json:"line"`        // 1-based line number
	Column      int     `json:"column"`      // 1-based column number (optional)
	Symbol      string  `json:"symbol"`      // Symbol name (for LSP)
	Snippet     string  `json:"snippet"`     // Code snippet with context
	Score       float64 `json:"score"`       // Relevance score (0-1)
	Stale       bool    `json:"stale"`       // True if file was modified after indexing
	Verified    bool    `json:"verified"`    // True for LSP results (structural truth)
	Explanation string  `json:"explanation"` // Why this result is relevant
}

// RetrievalResult is the unified output from any retrieval query.
type RetrievalResult struct {
	Query    string               `json:"query"`
	Strategy string               `json:"strategy"` // "grep" | "lsp" | "rag" | "identifier" | "semantic"
	Hits     []RetrievalEvidence  `json:"hits"`
	Duration int                  `json:"duration_ms"`
}

// FormatForPrompt formats evidence list as LLM-readable markdown.
func FormatForPrompt(hits []RetrievalEvidence) string {
	if len(hits) == 0 {
		return "*No results found.*"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Found %d result(s):**\n\n", len(hits)))

	for i, hit := range hits {
		// Header with source badge
		sourceBadge := fmt.Sprintf("[%s]", hit.Source)
		if hit.Verified {
			sourceBadge = "[✓ " + hit.Source + "]"
		}
		if hit.Stale {
			sourceBadge = "[stale " + hit.Source + "]"
		}

		location := hit.Path
		if hit.Line > 0 {
			location = fmt.Sprintf("%s:%d", hit.Path, hit.Line)
		}

		sb.WriteString(fmt.Sprintf("%d. %s `%s`\n", i+1, sourceBadge, location))

		// Symbol if available
		if hit.Symbol != "" {
			sb.WriteString(fmt.Sprintf("   **Symbol:** %s\n", hit.Symbol))
		}

		// Score for RAG
		if hit.Source == "rag" && hit.Score > 0 {
			sb.WriteString(fmt.Sprintf("   **Relevance:** %.2f\n", hit.Score))
		}

		// Snippet with code fence
		if hit.Snippet != "" {
			sb.WriteString("   ```\n")
			for _, line := range strings.Split(hit.Snippet, "\n") {
				sb.WriteString("   " + line + "\n")
			}
			sb.WriteString("   ```\n")
		}

		// Explanation
		if hit.Explanation != "" {
			sb.WriteString(fmt.Sprintf("   *%s*\n", hit.Explanation))
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

// GrepHit represents a raw result from grep/ripgrep.
type GrepHit struct {
	Path    string
	Line    int
	Column  int
	Snippet string
	Context []string // ±N context lines
}

// GrepHitsToEvidence converts grep results to unified evidence format.
func GrepHitsToEvidence(hits []GrepHit, query string) []RetrievalEvidence {
	evidence := make([]RetrievalEvidence, 0, len(hits))
	for _, hit := range hits {
		e := RetrievalEvidence{
			Source:   "grep",
			Path:     hit.Path,
			Line:     hit.Line,
			Column:   hit.Column,
			Snippet:  hit.Snippet,
			Verified: false, // grep is not verified
			Stale:    false,
		}
		if query != "" {
			e.Explanation = fmt.Sprintf("Text match for '%s'", query)
		}
		evidence = append(evidence, e)
	}
	return evidence
}

// LSPHit represents a raw result from LSP.
type LSPHit struct {
	Path   string
	Line   int
	Column int
	Symbol string
	Kind   string // "function", "variable", "type", etc.
}

// LSPHitsToEvidence converts LSP results to unified evidence format.
func LSPHitsToEvidence(hits []LSPHit, query string) []RetrievalEvidence {
	evidence := make([]RetrievalEvidence, 0, len(hits))
	for _, hit := range hits {
		e := RetrievalEvidence{
			Source:   "lsp",
			Path:     hit.Path,
			Line:     hit.Line,
			Column:   hit.Column,
			Symbol:   hit.Symbol,
			Verified: true, // LSP is verified structural truth
			Stale:    false,
		}
		if hit.Kind != "" {
			e.Explanation = fmt.Sprintf("LSP %s: %s", hit.Kind, query)
		} else {
			e.Explanation = query
		}
		evidence = append(evidence, e)
	}
	return evidence
}

// RAGChunk represents a semantic chunk from RAG.
type RAGChunk struct {
	Path      string
	StartLine int
	EndLine   int
	Content   string
	Score     float64
}

// RAGChunksToEvidence converts RAG results to unified evidence format.
func RAGChunksToEvidence(chunks []RAGChunk, query string) []RetrievalEvidence {
	evidence := make([]RetrievalEvidence, 0, len(chunks))
	for _, chunk := range chunks {
		e := RetrievalEvidence{
			Source:   "rag",
			Path:     chunk.Path,
			Line:     chunk.StartLine,
			Snippet:  chunk.Content,
			Score:    chunk.Score,
			Verified: false, // RAG is semantic, not verified
			Stale:    false,
		}
		e.Explanation = fmt.Sprintf("Semantic match (score %.2f) for '%s'", chunk.Score, query)
		evidence = append(evidence, e)
	}
	return evidence
}
