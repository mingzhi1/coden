// Package profile maintains a cached "project profile" — durable, slow-changing
// facts about a workspace (languages, toolchain/build commands, a prose overview
// of what the project is, and its code style) so agents don't rediscover the
// basics on every run. The deterministic fields (languages, toolchain) are fed
// in by the caller from the tool inventory; the semantic fields (Overview,
// Style) are filled by a one-time LLM pass and cached until the project's
// dependency manifests change or the TTL expires.
package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultTTL bounds how long a profile is trusted even if manifests are unchanged.
const DefaultTTL = 7 * 24 * time.Hour

// manifestFiles are the dependency/build manifests whose content defines the
// project's identity. A change to any of them invalidates the cached profile.
var manifestFiles = []string{
	"go.mod", "go.sum",
	"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
	"requirements.txt", "pyproject.toml", "poetry.lock",
	"Cargo.toml", "Cargo.lock",
	"pom.xml", "build.gradle", "go.work",
}

// ProjectProfile is the cached, slow-changing knowledge about a workspace.
type ProjectProfile struct {
	Languages   []string  `json:"languages"`            // e.g. ["Go"] — from the tool inventory
	Toolchain   string    `json:"toolchain"`            // formatted compilers/build tools summary
	Overview    string    `json:"overview,omitempty"`   // prose: what the project is (LLM-filled)
	Style       string    `json:"style,omitempty"`      // prose: code conventions (LLM-filled)
	SourceHash  string    `json:"source_hash"`          // hash of manifest files; invalidates on change
	GeneratedAt time.Time `json:"generated_at"`
}

// HasSemantic reports whether the LLM-filled fields are present (so callers can
// decide whether the one-time enrichment pass still needs to run).
func (p *ProjectProfile) HasSemantic() bool {
	return p != nil && (strings.TrimSpace(p.Overview) != "" || strings.TrimSpace(p.Style) != "")
}

// Stale reports whether the profile must be rebuilt: missing, manifest hash
// changed, or older than ttl.
func (p *ProjectProfile) Stale(workspaceRoot string, ttl time.Duration) bool {
	if p == nil {
		return true
	}
	if p.SourceHash != ComputeSourceHash(workspaceRoot) {
		return true
	}
	return time.Since(p.GeneratedAt) > ttl
}

// Format renders the profile as a compact context block for injection into a
// worker's prompt. Returns "" when the profile carries nothing useful.
func (p *ProjectProfile) Format() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Project profile\n")
	if len(p.Languages) > 0 {
		fmt.Fprintf(&b, "- Languages: %s\n", strings.Join(p.Languages, ", "))
	}
	if s := strings.TrimSpace(p.Toolchain); s != "" {
		fmt.Fprintf(&b, "- Toolchain: %s\n", s)
	}
	if s := strings.TrimSpace(p.Overview); s != "" {
		fmt.Fprintf(&b, "- Overview: %s\n", s)
	}
	if s := strings.TrimSpace(p.Style); s != "" {
		fmt.Fprintf(&b, "- Style: %s\n", s)
	}
	out := b.String()
	if out == "## Project profile\n" {
		return ""
	}
	return out
}

// cachePath is where the profile is persisted, alongside the tool cache.
func cachePath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".coden", "project_profile.json")
}

// Load reads the cached profile. Returns nil (no error) when absent/unreadable —
// the caller treats nil as "rebuild".
func Load(workspaceRoot string) *ProjectProfile {
	data, err := os.ReadFile(cachePath(workspaceRoot))
	if err != nil {
		return nil
	}
	var p ProjectProfile
	if json.Unmarshal(data, &p) != nil {
		return nil
	}
	return &p
}

// Save persists the profile. Best-effort: returns an error but callers may log
// and continue (a failed cache write only costs a rebuild next time).
func Save(workspaceRoot string, p *ProjectProfile) error {
	if p == nil {
		return fmt.Errorf("nil profile")
	}
	dir := filepath.Join(workspaceRoot, ".coden")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath(workspaceRoot), data, 0o644)
}

// Invalidate removes the cached profile so the next build regenerates it. Used
// when something other than a manifest change (e.g. a structural code edit judged
// by the Secretary) makes the cached overview/style stale. Best-effort.
func Invalidate(workspaceRoot string) error {
	err := os.Remove(cachePath(workspaceRoot))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ComputeSourceHash hashes the contents of the project's manifest files. Missing
// files are skipped; the result is stable across runs and changes only when a
// manifest's bytes change.
func ComputeSourceHash(workspaceRoot string) string {
	h := sha256.New()
	names := append([]string(nil), manifestFiles...)
	sort.Strings(names) // deterministic order
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(workspaceRoot, name))
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}
