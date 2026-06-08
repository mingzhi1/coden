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

// maxInsightsPerTurn caps how many insights a single turn contributes, so a long
// analysis with many sections doesn't flood the memory store.
const maxInsightsPerTurn = 12

// ExtractInsights scans text for structural patterns indicating a decision,
// finding, comparison, or design note. Zero LLM cost — string-matching only.
// Specific marker rules win; otherwise markdown headings (## / ###) are captured
// as findings so ordinary analysis prose still yields memory. Callers must set
// SessionID on each returned Insight before saving.
func ExtractInsights(turnID, text string, now time.Time) []Insight {
	lines := strings.Split(text, "\n")
	var out []Insight
	seen := make(map[string]struct{})

	nextNonEmpty := func(i int) string {
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			if s := strings.TrimSpace(lines[j]); s != "" {
				return s
			}
		}
		return ""
	}
	add := func(cat Category, titleLine, content string, conf float64) {
		if len(out) >= maxInsightsPerTurn {
			return
		}
		if strings.TrimSpace(content) == "" {
			content = titleLine
		}
		if len(content) > 200 {
			content = content[:200] + "…"
		}
		key := string(cat) + ":" + content
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		title := titleLine
		if len(title) > 80 {
			title = title[:80] + "…"
		}
		out = append(out, Insight{
			ID:         fmt.Sprintf("ins-%s-%d", turnID, len(out)),
			Category:   cat,
			Title:      title,
			Content:    content,
			Confidence: conf,
			Tags:       inferTags(titleLine),
			CreatedAt:  now,
			UpdatedAt:  now,
		})
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 5 {
			continue
		}
		matched := false
		for _, rule := range patternRules {
			if strings.Contains(trimmed, rule.trigger) {
				add(rule.category, trimmed, nextNonEmpty(i), rule.confidence)
				matched = true
				break // one rule per line
			}
		}
		// Fall back to markdown headings (h2/h3) — they carry the structure and
		// TL;DRs of an analysis, so prose without explicit markers still produces
		// memory. Title = heading text, content = the first line beneath it.
		if !matched {
			if h := atxHeadingText(trimmed); h != "" {
				add(CategoryFinding, h, nextNonEmpty(i), 0.5)
			}
		}
		if len(out) >= maxInsightsPerTurn {
			break
		}
	}
	return out
}

// atxHeadingText returns the cleaned text of a "## "/"### " markdown heading, or
// "" if the line isn't an h2/h3 heading. Deeper levels are ignored as too
// granular to be useful memory.
func atxHeadingText(line string) string {
	if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
		return strings.TrimSpace(strings.TrimLeft(line, "# "))
	}
	return ""
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
