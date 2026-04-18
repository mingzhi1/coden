package hook

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/events"
)

func TestManagerRegisterAndList(t *testing.T) {
	mgr := NewManager(nil)

	mgr.Register(Config{Name: "vet", Point: PostCode, Command: "go vet", Priority: 10})
	mgr.Register(Config{Name: "lint", Point: PostCode, Command: "golint", Priority: 5})
	mgr.Register(Config{Name: "notify", Point: PostWorkflow, Command: "echo done"})

	// List by point
	postCode := mgr.List(PostCode)
	if len(postCode) != 2 {
		t.Fatalf("expected 2 PostCode hooks, got %d", len(postCode))
	}
	// Should be sorted by priority: lint(5) before vet(10)
	if postCode[0].Name != "lint" {
		t.Errorf("expected first hook to be lint, got %s", postCode[0].Name)
	}

	// List all
	all := mgr.List("")
	if len(all) != 3 {
		t.Fatalf("expected 3 total hooks, got %d", len(all))
	}
}

func TestManagerRemove(t *testing.T) {
	mgr := NewManager(nil)
	mgr.Register(Config{Name: "vet", Point: PostCode, Command: "go vet"})
	mgr.Register(Config{Name: "notify", Point: PostWorkflow, Command: "echo done"})

	if !mgr.Remove("vet") {
		t.Error("expected Remove to return true")
	}
	if mgr.Remove("nonexistent") {
		t.Error("expected Remove to return false for nonexistent hook")
	}
	if mgr.HasHooks(PostCode) {
		t.Error("expected no PostCode hooks after removal")
	}
	if !mgr.HasHooks(PostWorkflow) {
		t.Error("expected PostWorkflow hooks to remain")
	}
}

func TestManagerRunSerialWithShortCircuit(t *testing.T) {
	mgr := NewManager(nil)

	// First hook passes, second blocks
	mgr.Register(Config{Name: "pass", Point: PostCode, Command: "true", Blocking: false, Priority: 1})
	mgr.Register(Config{Name: "fail", Point: PostCode, Command: "false", Blocking: true, Priority: 2})
	mgr.Register(Config{Name: "never", Point: PostCode, Command: "true", Priority: 3})

	results := mgr.Run(context.Background(), PostCode, &Context{WorkspaceRoot: "/tmp"})
	if len(results) != 2 {
		t.Fatalf("expected 2 results (short-circuited), got %d", len(results))
	}
	if results[0].Verdict != VerdictContinue {
		t.Errorf("first hook should continue, got %s", results[0].Verdict)
	}
	if results[1].Verdict != VerdictBlock {
		t.Errorf("second hook should block, got %s", results[1].Verdict)
	}
}

func TestManagerRunNoHooks(t *testing.T) {
	mgr := NewManager(nil)
	results := mgr.Run(context.Background(), PostCode, nil)
	if results != nil {
		t.Errorf("expected nil results for no hooks, got %v", results)
	}
}

func TestManagerRunWithEvents(t *testing.T) {
	bus := events.NewBus()
	mgr := NewManager(bus)
	mgr.Register(Config{Name: "echo", Point: PostCode, Command: "echo hello", Blocking: false})

	ch, cancel := bus.Subscribe("")
	defer cancel()

	go mgr.Run(context.Background(), PostCode, &Context{
		SessionID:     "s1",
		WorkflowID:    "wf1",
		WorkspaceRoot: "/tmp",
	})

	// Should receive hook.started and hook.finished events
	timeout := time.After(5 * time.Second)
	var topics []string
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			topics = append(topics, ev.Topic)
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
	if len(topics) != 2 {
		t.Fatalf("expected 2 events, got %d", len(topics))
	}
	if topics[0] != "hook.started" {
		t.Errorf("expected hook.started, got %s", topics[0])
	}
	if topics[1] != "hook.finished" {
		t.Errorf("expected hook.finished, got %s", topics[1])
	}
}

func TestHasBlockingFailure(t *testing.T) {
	results := []Result{
		{Name: "a", Verdict: VerdictContinue},
		{Name: "b", Verdict: VerdictBlock},
	}
	if !HasBlockingFailure(results) {
		t.Error("expected blocking failure")
	}
	if HasBlockingFailure(results[:1]) {
		t.Error("expected no blocking failure")
	}
}

func TestFormatBlockingErrors(t *testing.T) {
	results := []Result{
		{Name: "a", Verdict: VerdictContinue, Output: "ok"},
		{Name: "b", Verdict: VerdictBlock, Output: "error: test failed"},
	}
	msg := FormatBlockingErrors(results)
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}
	if !contains(msg, "error: test failed") {
		t.Errorf("expected error output in message, got: %s", msg)
	}
}

func TestValidPoint(t *testing.T) {
	if !ValidPoint(PostCode) {
		t.Error("PostCode should be valid")
	}
	if ValidPoint("invalid_point") {
		t.Error("invalid_point should not be valid")
	}
}

func TestContextToEnv(t *testing.T) {
	ctx := &Context{
		SessionID:  "s1",
		WorkflowID: "wf1",
		ToolName:   "run_shell",
	}
	env := ctx.ToEnv()
	found := map[string]bool{}
	for _, e := range env {
		if contains(e, "CODEN_HOOK_SESSION_ID=s1") {
			found["session"] = true
		}
		if contains(e, "CODEN_HOOK_TOOL_NAME=run_shell") {
			found["tool"] = true
		}
	}
	if !found["session"] {
		t.Error("expected CODEN_HOOK_SESSION_ID in env")
	}
	if !found["tool"] {
		t.Error("expected CODEN_HOOK_TOOL_NAME in env")
	}
}

func TestRegisterBatch(t *testing.T) {
	mgr := NewManager(nil)
	mgr.RegisterBatch([]Config{
		{Name: "a", Point: PreCode, Command: "true"},
		{Name: "b", Point: PostCode, Command: "true"},
		{Name: "c", Point: PostCode, Command: "true"},
	})
	if len(mgr.List("")) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(mgr.List("")))
	}
	if len(mgr.List(PostCode)) != 2 {
		t.Fatalf("expected 2 PostCode hooks, got %d", len(mgr.List(PostCode)))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
