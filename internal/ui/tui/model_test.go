package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/model"

	"github.com/charmbracelet/x/ansi"
)

func testEvent(seq uint64, topic string, payload any) model.Event {
	return model.Event{
		Seq:       seq,
		SessionID: "demo-session",
		Topic:     topic,
		Payload:   model.EncodePayload(payload),
	}
}

func TestRenderChangedLinesUseAnsiColorsForStatusAndPath(t *testing.T) {
	m := NewModel("demo-session", "")
	m.addChangedPath("workspace/artifacts/intent-1.md", "written")

	lines := m.renderChangedPanelLines(40, 10)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"written",
		"intent-1.md",
		"\x1b[",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendered lines missing %q: %q", want, joined)
		}
	}
}

func TestModelInitialViewIncludesPanelsAndInput(t *testing.T) {
	m := NewModel("demo-session", "bootstrap CodeN")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	view := ansi.Strip(m.View().Content)

	for _, want := range []string{
		"CodeN",
		"idle",
		"demo-session",
		"Chat",
		"Input",
		"Workers + Tasks",
		"Changed Code",
		"input> bootstrap CodeN",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelInitialViewShowsSnapshotLoadingInsteadOfTaskPlaceholder(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "loading session snapshot...") {
		t.Fatalf("expected snapshot loading state\n%s", view)
	}
	if strings.Contains(view, "waiting for task plan") {
		t.Fatalf("unexpected placeholder copy\n%s", view)
	}
}

func TestModelTabCyclesPanelFocus(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	if m.focus != focusInput {
		t.Fatalf("expected initial focus input, got %q", m.focus)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = next.(*Model)
	if m.focus != focusTodo {
		t.Fatalf("expected todo focus after tab from input, got %q", m.focus)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "shift+tab"}))
	m = next.(*Model)
	if m.focus != focusInput {
		t.Fatalf("expected input focus after shift+tab, got %q", m.focus)
	}
}

func TestModelQuestionMarkShowsHelpOverlay(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "?"}))
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Help", "tab / shift+tab", "ctrl+x", "/skip [id]", "/undo", "Alt+n"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelShowsRuntimeConfigOverlayFromChatFocus(t *testing.T) {
	m := NewModel("demo-session", "").WithRuntimeInfo(RuntimeInfo{
		Model:           "gpt-5.4",
		Provider:        "openai",
		Mode:            "local",
		AllowShellKnown: true,
		AllowShell:      false,
		ConfigSource:    "env/cli",
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusChat

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "c"}))
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Model / Config",
		"LIVE RUNTIME",
		"AVAILABLE NOW",
		"SESSIONS",
		"PLANNED",
		"mode: local",
		"provider: openai",
		"model: gpt-5.4",
		"shell execution: blocked",
		"config source: env/cli",
		"[open] focus chat panel",
		"[open] focus input panel",
		"[open] focus changed code panel",
		"[Alt+n] new session",
		"[Alt+w] close session",
		"[unavailable] switch model at runtime",
		"[unavailable] edit runtime config",
		"[do] dismiss overlay",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelRuntimeOverlayNavigationAndEnterAction(t *testing.T) {
	m := NewModel("demo-session", "").WithRuntimeInfo(RuntimeInfo{
		Model:    "gpt-5.4",
		Provider: "openai",
		Mode:     "local",
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusChat

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "c"}))
	m = next.(*Model)
	if m.alert == nil {
		t.Fatal("expected runtime overlay")
	}
	if m.alert.cursor != 0 {
		t.Fatalf("expected first selectable action initially, got %d", m.alert.cursor)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = next.(*Model)
	if m.alert == nil || m.alert.cursor != 1 {
		t.Fatalf("expected cursor to move to second action, got %+v", m.alert)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(*Model)
	if m.alert != nil {
		t.Fatalf("expected overlay dismissed after action, got %+v", m.alert)
	}
	if m.focus != focusInput {
		t.Fatalf("expected focus to move to input, got %q", m.focus)
	}
}

func TestModelDoesNotOpenRuntimeConfigOverlayWhileTyping(t *testing.T) {
	m := NewModel("demo-session", "abc").WithRuntimeInfo(RuntimeInfo{
		Model: "gpt-5.4",
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusInput

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "c"}))
	m = next.(*Model)

	if m.alert != nil {
		t.Fatalf("expected no overlay while typing, got %+v", m.alert)
	}
	if !strings.Contains(m.ti.Value(), "c") {
		t.Fatalf("expected typed character to reach textarea, got %q", m.ti.Value())
	}
}

func TestModelEscDismissesHelpOverlay(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.alert = &alertState{level: "info", title: "Help", lines: []string{"x"}}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Help") {
		t.Fatalf("expected help overlay dismissed\n%s", view)
	}
}

func TestModelShiftEnterAddsNewlineInInput(t *testing.T) {
	m := NewModel("demo-session", "line1")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "shift+enter"}))
	m = next.(*Model)
	if m.ti.Value() != "line1\n" {
		t.Fatalf("expected newline in input, got %q", m.ti.Value())
	}
}

func TestModelEnterStartsSubmitAndCheckpointReturnsToIdle(t *testing.T) {
	m := NewModel("demo-session", "bootstrap CodeN").WithSubmitter(func(prompt string) tea.Cmd {
		return func() tea.Msg {
			if prompt != "bootstrap CodeN" {
				t.Fatalf("unexpected prompt: %s", prompt)
			}
			return CheckpointMsg{
				Result: model.CheckpointResult{
					WorkflowID:    "wf-1",
					SessionID:     "demo-session",
					Status:        "pass",
					ArtifactPaths: []string{"workspace/artifacts/intent-1.md"},
					Evidence:      []string{"artifact exists"},
				},
			}
		}
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(*Model)
	if m.status != "running" {
		t.Fatalf("expected running status, got %q", m.status)
	}
	if got := m.ti.Value(); got != "" {
		t.Fatalf("expected input to be cleared, got %q", got)
	}
	if cmd == nil {
		t.Fatal("expected submit command")
	}

	next, _ = m.Update(runCmdUntilNonSpinner(t, cmd))
	m = next.(*Model)
	view := m.View().Content

	for _, want := range []string{
		"idle",
		"checkpoint pass (wf-1)  artifacts=1 evidence=1",
		"intent-1.md",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelQQuitsOnlyWhenNotRunning(t *testing.T) {
	m := NewModel("demo-session", "")
	m.focus = focusChat // q should only quit from non-input panels

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "q"}))
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("expected quit command while idle")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %#v", cmd())
	}

	m.status = "running"
	next, cmd = m.Update(tea.KeyPressMsg(tea.Key{Text: "q"}))
	m = next.(*Model)
	if cmd != nil {
		t.Fatal("expected no quit command while running")
	}

	// q should NOT quit when focus is on input
	m.status = "idle"
	m.focus = focusInput
	next, cmd = m.Update(tea.KeyPressMsg(tea.Key{Text: "q"}))
	m = next.(*Model)
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q should not quit while input is focused")
		}
	}
}

func TestModelTracksTodoAndChangedCodePanels(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventWorkerStarted, model.WorkerStartedPayload{
			WorkerID:   "worker-planner-1",
			WorkerRole: "planner",
			Step:       "plan",
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(2, model.EventWorkflowTasks, model.WorkflowTasksUpdatedPayload{
			Tasks: []model.Task{
				{ID: "task-1", Title: "capture the user goal as an artifact", Status: "done"},
				{ID: "task-2", Title: "validate that an artifact exists and record evidence", Status: "running"},
			},
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(3, model.EventWorkerFinished, model.WorkerFinishedPayload{
			WorkerID:   "worker-planner-1",
			WorkerRole: "planner",
			Step:       "plan",
			DurationMS: 7,
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(4, model.EventWorkerStarted, model.WorkerStartedPayload{
			WorkerID:   "worker-coder-1",
			WorkerRole: "coder",
			Step:       "code",
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(5, model.EventWorkerFinished, model.WorkerFinishedPayload{
			WorkerID:   "worker-coder-1",
			WorkerRole: "coder",
			Step:       "code",
			ToolCallID: "tool-wf-1-write-file",
			DurationMS: 9,
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(6, model.EventToolStarted, model.ToolStartedPayload{
			ToolCallID: "tool-wf-1-write-file",
			Tool:       "write_file",
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(7, model.EventToolFinished, model.ToolFinishedPayload{
			ToolCallID: "tool-wf-1-write-file",
			Tool:       "write_file",
			Path:       "workspace/artifacts/intent-1.md",
			Status:     "written",
			DurationMS: 12,
		}),
	})
	m = next.(*Model)

	view := m.View().Content
	plainView := ansi.Strip(view)

	for _, want := range []string{
		"✓ planner / plan  7ms",
		"✓ coder / code  tool 9ms",
		"✓ capture the user goal as an artifact",
		"› validate that an artifact exists",
		"written x1",
		"intent-1.md",
		"write_file 12ms",
	} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("expected ANSI colors in changed code panel\n%s", view)
	}
}

func TestCheckpointMarksRunningWorkersDone(t *testing.T) {
	m := NewModel("demo-session", "")
	m.workers = []workerItem{
		{ID: "worker-1", Role: "planner", Step: "plan", Status: "running"},
		{ID: "worker-2", Role: "coder", Step: "code", Status: "done"},
	}
	m.spinnerActive = true

	next, _ := m.Update(CheckpointMsg{
		Result: model.CheckpointResult{
			WorkflowID:    "wf-1",
			SessionID:     "demo-session",
			Status:        "pass",
			ArtifactPaths: []string{"workspace/artifacts/intent-1.md"},
			Evidence:      []string{"artifact exists"},
		},
	})
	m = next.(*Model)

	if m.workers[0].Status != "done" || m.workers[1].Status != "done" {
		t.Fatalf("expected all workers done after checkpoint, got %+v", m.workers)
	}
	if m.spinnerActive {
		t.Fatal("expected spinner to stop after checkpoint")
	}
}

func TestModelShowsSpinnerWhileRunning(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.status = "running"
	m.spinnerActive = true

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "thinking...") {
		t.Fatalf("expected running spinner line\n%s", view)
	}
}

func TestStreamClosedIsNotRenderedAsError(t *testing.T) {
	m := NewModel("demo-session", "bootstrap CodeN")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.status = "running"
	m.spinnerActive = true

	next, _ := m.Update(StreamClosedMsg{})
	m = next.(*Model)

	if m.status != "disconnected" {
		t.Fatalf("expected disconnected status, got %q", m.status)
	}
	if m.spinnerActive {
		t.Fatal("expected spinner to stop after stream close")
	}

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"disconnected",
		"connection closed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "error: event stream closed") {
		t.Fatalf("stream close should not render as error\n%s", view)
	}
}

func TestModelRendersMessageConversation(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 32})

	events := []model.Event{
		testEvent(1, model.EventMessageCreated, model.MessageCreatedPayload{Role: "user", Content: "build it"}),
		testEvent(2, model.EventWorkflowStarted, model.WorkflowStartedPayload{WorkflowID: "wf-1"}),
		testEvent(3, model.EventToolStarted, model.ToolStartedPayload{
			WorkflowID: "wf-1",
			ToolCallID: "tool-wf-1-write-file",
			Tool:       "write_file",
			Path:       "workspace/artifacts/intent-1.md",
		}),
		testEvent(4, model.EventToolFinished, model.ToolFinishedPayload{
			WorkflowID: "wf-1",
			ToolCallID: "tool-wf-1-write-file",
			Tool:       "write_file",
			Path:       "workspace/artifacts/intent-1.md",
			Status:     "written",
			Summary:    "artifact updated",
			DurationMS: 12,
		}),
		testEvent(5, model.EventWorkspaceChanged, model.WorkspaceChangedPayload{
			WorkflowID: "wf-1",
			Path:       "workspace/artifacts/intent-1.md",
			Operation:  "write",
		}),
		testEvent(6, model.EventMessageCreated, model.MessageCreatedPayload{Role: "assistant", Content: "done"}),
	}

	for _, ev := range events {
		next, _ := m.Update(EventMsg{Event: ev})
		m = next.(*Model)
	}

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"build it",
		"CODE",
		"done",
		"workflow started  wf-1",
		"write_file intent-1.md",
		"intent-1.md",
		"artifact updated",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "workspace  write intent-1.md") {
		t.Fatalf("expected workspace echo to be suppressed\n%s", view)
	}
}

func TestModelRendersStandaloneWorkspaceChange(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventWorkspaceChanged, model.WorkspaceChangedPayload{
			WorkflowID: "wf-external",
			Path:       "workspace/src/app.go",
			Operation:  "write",
		}),
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"workspace  write app.go",
		"path: workspace/src/app.go",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelRendersReadFileToolPreview(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventToolStarted, model.ToolStartedPayload{
			WorkflowID: "wf-read",
			ToolCallID: "tool-wf-read-file",
			Tool:       "read_file",
			Path:       "workspace/internal/app/main.go",
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(2, model.EventToolFinished, model.ToolFinishedPayload{
			WorkflowID: "wf-read",
			ToolCallID: "tool-wf-read-file",
			Tool:       "read_file",
			Path:       "workspace/internal/app/main.go",
			Status:     "read",
			Summary:    "read 32 bytes from workspace/internal/app/main.go",
			Detail:     "package main\n\nfunc main() {}",
			DurationMS: 4,
		}),
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"read_file main.go",
		"main.go  4ms",
		"workspace/internal/app/main.go",
		"read 32 bytes from workspace/internal/app/main.go",
		"package main",
		"func main() {}",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelShowsDeniedShellToolWithoutPath(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventToolStarted, model.ToolStartedPayload{
			ToolCallID: "tool-wf-1-shell",
			Tool:       "run_shell",
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(EventMsg{
		Event: testEvent(2, model.EventToolFinished, model.ToolFinishedPayload{
			ToolCallID: "tool-wf-1-shell",
			Tool:       "run_shell",
			Status:     "denied",
			Detail:     "run_shell requires explicit approval (--allow-shell)",
			DurationMS: 3,
		}),
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"run_shell requires explicit approval",
		"tool: run_shell",
		"coden --allow-shell",
		"Permission Required",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelRendersAssistantMarkdownStructure(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	content := "# Title\n- first item\n> quoted\n```go\nfmt.Println(\"x\")\n```"
	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventMessageCreated, model.MessageCreatedPayload{
			Role:    "assistant",
			Content: content,
		}),
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"CODE", "Title", "first item", "quoted", "[go]", "fmt.Println(\"x\")"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestWorkflowAcceptedRendersLightSystemMessage(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(WorkflowAcceptedMsg{WorkflowID: "wf-123"})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "working... (wf-123)") {
		t.Fatalf("view missing accepted system message\n%s", view)
	}
}

func TestModelCtrlXRequestsWorkflowCancel(t *testing.T) {
	m := NewModel("demo-session", "").WithCanceler(func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			if workflowID != "wf-123" {
				t.Fatalf("unexpected workflow id: %s", workflowID)
			}
			return WorkflowCancelRequestedMsg{WorkflowID: workflowID}
		}
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.status = "running"
	m.activeWorkflowID = "wf-123"

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "ctrl+x"}))
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("expected cancel command")
	}

	next, _ = m.Update(cmd())
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"cancel requested (wf-123)", "Cancel Requested"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelCtrlXCancelFailureShowsError(t *testing.T) {
	m := NewModel("demo-session", "").WithCanceler(func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			return WorkflowCancelFailedMsg{WorkflowID: workflowID, Err: fmt.Errorf("kernel busy")}
		}
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.status = "running"
	m.activeWorkflowID = "wf-123"

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "ctrl+x"}))
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("expected cancel command")
	}

	next, _ = m.Update(cmd())
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"cancel failed (wf-123): kernel busy", "Cancel Failed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestCheckpointUpdatedLoadsCheckpointAndUpdatesSummary(t *testing.T) {
	m := NewModel("demo-session", "").WithCheckpointLoader(func(workflowID string) tea.Cmd {
		return func() tea.Msg {
			if workflowID != "wf-1" {
				t.Fatalf("unexpected workflow id: %s", workflowID)
			}
			return CheckpointMsg{
				Result: model.CheckpointResult{
					WorkflowID:    "wf-1",
					SessionID:     "demo-session",
					Status:        "pass",
					ArtifactPaths: []string{"workspace/artifacts/out.md"},
					Evidence:      []string{"ok"},
				},
			}
		}
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.status = "running"
	m.activeWorkflowID = "wf-1"

	next, cmd := m.Update(EventMsg{
		Event: testEvent(1, model.EventCheckpointUpdated, model.CheckpointUpdatedPayload{
			WorkflowID: "wf-1",
			Status:     "pass",
		}),
	})
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("expected checkpoint loader command")
	}

	next, _ = m.Update(cmd())
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"checkpoint pass (wf-1)  artifacts=1 evidence=1", "out.md", "idle"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestChangedPanelEnterLoadsWorkspacePreview(t *testing.T) {
	m := NewModel("demo-session", "").WithWorkspaceReader(func(path string) tea.Cmd {
		return func() tea.Msg {
			if path != "workspace/src/app.go" {
				t.Fatalf("unexpected path: %s", path)
			}
			return WorkspaceReadLoadedMsg{
				Path:    path,
				Content: "package main\n\nfunc main() {}",
			}
		}
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 32})
	m.focus = focusChanged
	m.changed = []changeItem{{Path: "workspace/src/app.go", Name: "app.go", Detail: "@@ -1 +1 @@\n-old\n+new"}}

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("expected workspace read command")
	}

	next, _ = m.Update(cmd())
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"preview", "package main", "func main() {}", "diff", "@@ -1 +1 @@"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestChangedPanelPreviewFailureShowsAlert(t *testing.T) {
	m := NewModel("demo-session", "").WithWorkspaceReader(func(path string) tea.Cmd {
		return func() tea.Msg {
			return WorkspaceReadLoadedMsg{Path: path, Err: fmt.Errorf("read denied")}
		}
	})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 32})
	m.focus = focusChanged
	m.changed = []changeItem{{Path: "workspace/src/app.go", Name: "app.go"}}

	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("expected workspace read command")
	}

	next, _ = m.Update(cmd())
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Preview Failed", "read denied"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelShowsWriteDiffPreviewInChatAndChangedCode(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventToolFinished, model.ToolFinishedPayload{
			ToolCallID: "tool-wf-1-write",
			Tool:       "write_file",
			Path:       "workspace/artifacts/test.txt",
			Status:     "written",
			Detail:     "--- artifacts/test.txt\n+++ artifacts/test.txt\n-old\n+new",
			DurationMS: 5,
		}),
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"written x1",
		"test.txt",
		"--- artifacts/test.txt",
		"+new",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestChangedPanelShowsSelectedDetailPreview(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusChanged

	for i, name := range []string{"a.txt", "b.txt"} {
		next, _ := m.Update(EventMsg{
			Event: testEvent(uint64(i+1), model.EventToolFinished, model.ToolFinishedPayload{
				ToolCallID: fmt.Sprintf("tool-%d", i+1),
				Tool:       "write_file",
				Path:       "workspace/artifacts/" + name,
				Status:     "written",
				Summary:    "wrote artifact",
				Detail:     "--- " + name + "\n+++ " + name + "\n-old\n+new",
			}),
		})
		m = next.(*Model)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "j"}))
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"b.txt", "--- b.txt", "+new"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestChangedPanelDiffPreviewUsesAnsiColors(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusChanged
	m.changed = []changeItem{{
		Name:   "a.txt",
		Detail: "@@ -1 +1 @@\n-old\n+new\n context",
	}}

	view := m.View().Content
	for _, want := range []string{"@@ -1 +1 @@", "-old", "+new"} {
		if !strings.Contains(ansi.Strip(view), want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("expected ANSI colors in diff detail\n%s", view)
	}
}

func TestWorkspaceChangedEventUpdatesChatAndChangedPanel(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventWorkspaceChanged, model.WorkspaceChangedPayload{
			WorkflowID: "wf-1",
			Path:       "workspace/src/app.go",
			Operation:  "updated",
		}),
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"workspace  wrote app.go", "updated x1", "app.go"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestChangedPanelNavigationMovesSelection(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusChanged
	m.changed = []changeItem{
		{Name: "a.txt"},
		{Name: "b.txt"},
		{Name: "c.txt"},
	}

	m.scrollFocusedTo(len(m.changed) - 1)
	if m.changedSel != 2 {
		t.Fatalf("expected bottom selection, got %d", m.changedSel)
	}

	m.scrollFocusedTo(0)
	if m.changedSel != 0 {
		t.Fatalf("expected top selection, got %d", m.changedSel)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = next.(*Model)
	if m.changedSel != 1 {
		t.Fatalf("expected down to move changed selection, got %d", m.changedSel)
	}
}

func TestChangedPanelPageKeysScrollDetail(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.focus = focusChanged
	m.changed = []changeItem{{
		Name:    "a.txt",
		Summary: "wrote artifact",
		Detail:  "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8",
	}}

	before := ansi.Strip(m.View().Content)
	if !strings.Contains(before, "line1") {
		t.Fatalf("expected initial detail to show first lines\n%s", before)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	m = next.(*Model)

	after := ansi.Strip(m.View().Content)
	if !strings.Contains(after, "line5") {
		t.Fatalf("expected paged detail to show later lines\n%s", after)
	}
}

func TestWorkflowObjectsLoadedUpdatesChangedDetail(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 32})
	m.activeWorkflowID = "wf-1"

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventToolFinished, model.ToolFinishedPayload{
			ToolCallID: "tool-wf-1-write",
			Tool:       "write_file",
			Path:       "workspace/artifacts/test.txt",
			Status:     "written",
			Detail:     "--- preview",
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(WorkflowObjectsLoadedMsg{
		WorkflowID: "wf-1",
		Items: []api.ObjectDetail{{
			ToolCallID: "tool-wf-1-write",
			Path:       "workspace/artifacts/test.txt",
			Tool:       "write_file",
			Status:     "written",
			Summary:    "wrote artifact",
			Detail:     "--- full\n+++ full\n-old\n+new",
		}},
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"wrote artifact", "--- full", "+new"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestChangedPanelListRowsStaySingleLine(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	m.focus = focusChanged
	m.changed = []changeItem{{
		Path:   `D:\_home\code_claw\CodeN\workspace\artifacts\intent-1774834781181-1.md`,
		Name:   `intent-1774834781181-1.md`,
		Status: "written",
		Tool:   "write_file",
		Detail: "--- old\n+++ new\n-old\n+new",
	}}

	lines := m.renderChangedPanelLines(40, 8)
	if len(lines) == 0 {
		t.Fatal("expected changed panel lines")
	}
	if strings.Contains(lines[0], "\n") {
		t.Fatalf("expected changed list row to stay single line, got %q", lines[0])
	}
}

func TestChangedPanelTruncatesLongPathsInDetail(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.focus = focusChanged
	m.changed = []changeItem{{
		Path:    `D:\_home\code_claw\CodeN\workspace\artifacts\intent-1774834781181-1.md`,
		Name:    `intent-1774834781181-1.md`,
		Status:  "written",
		Summary: "wrote artifact",
	}}

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "...") {
		t.Fatalf("expected long path to be truncated in detail\n%s", view)
	}
}

func TestWorkflowObjectsLoadedUpdatesReadFilePreview(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	m.activeWorkflowID = "wf-read"
	m.focus = focusChanged

	next, _ := m.Update(EventMsg{
		Event: testEvent(1, model.EventToolFinished, model.ToolFinishedPayload{
			ToolCallID: "tool-wf-read-1",
			Tool:       "read_file",
			Path:       "workspace/README.md",
			Status:     "read",
			Summary:    "read README",
			Detail:     "# preview truncated",
		}),
	})
	m = next.(*Model)

	next, _ = m.Update(WorkflowObjectsLoadedMsg{
		WorkflowID: "wf-read",
		Items: []api.ObjectDetail{{
			ToolCallID: "tool-wf-read-1",
			Path:       "workspace/README.md",
			Tool:       "read_file",
			Status:     "read",
			Summary:    "read README",
			Preview:    "# Title\nbody",
		}},
	})
	m = next.(*Model)

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"README.md", "read README", "preview", "# Title", "body"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestChangedPanelUsesReadFileOutputLabel(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	m.focus = focusChanged
	m.changed = []changeItem{{
		Name:   "README.md",
		Tool:   "read_file",
		Detail: "line1\nline2",
	}}

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"output", "line1", "line2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelRendersZeroMillisecondDurations(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})

	events := []model.Event{
		testEvent(1, model.EventToolFinished, model.ToolFinishedPayload{
			ToolCallID: "tool-wf-1-write-file",
			Tool:       "write_file",
			Path:       "workspace/artifacts/intent-1.md",
			Status:     "written",
			DurationMS: 0,
		}),
	}

	for _, ev := range events {
		next, _ := m.Update(EventMsg{Event: ev})
		m = next.(*Model)
	}

	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"write_file 0ms",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestModelScrollsChatWithoutSelection(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	m.focus = focusChat

	for i := 1; i <= 30; i++ {
		next, _ := m.Update(EventMsg{
			Event: testEvent(uint64(i), model.EventMessageCreated, model.MessageCreatedPayload{
				Role:    "assistant",
				Content: fmt.Sprintf("line %03d", i),
			}),
		})
		m = next.(*Model)
	}

	view := m.View().Content
	if strings.Contains(view, "line 001") {
		t.Fatalf("expected oldest event to be out of viewport by default\n%s", view)
	}

	// PgUp several times to scroll back toward the top
	for i := 0; i < 20; i++ {
		next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
		m = next.(*Model)
	}

	scrolled := m.View().Content
	if !strings.Contains(scrolled, "line 001") {
		t.Fatalf("expected scroll up to reveal oldest events\n%s", scrolled)
	}
}

func TestModelVimChatScrollKeys(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	m.focus = focusChat

	for i := 1; i <= 30; i++ {
		next, _ := m.Update(EventMsg{
			Event: testEvent(uint64(i), model.EventMessageCreated, model.MessageCreatedPayload{
				Role:    "assistant",
				Content: fmt.Sprintf("line %03d", i),
			}),
		})
		m = next.(*Model)
	}

	bottom := m.chatScroll
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "k"}))
	m = next.(*Model)
	if m.chatScroll >= bottom {
		t.Fatalf("expected k to scroll up, before=%d after=%d", bottom, m.chatScroll)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "g"}))
	m = next.(*Model)
	if m.chatScroll != 0 {
		t.Fatalf("expected g to jump to top, got %d", m.chatScroll)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "G"}))
	m = next.(*Model)
	if m.chatScroll != m.maxChatScroll() {
		t.Fatalf("expected G to jump to bottom, got %d want %d", m.chatScroll, m.maxChatScroll())
	}
}

func TestModelInitWithoutPromptDoesNotAutoSubmit(t *testing.T) {
	m := NewModel("demo-session", "").WithSubmitter(func(prompt string) tea.Cmd {
		t.Fatalf("unexpected auto submit for prompt %q", prompt)
		return nil
	})

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init command batch")
	}

	if msg := runCmdUntilNonSpinner(t, cmd); msg != nil {
		if _, ok := msg.(startSubmitMsg); ok {
			t.Fatalf("unexpected auto submit msg: %#v", msg)
		}
	}
}

func TestModelHistoryVimAndPagingKeys(t *testing.T) {
	m := NewModel("demo-session", "")
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	m.focus = focusChat
	m.chatTabActive = tabHistory

	for i := 1; i <= 20; i++ {
		next, _ := m.Update(EventMsg{
			Event: testEvent(uint64(i*2-1), model.EventMessageCreated, model.MessageCreatedPayload{
				Role:    "user",
				Content: fmt.Sprintf("prompt %02d", i),
			}),
		})
		m = next.(*Model)
		next, _ = m.Update(EventMsg{
			Event: testEvent(uint64(i*2), model.EventMessageCreated, model.MessageCreatedPayload{
				Role:    "assistant",
				Content: fmt.Sprintf("response %02d", i),
			}),
		})
		m = next.(*Model)
	}

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "j"}))
	m = next.(*Model)
	if m.turnSel != 1 {
		t.Fatalf("expected j to move history selection, got %d", m.turnSel)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "G"}))
	m = next.(*Model)
	if m.turnSel != len(m.turns)-1 {
		t.Fatalf("expected G to jump to bottom history entry, got %d", m.turnSel)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "g"}))
	m = next.(*Model)
	if m.turnSel != 0 {
		t.Fatalf("expected g to jump to top history entry, got %d", m.turnSel)
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	m = next.(*Model)
	if m.turnSel == 0 {
		t.Fatalf("expected PgDown to page history selection")
	}

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	m = next.(*Model)
	if m.turnSel != 0 {
		t.Fatalf("expected PgUp to page history back to top, got %d", m.turnSel)
	}
}

func runCmdUntilNonSpinner(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	msg := cmd()
	for {
		switch msg := msg.(type) {
		case nil:
			return nil
		case tea.BatchMsg:
			for _, sub := range msg {
				if sub == nil {
					continue
				}
				next := runCmdUntilNonSpinner(t, sub)
				if next != nil {
					return next
				}
			}
			return nil
		case spinner.TickMsg:
			return nil
		default:
			return msg
		}
	}
}
