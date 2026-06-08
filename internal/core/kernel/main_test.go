package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points CODEN_HOME at a throwaway dir so any RAG/spill paths that
// resolve to ~/.coden/workspace/<key>/ during tests stay hermetic and never
// touch the real home directory.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "coden-kernel-home-*")
	if err == nil {
		_ = os.Setenv("CODEN_HOME", filepath.Join(tmp, ".coden"))
		defer os.RemoveAll(tmp)
	}
	os.Exit(m.Run())
}
