package discovery

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// WorkspaceChange records a single file operation for audit purposes.
type WorkspaceChange struct {
	Path      string    `json:"path"`
	Op        string    `json:"op"`        // "write", "create", "delete"
	Timestamp time.Time `json:"timestamp"`
}

// DirtyTracker is the unified dirty-state manager for the Search layer.
//
// It consolidates the three previously separate dirty mechanisms:
//   - workspace.Service.dirty  — file modification times
//   - kernel.workspaceChanges  — per-session change event log
//   - rag.Index.dirty          — paths pending RAG re-indexing
//
// In addition it invalidates the shared orchestratorCache whenever a file is
// marked dirty, so subsequent searches never return stale cached results.
//
// DirtyTracker is safe for concurrent use.
type DirtyTracker struct {
	mu          sync.RWMutex
	fileDirties map[string]time.Time // path → time of last modification
	ragDirty    map[string]bool      // paths that need RAG re-indexing
	changeLog   []WorkspaceChange    // capped audit log
}

const maxChangeLogEntries = 100

// NewDirtyTracker returns an initialised, empty DirtyTracker.
func NewDirtyTracker() *DirtyTracker {
	return &DirtyTracker{
		fileDirties: make(map[string]time.Time),
		ragDirty:    make(map[string]bool),
	}
}

// MarkDirty records a file modification.
//
// It updates the file-dirty set, the RAG-dirty set, and the change log, then
// invalidates any orchestratorCache entries that reference path so that the
// next Search call fetches fresh results.
func (d *DirtyTracker) MarkDirty(path, op string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	d.mu.Lock()
	d.fileDirties[path] = time.Now()
	d.ragDirty[path] = true
	d.changeLog = append(d.changeLog, WorkspaceChange{
		Path:      path,
		Op:        op,
		Timestamp: time.Now(),
	})
	if len(d.changeLog) > maxChangeLogEntries {
		d.changeLog = d.changeLog[len(d.changeLog)-maxChangeLogEntries:]
	}
	d.mu.Unlock()

	// Invalidate cache entries referencing this path outside the lock.
	cacheInvalidateByPath(path)
}

// SyncFrom merges an external dirty-path list (e.g. from workspace.Service.DirtyPaths)
// into the tracker. Paths already present are not overwritten.
func (d *DirtyTracker) SyncFrom(paths []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := d.fileDirties[path]; !ok {
			d.fileDirties[path] = time.Now()
			d.ragDirty[path] = true
		}
	}
}

// ClearDirty resets the file-dirty and RAG-dirty sets.
// The changeLog is retained (capped) for audit purposes.
// Typically called after a successful checkpoint.
func (d *DirtyTracker) ClearDirty() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fileDirties = make(map[string]time.Time)
	d.ragDirty = make(map[string]bool)
}

// IsDirty returns true if path is currently marked as dirty.
func (d *DirtyTracker) IsDirty(path string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.fileDirties[path]
	return ok
}

// DirtyPaths returns all currently dirty paths in sorted order.
func (d *DirtyTracker) DirtyPaths() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	paths := make([]string, 0, len(d.fileDirties))
	for path := range d.fileDirties {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// RAGDirtyPaths returns paths that need RAG re-indexing, in sorted order.
func (d *DirtyTracker) RAGDirtyPaths() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	paths := make([]string, 0, len(d.ragDirty))
	for path, dirty := range d.ragDirty {
		if dirty {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// ChangeLog returns a copy of the audit change log.
func (d *DirtyTracker) ChangeLog() []WorkspaceChange {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]WorkspaceChange, len(d.changeLog))
	copy(out, d.changeLog)
	return out
}
