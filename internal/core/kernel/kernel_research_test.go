package kernel

import (
	"context"
	"strings"
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

// TestRunResearchTask_ProducesFindingsArtifact verifies an in-bucket research task
// runs the read-only Analyzer on its query and hands the findings back as the
// outcome's Artifact (Summary), so they can flow to dependents via DepArtifacts.
// No analyzer is wired, so the deterministic LocalAnalyzer answers — enough to
// assert the query drives the findings and the task auto-passes read-only.
func TestRunResearchTask_ProducesFindingsArtifact(t *testing.T) {
	k := NewWithWorkflowDependencies(t.TempDir(), researchInputter{}, testPlanner{}, testExecutor{}, testToolExecutor{}, testAcceptor{})
	defer k.Close()

	in := taskInput{task: model.Task{
		ID:      "research-stripe-charges-api",
		Title:   "look up the Stripe charges API",
		SubGoal: "what fields does the Stripe charges API require",
		Status:  model.TaskStatusPlanned,
	}}
	out := k.runResearchTask(context.Background(), "s1", "wf1",
		model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "build a payment flow", Kind: model.IntentKindCodeGen},
		in, model.WorkflowContext{})

	if out.status != model.TaskStatusPassed {
		t.Fatalf("expected passed, got %q (err=%v)", out.status, out.err)
	}
	if !strings.Contains(out.artifact.Summary, "Stripe charges API") {
		t.Errorf("findings should reflect the query, got %q", out.artifact.Summary)
	}
	if len(out.artifact.Evidence) == 0 {
		t.Error("expected read-only research evidence on the artifact")
	}
}

// TestScheduler_ResearchArtifactFlowsToDependent proves the end-to-end mechanism
// the research tmp artifact exists for: a task that DEPENDS on a research task is
// blocked until it passes, then receives its findings via DepArtifacts (§11). A
// stub worker stands in for the kernel so the test isolates the scheduler plumbing
// — research-task routing, artifact retention, and dep projection.
func TestScheduler_ResearchArtifactFlowsToDependent(t *testing.T) {
	research := model.Task{ID: "research-foo", Title: "research foo", Status: model.TaskStatusPlanned}
	dependent := model.Task{ID: "impl", Title: "use foo", Status: model.TaskStatusPlanned, DependsOn: []string{"research-foo"}}
	s := newBucketScheduler([]model.Task{research, dependent}, 1)

	var gotFindings string
	worker := func(_ context.Context, in taskInput) taskOutcome {
		if isResearchTaskID(in.task.ID) {
			return taskOutcome{taskID: in.task.ID, status: model.TaskStatusPassed,
				artifact: model.Artifact{Summary: "FINDINGS: foo is X", Evidence: []string{"external research"}}}
		}
		gotFindings = formatDepFindings(in.depArtifacts)
		return taskOutcome{taskID: in.task.ID, status: model.TaskStatusPassed}
	}
	if err := s.run(context.Background(), worker); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(gotFindings, "FINDINGS: foo is X") {
		t.Errorf("dependent did not receive research findings via DepArtifacts; got %q", gotFindings)
	}
}
