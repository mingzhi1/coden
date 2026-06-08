package storagepath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultRoot returns coden's home root. CODEN_HOME overrides it (used to
// relocate state and to keep tests hermetic); otherwise it is ~/.coden.
func DefaultRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv("CODEN_HOME")); override != "" {
		return override, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(homeDir, ".coden"), nil
}

func DefaultMainDBPath() (string, error) {
	root, err := DefaultRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "main.sqlite"), nil
}

func WorkspaceDBPath(mainDBPath, workspaceID string) string {
	return filepath.Join(filepath.Dir(mainDBPath), "workspace", workspaceID+".sqlite")
}

func TurnObjectsDir(mainDBPath, turnID string) string {
	return filepath.Join(filepath.Dir(mainDBPath), "workspace", "objects", "turn_"+turnID)
}

// ArtifactDataDir returns the directory where artifact DB and blobs are stored
// for the given workspace.
func ArtifactDataDir(mainDBPath, workspaceID string) string {
	return filepath.Join(filepath.Dir(mainDBPath), "workspace", workspaceID+".artifacts")
}

// BoardDBPath returns the path for the Kanban board SQLite database.
func BoardDBPath(mainDBPath string) string {
	return filepath.Join(filepath.Dir(mainDBPath), "board.sqlite")
}

// workspaceKey is a stable per-repo key derived from the absolute workspace
// root. The launcher that builds the RAG/spill tools does not have the
// main.sqlite workspace UUID in scope, so a path hash gives a deterministic
// per-workspace partition with no registry lookup.
func workspaceKey(workspaceRoot string) string {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		abs = workspaceRoot
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:16]
}

// WorkspaceDataDir returns the home-side per-workspace directory that holds a
// repo's derived state (RAG index, spilled tool output, …) under
// ~/.coden/workspace/<key>/, so the repo working tree stays clean and all of a
// workspace's coden data lives in one place. Returns "" if the home root can't
// be resolved, so callers can fall back to an in-repo default.
func WorkspaceDataDir(workspaceRoot string) string {
	root, err := DefaultRoot()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "workspace", workspaceKey(workspaceRoot))
}

// RAGDBPath returns the home-side path for a workspace's RAG index.
func RAGDBPath(workspaceRoot string) string {
	dir := WorkspaceDataDir(workspaceRoot)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "rag.sqlite")
}

// SpillDir returns the home-side directory for a workspace's spilled tool
// output. Returns "" if the home root can't be resolved.
func SpillDir(workspaceRoot string) string {
	dir := WorkspaceDataDir(workspaceRoot)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "spill")
}
