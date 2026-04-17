package llm_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/llm"
)

// TestFullWorkflowE2E runs the complete Inputter → Planner → Coder → Acceptor
// pipeline through the broker with the MiniMax provider.
func TestFullWorkflowE2E(t *testing.T) {
	miniMaxKey := os.Getenv("TEST_MINIMAX_API_KEY")
	if miniMaxKey == "" {
		t.Skip("TEST_MINIMAX_API_KEY not set")
	}

	miniMaxBaseURL := os.Getenv("TEST_MINIMAX_BASE_URL")
	if miniMaxBaseURL == "" {
		t.Skip("TEST_MINIMAX_BASE_URL not set")
	}

	pool := llm.NewPool()
	pool.Add(llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})
	pool.AddLight(llm.Config{
		Provider: "minimax",
		APIKey:   miniMaxKey,
		BaseURL:  miniMaxBaseURL,
		Model:    "coding-minimax-m2.7-free",
	})

	broker := llm.NewBroker(pool)
	t.Logf("pool summary: %s", broker.Summary())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sessionID := "test-e2e-1"
	workflowID := "wf-e2e-1"

	// Step 1: Inputter — normalize user prompt into IntentSpec
	t.Log("=== Step 1: Inputter ===")
	inputter := llm.NewLLMInputter(broker)
	intent, err := inputter.Build(ctx, sessionID, "Create a Go function that calculates fibonacci(n)")
	if err != nil {
		t.Fatalf("inputter failed: %v", err)
	}
	t.Logf("intent: kind=%s goal=%s criteria=%v", intent.Kind, intent.Goal, intent.SuccessCriteria)

	// Step 2: Planner — decompose intent into tasks
	t.Log("=== Step 2: Planner ===")
	planner := llm.NewLLMPlanner(broker)
	tasks, err := planner.Plan(ctx, workflowID, intent)
	if err != nil {
		t.Fatalf("planner failed: %v", err)
	}
	t.Logf("plan: %d tasks", len(tasks))
	for i, task := range tasks {
		t.Logf("  task[%d]: %s — %s", i, task.ID, task.Title)
	}

	// Step 3: Coder — generate tool calls for implementation
	t.Log("=== Step 3: Coder ===")
	coder := llm.NewLLMCoder(broker)
	codePlan, err := coder.Build(ctx, workflowID, intent, tasks)
	if err != nil {
		t.Fatalf("coder failed: %v", err)
	}
	t.Logf("code plan: %d tool calls", len(codePlan.Calls()))
	for i, call := range codePlan.Calls() {
		contentPreview := call.Request.Content
		if len(contentPreview) > 100 {
			contentPreview = contentPreview[:100] + "..."
		}
		t.Logf("  call[%d]: %s → %s (content: %s)", i, call.Request.Kind, call.Request.Path, contentPreview)
	}

	// Step 4: Acceptor — verify the code plan
	t.Log("=== Step 4: Acceptor ===")
	acceptor := llm.NewLLMAcceptor(broker)
	artifact := model.Artifact{
		Path:    "artifacts/e2e-test.md",
		Summary: "test artifact",
	}
	acceptResult, err := acceptor.Accept(ctx, workflowID, intent, artifact)
	if err != nil {
		t.Fatalf("acceptor failed: %v", err)
	}
	t.Logf("acceptor result: status=%s", acceptResult.Status)

	// Print usage summary
	t.Log("=== Usage Summary ===")
	usage := broker.Usage()
	for role, stats := range usage {
		t.Logf("  role=%s calls=%d input_tokens=%d output_tokens=%d",
			role, stats.Calls, stats.InputTokens, stats.OutTokens)
	}
}
