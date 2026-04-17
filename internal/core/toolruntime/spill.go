// Package toolruntime — spill.go handles large tool results by writing them
// to a temporary directory and returning a preview + path reference.
package toolruntime

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxResultChars: results larger than this are spilled to disk.
	MaxResultChars = 8000
	// spillPreviewLines: number of leading lines to keep as inline preview.
	spillPreviewLines = 20
	// SpillDirName is the subdirectory under workspace root for spilled results.
	SpillDirName = ".coden/spill"
)

// SpillResult writes content to a temp file under workspaceRoot/.coden/spill/
// and returns the file path plus a short preview (first N lines).
// It is safe to call concurrently — file names are content-addressed.
func SpillResult(workspaceRoot, toolKind, target, content string) (spillPath, preview string, err error) {
	dir := filepath.Join(workspaceRoot, SpillDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("spill: mkdir %s: %w", dir, err)
	}

	// Content-addressed filename to avoid collisions.
	h := sha256.Sum256([]byte(content))
	safeName := sanitiseSpillName(target)
	fileName := fmt.Sprintf("%s_%x.txt", safeName, h[:8])
	fullPath := filepath.Join(dir, fileName)

	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return "", "", fmt.Errorf("spill: write %s: %w", fullPath, err)
	}

	preview = extractPreview(content, spillPreviewLines)
	return fullPath, preview, nil
}

// CleanupSpillDir removes the entire .coden/spill/ directory tree.
func CleanupSpillDir(workspaceRoot string) error {
	dir := filepath.Join(workspaceRoot, SpillDirName)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(dir)
}

// ShouldSpill returns true when content exceeds the inline threshold.
func ShouldSpill(content string) bool {
	return len(content) > MaxResultChars
}

// extractPreview returns the first n lines of text.
func extractPreview(text string, n int) string {
	lines := strings.SplitN(text, "\n", n+1)
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n") + "\n..."
}

// sanitiseSpillName produces a short, filesystem-safe base name.
func sanitiseSpillName(target string) string {
	if target == "" {
		return "result"
	}
	base := filepath.Base(target)
	// Remove path separators and keep alphanumeric + dot/dash/underscore.
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "result"
	}
	return b.String()
}
