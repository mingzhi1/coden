// Command coden-eval runs a repeatable evaluation suite against coden.
//
// It drives the real `coden --plain` binary headless for each case in a YAML
// suite and scores the run on observable signals — which workflow path ran, the
// checkpoint outcome, analysis size, whether RAG was used, and how many insights
// were persisted. It turns "I think it works" into a pass/fail table.
//
//	go run ./cmd/coden-eval                          # all cases, workspace = .
//	go run ./cmd/coden-eval -cases eval/cases.yaml -workspace . -run analyze
//
// Exits non-zero if any case fails (CI-friendly). Uses ~/.coden/config.yaml, so
// runs make real LLM calls (slow + metered).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type expect struct {
	Path        string `yaml:"path"`
	Checkpoint  string `yaml:"checkpoint"`
	MinChars    int    `yaml:"min_chars"`
	RAGHitsMin  int    `yaml:"rag_hits_min"`
	InsightsMin int    `yaml:"insights_min"`
}

type evalCase struct {
	Name      string `yaml:"name"`
	Prompt    string `yaml:"prompt"`
	Workspace string `yaml:"workspace"`
	Expect    expect `yaml:"expect"`
}

// observed holds the signals parsed from a run's combined output.
type observed struct {
	path       string
	checkpoint string
	chars      int
	ragHits    int
	insights   int
	durSec     float64
}

var (
	reAnalyzeLen = regexp.MustCompile(`analysis_len=(\d+)`)
	reRAGHits    = regexp.MustCompile(`rag_search.*?hits=(\d+)`)
	reInsights   = regexp.MustCompile(`extracted insights.*?count=(\d+)`)
	reCheckpoint = regexp.MustCompile(`checkpoint: (pass|fail)`)
)

func main() {
	casesPath := flag.String("cases", "eval/cases.yaml", "path to the YAML eval suite")
	bin := flag.String("bin", "", "coden binary to run (built from ./cmd/coden if empty)")
	workspace := flag.String("workspace", ".", "default workspace root for cases")
	runFilter := flag.String("run", "", "only run cases whose name contains this substring")
	timeout := flag.Duration("timeout", 6*time.Minute, "per-case timeout")
	flag.Parse()

	cases, err := loadCases(*casesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load cases:", err)
		os.Exit(2)
	}

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

	fmt.Printf("%-32s %-9s %-5s %6s %4s %4s %5s\n", "CASE", "PATH", "CKPT", "CHARS", "RAG", "INS", "RESULT")
	fmt.Println(strings.Repeat("─", 78))

	pass := 0
	run := 0
	for i, c := range cases {
		if *runFilter != "" && !strings.Contains(c.Name, *runFilter) {
			continue
		}
		run++
		ws := defaultWS
		if c.Workspace != "" {
			ws, _ = filepath.Abs(c.Workspace)
		}
		out := runCase(binPath, ws, c, *timeout, i)
		obs := parse(out)
		fails := check(c.Expect, obs)
		ok := len(fails) == 0
		if ok {
			pass++
		}
		result := "PASS"
		if !ok {
			result = "FAIL"
		}
		fmt.Printf("%-32s %-9s %-5s %6d %4d %4d %5s\n",
			truncate(c.Name, 32), obs.path, obs.checkpoint, obs.chars, obs.ragHits, obs.insights, result)
		for _, f := range fails {
			fmt.Printf("    ✗ %s\n", f)
		}
	}

	fmt.Println(strings.Repeat("─", 78))
	fmt.Printf("%d/%d passed\n", pass, run)
	if pass < run {
		os.Exit(1)
	}
}

func loadCases(path string) ([]evalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []evalCase
	if err := yaml.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
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

func runCase(bin, ws string, c evalCase, timeout time.Duration, idx int) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	session := fmt.Sprintf("eval-%d-%s", idx, sanitize(c.Name))
	cmd := exec.CommandContext(ctx, bin,
		"--plain", "--new-session", "--session", session,
		"--workspace", ws, "--prompt", c.Prompt)
	out, _ := cmd.CombinedOutput() // non-zero exit is captured as a fail via signals
	return string(out)
}

func parse(out string) observed {
	o := observed{checkpoint: "none"}
	switch {
	case strings.Contains(out, "analyze workflow completed"):
		o.path = "analyze"
	case strings.Contains(out, "plan_only workflow completed"):
		o.path = "plan_only"
	case strings.Contains(out, "question answered directly"):
		o.path = "question"
	case strings.Contains(out, "workflow completed") || strings.Contains(out, "checkpoint: pass"):
		o.path = "code"
	default:
		o.path = "?"
	}
	if m := reCheckpoint.FindStringSubmatch(out); m != nil {
		o.checkpoint = m[1]
	} else if strings.Contains(out, "workflow.failed") || strings.Contains(out, "workflow failed") {
		o.checkpoint = "fail"
	}
	o.chars = lastInt(reAnalyzeLen, out)
	o.ragHits = maxInt(reRAGHits, out)
	o.insights = maxInt(reInsights, out)
	return o
}

func check(e expect, o observed) []string {
	var fails []string
	wantCkpt := e.Checkpoint
	if wantCkpt == "" {
		wantCkpt = "pass"
	}
	if o.checkpoint != wantCkpt {
		fails = append(fails, fmt.Sprintf("checkpoint=%s, want %s", o.checkpoint, wantCkpt))
	}
	if e.Path != "" && o.path != e.Path {
		fails = append(fails, fmt.Sprintf("path=%s, want %s", o.path, e.Path))
	}
	if e.MinChars > 0 && o.chars < e.MinChars {
		fails = append(fails, fmt.Sprintf("chars=%d, want >=%d", o.chars, e.MinChars))
	}
	if e.RAGHitsMin > 0 && o.ragHits < e.RAGHitsMin {
		fails = append(fails, fmt.Sprintf("rag_hits=%d, want >=%d", o.ragHits, e.RAGHitsMin))
	}
	if e.InsightsMin > 0 && o.insights < e.InsightsMin {
		fails = append(fails, fmt.Sprintf("insights=%d, want >=%d", o.insights, e.InsightsMin))
	}
	return fails
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

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
