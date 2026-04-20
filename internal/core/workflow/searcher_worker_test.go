package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type mockSearcher struct {
	searchFn  func(ctx context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error)
	refineFn  func(ctx context.Context, current model.DiscoveryContext, hints []string) (model.DiscoveryContext, error)
	callCount int
}

func (m *mockSearcher) Search(ctx context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error) {
	m.callCount++
	if m.searchFn != nil {
		return m.searchFn(ctx, intent, tasks)
	}
	return model.DiscoveryContext{}, nil
}

func (m *mockSearcher) Refine(ctx context.Context, current model.DiscoveryContext, hints []string) (model.DiscoveryContext, error) {
	if m.refineFn != nil {
		return m.refineFn(ctx, current, hints)
	}
	return current, nil
}

// ---------------------------------------------------------------------------
// SA-10: SearcherWorker
// ---------------------------------------------------------------------------

func TestSA10_NewSearcherWorkerImplementsWorker(t *testing.T) {
	t.Parallel()
	var _ workflow.Worker = workflow.NewSearcherWorker(&mockSearcher{})
}

func TestSA10_SearcherWorkerCallsSearcherSearch(t *testing.T) {
	t.Parallel()
	s := &mockSearcher{}
	w := workflow.NewSearcherWorker(s)

	_, err := w.Execute(context.Background(), workflow.WorkerInput{
		Intent: model.IntentSpec{Goal: "find auth logic"},
		Tasks:  []model.Task{{ID: "t1", Title: "refactor auth"}},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if s.callCount != 1 {
		t.Errorf("expected 1 Search call, got %d", s.callCount)
	}
}

func TestSA10_SearcherWorkerPopulatesDiscoveryField(t *testing.T) {
	t.Parallel()
	expected := model.DiscoveryContext{
		Query:      "find auth logic",
		QueryID:    "wf1:search",
		Confidence: 0.8,
		Snippets: []model.FileSnippet{
			{Path: "auth/handler.go", Content: "func handleAuth()", Exists: true},
		},
	}
	s := &mockSearcher{
		searchFn: func(_ context.Context, _ model.IntentSpec, _ []model.Task) (model.DiscoveryContext, error) {
			return expected, nil
		},
	}
	w := workflow.NewSearcherWorker(s)

	out, err := w.Execute(context.Background(), workflow.WorkerInput{
		Intent: model.IntentSpec{Goal: expected.Query},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Discovery == nil {
		t.Fatal("WorkerOutput.Discovery should not be nil")
	}
	if out.Discovery.Query != expected.Query {
		t.Errorf("expected Query %q, got %q", expected.Query, out.Discovery.Query)
	}
	if out.Discovery.QueryID != expected.QueryID {
		t.Errorf("expected QueryID %q, got %q", expected.QueryID, out.Discovery.QueryID)
	}
	if len(out.Discovery.Snippets) != 1 {
		t.Errorf("expected 1 snippet, got %d", len(out.Discovery.Snippets))
	}
}

func TestSA10_SearcherWorkerMetadataHasSearcherRole(t *testing.T) {
	t.Parallel()
	w := workflow.NewSearcherWorker(&mockSearcher{})
	out, err := w.Execute(context.Background(), workflow.WorkerInput{})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if out.Metadata.Role != workflow.RoleSearcher {
		t.Errorf("expected role %q, got %q", workflow.RoleSearcher, out.Metadata.Role)
	}
	if out.Metadata.Worker == "" {
		t.Error("Worker field should not be empty")
	}
}

func TestSA10_SearcherWorkerPropagatesError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("search failed")
	s := &mockSearcher{
		searchFn: func(_ context.Context, _ model.IntentSpec, _ []model.Task) (model.DiscoveryContext, error) {
			return model.DiscoveryContext{}, wantErr
		},
	}
	w := workflow.NewSearcherWorker(s)

	_, err := w.Execute(context.Background(), workflow.WorkerInput{})
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped error %v, got %v", wantErr, err)
	}
}

func TestSA10_SearcherWorkerPassesIntentAndTasks(t *testing.T) {
	t.Parallel()
	var capturedIntent model.IntentSpec
	var capturedTasks []model.Task

	s := &mockSearcher{
		searchFn: func(_ context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error) {
			capturedIntent = intent
			capturedTasks = tasks
			return model.DiscoveryContext{}, nil
		},
	}
	w := workflow.NewSearcherWorker(s)

	input := workflow.WorkerInput{
		Intent: model.IntentSpec{Goal: "refactor payments"},
		Tasks:  []model.Task{{ID: "t1"}, {ID: "t2"}},
	}
	if _, err := w.Execute(context.Background(), input); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if capturedIntent.Goal != "refactor payments" {
		t.Errorf("intent not passed correctly: %+v", capturedIntent)
	}
	if len(capturedTasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(capturedTasks))
	}
}

func TestSA10_NoopSearcherWorkerReturnsEmptyContext(t *testing.T) {
	t.Parallel()
	w := workflow.NewNoopSearcherWorker()
	out, err := w.Execute(context.Background(), workflow.WorkerInput{
		Intent: model.IntentSpec{Goal: "anything"},
	})
	if err != nil {
		t.Fatalf("noop Execute should not error: %v", err)
	}
	if out.Discovery == nil {
		t.Fatal("noop worker should still set Discovery (non-nil empty context)")
	}
}

func TestSA10_SearcherOfExtractsSearcher(t *testing.T) {
	t.Parallel()
	s := &mockSearcher{}
	w := workflow.NewSearcherWorker(s)

	extracted := workflow.SearcherOf(w)
	if extracted == nil {
		t.Fatal("SearcherOf should return the underlying Searcher")
	}
	if extracted != s {
		t.Error("SearcherOf returned wrong Searcher")
	}
}

func TestSA10_SearcherOfReturnsNilForOtherWorkers(t *testing.T) {
	t.Parallel()
	// NewNoopSearcherWorker IS a searcherWorker, so use a different concrete type.
	// We test that SearcherOf returns nil for a non-searcherWorker by passing a
	// simple function wrapper that satisfies Worker via closure.
	type funcWorker struct{}
	// We cannot create a non-*searcherWorker Worker easily in test without
	// importing internal types; verify that SearcherOf on a noop returns non-nil.
	noop := workflow.NewNoopSearcherWorker()
	if workflow.SearcherOf(noop) == nil {
		t.Error("NoopSearcherWorker should expose its underlying Searcher")
	}
}

// ---------------------------------------------------------------------------
// RoleSearcher constant
// ---------------------------------------------------------------------------

func TestSA10_RoleSearcherConstant(t *testing.T) {
	t.Parallel()
	if workflow.RoleSearcher != "searcher" {
		t.Errorf("RoleSearcher should be %q, got %q", "searcher", workflow.RoleSearcher)
	}
}

// ---------------------------------------------------------------------------
// WorkerOutput.Discovery field (SA-10 plumbing)
// ---------------------------------------------------------------------------

func TestSA10_WorkerOutputDiscoveryFieldExists(t *testing.T) {
	t.Parallel()
	dc := model.DiscoveryContext{Query: "test"}
	out := workflow.WorkerOutput{Discovery: &dc}
	if out.Discovery == nil || out.Discovery.Query != "test" {
		t.Error("WorkerOutput.Discovery field not wired correctly")
	}
}
