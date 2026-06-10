package llm

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
)

// fixedChatter returns a canned reply regardless of input.
type fixedChatter struct{ reply string }

func (c fixedChatter) Chat(_ context.Context, _ string, _ []Message) (string, error) {
	return c.reply, nil
}

// TestReplannerTwoChoice locks #15: the RePlanner refines each task into EITHER
// concrete steps (base case) OR a sub_goal (recursion case), mutually exclusive.
// SubGoal is the only path that sets Task.SubGoal — the recursion trigger.
func TestReplannerTwoChoice(t *testing.T) {
	t.Parallel()

	reply := `[
	  {"id":"task-1","title":"small fix","steps":["edit foo.go line 10"],"success_cmd":"go build ./..."},
	  {"id":"task-2","title":"big subsystem","sub_goal":"Build the whole auth subsystem with tests."},
	  {"id":"task-3","title":"model emitted both","steps":["do a thing"],"sub_goal":"decompose this instead"}
	]`
	rp := NewLLMReplanner(fixedChatter{reply: reply})

	tasks := []model.Task{
		{ID: "task-1", Title: "t1"},
		{ID: "task-2", Title: "t2"},
		{ID: "task-3", Title: "t3"},
	}
	out, err := rp.RePlan(context.Background(), model.IntentSpec{Goal: "x"}, tasks, nil)
	if err != nil {
		t.Fatalf("RePlan: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(out))
	}
	byID := map[string]model.Task{}
	for _, tk := range out {
		byID[tk.ID] = tk
	}

	// Base case: steps set, SubGoal empty.
	if base := byID["task-1"]; base.SubGoal != "" || len(base.Steps) == 0 {
		t.Errorf("task-1 should be base case (steps, no sub_goal): %+v", base)
	}
	// Recursion case: SubGoal set, steps empty, no forced success_cmd.
	if rec := byID["task-2"]; rec.SubGoal == "" || len(rec.Steps) != 0 || rec.SuccessCmd != "" {
		t.Errorf("task-2 should be recursion case (sub_goal only): %+v", rec)
	}
	// Mutual exclusivity: sub_goal supersedes steps when the model emits both.
	if both := byID["task-3"]; both.SubGoal == "" || len(both.Steps) != 0 {
		t.Errorf("task-3: sub_goal must supersede steps, got %+v", both)
	}
}
