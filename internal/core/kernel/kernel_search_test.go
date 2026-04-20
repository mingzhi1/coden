package kernel

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// searchCapturingPlanner records the WorkflowContext it sees at plan time.
type searchCapturingPlanner struct {
	mu  sync.Mutex
	wcs []model.WorkflowContext
}

func (p *searchCapturingPlanner) Plan(ctx context.Context, _ string, _ model.IntentSpec) ([]model.Task, error) {
	wc := model.WorkflowContextFrom(ctx)
	p.mu.Lock()
	p.wcs = append(p.wcs, wc)
	p.mu.Unlock()
	return []model.Task{
		{ID: "task-1", Title: "captured plan", Status: "planned", Created: time.Now()},
	}, nil
}

func (p *searchCapturingPlanner) captured() []model.WorkflowContext {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]model.WorkflowContext, len(p.wcs))
	copy(out, p.wcs)
	return out
}

// searchExecutor responds to search requests with canned grep output.
type searchExecutor struct {
	testExecutor // embeds the pass-through executor
	mu           sync.Mutex
	kindsSeen    []string
}

func (e *searchExecutor) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	e.mu.Lock()
	e.kindsSeen = append(e.kindsSeen, req.Kind)
	e.mu.Unlock()

	switch req.Kind {
	case "search":
		return toolruntime.Result{
			Output: "internal/kernel/main.go:42: func RunKernel() error {\ninternal/kernel/main.go:100: func StopKernel() {\n",
		}, nil
	case "read_file":
		return toolruntime.Result{
			Output:  "package kernel\n\nfunc RunKernel() error { return nil }\n",
			Summary: "read ok",
		}, nil
	}
	return e.testExecutor.Execute(ctx, req)
}

func (e *searchExecutor) seenKinds() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.kindsSeen))
	copy(out, e.kindsSeen)
	return out
}

// collectAllEvents drains the channel until topic is seen or timeout fires.
// Returns all collected events.
func collectAllEvents(t *testing.T, ch <-chan model.Event, wantTopic string, timeout time.Duration) []model.Event {
	t.Helper()
	var events []model.Event
	timer := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("event stream closed")
			}
			events = append(events, ev)
			if ev.Topic == wantTopic {
				return events
			}
		case <-timer:
			t.Fatalf("timed out waiting for topic %q (got %d events)", wantTopic, len(events))
			return events
		}
	}
}

// topicsFrom returns the list of topic strings from events.
func topicsFrom(events []model.Event) []string {
	topics := make([]string, len(events))
	for i, e := range events {
		topics[i] = e.Topic
	}
	return topics
}

func hasTopicPrefix(events []model.Event, prefix string) bool {
	for _, e := range events {
		if strings.HasPrefix(e.Topic, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// SA-08: Planner receives macro discovery context
// ---------------------------------------------------------------------------

func TestSA08_PlannerReceivesMacroDiscoveryContext(t *testing.T) {
	t.Parallel()

	planner := &searchCapturingPlanner{}
	exec := &searchExecutor{}

	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		planner,
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "RunKernel implementation")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	captured := planner.captured()
	if len(captured) == 0 {
		t.Fatal("planner was never called")
	}
	wc := captured[0]

	// SA-08: the planner must see discovery snippets injected BEFORE it ran.
	if len(wc.DiscoveryContext) == 0 {
		t.Error("SA-08: Planner received empty DiscoveryContext; macro grep-only pre-search did not inject snippets")
	}
}

func TestSA08_MacroContextContainsGrepHits(t *testing.T) {
	t.Parallel()

	planner := &searchCapturingPlanner{}
	exec := &searchExecutor{}

	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		planner,
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("session-1")
	defer cancel()

	_, err := k.Submit(context.Background(), "session-1", "StopKernel function")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	captured := planner.captured()
	if len(captured) == 0 {
		t.Fatal("planner was never called")
	}

	// Every snippet in the macro context must come from a real path.
	for _, sn := range captured[0].DiscoveryContext {
		if sn.Path == "" {
			t.Errorf("SA-08: got snippet with empty path in Planner context: %+v", sn)
		}
	}
}

func TestSA08_MacroContextInjectedBeforePlanStarts(t *testing.T) {
	t.Parallel()

	// Record the sequence: first Plan call index vs first search.started event.
	var (
		mu           sync.Mutex
		planCallTime time.Time
		searchSeen   time.Time
	)

	planner := &funcPlanner{fn: func(ctx context.Context, _ string, _ model.IntentSpec) ([]model.Task, error) {
		wc := model.WorkflowContextFrom(ctx)
		mu.Lock()
		planCallTime = time.Now()
		if len(wc.DiscoveryContext) > 0 && searchSeen.IsZero() {
			// Plan sees discovery, meaning macro search ran first.
			searchSeen = time.Now()
		}
		mu.Unlock()
		return []model.Task{{ID: "t1", Title: "t", Status: "planned", Created: time.Now()}}, nil
	}}

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		planner,
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	_, err := k.Submit(context.Background(), "s1", "find RunKernel")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	mu.Lock()
	noSearch := searchSeen.IsZero()
	mu.Unlock()

	if noSearch {
		t.Error("SA-08: Planner was called but DiscoveryContext was empty — macro search did not inject context before plan")
	}
	_ = planCallTime
}

// funcPlanner is a Planner backed by a plain function.
type funcPlanner struct {
	fn func(context.Context, string, model.IntentSpec) ([]model.Task, error)
}

func (p *funcPlanner) Plan(ctx context.Context, id string, intent model.IntentSpec) ([]model.Task, error) {
	return p.fn(ctx, id, intent)
}

// ---------------------------------------------------------------------------
// SA-09: search events are emitted
// ---------------------------------------------------------------------------

func TestSA09_SearchStartedEventEmitted(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	_, err := k.Submit(context.Background(), "s1", "search event test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	all := collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	if !hasTopicPrefix(all, "search.started") {
		t.Errorf("SA-09: expected search.started event, got topics: %v", topicsFrom(all))
	}
}

func TestSA09_SearchFinishedEventEmitted(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	_, err := k.Submit(context.Background(), "s1", "search finished event test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	all := collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	if !hasTopicPrefix(all, "search.finished") {
		t.Errorf("SA-09: expected search.finished event, got topics: %v", topicsFrom(all))
	}
}

func TestSA09_SearchStartedPayloadHasWorkflowID(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "s1", "payload check")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	all := collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	for _, ev := range all {
		if ev.Topic != model.EventSearchStarted {
			continue
		}
		var p model.SearchStartedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("failed to decode search.started payload: %v", err)
		}
		if p.WorkflowID != wfID {
			t.Errorf("search.started payload WorkflowID = %q, want %q", p.WorkflowID, wfID)
		}
		if p.Query == "" {
			t.Error("search.started payload Query must not be empty")
		}
		if p.QueryID == "" {
			t.Error("search.started payload QueryID must not be empty")
		}
		return
	}
	t.Error("no search.started event found")
}

func TestSA09_SearchFinishedPayloadHasSnippetCount(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "s1", "snippet count check")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	all := collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	var found bool
	for _, ev := range all {
		if ev.Topic != model.EventSearchFinished {
			continue
		}
		var p model.SearchFinishedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("failed to decode search.finished payload: %v", err)
		}
		if p.WorkflowID != wfID && p.WorkflowID != "" {
			continue // skip other workflow's events (shouldn't happen but be safe)
		}
		found = true
		// snippet_count must be non-negative
		if p.SnippetCount < 0 {
			t.Errorf("search.finished SnippetCount = %d, must be ≥ 0", p.SnippetCount)
		}
		// duration_ms must be non-negative
		if p.DurationMs < 0 {
			t.Errorf("search.finished DurationMs = %d, must be ≥ 0", p.DurationMs)
		}
	}
	if !found {
		t.Error("no search.finished event found")
	}
}

func TestSA09_SearchEventsBeforePlanEvent(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	_, err := k.Submit(context.Background(), "s1", "event ordering test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	all := collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	// Macro search.started must appear before "plan" step_updated.
	searchIdx, planIdx := -1, -1
	for i, ev := range all {
		if ev.Topic == model.EventSearchStarted && searchIdx == -1 {
			searchIdx = i
		}
		if ev.Topic == model.EventWorkflowStepUpdate {
			var p model.WorkflowStepUpdatedPayload
			if err := json.Unmarshal(ev.Payload, &p); err == nil && p.Step == "plan" && p.Status == "running" {
				if planIdx == -1 {
					planIdx = i
				}
			}
		}
	}

	if searchIdx == -1 {
		t.Error("search.started event not found")
	}
	if planIdx == -1 {
		t.Error("plan step running event not found")
	}
	if searchIdx != -1 && planIdx != -1 && searchIdx >= planIdx {
		t.Errorf("search.started (index %d) should come before plan running (index %d)", searchIdx, planIdx)
	}
}

// ---------------------------------------------------------------------------
// LocalSearcher unit tests
// ---------------------------------------------------------------------------

func TestLocalSearcher_SearchReturnsDiscoveryContext(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	searcher := NewLocalSearcher(k, "session-ls", "wf-ls-1")
	intent := model.IntentSpec{
		ID:        "intent-ls",
		SessionID: "session-ls",
		Goal:      "RunKernel function",
	}

	dc, err := searcher.Search(context.Background(), intent, nil)
	if err != nil {
		t.Fatalf("LocalSearcher.Search failed: %v", err)
	}

	if dc.Query == "" {
		t.Error("DiscoveryContext.Query should not be empty")
	}
	if dc.QueryID == "" {
		t.Error("DiscoveryContext.QueryID should not be empty")
	}
	// Must have positive confidence when snippets exist (search executor provides hits).
	if len(dc.Snippets) > 0 && dc.Confidence <= 0 {
		t.Errorf("expected positive confidence when snippets present, got %v", dc.Confidence)
	}
}

func TestLocalSearcher_SearchUsesIntentGoalAsQuery(t *testing.T) {
	t.Parallel()

	var capturedQuery string
	exec := &funcExec{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		if req.Kind == "search" {
			capturedQuery = req.Query
		}
		return toolruntime.Result{Output: "main.go:1: func main() {}"}, nil
	}}

	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	searcher := NewLocalSearcher(k, "session-ls", "wf-ls")
	intent := model.IntentSpec{
		Goal: "find the entry point",
	}
	_, err := searcher.Search(context.Background(), intent, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if capturedQuery == "" {
		t.Error("expected search query to be propagated to executor")
	}
	if capturedQuery != intent.Goal {
		t.Errorf("expected query %q, got %q", intent.Goal, capturedQuery)
	}
}

func TestLocalSearcher_SearchFallsBackToTaskTitleWhenGoalEmpty(t *testing.T) {
	t.Parallel()

	exec := &funcExec{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		return toolruntime.Result{}, nil
	}}

	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	searcher := NewLocalSearcher(k, "s", "w")
	intent := model.IntentSpec{Goal: ""} // no goal
	tasks := []model.Task{{ID: "t1", Title: "implement login", Status: "planned"}}

	dc, err := searcher.Search(context.Background(), intent, tasks)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// When goal is empty, LocalSearcher falls back to the first task title as the
	// query. Verify via the returned DiscoveryContext.Query (the fallback path uses
	// req.Content, not req.Query, so we cannot intercept it via the executor).
	if dc.Query != "implement login" {
		t.Errorf("expected fallback query %q in DiscoveryContext.Query, got %q", "implement login", dc.Query)
	}
}

func TestLocalSearcher_RefineExpandsEvidence(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	searcher := NewLocalSearcher(k, "s", "w")

	// Seed an initial context with one snippet.
	initial := model.DiscoveryContext{
		Query:   "RunKernel",
		QueryID: "wf:seed",
		Evidence: []model.DiscoveryEvidence{
			{Source: "grep", Path: "kernel.go", Line: 42, Snippet: "func RunKernel()"},
		},
		Snippets: []model.FileSnippet{
			{Path: "kernel.go", Content: "func RunKernel() error { return nil }", Exists: true},
		},
		Confidence: 0.5,
	}

	refined, err := searcher.Refine(context.Background(), initial, []string{"error handling"})
	if err != nil {
		t.Fatalf("Refine failed: %v", err)
	}

	if refined.Query == "" {
		t.Error("Refine returned empty Query")
	}
	if refined.QueryID == "" {
		t.Error("Refine returned empty QueryID")
	}
	// Refined result should retain or expand snippets.
	if len(refined.Snippets) == 0 && len(refined.Evidence) == 0 {
		t.Error("Refine returned empty Snippets and Evidence")
	}
}

func TestLocalSearcher_ConfidenceScalesWithSnippetCount(t *testing.T) {
	t.Parallel()

	// executor returns N hits based on query length as a hack
	exec := &funcExec{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		switch req.Kind {
		case "search":
			return toolruntime.Result{
				Output: "a.go:1: func A() {}\nb.go:2: func B() {}\nc.go:3: func C() {}\nd.go:4: func D() {}\ne.go:5: func E() {}",
			}, nil
		case "read_file":
			return toolruntime.Result{Output: "package p\nfunc F() {}", Summary: "ok"}, nil
		}
		return toolruntime.Result{}, nil
	}}

	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	searcher := NewLocalSearcher(k, "s", "w")
	dc, err := searcher.Search(context.Background(), model.IntentSpec{Goal: "find functions"}, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(dc.Snippets) >= 5 && dc.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0 with ≥5 snippets, got %v", dc.Confidence)
	}
	if len(dc.Snippets) > 0 && dc.Confidence <= 0 {
		t.Errorf("expected positive confidence with snippets, got %v", dc.Confidence)
	}
}

// ---------------------------------------------------------------------------
// SA-07: stale evidence marking
// ---------------------------------------------------------------------------

func TestSA07_StaleEvidenceMarkedForDirtyPaths(t *testing.T) {
	t.Parallel()

	exec := &funcExec{fn: func(_ context.Context, req toolruntime.Request) (toolruntime.Result, error) {
		if req.Kind == "search" {
			return toolruntime.Result{
				Output: "dirty.go:1: func DirtyFunc() {}\nclean.go:2: func CleanFunc() {}\n",
			}, nil
		}
		if req.Kind == "read_file" {
			return toolruntime.Result{Output: "package p\nfunc F() {}", Summary: "ok"}, nil
		}
		return toolruntime.Result{}, nil
	}}

	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	// Mark dirty.go as dirty before searching.
	k.workspace.MarkDirty("dirty.go")

	searcher := NewLocalSearcher(k, "s", "w")
	dc, err := searcher.Search(context.Background(), model.IntentSpec{Goal: "find functions"}, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	dirtyCount := 0
	for _, e := range dc.Evidence {
		if e.Path == "dirty.go" && e.Stale {
			dirtyCount++
		}
	}
	if dirtyCount == 0 {
		t.Error("SA-07: dirty.go evidence was not marked Stale")
	}

	for _, e := range dc.Evidence {
		if e.Path == "clean.go" && e.Stale {
			t.Error("SA-07: clean.go evidence must not be marked Stale")
		}
	}
}

// ---------------------------------------------------------------------------
// DiscoveryHints uses task titles and files
// ---------------------------------------------------------------------------

func TestDiscoveryHints_IncludesGoalAndTaskTitles(t *testing.T) {
	t.Parallel()

	tasks := []model.Task{
		{ID: "t1", Title: "add retry logic", Files: []string{"retry.go"}},
		{ID: "t2", Title: "update tests"},
	}
	hints := discoveryHints(tasks, "improve reliability")

	want := map[string]bool{
		"improve reliability": true,
		"add retry logic":     true,
		"retry.go":            true,
		"update tests":        true,
	}
	for _, h := range hints {
		delete(want, h)
	}
	for missing := range want {
		t.Errorf("expected hint %q not found in: %v", missing, hints)
	}
}

func TestDiscoveryHints_DeduplicatesEntries(t *testing.T) {
	t.Parallel()

	tasks := []model.Task{
		{ID: "t1", Title: "goal", Files: []string{"f.go"}},
		{ID: "t2", Title: "goal"}, // duplicate title
	}
	hints := discoveryHints(tasks, "goal") // also same as task title

	// Count occurrences of "goal".
	count := 0
	for _, h := range hints {
		if h == "goal" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'goal' to appear exactly once in hints, got %d times", count)
	}
}

func TestDiscoveryHints_CappedAtSix(t *testing.T) {
	t.Parallel()

	tasks := make([]model.Task, 20)
	for i := range tasks {
		tasks[i] = model.Task{
			ID:    "t",
			Title: "title " + string(rune('a'+i)),
			Files: []string{"file" + string(rune('a'+i)) + ".go"},
		}
	}
	hints := discoveryHints(tasks, "the goal")
	if len(hints) > 6 {
		t.Errorf("hints should be capped at 6, got %d", len(hints))
	}
}

// ---------------------------------------------------------------------------
// ShouldRefineDiscovery heuristics
// ---------------------------------------------------------------------------

func TestShouldRefineDiscovery_TrueWhenNoSnippets(t *testing.T) {
	t.Parallel()
	dc := model.DiscoveryContext{Confidence: 0.9}
	if !shouldRefineDiscovery(dc, nil) {
		t.Error("expected shouldRefine=true when no snippets")
	}
}

func TestShouldRefineDiscovery_TrueWhenLowConfidence(t *testing.T) {
	t.Parallel()
	dc := model.DiscoveryContext{
		Snippets:   []model.FileSnippet{{Path: "a.go", Exists: true}},
		Confidence: 0.2, // below 0.4 threshold
	}
	if !shouldRefineDiscovery(dc, nil) {
		t.Error("expected shouldRefine=true with confidence < 0.4")
	}
}

func TestShouldRefineDiscovery_TrueWhenFewerEvidenceThanTasks(t *testing.T) {
	t.Parallel()
	dc := model.DiscoveryContext{
		Snippets:   []model.FileSnippet{{Path: "a.go", Exists: true}},
		Confidence: 0.8,
		Evidence:   []model.DiscoveryEvidence{{Path: "a.go"}},
	}
	tasks := []model.Task{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}} // 3 tasks, only 1 evidence
	if !shouldRefineDiscovery(dc, tasks) {
		t.Error("expected shouldRefine=true when evidence count < task count")
	}
}

func TestShouldRefineDiscovery_FalseWhenSufficient(t *testing.T) {
	t.Parallel()
	dc := model.DiscoveryContext{
		Snippets: []model.FileSnippet{
			{Path: "a.go", Exists: true},
			{Path: "b.go", Exists: true},
		},
		Confidence: 0.8,
		Evidence: []model.DiscoveryEvidence{
			{Path: "a.go"},
			{Path: "b.go"},
		},
	}
	tasks := []model.Task{{ID: "t1"}} // 1 task, 2 evidence
	if shouldRefineDiscovery(dc, tasks) {
		t.Error("expected shouldRefine=false when snippets, confidence, and evidence are sufficient")
	}
}

// ---------------------------------------------------------------------------
// Full workflow: search events accompany checkpoint
// ---------------------------------------------------------------------------

func TestFullWorkflow_SearchEventsBookendWorkflow(t *testing.T) {
	t.Parallel()

	exec := &searchExecutor{}
	k := NewWithWorkflowDependencies(
		t.TempDir(),
		testInputter{},
		testPlanner{},
		testCoder{},
		exec,
		testAcceptor{},
	)
	defer k.Close()

	events, cancel := k.Subscribe("s1")
	defer cancel()

	wfID, err := k.Submit(context.Background(), "s1", "bookend test")
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	all := collectAllEvents(t, events, model.EventCheckpointUpdated, 10*time.Second)

	// Must have workflow.started, at least one search.started, and checkpoint.updated.
	var hasStart, hasSearch, hasCheckpoint bool
	for _, ev := range all {
		switch ev.Topic {
		case model.EventWorkflowStarted:
			var p model.WorkflowStartedPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if p.WorkflowID == wfID {
				hasStart = true
			}
		case model.EventSearchStarted:
			hasSearch = true
		case model.EventCheckpointUpdated:
			hasCheckpoint = true
		}
	}
	if !hasStart {
		t.Error("expected workflow.started event")
	}
	if !hasSearch {
		t.Error("expected search.started event")
	}
	if !hasCheckpoint {
		t.Error("expected checkpoint.updated event")
	}
}

// ---------------------------------------------------------------------------
// funcExec: general test executor backed by a closure
// ---------------------------------------------------------------------------

type funcExec struct {
	fn func(context.Context, toolruntime.Request) (toolruntime.Result, error)
}

func (e *funcExec) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	return e.fn(ctx, req)
}

// Ensure funcPlanner satisfies workflow.Planner.
var _ workflow.Planner = (*funcPlanner)(nil)
