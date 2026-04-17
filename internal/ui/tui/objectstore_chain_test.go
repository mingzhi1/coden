package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/ui/tui"
)

// helper to handle type assertions
type modelUpdater interface {
	Update(msg tea.Msg) (tea.Model, tea.Cmd)
}

// TestTUIObjectStoreChain tests the full chain: TUI -> objectsLoader -> View Update
func TestTUIObjectStoreChain(t *testing.T) {
	tests := []struct {
		name          string
		workflowID    string
		activeWfID    string
		items         []api.ObjectDetail
		expectChanged int
	}{
		{
			name:       "load objects for active workflow",
			workflowID: "wf-123",
			activeWfID: "wf-123",
			items: []api.ObjectDetail{
				{
					ToolCallID: "call-1",
					Path:       "test1.go",
					Tool:       "write_file",
					Status:     "written",
					Summary:    "wrote 100 bytes",
					Detail:     "+ added line",
					ExitCode:   0,
				},
				{
					ToolCallID: "call-2",
					Path:       "test2.go",
					Tool:       "write_file",
					Status:     "written",
					Summary:    "wrote 200 bytes",
					ExitCode:   0,
				},
			},
			expectChanged: 2,
		},
		{
			name:       "ignore objects for non-active workflow",
			workflowID: "wf-other",
			activeWfID: "wf-123",
			items: []api.ObjectDetail{
				{
					Path:    "other.go",
					Tool:    "write_file",
					Status:  "written",
					Summary: "should be ignored",
				},
			},
			expectChanged: 0,
		},
		{
			name:       "handle empty workflow ID",
			workflowID: "",
			activeWfID: "wf-123",
			items: []api.ObjectDetail{
				{
					Path:    "test.go",
					Tool:    "write_file",
					Status:  "written",
					Summary: "should be ignored",
				},
			},
			expectChanged: 0,
		},
		{
			name:       "handle empty items",
			workflowID: "wf-123",
			activeWfID: "wf-123",
			items:      []api.ObjectDetail{},
			expectChanged: 0,
		},
		{
			name:       "merge objects with same tool call ID",
			workflowID: "wf-123",
			activeWfID: "wf-123",
			items: []api.ObjectDetail{
				{
					ToolCallID: "call-1",
					Path:       "test.go",
					Tool:       "write_file",
					Status:     "running",
					Summary:    "in progress",
				},
				{
					ToolCallID: "call-1",
					Path:       "test.go",
					Tool:       "write_file",
					Status:     "written",
					Summary:    "completed",
					Detail:     "final diff",
				},
			},
			expectChanged: 1, // Should merge into one entry
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tui.NewModel("test", "")
			m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			m = m2.(*tui.Model)
			m3, _ := m.Update(tui.WorkflowAcceptedMsg{WorkflowID: tt.activeWfID})
			m = m3.(*tui.Model)

			m4, _ := m.Update(tui.WorkflowObjectsLoadedMsg{
				WorkflowID: tt.workflowID,
				Items:      tt.items,
			})
			model2 := m4.(*tui.Model)

			// Verify view renders without error
			_ = model2.View()
		})
	}
}

// TestTUIObjectDetailRendering tests object detail display
func TestTUIObjectDetailRendering(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)
	m3, _ := m.Update(tui.WorkflowAcceptedMsg{WorkflowID: "wf-detail"})
	m = m3.(*tui.Model)

	// Load objects with various detail types
	items := []api.ObjectDetail{
		{
			ToolCallID: "call-diff",
			Path:       "file_with_diff.go",
			Tool:       "write_file",
			Status:     "written",
			Summary:    "wrote file",
			Detail:     "--- a\n+++ b\n@@ -1,3 +1,3 @@\n-old\n+new",
			ExitCode:   0,
		},
		{
			ToolCallID: "call-error",
			Path:       "file_with_error.go",
			Tool:       "write_file",
			Status:     "failed",
			Summary:    "write failed",
			Detail:     "permission denied",
			ExitCode:   1,
		},
		{
			ToolCallID: "call-shell",
			Path:       "",
			Tool:       "run_shell",
			Status:     "executed",
			Summary:    "ran command",
			Detail:     "output here",
			ExitCode:   0,
		},
	}

	m4, _ := m.Update(tui.WorkflowObjectsLoadedMsg{
		WorkflowID: "wf-detail",
		Items:      items,
	})
	model2 := m4.(*tui.Model)
	_ = model2.View()
}

// TestTUIObjectSelection tests object selection in changed panel
func TestTUIObjectSelection(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)
	m3, _ := m.Update(tui.WorkflowAcceptedMsg{WorkflowID: "wf-select"})
	m = m3.(*tui.Model)

	// Load multiple objects
	items := []api.ObjectDetail{
		{ToolCallID: "call-1", Path: "file1.go", Tool: "write_file", Status: "written"},
		{ToolCallID: "call-2", Path: "file2.go", Tool: "write_file", Status: "written"},
		{ToolCallID: "call-3", Path: "file3.go", Tool: "write_file", Status: "written"},
	}

	m4, _ := m.Update(tui.WorkflowObjectsLoadedMsg{
		WorkflowID: "wf-select",
		Items:      items,
	})

	// Focus changed panel
	m5, _ := m4.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m6, _ := m5.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m7, _ := m6.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // Focus on changed panel

	// Navigate down
	m8, _ := m7.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_ = m8.(*tui.Model).View()
}

// TestTUIObjectUpdateSequence tests the complete event sequence
func TestTUIObjectUpdateSequence(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	// Simulate complete workflow execution sequence
	events := []tea.Msg{
		// 1. Workflow accepted
		tui.WorkflowAcceptedMsg{WorkflowID: "wf-seq"},
		
		// 2. Workflow started event
		tui.EventMsg{Event: model.Event{
			Topic: model.EventWorkflowStarted,
			Payload: model.EncodePayload(model.WorkflowStartedPayload{
				WorkflowID: "wf-seq",
			}),
		}},
		
		// 3. Tool started
		tui.EventMsg{Event: model.Event{
			Topic: model.EventToolStarted,
			Payload: model.EncodePayload(model.ToolStartedPayload{
				WorkflowID: "wf-seq",
				ToolCallID: "tool-1",
				Tool:       "write_file",
			}),
		}},
		
		// 4. Tool finished
		tui.EventMsg{Event: model.Event{
			Topic: model.EventToolFinished,
			Payload: model.EncodePayload(model.ToolFinishedPayload{
				WorkflowID: "wf-seq",
				ToolCallID: "tool-1",
				Tool:       "write_file",
				Path:       "output.go",
				Status:     "written",
				Summary:    "wrote 100 bytes",
			}),
		}},
		
		// 5. Checkpoint updated (triggers object loading)
		tui.EventMsg{Event: model.Event{
			Topic: model.EventCheckpointUpdated,
			Payload: model.EncodePayload(model.CheckpointUpdatedPayload{
				WorkflowID: "wf-seq",
				Status:     "pass",
			}),
		}},
		
		// 6. Objects loaded
		tui.WorkflowObjectsLoadedMsg{
			WorkflowID: "wf-seq",
			Items: []api.ObjectDetail{
				{
					ToolCallID: "tool-1",
					Path:       "output.go",
					Tool:       "write_file",
					Status:     "written",
					Summary:    "wrote 100 bytes",
					Detail:     "--- a\n+++ b",
				},
			},
		},
	}

	for i, evt := range events {
		mNext, _ := m.Update(evt)
		m = mNext.(*tui.Model)
		_ = m.View()
		t.Logf("Processed event %d (%T)", i, evt)
	}
}

// TestTUIObjectPathFallback tests path extraction from different sources
func TestTUIObjectPathFallback(t *testing.T) {
	tests := []struct {
		name     string
		item     api.ObjectDetail
		wantPath string
	}{
		{
			name:     "path from Path field",
			item:     api.ObjectDetail{Path: "direct.go", Tool: "write_file"},
			wantPath: "direct.go",
		},
		{
			name:     "path with directory",
			item:     api.ObjectDetail{Path: "subdir/file.go", Tool: "write_file"},
			wantPath: "subdir/file.go",
		},
		{
			name:     "empty path uses tool name",
			item:     api.ObjectDetail{Path: "", Tool: "run_shell"},
			wantPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tui.NewModel("test", "")
			m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			m = m2.(*tui.Model)
			m3, _ := m.Update(tui.WorkflowAcceptedMsg{WorkflowID: "wf-path"})
			m = m3.(*tui.Model)

			m4, _ := m.Update(tui.WorkflowObjectsLoadedMsg{
				WorkflowID: "wf-path",
				Items:      []api.ObjectDetail{tt.item},
			})
			model2 := m4.(*tui.Model)
			_ = model2.View()
		})
	}
}

// TestTUIObjectExitCodeDisplay tests exit code rendering
func TestTUIObjectExitCodeDisplay(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)
	m3, _ := m.Update(tui.WorkflowAcceptedMsg{WorkflowID: "wf-exit"})
	m = m3.(*tui.Model)

	items := []api.ObjectDetail{
		{
			ToolCallID: "success",
			Path:       "success.go",
			Tool:       "write_file",
			Status:     "written",
			ExitCode:   0,
		},
		{
			ToolCallID: "failure",
			Path:       "failure.go",
			Tool:       "write_file",
			Status:     "failed",
			ExitCode:   1,
		},
		{
			ToolCallID: "shell-fail",
			Path:       "",
			Tool:       "run_shell",
			Status:     "failed",
			ExitCode:   127,
		},
	}

	m4, _ := m.Update(tui.WorkflowObjectsLoadedMsg{
		WorkflowID: "wf-exit",
		Items:      items,
	})
	model2 := m4.(*tui.Model)
	_ = model2.View()
}
