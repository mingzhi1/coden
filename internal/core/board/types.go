package board

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// ── Column IDs (Kanban 列标识) ──

const (
	ColumnBacklog    = "backlog"
	ColumnReady      = "ready"
	ColumnInProgress = "in_progress"
	ColumnReview     = "review"
	ColumnDone       = "done"
	ColumnBlocked    = "blocked"
)

// DefaultColumns returns the standard Kanban column set.
func DefaultColumns() []Column {
	return []Column{
		{ID: ColumnBacklog, Name: "Backlog", Position: 0, WIPLimit: 0, Color: "#6B7280"},
		{ID: ColumnReady, Name: "Ready", Position: 1, WIPLimit: 0, Color: "#3B82F6"},
		{ID: ColumnInProgress, Name: "In Progress", Position: 2, WIPLimit: 3, Color: "#F59E0B"},
		{ID: ColumnReview, Name: "Review", Position: 3, WIPLimit: 2, Color: "#8B5CF6"},
		{ID: ColumnDone, Name: "Done", Position: 4, WIPLimit: 0, Color: "#10B981"},
		{ID: ColumnBlocked, Name: "Blocked", Position: 5, WIPLimit: 0, Color: "#EF4444"},
	}
}

// ── Board ──

type Board struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ProjectID string    `json:"project_id"`
	Columns   []Column  `json:"columns"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Column struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	WIPLimit int    `json:"wip_limit"` // 0 = unlimited
	Color    string `json:"color"`     // hex color for UI
}

// ── Card ──

// Priority levels (lower = more urgent)
const (
	PriorityCritical = 0 // P0
	PriorityHigh     = 1 // P1
	PriorityMedium   = 2 // P2
	PriorityLow      = 3 // P3
)

type Card struct {
	ID          string     `json:"id"`          // hash-based: "cn-a1b2c3"
	ParentID    string     `json:"parent_id"`   // for hierarchical tasks: "cn-a1b2c3.1"
	BoardID     string     `json:"board_id"`
	ColumnID    string     `json:"column_id"`
	Position    int        `json:"position"`    // order within column
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    int        `json:"priority"`
	Labels      []string   `json:"labels"`
	AssigneeID  string     `json:"assignee_id"` // Agent ID
	Status      string     `json:"status"`      // mirrors task status
	// Graph links
	DependsOn  []string `json:"depends_on"`  // blocked by these card IDs
	RelatesTo  []string `json:"relates_to"`  // related cards
	Supersedes []string `json:"supersedes"`  // replaces these cards
	// File scope
	Files []string `json:"files"` // files this card will touch
	// Workflow binding
	WorkflowID string `json:"workflow_id"`
	SessionID  string `json:"session_id"`
	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ── Graph Link ──

type LinkType string

const (
	LinkBlocks     LinkType = "blocks"
	LinkRelatesTo  LinkType = "relates_to"
	LinkDuplicates LinkType = "duplicates"
	LinkSupersedes LinkType = "supersedes"
	LinkParentOf   LinkType = "parent_of"
)

type Link struct {
	ID       string   `json:"id"`
	FromCard string   `json:"from_card"`
	ToCard   string   `json:"to_card"`
	Type     LinkType `json:"type"`
}

// ── Card ID Generation ──

// GenerateCardID creates a hash-based card ID like "cn-a1b2c3".
func GenerateCardID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("board: crypto/rand failed: %v", err))
	}
	return "cn-" + hex.EncodeToString(b)
}

// GenerateSubCardID creates a hierarchical sub-card ID like "cn-a1b2c3.1".
func GenerateSubCardID(parentID string, seq int) string {
	return fmt.Sprintf("%s.%d", parentID, seq)
}

// ── Column / Task Status Mapping ──

// ColumnForStatus maps a task status to the appropriate kanban column.
func ColumnForStatus(taskStatus string) string {
	switch taskStatus {
	case model.TaskStatusPlanned:
		return ColumnBacklog
	case model.TaskStatusCoding:
		return ColumnInProgress
	case model.TaskStatusAccepting:
		return ColumnReview
	case model.TaskStatusPassed:
		return ColumnDone
	case model.TaskStatusFailed:
		return ColumnBlocked
	case model.TaskStatusRetrying:
		return ColumnInProgress
	case model.TaskStatusAbandoned:
		return ColumnDone
	case model.TaskStatusSkipped:
		return ColumnDone
	case model.TaskStatusRemoved:
		return ColumnDone
	default:
		return ColumnBacklog
	}
}

// StatusForColumn maps a kanban column to the default task status.
func StatusForColumn(columnID string) string {
	switch columnID {
	case ColumnBacklog:
		return model.TaskStatusPlanned
	case ColumnReady:
		return model.TaskStatusPlanned
	case ColumnInProgress:
		return model.TaskStatusCoding
	case ColumnReview:
		return model.TaskStatusAccepting
	case ColumnDone:
		return model.TaskStatusPassed
	case ColumnBlocked:
		return model.TaskStatusFailed
	default:
		return model.TaskStatusPlanned
	}
}

// ── Dependency Graph Helpers ──

// IsBlocked returns true if any of the card's dependencies are not in the Done column.
func (c *Card) IsBlocked(cardIndex map[string]*Card) bool {
	for _, depID := range c.DependsOn {
		dep, ok := cardIndex[depID]
		if !ok {
			// Unknown dependency is treated as unresolved.
			return true
		}
		if dep.ColumnID != ColumnDone {
			return true
		}
	}
	return false
}

// ReadyCards filters cards to those with all dependencies resolved.
func ReadyCards(cards []*Card) []*Card {
	index := make(map[string]*Card, len(cards))
	for _, c := range cards {
		index[c.ID] = c
	}

	var ready []*Card
	for _, c := range cards {
		if !c.IsBlocked(index) {
			ready = append(ready, c)
		}
	}
	return ready
}
