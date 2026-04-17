package toolruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpillResult_WritesAndReturnsPreview(t *testing.T) {
	tmp := t.TempDir()
	bigContent := strings.Repeat("line content here\n", 500) // ~9000 chars

	spillPath, preview, err := SpillResult(tmp, "read_file", "bigfile.go", bigContent)
	if err != nil {
		t.Fatalf("SpillResult error: %v", err)
	}
	if spillPath == "" {
		t.Fatal("spillPath is empty")
	}

	// Verify file exists with full content.
	data, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatalf("read spilled file: %v", err)
	}
	if string(data) != bigContent {
		t.Errorf("spilled content mismatch: got %d bytes, want %d", len(data), len(bigContent))
	}

	// Preview should be first N lines.
	previewLines := strings.Split(strings.TrimRight(preview, "\n."), "\n")
	if len(previewLines) > spillPreviewLines+1 {
		t.Errorf("preview has %d lines, want ≤ %d", len(previewLines), spillPreviewLines)
	}
}

func TestSpillResult_ContentAddressed(t *testing.T) {
	tmp := t.TempDir()
	content := "same content"
	p1, _, _ := SpillResult(tmp, "read_file", "a.go", content)
	p2, _, _ := SpillResult(tmp, "read_file", "a.go", content)
	if p1 != p2 {
		t.Errorf("same content should produce same path: %s vs %s", p1, p2)
	}
}

func TestShouldSpill(t *testing.T) {
	short := strings.Repeat("x", MaxResultChars)
	if ShouldSpill(short) {
		t.Error("should not spill content at threshold")
	}
	long := strings.Repeat("x", MaxResultChars+1)
	if !ShouldSpill(long) {
		t.Error("should spill content above threshold")
	}
}

func TestCleanupSpillDir(t *testing.T) {
	tmp := t.TempDir()
	content := "some data to spill"
	_, _, err := SpillResult(tmp, "read_file", "f.go", content)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, SpillDirName)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("spill dir should exist after SpillResult")
	}
	if err := CleanupSpillDir(tmp); err != nil {
		t.Fatalf("CleanupSpillDir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("spill dir should be removed after cleanup")
	}
}

func TestCleanupSpillDir_NoDir(t *testing.T) {
	tmp := t.TempDir()
	// Should not error if dir doesn't exist.
	if err := CleanupSpillDir(tmp); err != nil {
		t.Fatalf("CleanupSpillDir on missing dir: %v", err)
	}
}

func TestExtractPreview(t *testing.T) {
	text := "L1\nL2\nL3\nL4\nL5\n"
	preview := extractPreview(text, 3)
	if !strings.HasPrefix(preview, "L1\nL2\nL3\n") {
		t.Errorf("unexpected preview: %q", preview)
	}
}

func TestSanitiseSpillName(t *testing.T) {
	tests := []struct{ in, wantPrefix string }{
		{"internal/core/foo.go", "foo.go"},
		{"", "result"},
		{"../../../etc/passwd", "passwd"},
	}
	for _, tt := range tests {
		got := sanitiseSpillName(tt.in)
		if got != tt.wantPrefix {
			t.Errorf("sanitiseSpillName(%q) = %q, want %q", tt.in, got, tt.wantPrefix)
		}
	}
}
