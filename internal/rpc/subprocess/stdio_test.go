package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSiblingExecutable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	execPath := filepath.Join(dir, executableName("coden"))
	siblingPath := filepath.Join(dir, executableName("coden-agent-plan"))
	if err := os.WriteFile(execPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile exec failed: %v", err)
	}
	if err := os.WriteFile(siblingPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile sibling failed: %v", err)
	}

	path, ok := findSiblingExecutable(execPath, "coden-agent-plan")
	if !ok {
		t.Fatal("expected sibling executable")
	}
	if path != siblingPath {
		t.Fatalf("unexpected sibling path: %q", path)
	}
}
