package toolruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points CODEN_HOME at a throwaway dir so spill/RAG paths that resolve
// to ~/.coden/workspace/<key>/ during tests don't pollute the real home.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "coden-toolruntime-home-*")
	if err == nil {
		_ = os.Setenv("CODEN_HOME", filepath.Join(tmp, ".coden"))
		defer os.RemoveAll(tmp)
	}
	os.Exit(m.Run())
}
