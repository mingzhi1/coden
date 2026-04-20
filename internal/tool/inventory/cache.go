package inventory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultCacheTTL is the duration after which a cached tool entry is considered stale.
const DefaultCacheTTL = 24 * time.Hour

// Cache persists tool discovery results in a SQLite database so that
// subsequent startups can skip the expensive PATH + version probes.
type Cache struct {
	db  *sql.DB
	ttl time.Duration
}

// OpenCache opens (or creates) the tool_cache.db inside <workspace>/.coden/.
// Returns nil, nil if the database cannot be opened (discovery proceeds without cache).
func OpenCache(workspaceRoot string) (*Cache, error) {
	dir := filepath.Join(workspaceRoot, ".coden")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create .coden dir: %w", err)
	}
	dbPath := filepath.Join(dir, "tool_cache.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=3000")
	if err != nil {
		return nil, fmt.Errorf("open tool cache: %w", err)
	}
	if err := createCacheTable(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Cache{db: db, ttl: DefaultCacheTTL}, nil
}

func createCacheTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tool_cache (
			name       TEXT PRIMARY KEY,
			entry_json TEXT NOT NULL,
			checked_at TEXT NOT NULL
		)
	`)
	return err
}

// Get retrieves a cached ToolEntry by name. Returns nil if not found or stale.
func (c *Cache) Get(name string) *ToolEntry {
	if c == nil || c.db == nil {
		return nil
	}
	var entryJSON string
	var checkedAtStr string
	err := c.db.QueryRow(
		`SELECT entry_json, checked_at FROM tool_cache WHERE name = ?`, name,
	).Scan(&entryJSON, &checkedAtStr)
	if err != nil {
		return nil
	}
	checkedAt, err := time.Parse(time.RFC3339Nano, checkedAtStr)
	if err != nil {
		return nil
	}
	if time.Since(checkedAt) > c.ttl {
		return nil // stale
	}
	var entry ToolEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return nil
	}
	return &entry
}

// Put stores a ToolEntry in the cache.
func (c *Cache) Put(entry *ToolEntry) {
	if c == nil || c.db == nil || entry == nil {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		slog.Warn("[cache] failed to marshal tool entry", "name", entry.Name, "error", err)
		return
	}
	_, err = c.db.Exec(
		`INSERT OR REPLACE INTO tool_cache (name, entry_json, checked_at) VALUES (?, ?, ?)`,
		entry.Name, string(data), entry.CheckedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		slog.Warn("[cache] failed to write tool entry", "name", entry.Name, "error", err)
	}
}

// PutAll stores all entries from an Inventory into the cache.
func (c *Cache) PutAll(inv *Inventory) {
	if c == nil || inv == nil {
		return
	}
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	for _, entry := range inv.entries {
		c.Put(entry)
	}
}

// Invalidate removes a single entry from the cache.
func (c *Cache) Invalidate(name string) {
	if c == nil || c.db == nil {
		return
	}
	_, _ = c.db.Exec(`DELETE FROM tool_cache WHERE name = ?`, name)
}

// InvalidateAll clears all cached entries.
func (c *Cache) InvalidateAll() {
	if c == nil || c.db == nil {
		return
	}
	_, _ = c.db.Exec(`DELETE FROM tool_cache`)
}

// Close closes the underlying database connection.
func (c *Cache) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}
