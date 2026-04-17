package insight

import "time"

// Category classifies the kind of insight extracted from an analysis turn.
type Category string

const (
	CategoryDecision   Category = "decision"
	CategoryFinding    Category = "finding"
	CategoryComparison Category = "comparison"
	CategoryDesign     Category = "design"
)

// Insight is a structured piece of knowledge extracted from a turn's messages.
// Insights persist across turns so future LLM calls can reference past decisions.
type Insight struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	Category     Category  `json:"category"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	Tags         []string  `json:"tags,omitempty"`
	Confidence   float64   `json:"confidence"`             // 0.0–1.0
	SupersededBy string    `json:"superseded_by,omitempty"` // ID of replacing insight
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Store manages insights (dual in-memory + SQLite).
type Store interface {
	Save(insight Insight) error
	// Supersede marks oldID as superseded by newInsight and saves newInsight.
	Supersede(oldID string, newInsight Insight) error
	ListBySession(sessionID string, limit int) []Insight
	// TopKByTags returns up to k active (non-superseded) insights whose tags
	// overlap with the provided tags, ordered by confidence desc then recency.
	TopKByTags(sessionID string, tags []string, k int) []Insight
	Close() error
}
