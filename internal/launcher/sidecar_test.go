package launcher

import (
	"testing"
)

func TestFindSidecarBinary_NotExist(t *testing.T) {
	// A non-existent binary should return empty string.
	path := findSidecarBinary("this-binary-definitely-does-not-exist-12345")
	if path != "" {
		t.Errorf("expected empty path for non-existent binary, got %q", path)
	}
}

func TestSidecarConfig_DefaultAddr(t *testing.T) {
	cfg := SidecarConfig{}
	if cfg.Addr != "" {
		t.Errorf("expected empty default addr, got %q", cfg.Addr)
	}
	// StartSidecar would fill in the default — verify it's documented.
}

func TestSidecarStop_NilCmd(t *testing.T) {
	// Stop on a zero-value Sidecar should not panic.
	s := &Sidecar{cancel: func() {}}
	if err := s.Stop(); err != nil {
		t.Errorf("Stop on nil cmd should return nil, got %v", err)
	}
}
