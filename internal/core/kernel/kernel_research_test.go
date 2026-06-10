package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// researchInputter classifies every request as a research intent, so Submit
// routes through intent recognition → the Research workflow mode.
type researchInputter struct{}

func (researchInputter) Build(_ context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	return model.IntentSpec{
		ID: "intent-research", SessionID: sessionID, Goal: prompt,
		Kind: model.IntentKindResearch, CreatedAt: time.Now(),
	}, nil
}

func (researchInputter) TakeMessages() []model.WorkerMessage { return nil }

// TestResearchWorkflow_DispatchedAndMultiAgent drives the research path end-to-end
// through Submit: intent recognition classifies research → the dispatcher selects
// the Research workflow mode → the multi-agent bucket engine (Planner → Executor →
// Acceptor) runs read-only → the saga commits a passing checkpoint. Verifies the
// workflow TYPE is realized by the existing agent pool — no dedicated role.
func TestResearchWorkflow_DispatchedAndMultiAgent(t *testing.T) {
	k := NewWithWorkflowDependencies(t.TempDir(), researchInputter{}, testPlanner{}, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "s1", "research the Stripe API")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	timeout := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed unexpectedly")
			}
			if ev.Topic == model.EventCheckpointUpdated {
				done = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for research workflow to complete")
		}
	}

	summary, ok := k.turnSummaries.Get(wfID)
	if !ok {
		t.Fatal("expected a turn summary after research workflow")
	}
	if summary.Checkpoint.Status != "pass" {
		t.Errorf("expected pass checkpoint, got %q", summary.Checkpoint.Status)
	}
}
