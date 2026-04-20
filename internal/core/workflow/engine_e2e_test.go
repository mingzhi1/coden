package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// --- Stubs for E2E ---

// e2ePlanner returns tasks echoing the intent goal.
type e2ePlanner struct{}

func (e2ePlanner) Plan(_ context.Context, _ string, intent model.IntentSpec) ([]model.Task, error) {
	return []model.Task{
		{ID: "task-1", Title: "implement: " + intent.Goal, Status: "planned", Created: time.Now()},
	}, nil
}

func (e2ePlanner) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "plan", Role: "planner", Content: "plan produced"}}
}

func (e2ePlanner) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "e2e-planner", Role: RolePlanner}
}

// e2eCritic rejects the first call (score 0.3) and approves the second.
type e2eCritic struct {
	calls int
}

func (c *e2eCritic) Critique(_ context.Context, _ string, _ model.IntentSpec, _ []model.Task) (model.CritiqueResult, error) {
	c.calls++
	if c.calls == 1 {
		return model.CritiqueResult{
			Score:       0.3,
			Approved:    false,
			Issues:      []string{"missing error handling"},
			Suggestions: []string{"add error checks to all IO operations"},
			Summary:     "plan needs improvement",
		}, nil
	}
	return model.CritiqueResult{Score: 0.9, Approved: true, Summary: "plan approved"}, nil
}

func (c *e2eCritic) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "critique", Role: "critic", Content: fmt.Sprintf("critique #%d", c.calls)}}
}

func (c *e2eCritic) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "e2e-critic", Role: RoleCritic}
}

// e2eReplanner refines tasks by appending "refined" and incorporates snippets.
type e2eReplanner struct {
	receivedSnippets []model.FileSnippet
}

func (r *e2eReplanner) RePlan(_ context.Context, _ model.IntentSpec, tasks []model.Task, snippets []model.FileSnippet) ([]model.Task, error) {
	r.receivedSnippets = snippets
	refined := make([]model.Task, len(tasks))
	for i, t := range tasks {
		refined[i] = t
		refined[i].Title = t.Title + " [refined]"
		refined[i].Status = "refined"
	}
	return refined, nil
}

func (r *e2eReplanner) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "replan", Role: "replanner", Content: "tasks refined"}}
}

func (r *e2eReplanner) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "e2e-replanner", Role: RoleReplanner}
}

// e2eCoder checks that it receives the refined tasks.
type e2eCoder struct {
	receivedTasks []model.Task
}

func (c *e2eCoder) Build(_ context.Context, wfID string, intent model.IntentSpec, tasks []model.Task) (CodePlan, error) {
	c.receivedTasks = tasks
	return CodePlan{
		ToolCallID: "tool-" + wfID,
		Request: toolruntime.Request{
			Kind:    "write_file",
			Path:    "out/" + intent.ID + ".go",
			Content: "package main",
		},
		ToolCalls: []ToolCall{{
			ToolCallID: "tool-" + wfID,
			Request: toolruntime.Request{
				Kind:    "write_file",
				Path:    "out/" + intent.ID + ".go",
				Content: "package main",
			},
		}},
	}, nil
}

func (c *e2eCoder) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "code", Role: "coder", Content: "code produced"}}
}

func (c *e2eCoder) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "e2e-coder", Role: RoleCoder}
}

// e2eAcceptor always passes.
type e2eAcceptor struct{}

func (e2eAcceptor) Accept(_ context.Context, wfID string, intent model.IntentSpec, art model.Artifact, _ []model.Task) (model.CheckpointResult, error) {
	return model.CheckpointResult{
		WorkflowID: wfID,
		SessionID:  intent.SessionID,
		Status:     "pass",
		CreatedAt:  time.Now(),
	}, nil
}

func (e2eAcceptor) TakeMessages() []model.WorkerMessage {
	return []model.WorkerMessage{{Kind: "accept", Role: "acceptor", Content: "accepted"}}
}

func (e2eAcceptor) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "e2e-acceptor", Role: RoleAcceptor}
}

// TestFullPipelineE2E exercises the complete workflow:
// Inputter → Planner → Critic(reject) → Replanner → Critic(approve) → Coder → Acceptor
func TestFullPipelineE2E(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	critic := &e2eCritic{}
	replanner := &e2eReplanner{}
	coder := &e2eCoder{}

	engine := NewWithInputter(testInputter{}, e2ePlanner{}, coder, e2eAcceptor{})
	engine.SetCritic(critic)
	engine.SetReplanner(replanner)

	// Step 1: Build intent
	intent, err := engine.BuildIntent(ctx, "session-e2e", "add error handling")
	if err != nil {
		t.Fatalf("BuildIntent: %v", err)
	}
	if intent.Goal != "normalized: add error handling" {
		t.Fatalf("unexpected intent goal: %q", intent.Goal)
	}

	// Step 2: Plan
	tasks, err := engine.Plan(ctx, "wf-e2e", intent)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "implement: normalized: add error handling" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}

	// Step 3: Critic rejects
	critique, err := engine.Critique(ctx, "wf-e2e", intent, tasks)
	if err != nil {
		t.Fatalf("Critique: %v", err)
	}
	if critique.Approved {
		t.Fatal("expected critic to reject first time")
	}
	if len(critique.Issues) == 0 {
		t.Fatal("expected critique issues")
	}

	// Step 4: Replanner refines
	snippets := []model.FileSnippet{{Path: "main.go", Content: "func main() {}", Exists: true, Lines: 10}}
	refinedTasks, err := engine.RePlan(ctx, intent, tasks, snippets)
	if err != nil {
		t.Fatalf("RePlan: %v", err)
	}
	if len(refinedTasks) != 1 || refinedTasks[0].Status != "refined" {
		t.Fatalf("unexpected refined tasks: %+v", refinedTasks)
	}
	if len(replanner.receivedSnippets) != 1 || replanner.receivedSnippets[0].Path != "main.go" {
		t.Fatalf("replanner did not receive snippets: %+v", replanner.receivedSnippets)
	}

	// Step 5: Critic approves the refined plan
	critique2, err := engine.Critique(ctx, "wf-e2e", intent, refinedTasks)
	if err != nil {
		t.Fatalf("Critique (2nd): %v", err)
	}
	if !critique2.Approved {
		t.Fatal("expected critic to approve second time")
	}

	// Step 6: Code
	plan, err := engine.Code(ctx, "wf-e2e", intent, refinedTasks)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	if plan.ToolCallID != "tool-wf-e2e" {
		t.Fatalf("unexpected tool call id: %q", plan.ToolCallID)
	}
	// Verify coder received the refined tasks
	if len(coder.receivedTasks) != 1 || coder.receivedTasks[0].Status != "refined" {
		t.Fatalf("coder did not receive refined tasks: %+v", coder.receivedTasks)
	}

	// Step 7: Accept
	artifact := model.Artifact{Path: plan.Request.Path, Summary: plan.Request.Content}
	cp, err := engine.Accept(ctx, "wf-e2e", intent, artifact, nil)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if cp.Status != "pass" {
		t.Fatalf("unexpected checkpoint status: %q", cp.Status)
	}
}

// TestWorkerWrappers_CriticAndReplanner verifies the Worker interface for critic/replanner.
func TestWorkerWrappers_CriticAndReplanner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	intent := model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "test", CreatedAt: time.Now()}
	tasks := []model.Task{{ID: "t1", Title: "task", Status: "planned", Created: time.Now()}}

	// Critic worker
	criticW := NewCriticWorker(&e2eCritic{})
	out, err := criticW.Execute(ctx, WorkerInput{
		WorkflowID: "wf-1",
		Intent:     intent,
		Tasks:      tasks,
	})
	if err != nil {
		t.Fatalf("CriticWorker.Execute: %v", err)
	}
	if out.Critique == nil {
		t.Fatal("expected critique result")
	}
	if out.Critique.Approved {
		t.Fatal("expected first critique to reject")
	}
	if out.Metadata.Role != RoleCritic {
		t.Fatalf("expected role %q, got %q", RoleCritic, out.Metadata.Role)
	}
	if len(out.Messages) == 0 {
		t.Fatal("expected messages from critic worker")
	}

	// Replanner worker
	snippets := []model.FileSnippet{{Path: "foo.go", Content: "code", Exists: true, Lines: 5}}
	replanW := NewReplannerWorker(&e2eReplanner{})
	out2, err := replanW.Execute(ctx, WorkerInput{
		Intent:   intent,
		Tasks:    tasks,
		Snippets: snippets,
	})
	if err != nil {
		t.Fatalf("ReplannerWorker.Execute: %v", err)
	}
	if len(out2.Tasks) != 1 || out2.Tasks[0].Status != "refined" {
		t.Fatalf("unexpected replanner output: %+v", out2.Tasks)
	}
	if out2.Metadata.Role != RoleReplanner {
		t.Fatalf("expected role %q, got %q", RoleReplanner, out2.Metadata.Role)
	}
}

// TestNilCriticAndReplanner verifies graceful handling of nil critic/replanner.
func TestNilCriticAndReplanner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	engine := New(e2ePlanner{}, &e2eCoder{})
	// No critic or replanner set

	intent := model.IntentSpec{ID: "i1", SessionID: "s1", Goal: "test", CreatedAt: time.Now()}
	tasks := []model.Task{{ID: "t1", Title: "task", Status: "planned", Created: time.Now()}}

	// Critique should return approved (LocalCritic fallback)
	critique, err := engine.Critique(ctx, "wf-1", intent, tasks)
	if err != nil {
		t.Fatalf("Critique with nil critic: %v", err)
	}
	if !critique.Approved {
		t.Fatal("expected LocalCritic to approve")
	}

	// RePlan should return original tasks unchanged
	refined, err := engine.RePlan(ctx, intent, tasks, nil)
	if err != nil {
		t.Fatalf("RePlan with nil replanner: %v", err)
	}
	if len(refined) != len(tasks) || refined[0].ID != tasks[0].ID {
		t.Fatalf("expected original tasks returned, got: %+v", refined)
	}
}
