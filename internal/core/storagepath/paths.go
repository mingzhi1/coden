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

// workspaceKey is a stable per-repo key derived from the absolute workspace
// root. It needs no registry/UUID lookup, so every layer (kernel, launcher,
// spill) can compute the same per-workspace partition from just the root.
func workspaceKey(workspaceRoot string) string {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		abs = workspaceRoot
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:16]
}

// dataDirIn returns <baseDir>/workspace/<key>, the per-workspace partition.
func dataDirIn(baseDir, workspaceRoot string) string {
	return filepath.Join(baseDir, "workspace", workspaceKey(workspaceRoot))
}

// WorkspaceDataDir returns the home-side per-workspace directory that holds all
// of a repo's coden state — state.sqlite, artifacts/, rag.sqlite, spill/,
// MEMORY.md — under ~/.coden/workspace/<key>/, so the repo working tree stays
// clean and everything for a workspace lives in one place. Returns "" if the
// home root can't be resolved (callers fall back accordingly). Used by the
// launcher-built tools (RAG/spill) that don't have the main DB path in scope.
func WorkspaceDataDir(workspaceRoot string) string {
	root, err := DefaultRoot()
	if err != nil {
		return ""
	}
	return dataDirIn(root, workspaceRoot)
}

// WorkspaceDBPath returns the per-workspace state DB path. It derives the base
// from the main DB's directory so a custom --state-db (and test isolation) is
// respected; in the normal case that directory IS DefaultRoot, so it agrees
// with WorkspaceDataDir.
func WorkspaceDBPath(mainDBPath, workspaceRoot string) string {
	return filepath.Join(dataDirIn(filepath.Dir(mainDBPath), workspaceRoot), "state.sqlite")
}

// ArtifactDataDir returns the per-workspace artifact directory.
func ArtifactDataDir(mainDBPath, workspaceRoot string) string {
	return filepath.Join(dataDirIn(filepath.Dir(mainDBPath), workspaceRoot), "artifacts")
}

// MemoryFilePath returns the per-workspace MEMORY.md path (home-side, alongside
// the rest of the workspace's state instead of in the repo working tree).
func MemoryFilePath(mainDBPath, workspaceRoot string) string {
	return filepath.Join(dataDirIn(filepath.Dir(mainDBPath), workspaceRoot), "MEMORY.md")
}

// RAGDBPath returns the per-workspace RAG index path.
func RAGDBPath(workspaceRoot string) string {
	dir := WorkspaceDataDir(workspaceRoot)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "rag.sqlite")
}

// SpillDir returns the per-workspace spilled-output directory.
func SpillDir(workspaceRoot string) string {
	dir := WorkspaceDataDir(workspaceRoot)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "spill")
}

// TurnObjectsDir returns the (workspace-global) object snapshot directory.
func TurnObjectsDir(mainDBPath, turnID string) string {
	return filepath.Join(filepath.Dir(mainDBPath), "workspace", "objects", "turn_"+turnID)
}

// BoardDBPath returns the path for the Kanban board SQLite database.
func BoardDBPath(mainDBPath string) string {
	return filepath.Join(filepath.Dir(mainDBPath), "board.sqlite")
}

// MigrateWorkspaceLayout moves a workspace's legacy flat files into the unified
// per-workspace subdirectory, in place, on first open after the upgrade:
//
//	<base>/workspace/<uuid>.sqlite     -> <base>/workspace/<key>/state.sqlite
//	<base>/workspace/<uuid>.artifacts  -> <base>/workspace/<key>/artifacts
//
// Lazy and idempotent: only moves when the legacy path exists and the new one
// does not, so each workspace migrates once. Best-effort — returns the first
// error but leaves already-moved files in place.
func MigrateWorkspaceLayout(mainDBPath, workspaceID, workspaceRoot string) error {
	base := filepath.Join(filepath.Dir(mainDBPath), "workspace")
	newDir := dataDirIn(filepath.Dir(mainDBPath), workspaceRoot)

	moves := []struct{ from, to string }{
		{filepath.Join(base, workspaceID+".sqlite"), filepath.Join(newDir, "state.sqlite")},
		{filepath.Join(base, workspaceID+".artifacts"), filepath.Join(newDir, "artifacts")},
	}
	for _, m := range moves {
		if !pathExists(m.from) || pathExists(m.to) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(m.to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(m.from, m.to); err != nil {
			return err
		}
		// SQLite WAL/SHM sidecars travel with the DB file.
		for _, ext := range []string{"-wal", "-shm"} {
			if pathExists(m.from + ext) {
				_ = os.Rename(m.from+ext, m.to+ext)
			}
		}
	}
	return nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
