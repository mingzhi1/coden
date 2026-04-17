package storagepath

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultRoot() (string, error) {
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
