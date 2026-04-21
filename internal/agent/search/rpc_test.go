package search

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
)

// stubSearcher records the inputs it received and returns a configured
// DiscoveryContext. Used to verify the SA-10 RPC boundary.
type stubSearcher struct {
	gotIntent  model.IntentSpec
	gotTasks   []model.Task
	gotCurrent model.DiscoveryContext
	gotHints   []string
	dc         model.DiscoveryContext
	err        error
}

func (s *stubSearcher) Search(_ context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error) {
	s.gotIntent = intent
	s.gotTasks = tasks
	return s.dc, s.err
}

func (s *stubSearcher) Refine(_ context.Context, current model.DiscoveryContext, hints []string) (model.DiscoveryContext, error) {
	s.gotCurrent = current
	s.gotHints = hints
	return s.dc, s.err
}

func TestRPCSearcher_SearchRoundTrip(t *testing.T) {
	stub := &stubSearcher{
		dc: model.DiscoveryContext{
			Query:      "find foo",
			QueryID:    "q1",
			Confidence: 0.8,
			Snippets: []model.FileSnippet{
				{Path: "a.go", Content: "package a", Lines: 1, Exists: true},
			},
			Evidence: []model.DiscoveryEvidence{
				{Source: "grep", Path: "a.go", Line: 3},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	searcher, cleanup, err := NewLoopbackRPCSearcher(ctx, stub)
	if err != nil {
		t.Fatalf("NewLoopbackRPCSearcher: %v", err)
	}
	defer cleanup()

	intent := model.IntentSpec{Goal: "find foo", SessionID: "sess-1"}
	tasks := []model.Task{{ID: "task-1", Title: "do thing", Files: []string{"a.go"}}}

	got, err := searcher.Search(ctx, intent, tasks)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if stub.gotIntent.Goal != "find foo" {
		t.Errorf("server received intent %q, want %q", stub.gotIntent.Goal, "find foo")
	}
	if len(stub.gotTasks) != 1 || stub.gotTasks[0].ID != "task-1" {
		t.Errorf("server received tasks %#v", stub.gotTasks)
	}
	if got.Query != "find foo" || got.QueryID != "q1" {
		t.Errorf("client got Query=%q QueryID=%q", got.Query, got.QueryID)
	}
	if len(got.Snippets) != 1 || got.Snippets[0].Path != "a.go" {
		t.Errorf("client got snippets %#v", got.Snippets)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Source != "grep" {
		t.Errorf("client got evidence %#v", got.Evidence)
	}
}

func TestRPCSearcher_RefineRoundTrip(t *testing.T) {
	stub := &stubSearcher{
		dc: model.DiscoveryContext{Query: "q", QueryID: "q-refined", Confidence: 0.9},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	searcher, cleanup, err := NewLoopbackRPCSearcher(ctx, stub)
	if err != nil {
		t.Fatalf("NewLoopbackRPCSearcher: %v", err)
	}
	defer cleanup()

	current := model.DiscoveryContext{Query: "q", QueryID: "q1"}
	got, err := searcher.Refine(ctx, current, []string{"hint-a", "hint-b"})
	if err != nil {
		t.Fatalf("Refine: %v", err)
	}

	if stub.gotCurrent.QueryID != "q1" {
		t.Errorf("server received current.QueryID=%q, want q1", stub.gotCurrent.QueryID)
	}
	if len(stub.gotHints) != 2 || stub.gotHints[0] != "hint-a" {
		t.Errorf("server received hints %#v", stub.gotHints)
	}
	if got.QueryID != "q-refined" {
		t.Errorf("client got QueryID=%q, want q-refined", got.QueryID)
	}
}

func TestRPCSearcher_DescribeRole(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	searcher, cleanup, err := NewLoopbackRPCSearcher(ctx, &stubSearcher{})
	if err != nil {
		t.Fatalf("NewLoopbackRPCSearcher: %v", err)
	}
	defer cleanup()

	rpcSearcher, ok := searcher.(*RPCSearcher)
	if !ok {
		t.Fatalf("expected *RPCSearcher, got %T", searcher)
	}
	desc, err := rpcSearcher.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc.Role != "searcher" {
		t.Errorf("Describe.Role = %q, want %q", desc.Role, "searcher")
	}
	if desc.Name != "coden-agent-search" {
		t.Errorf("Describe.Name = %q, want coden-agent-search", desc.Name)
	}
}
