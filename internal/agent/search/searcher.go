package search

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mingzhi1/coden/internal/core/discovery"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/retrieval"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workspace"
)

// WorkspaceSearcher is a kernel-free workflow.Searcher implementation that
// depends only on a workspace service and tool runtime. It is suitable for
// the standalone `coden-agent-search` subprocess (SA-10) and as a default
// in-process Searcher when the kernel does not need to own search state.
//
// Compared with kernel.LocalSearcher it omits:
//   - DirtyTracker sync (the subprocess does not see workspace mutations)
//   - The legacy runDiscovery snippet fallback
//
// These features remain available via kernel.LocalSearcher when callers
// prefer the in-kernel implementation.
type WorkspaceSearcher struct {
	ws    *workspace.Service
	tools *toolruntime.Runtime
	orch  *discovery.ToolOrchestrator
	id    string // workflow / instance identifier used in QueryID
}

// NewWorkspaceSearcher constructs a Searcher backed by ws and tools.
// id is used to suffix QueryIDs (typically the workflow ID); when empty,
// "standalone" is used.
func NewWorkspaceSearcher(ws *workspace.Service, tools *toolruntime.Runtime, id string) *WorkspaceSearcher {
	if id == "" {
		id = "standalone"
	}
	return &WorkspaceSearcher{
		ws:    ws,
		tools: tools,
		orch:  discovery.NewToolOrchestrator(tools),
		id:    id,
	}
}

func (s *WorkspaceSearcher) Search(ctx context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error) {
	if s == nil || s.ws == nil || s.tools == nil {
		return model.DiscoveryContext{}, fmt.Errorf("searcher is not configured")
	}

	query := strings.TrimSpace(intent.Goal)
	if query == "" && len(tasks) > 0 {
		query = strings.TrimSpace(tasks[0].Title)
	}

	targetFiles := declaredTaskFiles(tasks)
	dirtyPaths := s.ws.DirtyPaths()

	hits, err := s.orch.Search(ctx, discovery.SearchParams{
		Query:       query,
		TargetFiles: targetFiles,
		Mode:        discoveryMode(query, targetFiles),
		DirtyPaths:  dirtyPaths,
		Workspace:   s.ws.Root(),
	})
	if err != nil {
		return model.DiscoveryContext{}, err
	}

	snippets := s.snippetsFromEvidence(hits)
	evidence := discoveryEvidenceFromRetrieval(hits)
	markEvidenceStale(evidence, dirtyPaths)
	if len(evidence) == 0 {
		for _, sn := range snippets {
			evidence = append(evidence, model.DiscoveryEvidence{
				Source:      "discovery",
				Path:        sn.Path,
				Snippet:     sn.Content,
				Verified:    false,
				Stale:       isDirty(sn.Path, dirtyPaths),
				Explanation: "WorkspaceSearcher synthesized from snippet",
			})
		}
	}

	return model.DiscoveryContext{
		Query:      query,
		QueryID:    fmt.Sprintf("%s:discovery", s.id),
		Evidence:   evidence,
		Snippets:   snippets,
		Confidence: snippetConfidence(snippets),
	}, nil
}

func (s *WorkspaceSearcher) Refine(ctx context.Context, current model.DiscoveryContext, hints []string) (model.DiscoveryContext, error) {
	if s == nil || s.ws == nil || s.tools == nil {
		return model.DiscoveryContext{}, fmt.Errorf("searcher is not configured")
	}
	if len(hints) == 0 {
		return current, nil
	}

	dirtyPaths := s.ws.DirtyPaths()
	hits, err := s.orch.Refine(ctx, discovery.RefineParams{
		Query:      current.Query,
		QueryID:    current.QueryID,
		Hints:      hints,
		DirtyPaths: dirtyPaths,
		Workspace:  s.ws.Root(),
	})
	if err != nil {
		return current, err
	}
	if len(hits) == 0 {
		return current, nil
	}

	extraSnippets := s.snippetsFromEvidence(hits)
	merged := mergeSnippets(current.Snippets, extraSnippets)
	mergedEvidence := append([]model.DiscoveryEvidence{}, current.Evidence...)
	for _, e := range discoveryEvidenceFromRetrieval(hits) {
		mergedEvidence = append(mergedEvidence, e)
	}
	markEvidenceStale(mergedEvidence, dirtyPaths)

	return model.DiscoveryContext{
		Query:      current.Query,
		QueryID:    current.QueryID,
		Evidence:   mergedEvidence,
		Snippets:   merged,
		Confidence: snippetConfidence(merged),
	}, nil
}

// ── internal helpers (mirror kernel_searcher.go but kernel-free) ────────────

func (s *WorkspaceSearcher) snippetsFromEvidence(hits []retrieval.RetrievalEvidence) []model.FileSnippet {
	const maxSnippetBytes = 3000
	const maxSnippets = 10

	seen := make(map[string]bool)
	out := make([]model.FileSnippet, 0, len(hits))
	for _, hit := range hits {
		path := strings.TrimSpace(hit.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true

		sn := model.FileSnippet{Path: path}
		content, err := s.ws.Read(path)
		if err != nil {
			sn.Exists = false
		} else {
			text := string(content)
			lines := strings.Count(text, "\n") + 1
			if len(text) > maxSnippetBytes {
				text = text[:maxSnippetBytes] + fmt.Sprintf("\n... (truncated, %d lines total)", lines)
			}
			sn.Content = text
			sn.Lines = lines
			sn.Exists = true
		}
		out = append(out, sn)
		if len(out) >= maxSnippets {
			break
		}
	}
	return out
}

func declaredTaskFiles(tasks []model.Task) []string {
	seen := make(map[string]bool)
	files := make([]string, 0)
	for _, task := range tasks {
		for _, p := range task.Files {
			p = strings.TrimSpace(p)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files
}

func discoveryMode(query string, targetFiles []string) string {
	switch {
	case len(targetFiles) > 0:
		return "symbol"
	case strings.Contains(query, " "):
		return "semantic"
	default:
		return "identifier"
	}
}

func snippetConfidence(snippets []model.FileSnippet) float64 {
	if len(snippets) == 0 {
		return 0
	}
	if len(snippets) >= 5 {
		return 1.0
	}
	return float64(len(snippets)) / 5.0
}

func discoveryEvidenceFromRetrieval(hits []retrieval.RetrievalEvidence) []model.DiscoveryEvidence {
	out := make([]model.DiscoveryEvidence, 0, len(hits))
	for _, h := range hits {
		out = append(out, model.DiscoveryEvidence{
			Source:      h.Source,
			Path:        h.Path,
			Line:        h.Line,
			Column:      h.Column,
			Symbol:      h.Symbol,
			Snippet:     h.Snippet,
			Score:       h.Score,
			Stale:       h.Stale,
			Verified:    h.Verified,
			Explanation: h.Explanation,
		})
	}
	return out
}

func markEvidenceStale(evidence []model.DiscoveryEvidence, dirtyPaths []string) {
	if len(dirtyPaths) == 0 {
		return
	}
	dirty := make(map[string]bool, len(dirtyPaths))
	for _, p := range dirtyPaths {
		dirty[p] = true
	}
	for i := range evidence {
		if dirty[evidence[i].Path] {
			evidence[i].Stale = true
		}
	}
}

func isDirty(path string, dirtyPaths []string) bool {
	for _, p := range dirtyPaths {
		if p == path {
			return true
		}
	}
	return false
}

func mergeSnippets(existing, extra []model.FileSnippet) []model.FileSnippet {
	if len(extra) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing))
	for _, s := range existing {
		seen[s.Path] = true
	}
	out := append([]model.FileSnippet{}, existing...)
	for _, s := range extra {
		if seen[s.Path] {
			continue
		}
		seen[s.Path] = true
		out = append(out, s)
	}
	return out
}
