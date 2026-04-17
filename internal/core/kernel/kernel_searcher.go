package kernel

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mingzhi1/coden/internal/core/discovery"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/retrieval"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// LocalSearcher is the minimal in-process Searcher implementation.
// It preserves the current Discovery behavior while exposing it through the
// workflow.Searcher boundary so kernel no longer owns the long-term contract.
type LocalSearcher struct {
	k          *Kernel
	sessionID  string
	workflowID string
}

func NewLocalSearcher(k *Kernel, sessionID, workflowID string) *LocalSearcher {
	return &LocalSearcher{k: k, sessionID: sessionID, workflowID: workflowID}
}

func (s *LocalSearcher) Search(ctx context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error) {
	if s == nil || s.k == nil {
		return model.DiscoveryContext{}, fmt.Errorf("searcher is not configured")
	}

	query := intent.Goal
	if query == "" && len(tasks) > 0 {
		query = tasks[0].Title
	}

	targetFiles := declaredTaskFiles(tasks)
	dirtyPaths := s.k.workspace.DirtyPaths()
	orch := discovery.NewToolOrchestrator(s.k.tools)
	hits, err := orch.Search(ctx, discovery.SearchParams{
		Query:       query,
		TargetFiles: targetFiles,
		Mode:        discoveryMode(query, targetFiles),
		DirtyPaths:  dirtyPaths,
	})
	if err != nil {
		return model.DiscoveryContext{}, err
	}

	snippets := snippetsFromEvidence(ctx, s.k, hits)
	if len(snippets) == 0 {
		// Keep the old behavior as a safety net while the orchestrator remains minimal.
		snippets = s.k.runDiscovery(ctx, s.sessionID, s.workflowID, intent, tasks)
	}
	evidence := discoveryEvidenceFromRetrieval(hits)
	markDiscoveryEvidenceStale(evidence, dirtyPaths)
	if len(evidence) == 0 {
		for _, sn := range snippets {
			evidence = append(evidence, model.DiscoveryEvidence{
				Source:      "discovery",
				Path:        sn.Path,
				Snippet:     sn.Content,
				Verified:    false,
				Stale:       s.k.workspace.IsDirty(sn.Path),
				Explanation: "prefetched by LocalSearcher from legacy discovery fallback",
			})
		}
	}

	return model.DiscoveryContext{
		Query:      query,
		QueryID:    fmt.Sprintf("%s:discovery", s.workflowID),
		Evidence:   evidence,
		Snippets:   snippets,
		Confidence: discoveryConfidence(snippets),
	}, nil
}

func (s *LocalSearcher) Refine(ctx context.Context, current model.DiscoveryContext, hints []string) (model.DiscoveryContext, error) {
	if s == nil || s.k == nil {
		return model.DiscoveryContext{}, fmt.Errorf("searcher is not configured")
	}
	orch := discovery.NewToolOrchestrator(s.k.tools)
	dirtyPaths := s.k.workspace.DirtyPaths()
	hits, err := orch.Refine(ctx, discovery.RefineParams{
		Query:       current.Query,
		QueryID:     current.QueryID,
		Hints:       hints,
		TargetFiles: pathsFromDiscoveryEvidence(current.Evidence),
		Mode:        discoveryMode(current.Query, pathsFromDiscoveryEvidence(current.Evidence)),
		DirtyPaths:  dirtyPaths,
	})
	if err != nil {
		return model.DiscoveryContext{}, err
	}
	mergedHits := mergeRetrievalEvidence(current.Evidence, hits)
	snippets := snippetsFromEvidence(ctx, s.k, mergedHits)
	if len(snippets) == 0 {
		snippets = current.Snippets
	}
	return model.DiscoveryContext{
		Query:      current.Query,
		QueryID:    current.QueryID,
		Evidence:   markedDiscoveryEvidence(mergedHits, dirtyPaths),
		Snippets:   snippets,
		Confidence: discoveryConfidence(snippets),
	}, nil
}

func discoveryConfidence(snippets []model.FileSnippet) float64 {
	if len(snippets) == 0 {
		return 0
	}
	if len(snippets) >= 5 {
		return 1.0
	}
	return float64(len(snippets)) / 5.0
}

func discoveryMode(query string, targetFiles []string) string {
	query = stringsTrim(query)
	switch {
	case len(targetFiles) > 0:
		return "symbol"
	case strings.Contains(query, " "):
		return "semantic"
	default:
		return "identifier"
	}
}

func declaredTaskFiles(tasks []model.Task) []string {
	seen := make(map[string]bool)
	files := make([]string, 0)
	for _, task := range tasks {
		for _, path := range task.Files {
			path = stringsTrim(path)
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files
}

func snippetsFromEvidence(ctx context.Context, k *Kernel, hits []retrieval.RetrievalEvidence) []model.FileSnippet {
	seen := make(map[string]bool)
	snippets := make([]model.FileSnippet, 0, len(hits))
	for _, hit := range hits {
		path := stringsTrim(hit.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		snippets = append(snippets, k.readFileSnippet(ctx, path))
		if len(snippets) >= 10 {
			break
		}
	}
	return snippets
}

func stringsTrim(s string) string {
	return strings.TrimSpace(s)
}

func pathsFromDiscoveryEvidence(evidence []model.DiscoveryEvidence) []string {
	seen := make(map[string]bool)
	paths := make([]string, 0, len(evidence))
	for _, e := range evidence {
		path := stringsTrim(e.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func mergeRetrievalEvidence(existing []model.DiscoveryEvidence, extra []retrieval.RetrievalEvidence) []retrieval.RetrievalEvidence {
	merged := make([]retrieval.RetrievalEvidence, 0, len(existing)+len(extra))
	for _, hit := range existing {
		merged = append(merged, retrieval.RetrievalEvidence{
			Source:      hit.Source,
			Path:        hit.Path,
			Line:        hit.Line,
			Column:      hit.Column,
			Symbol:      hit.Symbol,
			Snippet:     hit.Snippet,
			Score:       hit.Score,
			Stale:       hit.Stale,
			Verified:    hit.Verified,
			Explanation: hit.Explanation,
		})
	}
	merged = append(merged, extra...)
	return merged
}

func shouldRefineDiscovery(discovery model.DiscoveryContext, tasks []model.Task) bool {
	if len(discovery.Snippets) == 0 {
		return true
	}
	if discovery.Confidence < 0.4 {
		return true
	}
	if len(discovery.Evidence) < len(tasks) && len(tasks) > 0 {
		return true
	}
	return false
}

func discoveryHints(tasks []model.Task, goal string) []string {
	seen := make(map[string]bool)
	hints := make([]string, 0, 6)
	goal = stringsTrim(goal)
	if goal != "" {
		hints = append(hints, goal)
		seen[goal] = true
	}
	for _, task := range tasks {
		title := stringsTrim(task.Title)
		if title != "" && !seen[title] {
			seen[title] = true
			hints = append(hints, title)
		}
		for _, file := range task.Files {
			file = stringsTrim(file)
			if file != "" && !seen[file] {
				seen[file] = true
				hints = append(hints, file)
			}
			if len(hints) >= 6 {
				return hints
			}
		}
		if len(hints) >= 6 {
			return hints
		}
	}
	return hints
}

var _ workflow.Searcher = (*LocalSearcher)(nil)

// discoveryEvidenceFromRetrieval converts unified retrieval evidence into the
// model-local form. This is unused by LocalSearcher today but kept here so the
// future orchestrator-backed Searcher can plug in without changing WorkflowContext.
func discoveryEvidenceFromRetrieval(hits []retrieval.RetrievalEvidence) []model.DiscoveryEvidence {
	out := make([]model.DiscoveryEvidence, 0, len(hits))
	for _, hit := range hits {
		out = append(out, model.DiscoveryEvidence{
			Source:      hit.Source,
			Path:        hit.Path,
			Line:        hit.Line,
			Column:      hit.Column,
			Symbol:      hit.Symbol,
			Snippet:     hit.Snippet,
			Score:       hit.Score,
			Stale:       hit.Stale,
			Verified:    hit.Verified,
			Explanation: hit.Explanation,
		})
	}
	return out
}

func markedDiscoveryEvidence(hits []retrieval.RetrievalEvidence, dirtyPaths []string) []model.DiscoveryEvidence {
	evidence := discoveryEvidenceFromRetrieval(hits)
	markDiscoveryEvidenceStale(evidence, dirtyPaths)
	return evidence
}

func markDiscoveryEvidenceStale(evidence []model.DiscoveryEvidence, dirtyPaths []string) {
	if len(evidence) == 0 || len(dirtyPaths) == 0 {
		return
	}
	dirty := make(map[string]bool, len(dirtyPaths))
	for _, path := range dirtyPaths {
		path = stringsTrim(path)
		if path != "" {
			dirty[path] = true
		}
	}
	for i := range evidence {
		if dirty[stringsTrim(evidence[i].Path)] {
			evidence[i].Stale = true
		}
	}
}
