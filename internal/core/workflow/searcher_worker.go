package workflow

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/model"
)

// searcherWorker wraps a Searcher as a first-class workflow.Worker (SA-10).
//
// This enables the Search phase to be independently scheduled, replaced, and
// tested in the same way as Planner, Coder, and Acceptor workers.  It supports
// three implementation forms: local (LocalSearcher), RPC, or LLM-backed.
type searcherWorker struct {
	searcher Searcher
}

// NewSearcherWorker wraps s as a workflow.Worker that executes the Search phase.
// The returned worker can be registered with the Launcher alongside other workers.
func NewSearcherWorker(s Searcher) Worker {
	return &searcherWorker{searcher: s}
}

// Execute runs the Search phase and returns the resulting DiscoveryContext in
// WorkerOutput.Discovery.  It never modifies Tasks or CodePlan.
func (w *searcherWorker) Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error) {
	dc, err := w.searcher.Search(ctx, input.Intent, input.Tasks)
	if err != nil {
		return WorkerOutput{}, err
	}
	return WorkerOutput{
		Discovery: &dc,
		Metadata: WorkerMetadata{
			Worker: "local-searcher",
			Role:   RoleSearcher,
		},
	}, nil
}

// searcher returns the underlying Searcher for inspection or testing.
func (w *searcherWorker) Searcher() Searcher { return w.searcher }

// ensure compile-time conformance
var _ Worker = (*searcherWorker)(nil)

// LocalSearcherAdapter is a convenience wrapper that satisfies workflow.Worker
// using any workflow.Searcher, allowing any Searcher implementation (local,
// RPC, or LLM-backed) to be used interchangeably in the Launcher.
//
// Usage:
//
//	launcher.RegisterWorker(workflow.RoleSearcher, workflow.NewSearcherWorker(mySearcher))
type LocalSearcherAdapter = searcherWorker

// SearcherOf returns the underlying Searcher from a Worker created by
// NewSearcherWorker, or nil if w is not a searcherWorker.
func SearcherOf(w Worker) Searcher {
	if sw, ok := w.(*searcherWorker); ok {
		return sw.searcher
	}
	return nil
}

// noopSearcher is used as a safe zero-value fallback for tests or disabled
// search configurations.
type noopSearcher struct{}

func (noopSearcher) Search(_ context.Context, _ model.IntentSpec, _ []model.Task) (model.DiscoveryContext, error) {
	return model.DiscoveryContext{}, nil
}

func (noopSearcher) Refine(_ context.Context, current model.DiscoveryContext, _ []string) (model.DiscoveryContext, error) {
	return current, nil
}

// NewNoopSearcherWorker returns a Worker that immediately returns an empty
// DiscoveryContext without calling any tools.  Useful for tests that do not
// exercise the Search phase.
func NewNoopSearcherWorker() Worker {
	return NewSearcherWorker(noopSearcher{})
}
