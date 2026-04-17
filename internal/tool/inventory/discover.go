package inventory

import (
	"context"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/config"
)

// DiscoverOptions controls the discovery process.
type DiscoverOptions struct {
	// CheckTimeout is the max duration for a single tool probe.
	CheckTimeout time.Duration
	// Concurrency limits parallel probes.
	Concurrency int
	// OnMissing controls behavior when a tool is not found: "warn", "error", "skip".
	OnMissing string
}

// DefaultDiscoverOptions returns sensible defaults.
func DefaultDiscoverOptions() DiscoverOptions {
	return DiscoverOptions{
		CheckTimeout: 5 * time.Second,
		Concurrency:  8,
		OnMissing:    "warn",
	}
}

// OptionsFromConfig builds DiscoverOptions from a DiscoveryConfig.
func OptionsFromConfig(dc config.DiscoveryConfig) DiscoverOptions {
	opts := DefaultDiscoverOptions()
	if dc.CheckTimeout != "" {
		if d, err := time.ParseDuration(dc.CheckTimeout); err == nil {
			opts.CheckTimeout = d
		}
	}
	if dc.OnMissing != "" {
		opts.OnMissing = dc.OnMissing
	}
	return opts
}

// Discover performs project-aware tool discovery:
//  1. Detects project languages from workspace files.
//  2. Filters the builtin catalog to relevant candidates.
//  3. Checks cache for recent results; probes only stale/missing entries.
//  4. Returns a populated Inventory and updates the cache.
func Discover(workspaceRoot string, cfg *config.ToolsConfig) *Inventory {
	opts := OptionsFromConfig(cfg.Discovery)

	// Step 1: Detect project languages.
	langs := DetectProjectLanguages(workspaceRoot)
	slog.Info("[inventory] detected project languages", "languages", langs)

	// Step 2: Filter catalog.
	candidates := FilterByLanguages(BuiltinCatalog(), langs)
	slog.Info("[inventory] filtered candidates", "count", len(candidates), "from", len(builtinCatalog))

	// Step 2.5: Open cache (best-effort; nil cache = probe everything).
	cache, cacheErr := OpenCache(workspaceRoot)
	if cacheErr != nil {
		slog.Warn("[inventory] cache unavailable, probing all tools", "error", cacheErr)
	}
	defer func() {
		if cache != nil {
			_ = cache.Close()
		}
	}()

	// Step 3: Probe concurrently (skip cached entries).
	inv := New()
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.Concurrency)
	cacheHits := 0

	for _, c := range candidates {
		// Check cache first.
		if cached := cache.Get(c.Name); cached != nil {
			inv.Add(cached)
			cacheHits++
			continue
		}

		wg.Add(1)
		go func(cand ToolCandidate) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			entry := probeOne(cand, opts.CheckTimeout)

			if entry.Status == StatusUnavailable {
				switch opts.OnMissing {
				case "warn":
					slog.Warn("[inventory] tool not found",
						"name", entry.Name, "command", entry.Command,
						"hint", entry.InstallHint)
				case "error":
					slog.Error("[inventory] required tool not found",
						"name", entry.Name, "command", entry.Command)
					// "skip": silent
				}
			} else {
				slog.Info("[inventory] tool found",
					"name", entry.Name, "command", entry.Command,
					"version", entry.Version, "path", entry.Path)
			}

			inv.Add(entry)
			cache.Put(entry)
		}(c)
	}
	wg.Wait()

	slog.Info("[inventory] discovery complete",
		"summary", inv.Summary(),
		"cache_hits", cacheHits,
		"probed", len(candidates)-cacheHits)
	return inv
}

// DiscoverFresh forces a full re-probe, ignoring the cache.
// Used by the `coden --tools-check` CLI command.
func DiscoverFresh(workspaceRoot string, cfg *config.ToolsConfig) *Inventory {
	cache, _ := OpenCache(workspaceRoot)
	if cache != nil {
		cache.InvalidateAll()
		_ = cache.Close()
	}
	return Discover(workspaceRoot, cfg)
}

// probeOne checks if a single tool candidate is available on the system.
func probeOne(cand ToolCandidate, timeout time.Duration) *ToolEntry {
	entry := &ToolEntry{
		Category:    cand.Category,
		Name:        cand.Name,
		Command:     cand.Command,
		Args:        cand.Args,
		Languages:   cand.Languages,
		InstallHint: cand.InstallHint,
		Priority:    cand.Priority,
		CheckedAt:   time.Now(),
	}

	// Step 1: Check if command exists in PATH.
	path, err := exec.LookPath(cand.Command)
	if err != nil {
		entry.Status = StatusUnavailable
		entry.Error = "not found in PATH"
		return entry
	}
	entry.Path = path

	// Step 2: Run version command if specified.
	if len(cand.VersionCmd) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, cand.VersionCmd[0], cand.VersionCmd[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Command exists but version check failed — still mark as available.
			// Some tools (like goimports) don't have a clean --version flag.
			entry.Status = StatusAvailable
			entry.Version = ""
			return entry
		}

		entry.Version = extractVersion(strings.TrimSpace(string(out)), cand.VersionPattern)
	}

	entry.Status = StatusAvailable
	return entry
}

// extractVersion tries to pull a version string from raw output.
// If pattern is provided, it's used as a regex with the first capture group.
// Otherwise, it looks for common version patterns.
func extractVersion(raw string, pattern string) string {
	if raw == "" {
		return ""
	}

	// Use custom pattern if provided.
	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err == nil {
			if m := re.FindStringSubmatch(raw); len(m) > 1 {
				return m[1]
			}
		}
	}

	// Try common version patterns: "v1.2.3", "1.2.3", "version 1.2.3"
	commonPatterns := []string{
		`v?(\d+\.\d+\.\d+[-\w.]*)`,
		`version\s+(\S+)`,
	}
	for _, p := range commonPatterns {
		re := regexp.MustCompile(p)
		if m := re.FindStringSubmatch(raw); len(m) > 1 {
			return m[1]
		}
	}

	// Last resort: return first line, capped.
	first := raw
	if idx := strings.IndexByte(first, '\n'); idx >= 0 {
		first = first[:idx]
	}
	if len(first) > 80 {
		first = first[:80]
	}
	return first
}

// HasNewFindings reports whether the Inventory contains available tools that
// are not already present in the given ToolsConfig. This is used to decide
// whether to auto-write config.
func HasNewFindings(inv *Inventory, cfg *config.ToolsConfig) bool {
	for _, e := range inv.Available() {
		switch e.Category {
		case CatLSP:
			if _, ok := cfg.LSP[e.Name]; cfg.LSP == nil || !ok {
				return true
			}
		case CatPackageManager:
			if _, ok := cfg.PackageManagers[e.Name]; cfg.PackageManagers == nil || !ok {
				return true
			}
		case CatInterpreter:
			if _, ok := cfg.Interpreters[e.Name]; cfg.Interpreters == nil || !ok {
				return true
			}
		case CatFormatter:
			if _, ok := cfg.Formatters[e.Name]; cfg.Formatters == nil || !ok {
				return true
			}
		case CatLinter:
			if _, ok := cfg.Linters[e.Name]; cfg.Linters == nil || !ok {
				return true
			}
		}
	}
	return false
}
