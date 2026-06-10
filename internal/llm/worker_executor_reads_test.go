package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/outputcompressor"
)

// fakeReadExecutor returns a canned Result per path.
type fakeReadExecutor struct {
	results map[string]toolruntime.Result
}

func (f fakeReadExecutor) Execute(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	return f.results[req.Path], nil
}

func noopPush(string, string, string) {}

// TestExecuteReadsParallel_SpillIsNotAVisibilityLimit locks the contract that
// spill is archival bookkeeping, not a cap on what the model sees: a read
// result that was spilled (tool-side threshold is ~8K chars) but fits the
// per-read budget must reach the model in full. The old behavior keyed the
// preview path on SpilledPath alone, so any file over the spill threshold
// degraded to a 20-line preview regardless of budget.
func TestExecuteReadsParallel_SpillIsNotAVisibilityLimit(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("line of source code\n", 500) // ~10K chars: spilled, under budget
	exec := fakeReadExecutor{results: map[string]toolruntime.Result{
		"big.go": {
			Output:      body,
			Preview:     "line of source code",
			SpilledPath: "artifact:spill-1",
			Summary:     "read big.go",
		},
	}}
	reads := []workflow.ToolCall{{Request: toolruntime.Request{Kind: "read_file", Path: "big.go"}}}

	out := executeReadsParallel(context.Background(), "executor", exec, reads, maxReadResultChars, 1, noopPush, outputcompressor.New())

	if !strings.Contains(out, body) {
		t.Errorf("spilled-but-under-budget read must feed full content, got %d chars:\n%.200s", len(out), out)
	}
	if strings.Contains(out, "too large to inline") {
		t.Errorf("under-budget read must not degrade to the preview path")
	}
}

// TestExecuteReadsParallel_OverBudgetDegradesToPreview verifies the guard side
// of the same contract: a result genuinely over the per-read budget falls back
// to the preview + narrow-down hint instead of flooding the round history.
func TestExecuteReadsParallel_OverBudgetDegradesToPreview(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", maxReadResultChars+1)
	exec := fakeReadExecutor{results: map[string]toolruntime.Result{
		"huge.go": {
			Output:      body,
			Preview:     "PREVIEW_HEAD",
			SpilledPath: "artifact:spill-2",
			Summary:     "read huge.go",
		},
	}}
	reads := []workflow.ToolCall{{Request: toolruntime.Request{Kind: "read_file", Path: "huge.go"}}}

	out := executeReadsParallel(context.Background(), "executor", exec, reads, maxReadResultChars, 1, noopPush, outputcompressor.New())

	if !strings.Contains(out, "too large to inline") || !strings.Contains(out, "PREVIEW_HEAD") {
		t.Errorf("over-budget read must degrade to preview + hint, got:\n%.300s", out)
	}
	if strings.Contains(out, body) {
		t.Errorf("over-budget read must not inline the full body")
	}
}
