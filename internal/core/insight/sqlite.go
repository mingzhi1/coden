package insight

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"database/sql"

	_ "modernc.org/sqlite"
)

// NewSQLiteStore opens (or creates) a SQLite-backed insight store at path.
func NewSQLiteStore(path string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create insight db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open insight db: %w", err)
	}
	st := &sqliteStore{db: db}
	if err := st.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

// ── Pattern extraction (zero LLM cost) ───────────────────────────────────────

// patternRules lists heuristics for extracting insights from turn text.
var patternRules = []struct {
	trigger    string
	category   Category
	confidence float64
}{
	{"## 结论", CategoryDecision, 0.7},
	{"## 决策", CategoryDecision, 0.7},
	{"决定:", CategoryDecision, 0.65},
	{"P0", CategoryFinding, 0.6},
	{"P1", CategoryFinding, 0.55},
	{"## 对比", CategoryComparison, 0.6},
	{"方案A", CategoryComparison, 0.55},
	{"✅", CategoryFinding, 0.5},
	{"❌", CategoryFinding, 0.5},
	{"设计原则:", CategoryDesign, 0.65},
}

// ExtractInsights scans text for structural patterns indicating a decision,
// finding, comparison, or design note. Zero LLM cost — string-matching only.
// Callers must set SessionID on each returned Insight before saving.
func ExtractInsights(turnID, text string, now time.Time) []Insight {
	lines := strings.Split(text, "\n")
	var out []Insight
	seen := make(map[string]struct{})

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 5 {
			continue
		}
		for _, rule := range patternRules {
			if !strings.Contains(trimmed, rule.trigger) {
				continue
			}
			// Use the next non-empty line as content (max 200 chars).
			content := trimmed
			for j := i + 1; j < len(lines) && j <= i+3; j++ {
				next := strings.TrimSpace(lines[j])
				if next != "" {
					content = next
					break
				}
			}
			if len(content) > 200 {
				content = content[:200] + "…"
			}
			key := rule.trigger + ":" + content
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			title := trimmed
			if len(title) > 80 {
				title = title[:80] + "…"
			}
			ins := Insight{
				ID:         fmt.Sprintf("ins-%s-%d", turnID, len(out)),
				Category:   rule.category,
				Title:      title,
				Content:    content,
				Confidence: rule.confidence,
				Tags:       inferTags(trimmed),
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			out = append(out, ins)
			break // one rule per line
		}
	}
	return out
}

// inferTags extracts 1-3 lowercase tags from a line via keyword detection.
func inferTags(text string) []string {
	lower := strings.ToLower(text)
	candidates := []struct{ substr, tag string }{
		{"architect", "architecture"}, {"storage", "storage"},
		{"api", "api"}, {"rpc", "rpc"}, {"llm", "llm"},
		{"test", "testing"}, {"security", "security"},
		{"ui", "ui"}, {"tui", "ui"}, {"performan", "performance"},
		{"kernel", "kernel"}, {"workflow", "workflow"},
	}
	var tags []string
	for _, c := range candidates {
		if strings.Contains(lower, c.substr) {
			tags = append(tags, c.tag)
			if len(tags) >= 3 {
				break
			}
		}
	}
	return tags
}
