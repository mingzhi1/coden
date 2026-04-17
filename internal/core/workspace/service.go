package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Service provides safe workspace file operations with dirty set tracking.
type Service struct {
	root string

	// Dirty set tracking (R-03): paths modified since last checkpoint
	dirty   map[string]time.Time
	dirtyMu sync.RWMutex
}

// New creates a new workspace service.
func New(root string) *Service {
	return &Service{
		root:  root,
		dirty: make(map[string]time.Time),
	}
}

func (s *Service) Root() string {
	return s.root
}

// Write writes a file and marks it as dirty.
func (s *Service) Write(relativePath string, body []byte) (string, error) {
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	fullPath, err := safeJoin(rootAbs, relativePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", err
	}
	existing, _ := os.ReadFile(fullPath)
	body = normalizeTextForWrite(existing, body)
	if err := os.WriteFile(fullPath, body, 0o644); err != nil {
		return "", err
	}

	// Mark as dirty (R-03)
	 s.MarkDirty(relativePath)

	return fullPath, nil
}

// MarkDirty marks a path as dirty (modified since last checkpoint).
func (s *Service) MarkDirty(relativePath string) {
	s.dirtyMu.Lock()
	defer s.dirtyMu.Unlock()
	s.dirty[relativePath] = time.Now()
}

// ClearDirty clears the dirty status for specific paths.
func (s *Service) ClearDirty(paths []string) {
	s.dirtyMu.Lock()
	defer s.dirtyMu.Unlock()
	for _, path := range paths {
		delete(s.dirty, path)
	}
}

// ClearAllDirty clears all dirty paths.
func (s *Service) ClearAllDirty() {
	s.dirtyMu.Lock()
	defer s.dirtyMu.Unlock()
	s.dirty = make(map[string]time.Time)
}

// DirtyPaths returns all paths currently marked as dirty.
func (s *Service) DirtyPaths() []string {
	s.dirtyMu.RLock()
	defer s.dirtyMu.RUnlock()

	paths := make([]string, 0, len(s.dirty))
	for path := range s.dirty {
		paths = append(paths, path)
	}
	return paths
}

// IsDirty returns true if the path is marked as dirty.
func (s *Service) IsDirty(relativePath string) bool {
	s.dirtyMu.RLock()
	defer s.dirtyMu.RUnlock()
	_, ok := s.dirty[relativePath]
	return ok
}

func normalizeTextForWrite(existing, body []byte) []byte {
	body = bytes.TrimPrefix(body, utf8BOM)
	if !utf8.Valid(body) {
		return body
	}

	preserveBOM := bytes.HasPrefix(existing, utf8BOM)
	useCRLF := bytes.Contains(existing, []byte("\r\n"))

	if useCRLF {
		body = normalizeLineEndings(body, "\r\n")
	}
	if preserveBOM {
		return append(append([]byte(nil), utf8BOM...), body...)
	}
	return body
}

func normalizeLineEndings(body []byte, newline string) []byte {
	text := string(body)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\n", newline)
	return []byte(text)
}

// Read returns the contents of a workspace-relative file.
func (s *Service) Read(relativePath string) ([]byte, error) {
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	fullPath, err := safeJoin(rootAbs, relativePath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(fullPath)
}

// Delete removes a workspace-relative file. Returns nil if the file does not exist.
func (s *Service) Delete(relativePath string) error {
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	fullPath, err := safeJoin(rootAbs, relativePath)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}


// ListFiles returns workspace-relative paths for all regular files under
// the given subdirectory (or the root when dir is empty).
// Hidden directories (name starting with ".") are skipped.
// Results are capped at maxFiles to avoid overwhelming LLM context.
func (s *Service) ListFiles(dir string, maxFiles int) ([]string, error) {
	if maxFiles <= 0 {
		maxFiles = 200
	}
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}

	var searchRoot string
	if dir == "" {
		searchRoot = rootAbs
	} else {
		searchRoot, err = safeJoin(rootAbs, dir)
		if err != nil {
			return nil, err
		}
	}

	var paths []string
	err = filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(rootAbs, path)
		if relErr != nil {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		if len(paths) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return paths, err
}

func safeJoin(rootAbs, relativePath string) (string, error) {
	if strings.TrimSpace(relativePath) == "" {
		return "", fmt.Errorf("relative path is required")
	}

	targetAbs, err := filepath.Abs(filepath.Join(rootAbs, filepath.Clean(relativePath)))
	if err != nil {
		return "", fmt.Errorf("resolve target path: %w", err)
	}

	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", fmt.Errorf("compare target path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace root: %s", relativePath)
	}

	return targetAbs, nil
}
