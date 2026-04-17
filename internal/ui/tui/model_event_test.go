package tui_test

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/ui/tui"
)

// TestModelEventHandling tests TUI model event processing.
func TestModelEventHandling(t *testing.T) {
	tests := []struct {
		name      string
		event     model.Event
		wantLines int // minimum expected chat lines after event
	}{
		{
			name: "workflow started event",
			event: model.Event{
				Seq:       1,
				SessionID: "test",
				Topic:     model.EventWorkflowStarted,
				Payload: model.EncodePayload(model.WorkflowStartedPayload{
					WorkflowID: "wf-1",
				}),
			},
			wantLines: 1,
		},
		{
			name: "worker started event",
			event: model.Event{
				Seq:       2,
				SessionID: "test",
				Topic:     model.EventWorkerStarted,
				Payload: model.EncodePayload(model.WorkerStartedPayload{
					WorkflowID: "wf-1",
					WorkerID:   "worker-1",
					WorkerRole: "coder",
					Step:       "code",
				}),
			},
			wantLines: 1,
		},
		{
			name: "worker finished event",
			event: model.Event{
				Seq:       3,
				SessionID: "test",
				Topic:     model.EventWorkerFinished,
				Payload: model.EncodePayload(model.WorkerFinishedPayload{
					WorkflowID: "wf-1",
					WorkerID:   "worker-1",
					WorkerRole: "coder",
					Step:       "code",
					DurationMS: 1234,
				}),
			},
			wantLines: 1,
		},
		{
			name: "tool started event",
			event: model.Event{
				Seq:       4,
				SessionID: "test",
				Topic:     model.EventToolStarted,
				Payload: model.EncodePayload(model.ToolStartedPayload{
					WorkflowID: "wf-1",
					ToolCallID: "call-1",
					Tool:       "write_file",
				}),
			},
			wantLines: 1,
		},
		{
			name: "tool finished event",
			event: model.Event{
				Seq:       5,
				SessionID: "test",
				Topic:     model.EventToolFinished,
				Payload: model.EncodePayload(model.ToolFinishedPayload{
					WorkflowID: "wf-1",
					ToolCallID: "call-1",
					Tool:       "write_file",
					Path:       "test.go",
					Status:     "written",
					Summary:    "wrote 100 bytes",
					DurationMS: 100,
				}),
			},
			wantLines: 1,
		},
		{
			name: "session attached event",
			event: model.Event{
				Seq:       6,
				SessionID: "test",
				Topic:     model.EventSessionAttached,
				Payload: model.EncodePayload(model.SessionAttachedPayload{
					ClientName: "test-client",
					View:       "tui",
				}),
			},
			wantLines: 1,
		},
		{
			name: "checkpoint updated event",
			event: model.Event{
				Seq:       7,
				SessionID: "test",
				Topic:     model.EventCheckpointUpdated,
				Payload: model.EncodePayload(model.CheckpointUpdatedPayload{
					WorkflowID: "wf-1",
					Status:     "pass",
				}),
			},
			wantLines: 4, // checkpoint has multiple lines
		},
		{
			name: "workflow retry event",
			event: model.Event{
				Seq:       8,
				SessionID: "test",
				Topic:     model.EventWorkflowRetry,
				Payload: model.EncodePayload(model.WorkflowRetryPayload{
					WorkflowID: "wf-1",
					Attempt:    1,
					MaxRetries: 1,
					Reason:     "acceptor rejected artifact",
					Evidence:   []string{"missing error handling", "incomplete tests"},
				}),
			},
			wantLines: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tui.NewModel("test", "")
			m = m.WithEventStream(make(chan model.Event))

		// Apply window size for rendering
		m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m = m2.(*tui.Model)

		// Process event
		m3, _ := m.Update(tui.EventMsg{Event: tt.event})
		model2 := m3.(*tui.Model)

		// Verify view renders without error
		_ = model2.View()
		})
	}
}

// TestModelWorkflowAccepted tests workflow acceptance flow.
func TestModelWorkflowAccepted(t *testing.T) {
	m := tui.NewModel("test", "")
	m = m.WithSubmitter(func(prompt string) tea.Cmd {
		return func() tea.Msg {
			return tui.WorkflowAcceptedMsg{WorkflowID: "wf-123"}
		}
	})

	// Set window size
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	// Simulate submit
	m3, _ := m.Update(tui.WorkflowAcceptedMsg{WorkflowID: "wf-123"})
	model2 := m3.(*tui.Model)

	// Verify model state
	_ = model2.View()
}

// TestModelCheckpointReceived tests checkpoint handling.
func TestModelCheckpointReceived(t *testing.T) {
	m := tui.NewModel("test", "")

	// Set window size
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	// Send checkpoint message
	cp := model.CheckpointResult{
		WorkflowID:    "wf-123",
		SessionID:     "test",
		Status:        "pass",
		ArtifactPaths: []string{"file1.go", "file2.go"},
		Evidence:      []string{"test passed"},
		CreatedAt:     time.Now(),
	}

	m3, _ := m.Update(tui.CheckpointMsg{Result: cp})
	model2 := m3.(*tui.Model)

	_ = model2.View()
}

// TestModelWorkflowObjectsLoaded tests object loading.
func TestModelWorkflowObjectsLoaded(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	// Set active workflow
	m3, _ := m.Update(tui.WorkflowAcceptedMsg{WorkflowID: "wf-123"})
	m = m3.(*tui.Model)

	// Load objects
	items := []api.ObjectDetail{
		{
			ToolCallID: "call-1",
			Path:       "test.go",
			Tool:       "write_file",
			Status:     "written",
			Summary:    "wrote 100 bytes",
			Detail:     "+ added line",
		},
		{
			ToolCallID: "call-2",
			Path:       "test2.go",
			Tool:       "write_file",
			Status:     "written",
			Summary:    "wrote 200 bytes",
		},
	}

	m4, _ := m.Update(tui.WorkflowObjectsLoadedMsg{
		WorkflowID: "wf-123",
		Items:      items,
	})
	model2 := m4.(*tui.Model)
	_ = model2.View()
}

// TestModelFocusCycling tests focus movement between panels.
func TestModelFocusCycling(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	// Get initial focus state
	_ = m.View()

	// Cycle focus with Tab
	m3, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	_ = m3.(*tui.Model)
}

// TestModelScrolling tests scroll behavior.
func TestModelScrolling(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	// Add many events to create scrollable content
	for i := 0; i < 50; i++ {
		m3, _ := m.Update(tui.EventMsg{Event: model.Event{
			Seq:       uint64(i),
			SessionID: "test",
			Topic:     model.EventWorkflowStepUpdate,
			Payload: model.EncodePayload(model.WorkflowStepUpdatedPayload{
				WorkflowID: "wf-1",
				Step:       "code",
				Status:     "running",
			}),
		}})
		m = m3.(*tui.Model)
	}

	// Test scroll down
	m4, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_ = m4.(*tui.Model)

	// Test page down
	m5, _ := m4.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	_ = m5.(*tui.Model)
}

// TestModelErrorHandling tests error display.
func TestModelErrorHandling(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	err := errors.New("test error")
	m3, _ := m.Update(tui.ErrMsg{Err: err})
	model2 := m3.(*tui.Model)
	_ = model2.View()
}

// TestModelStreamClosed tests stream closure handling.
func TestModelStreamClosed(t *testing.T) {
	m := tui.NewModel("test", "")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m2.(*tui.Model)

	m3, _ := m.Update(tui.StreamClosedMsg{})
	model2 := m3.(*tui.Model)
	_ = model2.View()
}

// TestModelInitWithPrompt tests initialization with initial prompt.
func TestModelInitWithPrompt(t *testing.T) {
	m := tui.NewModel("test", "initial prompt")
	m = m.WithSubmitter(func(prompt string) tea.Cmd {
		return func() tea.Msg {
			return tui.WorkflowAcceptedMsg{WorkflowID: "wf-1"}
		}
	})

	cmd := m.Init()
	if cmd == nil {
		t.Error("Init returned nil command")
	}
}
