// Package toolruntime — spill.go handles large tool results by writing them
// to a temporary directory and returning a preview + path reference.
package toolruntime

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mingzhi1/coden/internal/core/storagepath"
)

const (
	// MaxResultChars: results larger than this are spilled to disk.
	MaxResultChars = 8000
	// spillPreviewLines: number of leading lines to keep as inline preview.
	spillPreviewLines = 20
	// SpillDirName is the in-repo fallback subdirectory for spilled results,
	// used only when the home-side per-workspace dir can't be resolved.
	SpillDirName = ".coden/spill"
)

// spillDir returns the home-side spill directory for a workspace, falling back
// to the in-repo .coden/spill when the home root can't be resolved.
func spillDir(workspaceRoot string) string {
	if d := storagepath.SpillDir(workspaceRoot); d != "" {
		return d
	}
	return filepath.Join(workspaceRoot, SpillDirName)
}

// SpillResult writes content to a content-addressed file under the workspace's
// home-side spill dir (~/.coden/workspace/<key>/spill/) and returns the path
// plus a short preview. Safe to call concurrently — file names are hashed.
func SpillResult(workspaceRoot, toolKind, target, content string) (spillPath, preview string, err error) {
	dir := spillDir(workspaceRoot)
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

// CleanupSpillDir removes the workspace's spill directory tree (home-side).
func CleanupSpillDir(workspaceRoot string) error {
	dir := spillDir(workspaceRoot)
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
