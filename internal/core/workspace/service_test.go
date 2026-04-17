package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

func TestWriteRejectsEscapingWorkspaceRoot(t *testing.T) {
	t.Parallel()

	ws := New(t.TempDir())
	if _, err := ws.Write(filepath.Join("..", "escape.txt"), []byte("nope")); err == nil {
		t.Fatal("expected path escape to be rejected")
	}
}

func TestWriteKeepsArtifactsInsideWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ws := New(root)

	got, err := ws.Write(filepath.Join("artifacts", "result.md"), []byte("ok"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	want := filepath.Join(root, "artifacts", "result.md")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestWriteStoresUtf8TextWithoutBOM(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ws := New(root)

	path, err := ws.Write(filepath.Join("artifacts", "cn.md"), []byte("\uFEFF你好，世界\n第二行"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		t.Fatalf("expected UTF-8 text without BOM, got BOM prefix: %v", raw[:3])
	}
	if !utf8.Valid(raw) {
		t.Fatal("expected valid UTF-8 content")
	}
	if string(raw) != "你好，世界\n第二行" {
		t.Fatalf("unexpected content: %q", string(raw))
	}
}

func TestWritePreservesExistingUtf8BOMAndCRLF(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ws := New(root)
	target := filepath.Join(root, "artifacts", "cn.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	original := append([]byte{0xEF, 0xBB, 0xBF}, []byte("old line\r\nnext line\r\n")...)
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatalf("seed file failed: %v", err)
	}

	path, err := ws.Write(filepath.Join("artifacts", "cn.md"), []byte("你好，世界\n第二行\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(raw) < 3 || raw[0] != 0xEF || raw[1] != 0xBB || raw[2] != 0xBF {
		t.Fatalf("expected UTF-8 BOM to be preserved, got prefix %v", raw[:min(3, len(raw))])
	}
	if !bytes.Contains(raw, []byte("\r\n")) {
		t.Fatalf("expected CRLF to be preserved, got %q", string(raw))
	}
	if bytes.Contains(raw, []byte("你好，世界\n第二行\n")) {
		t.Fatalf("expected newline normalization to CRLF, got %q", string(raw))
	}
	if !utf8.Valid(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})) {
		t.Fatal("expected valid UTF-8 content after BOM")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
