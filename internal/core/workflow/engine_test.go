package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

type testInputter struct{}

func (testInputter) Build(_ context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	return model.IntentSpec{
		ID:        "intent-test",
		SessionID: sessionID,
		Goal:      "normalized: " + prompt,
		CreatedAt: time.Now(),
	}, nil
}

func TestBuildIntentUsesInputter(t *testing.T) {
	t.Parallel()

	engine := NewWithInputter(testInputter{}, NewLocalPlanner(), NewLocalCoder())

	intent, err := engine.BuildIntent(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("BuildIntent failed: %v", err)
	}
	if intent.ID != "intent-test" {
		t.Fatalf("unexpected intent id: %q", intent.ID)
	}
	if intent.Goal != "normalized: hello" {
		t.Fatalf("unexpected intent goal: %q", intent.Goal)
	}
}

func TestCodeHandlesZeroTasks(t *testing.T) {
	t.Parallel()

	engine := New(NewLocalPlanner(), NewLocalCoder())

	plan, err := engine.Code(context.Background(), "wf-1", model.IntentSpec{
		ID:        "intent-1",
		SessionID: "session-1",
		Goal:      "handle empty tasks",
		CreatedAt: time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("Code failed: %v", err)
	}

	if plan.ToolCallID == "" {
		t.Fatal("expected tool call id")
	}
	if plan.Request.Path == "" {
		t.Fatal("expected artifact path")
	}
	if plan.Request.Content == "" {
		t.Fatal("expected artifact content")
	}
}
