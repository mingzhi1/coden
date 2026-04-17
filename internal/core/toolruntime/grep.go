// Package toolruntime provides grep functionality using ripgrep.
package toolruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mingzhi1/coden/internal/core/retrieval"
)

// ripText is the {text: "..."} wrapper used by rg --json for paths and line content.
type ripText struct {
	Text string `json:"text"`
}

// RipgrepResult represents a single line of ripgrep --json output.
type RipgrepResult struct {
	Type string `json:"type"`
	Data struct {
		Path       ripText `json:"path,omitempty"`
		Lines      ripText `json:"lines,omitempty"`
		LineNumber int     `json:"line_number,omitempty"`
		Submatches []struct {
			Match struct {
				Text string `json:"text"`
			} `json:"match"`
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches,omitempty"`
	} `json:"data,omitempty"`
}

// GrepHit represents a processed search result.
type GrepHit struct {
	Path      string
	Line      int
	Column    int
	Snippet   string
	MatchText string
	BeforeCtx []string
	AfterCtx  []string
}

// SearchOptions contains options for search.
type SearchOptions struct {
	Query         string
	Dir           string
	IsRegex       bool
	CaseSensitive bool
	MaxResults    int
	MaxFilesize   string // e.g., "1M"
	ContextLines  int    // ±N lines of context
	RgCommand     string // ripgrep command name (default: "rg")
}

// ExecuteRipgrep executes ripgrep and returns parsed hits.
// Falls back to built-in search if ripgrep is not available.
func ExecuteRipgrep(ctx context.Context, opts SearchOptions) ([]GrepHit, error) {
	rgCmd := opts.RgCommand
	if rgCmd == "" {
		rgCmd = "rg"
	}
	// Check if ripgrep is available
	_, err := exec.LookPath(rgCmd)
	if err != nil {
		// Fallback to built-in search
		return executeBuiltinGrep(ctx, opts)
	}

	return executeRipgrep(ctx, opts)
}

func executeRipgrep(ctx context.Context, opts SearchOptions) ([]GrepHit, error) {
	args := []string{
		"--json",
		"--max-count", fmt.Sprintf("%d", opts.MaxResults),
		"--max-filesize", opts.MaxFilesize,
	}

	// Context lines
	if opts.ContextLines > 0 {
		args = append(args, "-C", fmt.Sprintf("%d", opts.ContextLines))
	}

	// Case sensitivity
	if !opts.CaseSensitive {
		args = append(args, "-i")
	}

	// Regex vs fixed strings
	if !opts.IsRegex {
		args = append(args, "--fixed-strings")
	}

	// Add pattern and directory
	args = append(args, "-e", opts.Query)
	if opts.Dir != "" {
		args = append(args, opts.Dir)
	}

	rgCmd := opts.RgCommand
	if rgCmd == "" {
		rgCmd = "rg"
	}

	cmd := exec.CommandContext(ctx, rgCmd, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("rg stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("rg start: %w", err)
	}
	defer cmd.Wait() // Ensure process is cleaned up

	var hits []GrepHit
	var currentMatch *GrepHit

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		var result RipgrepResult
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			continue // Skip malformed lines
		}

		switch result.Type {
		case "match":
			// Save previous match if exists
			if currentMatch != nil {
				hits = append(hits, *currentMatch)
			}

			path := result.Data.Path.Text

			matchText := ""
			column := 0
			if len(result.Data.Submatches) > 0 {
				matchText = result.Data.Submatches[0].Match.Text
				column = result.Data.Submatches[0].Start + 1 // 1-based
			}

			currentMatch = &GrepHit{
				Path:      path,
				Line:      result.Data.LineNumber,
				Column:    column,
				Snippet:   strings.TrimSpace(result.Data.Lines.Text),
				MatchText: matchText,
			}

		case "context":
			// Context lines before/after match
			if currentMatch == nil {
				continue
			}
			ctxLine := strings.TrimSpace(result.Data.Lines.Text)
			if result.Data.LineNumber < currentMatch.Line {
				currentMatch.BeforeCtx = append(currentMatch.BeforeCtx, ctxLine)
			} else if result.Data.LineNumber > currentMatch.Line {
				currentMatch.AfterCtx = append(currentMatch.AfterCtx, ctxLine)
			}

		case "end":
			// End of file matches
			if currentMatch != nil {
				hits = append(hits, *currentMatch)
				currentMatch = nil
			}
		}
	}

	// Don't forget the last match
	if currentMatch != nil {
		hits = append(hits, *currentMatch)
	}

	if err := scanner.Err(); err != nil {
		return hits, fmt.Errorf("rg output scan: %w", err)
	}

	return hits, nil
}

// executeBuiltinGrep is the fallback when ripgrep is not available.
// It walks the directory tree and searches files using strings/regexp.
func executeBuiltinGrep(ctx context.Context, opts SearchOptions) ([]GrepHit, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}

	var pattern *regexp.Regexp
	if opts.IsRegex {
		var err error
		if opts.CaseSensitive {
			pattern, err = regexp.Compile(opts.Query)
		} else {
			pattern, err = regexp.Compile("(?i)" + opts.Query)
		}
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
	}

	queryLower := strings.ToLower(opts.Query)

	var hits []GrepHit
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	maxSize := int64(1 << 20) // 1MB default
	if opts.MaxFilesize != "" {
		maxSize = parseMaxFilesize(opts.MaxFilesize)
	}

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(hits) >= maxResults {
			return filepath.SkipAll
		}

		// Skip directories and hidden/vendor paths
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".hg" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files that are too large
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if info.Size() > maxSize {
			return nil
		}

		// Skip binary-looking extensions
		ext := filepath.Ext(name)
		if isBinaryExt(ext) {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		content := string(data)
		lines := strings.Split(content, "\n")

		for i, line := range lines {
			if len(hits) >= maxResults {
				break
			}
			var matched bool
			var col int
			if pattern != nil {
				loc := pattern.FindStringIndex(line)
				if loc != nil {
					matched = true
					col = loc[0] + 1
				}
			} else if opts.CaseSensitive {
				idx := strings.Index(line, opts.Query)
				if idx >= 0 {
					matched = true
					col = idx + 1
				}
			} else {
				idx := strings.Index(strings.ToLower(line), queryLower)
				if idx >= 0 {
					matched = true
					col = idx + 1
				}
			}

			if matched {
				hits = append(hits, GrepHit{
					Path:      path,
					Line:      i + 1,
					Column:    col,
					Snippet:   strings.TrimSpace(line),
					MatchText: opts.Query,
				})
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll && ctx.Err() == nil {
		return hits, err
	}

	return hits, nil
}

// parseMaxFilesize parses a human-readable file size like "1M", "512K".
func parseMaxFilesize(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 1 << 20
	}
	multiplier := int64(1)
	if strings.HasSuffix(s, "K") {
		multiplier = 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "M") {
		multiplier = 1 << 20
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "G") {
		multiplier = 1 << 30
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 1 << 20
	}
	return n * multiplier
}

// isBinaryExt returns true for file extensions that are typically binary.
func isBinaryExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".a",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
		".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".mp3", ".mp4", ".avi", ".mov", ".wav", ".flac",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".wasm", ".pyc", ".class", ".jar":
		return true
	}
	return false
}

// FormatHits formats grep hits as path:line: snippet output.
func FormatHits(hits []GrepHit, includeContext bool) string {
	if len(hits) == 0 {
		return "No matches found."
	}

	var sb strings.Builder
	for _, hit := range hits {
		// Main line: path:line: snippet
		sb.WriteString(fmt.Sprintf("%s:%d: %s\n", hit.Path, hit.Line, hit.Snippet))

		if includeContext {
			// Before context
			for i, line := range hit.BeforeCtx {
				ctxLineNum := hit.Line - len(hit.BeforeCtx) + i
				sb.WriteString(fmt.Sprintf("%s:%d: %s\n", hit.Path, ctxLineNum, line))
			}
			// After context
			for i, line := range hit.AfterCtx {
				ctxLineNum := hit.Line + i + 1
				sb.WriteString(fmt.Sprintf("%s:%d: %s\n", hit.Path, ctxLineNum, line))
			}
		}
	}

	return sb.String()
}

// HitsToEvidence converts GrepHit to RetrievalEvidence.
func HitsToEvidence(hits []GrepHit, query string) []retrieval.RetrievalEvidence {
	evidence := make([]retrieval.RetrievalEvidence, 0, len(hits))
	for _, hit := range hits {
		// Build snippet with context if available
		snippet := hit.Snippet
		if len(hit.BeforeCtx) > 0 || len(hit.AfterCtx) > 0 {
			var parts []string
			parts = append(parts, hit.BeforeCtx...)
			parts = append(parts, hit.Snippet)
			parts = append(parts, hit.AfterCtx...)
			snippet = strings.Join(parts, "\n")
		}

		e := retrieval.RetrievalEvidence{
			Source:      "grep",
			Path:        hit.Path,
			Line:        hit.Line,
			Column:      hit.Column,
			Snippet:     snippet,
			Verified:    false,
			Stale:       false,
			Explanation: fmt.Sprintf("Text match for '%s'", query),
		}
		evidence = append(evidence, e)
	}
	return evidence
}

// ExtractSnippet extracts a snippet from file content around a specific line.
func ExtractSnippet(content string, targetLine int, contextLines int) string {
	lines := strings.Split(content, "\n")
	if targetLine < 1 || targetLine > len(lines) {
		return ""
	}

	start := targetLine - contextLines - 1
	if start < 0 {
		start = 0
	}
	end := targetLine + contextLines
	if end > len(lines) {
		end = len(lines)
	}

	var result []string
	for i := start; i < end; i++ {
		lineNum := i + 1
		prefix := "  "
		if lineNum == targetLine {
			prefix = "> "
		}
		result = append(result, fmt.Sprintf("%s%d: %s", prefix, lineNum, lines[i]))
	}

	return strings.Join(result, "\n")
}

// SearchInContent searches for query in content using regex or literal.
func SearchInContent(content, query string, isRegex bool) ([]int, error) {
	if isRegex {
		re, err := regexp.Compile(query)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		var lines []int
		for i, line := range strings.Split(content, "\n") {
			if re.MatchString(line) {
				lines = append(lines, i+1) // 1-based
			}
		}
		return lines, nil
	}

	// Literal search
	var lines []int
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, query) {
			lines = append(lines, i+1) // 1-based
		}
	}
	return lines, nil
}

// DefaultSearchOptions returns sensible defaults.
func DefaultSearchOptions(query, dir string) SearchOptions {
	return SearchOptions{
		Query:         query,
		Dir:           dir,
		IsRegex:       false,
		CaseSensitive: false,
		MaxResults:    50,
		MaxFilesize:   "1M",
		ContextLines:  3,
		RgCommand:     "rg",
	}
}
