package profile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644)

	p := &ProjectProfile{
		Languages:   []string{"Go"},
		Toolchain:   "go 1.22",
		Overview:    "A CLI tool.",
		Style:       "tabs, short names",
		SourceHash:  ComputeSourceHash(root),
		GeneratedAt: time.Now().UTC(),
	}
	if err := Save(root, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := Load(root)
	if got == nil {
		t.Fatal("Load returned nil")
	}
	if got.Overview != "A CLI tool." || got.Style != "tabs, short names" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if !got.HasSemantic() {
		t.Errorf("HasSemantic should be true")
	}
}

func TestStaleOnManifestChange(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644)

	p := &ProjectProfile{SourceHash: ComputeSourceHash(root), GeneratedAt: time.Now().UTC()}
	if p.Stale(root, DefaultTTL) {
		t.Errorf("fresh profile should not be stale")
	}
	// Change the manifest → hash changes → stale.
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\nrequire y v1.0.0\n"), 0o644)
	if !p.Stale(root, DefaultTTL) {
		t.Errorf("profile should be stale after manifest change")
	}
}

func TestStaleOnTTL(t *testing.T) {
	root := t.TempDir()
	p := &ProjectProfile{SourceHash: ComputeSourceHash(root), GeneratedAt: time.Now().Add(-2 * time.Hour)}
	if !p.Stale(root, time.Hour) {
		t.Errorf("profile older than ttl should be stale")
	}
	if p.Stale(root, 3*time.Hour) {
		t.Errorf("profile within ttl should not be stale")
	}
}

func TestNilAndEmptyFormat(t *testing.T) {
	var p *ProjectProfile
	if p.Stale(t.TempDir(), DefaultTTL) != true {
		t.Errorf("nil profile must be stale")
	}
	if p.Format() != "" {
		t.Errorf("nil profile must format to empty")
	}
	empty := &ProjectProfile{}
	if empty.Format() != "" {
		t.Errorf("empty profile must format to empty, got %q", empty.Format())
	}
}

func TestFormatIncludesFields(t *testing.T) {
	p := &ProjectProfile{Languages: []string{"Go"}, Overview: "X"}
	out := p.Format()
	if out == "" {
		t.Fatal("expected non-empty format")
	}
	if want := "Languages: Go"; !contains(out, want) {
		t.Errorf("format missing %q: %s", want, out)
	}
	if !contains(out, "Overview: X") {
		t.Errorf("format missing overview: %s", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
