package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workspace"
)

func TestWorkspaceSearcher_SearchUsesGrep(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.go"), []byte("package x\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "beta.go"), []byte("package x\n\nfunc Other() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ws := workspace.New(root)
	rt := toolruntime.New(ws)
	s := NewWorkspaceSearcher(ws, rt, "test-wf")

	dc, err := s.Search(context.Background(), model.IntentSpec{Goal: "Hello"}, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if dc.Query != "Hello" {
		t.Errorf("Query = %q, want Hello", dc.Query)
	}
	if dc.QueryID != "test-wf:discovery" {
		t.Errorf("QueryID = %q, want test-wf:discovery", dc.QueryID)
	}
	// We expect at least the alpha.go snippet to be found via grep.
	foundAlpha := false
	for _, sn := range dc.Snippets {
		if sn.Path == "alpha.go" {
			foundAlpha = true
			if !sn.Exists {
				t.Errorf("alpha.go snippet must exist=true")
			}
			break
		}
	}
	if !foundAlpha {
		t.Logf("snippets: %#v", dc.Snippets)
		t.Errorf("expected alpha.go to appear in snippets (grep should find Hello)")
	}
}

func TestWorkspaceSearcher_NilSafe(t *testing.T) {
	var s *WorkspaceSearcher
	_, err := s.Search(context.Background(), model.IntentSpec{}, nil)
	if err == nil {
		t.Errorf("expected error for nil searcher")
	}
}

func TestDiscoveryMode(t *testing.T) {
	cases := []struct {
		query   string
		targets []string
		want    string
	}{
		{"foo", nil, "identifier"},
		{"hello world", nil, "semantic"},
		{"foo", []string{"a.go"}, "symbol"},
	}
	for _, tc := range cases {
		got := discoveryMode(tc.query, tc.targets)
		if got != tc.want {
			t.Errorf("discoveryMode(%q,%v)=%q want %q", tc.query, tc.targets, got, tc.want)
		}
	}
}

func TestSnippetConfidence(t *testing.T) {
	if c := snippetConfidence(nil); c != 0 {
		t.Errorf("empty=%v want 0", c)
	}
	if c := snippetConfidence(make([]model.FileSnippet, 5)); c != 1.0 {
		t.Errorf("5 snippets=%v want 1.0", c)
	}
	if c := snippetConfidence(make([]model.FileSnippet, 2)); c != 0.4 {
		t.Errorf("2 snippets=%v want 0.4", c)
	}
}
