// Command coden-eval runs a repeatable evaluation suite against coden.
//
// It drives the real `coden --plain` binary headless for each case in a YAML
// suite and scores the run on observable signals — which workflow path ran, the
// checkpoint outcome, analysis size, whether RAG was used, and how many insights
// were persisted. It turns "I think it works" into a pass/fail table.
//
// A baseline checkpoint records each case's metrics so later runs can be diffed
// against a known-good state (flow/regression testing across changes):
//
//	go run ./cmd/coden-eval -update-baseline    # snapshot current metrics → baseline
//	go run ./cmd/coden-eval                      # run + score + diff vs baseline
//	go run ./cmd/coden-eval -run analyze         # filter by name
//
// Exits non-zero if any case fails an assertion OR regresses vs the baseline
// (CI-friendly). Uses ~/.coden/config.yaml, so runs make real LLM calls.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type expect struct {
	Path            string `yaml:"path"`
	Checkpoint      string `yaml:"checkpoint"`
	MinChars        int    `yaml:"min_chars"`
	RAGHitsMin      int    `yaml:"rag_hits_min"`
	InsightsMin     int    `yaml:"insights_min"`
	RecallMin       int    `yaml:"recall_min"`        // >= N insights recalled from a PRIOR turn (cross-turn memory)
	TasksMin        int    `yaml:"tasks_min"`         // planner produced >= N tasks
	FilesWrittenMin int    `yaml:"files_written_min"` // coder wrote >= N files
	Build           string `yaml:"build"`             // shell cmd run in workspace post-run; must exit 0
}

// turn is one message in a multi-turn case. The first turn opens a fresh
// session; later turns APPEND to that same session in a NEW process — exercising
// session persistence (messages reloaded from disk) and cross-turn memory
// (insights persisted in turn N recalled in turn N+1).
type turn struct {
	Prompt string `yaml:"prompt"`
	Expect expect `yaml:"expect"`
}

type evalCase struct {
	Name       string `yaml:"name"`
	Prompt     string `yaml:"prompt"` // single-turn shorthand (mutually exclusive with turns)
	Turns      []turn `yaml:"turns"`  // multi-turn: append to one session across processes
	Workspace  string `yaml:"workspace"`
	Fixture    string `yaml:"fixture"`     // dir copied to a temp workspace per run (for code_gen)
	AllowShell bool   `yaml:"allow_shell"` // pass --allow-shell (code_gen that runs tools)
	Expect     expect `yaml:"expect"`      // single-turn expectations
}

// steps normalizes a case into its turn sequence — a single-prompt case becomes
// a one-turn sequence, so the runner has one code path.
func (c evalCase) steps() []turn {
	if len(c.Turns) > 0 {
		return c.Turns
	}
	return []turn{{Prompt: c.Prompt, Expect: c.Expect}}
}

// Metrics holds the signals parsed from a run's combined output — both the
// end-to-end outcome and per-role behavior (which agent did what), so the suite
// is an observatory of each role, not just a final pass/fail. Serialized into
// the baseline checkpoint for cross-run comparison.
type Metrics struct {
	Path       string  `json:"path"`
	Checkpoint string  `json:"checkpoint"`
	Chars      int     `json:"chars"`
	RAGHits    int     `json:"rag_hits"`
	Insights   int     `json:"insights"`
	Recall     int     `json:"recall"` // insights recalled from prior turns (cross-turn memory)
	DurSec     float64 `json:"dur_sec"`

	// Per-role signals (0 / "" when that role didn't run for this path).
	Snippets     int     `json:"snippets"`      // searcher: discovery snippets prefetched
	Tasks        int     `json:"tasks"`         // planner: tasks in the DAG
	CriticScore  float64 `json:"critic_score"`  // critic: plan score
	CriticIssues int     `json:"critic_issues"` // critic: issues raised
	CoderRounds  int     `json:"coder_rounds"`  // coder: agentic read/build rounds
	FilesWritten int     `json:"files_written"` // coder: write_file/edit_file calls
}

var (
	reAnalyzeLen  = regexp.MustCompile(`analysis_len=(\d+)`)
	reRAGHits     = regexp.MustCompile(`rag_search.*?hits=(\d+)`)
	reInsights    = regexp.MustCompile(`extracted insights.*?count=(\d+)`)
	reRecall      = regexp.MustCompile(`recalled insights.*?count=(\d+)`)
	reSession     = regexp.MustCompile(`(?m)^session:\s*(\S+)`)
	reCheckpoint  = regexp.MustCompile(`checkpoint: (pass|fail)`)
	reSnippets    = regexp.MustCompile(`(?:prefetch completed|discovery).*?snippets=(\d+)`)
	reTasks       = regexp.MustCompile(`tasks=(\d+)`)
	reCritic      = regexp.MustCompile(`critique complete.*?score=([\d.]+).*?issues=(\d+)`)
	reCoderRounds = regexp.MustCompile(`\[llm:coder\] executing reads in parallel round=(\d+)`)
	reFileWrite   = regexp.MustCompile(`executing kind=(?:write_file|edit_file)`)
)

func main() {
	casesPath := flag.String("cases", "eval/cases.yaml", "path to the YAML eval suite")
	bin := flag.String("bin", "", "coden binary to run (built from ./cmd/coden if empty)")
	workspace := flag.String("workspace", ".", "default workspace root for cases")
	runFilter := flag.String("run", "", "only run cases whose name contains this substring")
	timeout := flag.Duration("timeout", 6*time.Minute, "per-case timeout")
	baselinePath := flag.String("baseline", "eval/baseline.json", "baseline checkpoint file")
	updateBaseline := flag.Bool("update-baseline", false, "write current metrics to the baseline and exit")
	flag.Parse()

	cases, err := loadCases(*casesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load cases:", err)
		os.Exit(2)
	}
	baseline := loadBaseline(*baselinePath) // nil if absent

	binPath := *bin
	if binPath == "" {
		fmt.Fprintln(os.Stderr, "building coden…")
		binPath, err = buildCoden()
		if err != nil {
			fmt.Fprintln(os.Stderr, "build coden:", err)
			os.Exit(2)
		}
		defer os.Remove(binPath)
	}

	defaultWS, _ := filepath.Abs(*workspace)

	results := map[string]Metrics{}
	hasBaseline := baseline != nil && !*updateBaseline

	header := fmt.Sprintf("%-30s %-9s %-5s %6s %4s %4s %5s", "CASE", "PATH", "CKPT", "CHARS", "RAG", "INS", "RESULT")
	if hasBaseline {
		header += "  Δ(chars/rag/ins)"
	}
	fmt.Println(header)
	fmt.Println(strings.Repeat("─", len(header)+4))

	pass, run, regressed := 0, 0, 0
	for _, c := range cases {
		if *runFilter != "" && !strings.Contains(c.Name, *runFilter) {
			continue
		}
		run++
		ws, isTemp, wsErr := resolveWorkspace(c, defaultWS)
		if wsErr != nil {
			fmt.Printf("%-30s  setup error: %v\n", truncate(c.Name, 30), wsErr)
			continue
		}
		// Run the case's turn sequence. Turn 1 opens a fresh session; later turns
		// append to it in a NEW process (session reloaded from disk) — so a
		// multi-turn case tests session persistence AND cross-turn memory.
		steps := c.steps()
		turnMetrics := make([]Metrics, 0, len(steps))
		var fails []string
		session := ""
		for ti, st := range steps {
			t0 := time.Now()
			out, sid := runTurn(binPath, ws, c, st.Prompt, session, *timeout)
			m := parse(out)
			// Retry once on a dead run — the process produced nothing recognizable
			// (no workflow completed, no checkpoint). That's an infrastructure
			// hiccup (e.g. a transient LLM/provider error), not a capability the
			// case is testing, so it shouldn't be a false FAIL. A run that COMPLETES
			// but fails an assertion is NOT retried — that's a real signal.
			if isDeadRun(m) {
				out, sid = runTurn(binPath, ws, c, st.Prompt, session, *timeout)
				m = parse(out)
			}
			if ti == 0 {
				session = sid
			}
			m.DurSec = time.Since(t0).Seconds()
			turnMetrics = append(turnMetrics, m)
			for _, f := range check(st.Expect, m) {
				if len(steps) > 1 {
					fails = append(fails, fmt.Sprintf("turn%d: %s", ti+1, f))
				} else {
					fails = append(fails, f)
				}
			}
		}
		// Session-append sanity: a multi-turn case must carry a real session id
		// from turn 1, or turns 2+ silently ran as fresh sessions (no append).
		if len(steps) > 1 && session == "" {
			fails = append(fails, "session append failed: turn 1 emitted no session id")
		}
		// Build gate runs once after the final turn, against the (possibly mutated) ws.
		if c.Expect.Build != "" {
			if err := runBuild(ws, c.Expect.Build, *timeout); err != nil {
				fails = append(fails, "build failed: "+err.Error())
			}
		}
		if isTemp {
			os.RemoveAll(ws)
		}
		m := turnMetrics[len(turnMetrics)-1] // case-level metric = final turn
		results[c.Name] = m

		printRoles := func() {
			if len(turnMetrics) > 1 {
				for ti, tm := range turnMetrics {
					if d := rolesDetail(tm); d != "" {
						fmt.Printf("    turn%d [%s]: %s\n", ti+1, truncate(steps[ti].Prompt, 24), d)
					}
				}
			} else if d := rolesDetail(m); d != "" {
				fmt.Printf("    %s\n", d)
			}
		}

		ok := len(fails) == 0
		if ok {
			pass++
		}
		result := "PASS"
		if !ok {
			result = "FAIL"
		}

		row := fmt.Sprintf("%-30s %-9s %-5s %6d %4d %4d %5s",
			truncate(c.Name, 30), m.Path, m.Checkpoint, m.Chars, m.RAGHits, m.Insights, result)
		if hasBaseline {
			if base, found := baseline[c.Name]; found {
				row += "  " + deltaStr(base, m)
				if regs := regressions(base, m); len(regs) > 0 {
					regressed++
					row += "  ⚠ REGRESSED"
					fmt.Println(row)
					printRoles()
					for _, r := range regs {
						fmt.Printf("    ↓ %s\n", r)
					}
					for _, f := range fails {
						fmt.Printf("    ✗ %s\n", f)
					}
					continue
				}
			} else {
				row += "  (new)"
			}
		}
		fmt.Println(row)
		printRoles()
		for _, f := range fails {
			fmt.Printf("    ✗ %s\n", f)
		}
	}

	fmt.Println(strings.Repeat("─", len(header)+4))

	if *updateBaseline {
		if err := saveBaseline(*baselinePath, results); err != nil {
			fmt.Fprintln(os.Stderr, "write baseline:", err)
			os.Exit(2)
		}
		fmt.Printf("baseline checkpoint written → %s (%d cases)\n", *baselinePath, len(results))
		return
	}

	fmt.Printf("%d/%d passed", pass, run)
	if hasBaseline {
		fmt.Printf(", %d regressed vs baseline", regressed)
	}
	fmt.Println()
	if pass < run || regressed > 0 {
		os.Exit(1)
	}
}

func loadCases(path string) ([]evalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []evalCase
	return cases, yaml.Unmarshal(data, &cases)
}

func loadBaseline(path string) map[string]Metrics {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]Metrics
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

func saveBaseline(path string, m map[string]Metrics) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Stable key order for a clean diff in version control.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]Metrics, len(m))
	for _, k := range keys {
		ordered[k] = m[k]
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func buildCoden() (string, error) {
	out := filepath.Join(os.TempDir(), "coden-eval-bin")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/coden")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
}

// runTurn runs one turn. When session is empty it opens a fresh session
// (--new-session) and returns the generated id parsed from stdout; otherwise it
// appends to the given session in a new process (no --new-session), which forces
// coden to reload the session's messages + insights from disk — the crux of the
// session-append / cross-turn-memory test.
func runTurn(bin, ws string, c evalCase, prompt, session string, timeout time.Duration) (out, sid string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"--plain", "--workspace", ws, "--prompt", prompt}
	if session == "" {
		args = append(args, "--new-session")
	} else {
		args = append(args, "--session", session)
	}
	if c.AllowShell {
		args = append(args, "--allow-shell")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	b, _ := cmd.CombinedOutput() // non-zero exit is captured via signals
	out = string(b)
	sid = session
	if session == "" {
		if mm := reSession.FindStringSubmatch(out); mm != nil {
			sid = mm[1]
		}
	}
	return out, sid
}

// resolveWorkspace returns the workspace to run in. For a fixture case it copies
// the fixture to a fresh temp dir (so the coder's writes don't mutate the repo
// and each run starts clean); caller removes it when done.
func resolveWorkspace(c evalCase, defaultWS string) (ws string, isTemp bool, err error) {
	if c.Fixture != "" {
		src, _ := filepath.Abs(c.Fixture)
		tmp, err := os.MkdirTemp("", "coden-eval-fixture-*")
		if err != nil {
			return "", false, err
		}
		if err := copyDir(src, tmp); err != nil {
			os.RemoveAll(tmp)
			return "", false, err
		}
		return tmp, true, nil
	}
	if c.Workspace != "" {
		ws, _ = filepath.Abs(c.Workspace)
		return ws, false, nil
	}
	return defaultWS, false, nil
}

// runBuild runs a shell command in ws and returns an error if it exits non-zero.
func runBuild(ws, command string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = ws
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%q: %s", command, strings.TrimSpace(lastLine(string(out))))
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func parse(out string) Metrics {
	m := Metrics{Checkpoint: "none"}
	switch {
	case strings.Contains(out, "analyze workflow completed"):
		m.Path = "analyze"
	case strings.Contains(out, "plan_only workflow completed"):
		m.Path = "plan_only"
	case strings.Contains(out, "question answered directly"):
		m.Path = "question"
	case strings.Contains(out, "workflow completed") || strings.Contains(out, "checkpoint: pass"):
		m.Path = "code"
	default:
		m.Path = "?"
	}
	if mm := reCheckpoint.FindStringSubmatch(out); mm != nil {
		m.Checkpoint = mm[1]
	} else if strings.Contains(out, "workflow.failed") || strings.Contains(out, "workflow failed") {
		m.Checkpoint = "fail"
	}
	m.Chars = lastInt(reAnalyzeLen, out)
	m.RAGHits = maxInt(reRAGHits, out)
	m.Insights = maxInt(reInsights, out)
	m.Recall = maxInt(reRecall, out)
	m.Snippets = maxInt(reSnippets, out)
	m.Tasks = maxInt(reTasks, out)
	if mm := reCritic.FindStringSubmatch(out); mm != nil {
		m.CriticScore, _ = strconv.ParseFloat(mm[1], 64)
		m.CriticIssues, _ = strconv.Atoi(mm[2])
	}
	m.CoderRounds = maxInt(reCoderRounds, out)
	m.FilesWritten = len(reFileWrite.FindAllStringIndex(out, -1))
	return m
}

// rolesDetail renders the per-role behavior line so you can see what each agent
// did this turn, not just the final outcome.
func rolesDetail(m Metrics) string {
	var parts []string
	if m.RAGHits > 0 || m.Snippets > 0 {
		parts = append(parts, fmt.Sprintf("searcher(rag=%d,snip=%d)", m.RAGHits, m.Snippets))
	}
	if m.Tasks > 0 {
		parts = append(parts, fmt.Sprintf("planner(tasks=%d)", m.Tasks))
	}
	if m.CriticIssues > 0 || m.CriticScore > 0 {
		parts = append(parts, fmt.Sprintf("critic(score=%.2f,issues=%d)", m.CriticScore, m.CriticIssues))
	}
	if m.CoderRounds > 0 || m.FilesWritten > 0 {
		parts = append(parts, fmt.Sprintf("coder(rounds=%d,files=%d)", m.CoderRounds, m.FilesWritten))
	}
	if m.Chars > 0 {
		parts = append(parts, fmt.Sprintf("analyzer(chars=%d)", m.Chars))
	}
	if m.Insights > 0 || m.Recall > 0 {
		parts = append(parts, fmt.Sprintf("secretary(ins=%d,recall=%d)", m.Insights, m.Recall))
	}
	if len(parts) == 0 {
		return ""
	}
	return "roles: " + strings.Join(parts, " ")
}

// isDeadRun reports a run that produced no recognizable workflow output at all —
// the process effectively did nothing (transient infra failure), as opposed to
// running to completion and failing an assertion.
func isDeadRun(m Metrics) bool {
	return m.Path == "?" && m.Checkpoint == "none"
}

func check(e expect, m Metrics) []string {
	var fails []string
	wantCkpt := e.Checkpoint
	if wantCkpt == "" {
		wantCkpt = "pass"
	}
	if m.Checkpoint != wantCkpt {
		fails = append(fails, fmt.Sprintf("checkpoint=%s, want %s", m.Checkpoint, wantCkpt))
	}
	if e.Path != "" && m.Path != e.Path {
		fails = append(fails, fmt.Sprintf("path=%s, want %s", m.Path, e.Path))
	}
	if e.MinChars > 0 && m.Chars < e.MinChars {
		fails = append(fails, fmt.Sprintf("chars=%d, want >=%d", m.Chars, e.MinChars))
	}
	if e.RAGHitsMin > 0 && m.RAGHits < e.RAGHitsMin {
		fails = append(fails, fmt.Sprintf("rag_hits=%d, want >=%d", m.RAGHits, e.RAGHitsMin))
	}
	if e.InsightsMin > 0 && m.Insights < e.InsightsMin {
		fails = append(fails, fmt.Sprintf("insights=%d, want >=%d", m.Insights, e.InsightsMin))
	}
	if e.RecallMin > 0 && m.Recall < e.RecallMin {
		fails = append(fails, fmt.Sprintf("recall=%d, want >=%d", m.Recall, e.RecallMin))
	}
	if e.TasksMin > 0 && m.Tasks < e.TasksMin {
		fails = append(fails, fmt.Sprintf("tasks=%d, want >=%d", m.Tasks, e.TasksMin))
	}
	if e.FilesWrittenMin > 0 && m.FilesWritten < e.FilesWrittenMin {
		fails = append(fails, fmt.Sprintf("files_written=%d, want >=%d", m.FilesWritten, e.FilesWrittenMin))
	}
	return fails
}

// regressions reports capability losses vs the baseline. LLM output is noisy, so
// only hard losses count (pass→non-pass, a signal going from present to zero) —
// numeric wiggle is shown as a delta, not a regression.
func regressions(base, cur Metrics) []string {
	var r []string
	if base.Checkpoint == "pass" && cur.Checkpoint != "pass" {
		r = append(r, fmt.Sprintf("checkpoint %s → %s", base.Checkpoint, cur.Checkpoint))
	}
	if base.Path != "" && base.Path != "?" && cur.Path != base.Path {
		r = append(r, fmt.Sprintf("path %s → %s", base.Path, cur.Path))
	}
	if base.RAGHits > 0 && cur.RAGHits == 0 {
		r = append(r, "RAG hits → 0 (RAG stopped being used)")
	}
	if base.Insights > 0 && cur.Insights == 0 {
		r = append(r, "insights → 0 (memory stopped persisting)")
	}
	if base.Recall > 0 && cur.Recall == 0 {
		r = append(r, "recall → 0 (cross-turn memory stopped being recalled)")
	}
	if base.Chars > 0 && cur.Chars > 0 && cur.Chars < base.Chars/2 {
		r = append(r, fmt.Sprintf("analysis chars %d → %d (>50%% drop)", base.Chars, cur.Chars))
	}
	return r
}

func deltaStr(base, cur Metrics) string {
	return fmt.Sprintf("%+d/%+d/%+d", cur.Chars-base.Chars, cur.RAGHits-base.RAGHits, cur.Insights-base.Insights)
}

func lastInt(re *regexp.Regexp, s string) int {
	ms := re.FindAllStringSubmatch(s, -1)
	if len(ms) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(ms[len(ms)-1][1])
	return n
}

func maxInt(re *regexp.Regexp, s string) int {
	best := 0
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if n, _ := strconv.Atoi(m[1]); n > best {
			best = n
		}
	}
	return best
}

// truncate is rune-aware so multibyte (e.g. Chinese) prompt previews don't get
// sliced mid-character into invalid UTF-8.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
