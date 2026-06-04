package tui

import (
	"strings"

	"github.com/mingzhi1/coden/internal/ui/styles"
)

// overlay.go is the single source of truth for overlay item selection and
// rendering, per docs/modules/tui/overlay_action_spec.md §23 ("只保留一套
// overlay 体系"). Both the model-level overlays (Permission Required, Model /
// Config, Help) and the app-level overlays (session picker, system errors) route
// through these helpers so they share one visual language and one navigation rule.

// overlaySelectableIndexList returns the indices of items the cursor may rest on:
// `action` items and `disabled` items (spec §246/§371). `disabled` carries no
// action string but must be focusable so Enter can surface an "unavailable"
// explanation rather than silently doing nothing.
func overlaySelectableIndexList(items []overlayItem) []int {
	indices := make([]int, 0, len(items))
	for i, item := range items {
		if strings.TrimSpace(item.action) != "" || item.kind == "disabled" {
			indices = append(indices, i)
		}
	}
	return indices
}

// renderOverlayItemLines renders overlay items to body lines following the spec's
// visual hierarchy (§276): section bold, selected action highlighted with a "> "
// prefix (§297), disabled visibly weaker than action, todo as muted roadmap text,
// warn/ok colored, kv-muted dimmed. selectedIdx is the absolute item index that
// currently holds the cursor (-1 for none).
func renderOverlayItemLines(items []overlayItem, selectedIdx int) []string {
	lines := make([]string, 0, len(items))
	for index, item := range items {
		selected := index == selectedIdx
		switch item.kind {
		case "spacer":
			lines = append(lines, "")
		case "section":
			lines = append(lines, styles.BoldText.Render(item.text))
		case "action":
			if selected {
				lines = append(lines, styles.PanelFocus.Padding(0, 1).Render("> "+item.text))
			} else {
				lines = append(lines, styles.PrimaryText.Render("  "+item.text))
			}
		case "disabled":
			// disabled: visually weaker than action; keep the "> " prefix when
			// selected so the cursor is still locatable (§293/§297).
			if selected {
				lines = append(lines, styles.MutedText.Render("> "+item.text))
			} else {
				lines = append(lines, styles.MutedText.Render("  "+item.text))
			}
		case "todo":
			lines = append(lines, styles.MutedText.Render("  "+item.text))
		case "warn":
			lines = append(lines, styles.WarningText.Render("  "+item.text))
		case "ok":
			lines = append(lines, styles.SuccessText.Render("  "+item.text))
		case "kv-muted":
			lines = append(lines, styles.MutedText.Render("  "+item.text))
		default:
			lines = append(lines, styles.NormalText.Render("  "+item.text))
		}
	}
	return lines
}

// selectedOverlayIndex maps a cursor position (an index into the selectable list)
// to the absolute item index, returning -1 when nothing is selectable.
func selectedOverlayIndex(items []overlayItem, cursor int) int {
	selectable := overlaySelectableIndexList(items)
	if len(selectable) == 0 {
		return -1
	}
	cursor = clamp(cursor, 0, len(selectable)-1)
	return selectable[cursor]
}
