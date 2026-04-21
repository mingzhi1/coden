package outputcompressor

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Compressor framework
// ---------------------------------------------------------------------------

func TestCompress_ShortOutput_NoOp(t *testing.T) {
	c := New()
	out := c.Compress("run_shell", "echo hello", "hello world", 100, "")
	if out != "hello world" {
		t.Errorf("expected passthrough, got %q", out)
	}
}

func TestCompress_FallbackTruncation(t *testing.T) {
	c := New()
	long := strings.Repeat("x", 500)
	out := c.Compress("read_file", "", long, 100, "")
	if len(out) > 120 { // 100 + "... (truncated)" suffix
		t.Errorf("expected truncation, got len=%d", len(out))
	}
	if !strings.Contains(out, "... (truncated)") {
		t.Error("expected truncation marker")
	}
}

// ---------------------------------------------------------------------------
// GoTestStrategy — NDJSON
// ---------------------------------------------------------------------------

func TestGoTest_AllPass_NDJSON(t *testing.T) {
	input := `{"Action":"run","Package":"pkg/a","Test":"TestFoo"}
{"Action":"output","Package":"pkg/a","Test":"TestFoo","Output":"=== RUN   TestFoo\n"}
{"Action":"pass","Package":"pkg/a","Test":"TestFoo","Elapsed":0.01}
{"Action":"run","Package":"pkg/a","Test":"TestBar"}
{"Action":"pass","Package":"pkg/a","Test":"TestBar","Elapsed":0.02}
{"Action":"pass","Package":"pkg/a","Elapsed":0.5}
{"Action":"run","Package":"pkg/b","Test":"TestBaz"}
{"Action":"pass","Package":"pkg/b","Test":"TestBaz","Elapsed":0.01}
{"Action":"pass","Package":"pkg/b","Elapsed":0.3}`

	c := New()
	out := c.Compress("run_shell", "go test ./...", input, 2000, "")
	if !strings.Contains(out, "ok:") {
		t.Errorf("expected 'ok:' summary, got: %s", out)
	}
	if !strings.Contains(out, "3 passed") {
		t.Errorf("expected '3 passed', got: %s", out)
	}
	if !strings.Contains(out, "2 packages") {
		t.Errorf("expected '2 packages', got: %s", out)
	}
	// Should be very compact.
	if len(out) > 100 {
		t.Errorf("expected compact output, got len=%d: %s", len(out), out)
	}
}

func TestGoTest_WithFailures_NDJSON(t *testing.T) {
	input := `{"Action":"run","Package":"pkg/a","Test":"TestGood"}
{"Action":"pass","Package":"pkg/a","Test":"TestGood","Elapsed":0.01}
{"Action":"run","Package":"pkg/a","Test":"TestBad"}
{"Action":"output","Package":"pkg/a","Test":"TestBad","Output":"    expected 1, got 2\n"}
{"Action":"fail","Package":"pkg/a","Test":"TestBad","Elapsed":0.01}
{"Action":"fail","Package":"pkg/a","Elapsed":0.5}`

	c := New()
	out := c.Compress("run_shell", "go test ./...", input, 2000, "")
	if !strings.Contains(out, "FAIL:") {
		t.Errorf("expected 'FAIL:', got: %s", out)
	}
	if !strings.Contains(out, "1 failed") {
		t.Errorf("expected '1 failed', got: %s", out)
	}
	if !strings.Contains(out, "TestBad") {
		t.Errorf("expected 'TestBad' in output, got: %s", out)
	}
	if !strings.Contains(out, "expected 1, got 2") {
		t.Errorf("expected failure details, got: %s", out)
	}
}

func TestGoTest_PlainText_AllPass(t *testing.T) {
	input := `ok  	pkg/a	0.123s
ok  	pkg/b	0.456s
ok  	pkg/c	0.789s`

	c := New()
	out := c.Compress("run_shell", "go test ./...", input, 2000, "")
	if !strings.Contains(out, "ok:") {
		t.Errorf("expected 'ok:', got: %s", out)
	}
	if !strings.Contains(out, "3 packages passed") {
		t.Errorf("expected '3 packages passed', got: %s", out)
	}
}

func TestGoTest_PlainText_WithFailure(t *testing.T) {
	input := `--- FAIL: TestBroken (0.00s)
    foo_test.go:15: expected true, got false
FAIL	pkg/a	0.123s
ok  	pkg/b	0.456s`

	c := New()
	out := c.Compress("run_shell", "go test ./...", input, 2000, "")
	if !strings.Contains(out, "FAIL:") {
		t.Errorf("expected 'FAIL:', got: %s", out)
	}
	if !strings.Contains(out, "TestBroken") {
		t.Errorf("expected 'TestBroken', got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// CompileErrorStrategy
// ---------------------------------------------------------------------------

func TestCompileError_GoErrors(t *testing.T) {
	input := `./internal/foo/bar.go:15:3: undefined: SomeFunc
./internal/foo/bar.go:23:7: cannot use x (type int) as type string
./internal/baz/qux.go:8:2: imported and not used: "fmt"
./internal/baz/qux.go:12:5: undefined: AnotherFunc`

	c := New()
	out := c.Compress("run_shell", "go build ./...", input, 2000, "")
	if !strings.Contains(out, "compile errors") {
		t.Errorf("expected 'compile errors' header, got: %s", out)
	}
	if !strings.Contains(out, "4 in 2 files") {
		t.Errorf("expected '4 in 2 files', got: %s", out)
	}
	if !strings.Contains(out, "bar.go (2 errors)") {
		t.Errorf("expected grouped errors, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// ProgressBarStrategy
// ---------------------------------------------------------------------------

func TestProgressBar_StripAnsi(t *testing.T) {
	input := "\x1b[32mPASS\x1b[0m test_something\n\x1b[31mFAIL\x1b[0m test_other\n"
	c := New()
	out := c.Compress("run_shell", "npm test", input, 2000, "")
	if strings.Contains(out, "\x1b[") {
		t.Error("ANSI sequences should be stripped")
	}
	if !strings.Contains(out, "PASS") || !strings.Contains(out, "FAIL") {
		t.Errorf("content should be preserved: %s", out)
	}
}

func TestProgressBar_StripProgressLines(t *testing.T) {
	lines := []string{
		"\x1b[32mStarting...\x1b[0m",
		"50% done",
		"[========>          ] 40%",
		"Actual useful output line 1",
		"Actual useful output line 2",
	}
	input := strings.Join(lines, "\n")
	c := New()
	out := c.Compress("run_shell", "some-cmd", input, 2000, "")
	if strings.Contains(out, "50% done") {
		t.Error("progress line should be stripped")
	}
	if !strings.Contains(out, "Actual useful output line 1") {
		t.Error("useful content should be preserved")
	}
}

// ---------------------------------------------------------------------------
// DuplicateLineStrategy
// ---------------------------------------------------------------------------

func TestDuplicateLine_Collapse(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "[INFO] Processing request...")
	}
	lines = append(lines, "[ERROR] Connection failed")
	input := strings.Join(lines, "\n")

	c := New()
	out := c.Compress("run_shell", "my-server", input, 2000, "")
	if !strings.Contains(out, "×100") {
		t.Errorf("expected duplicate count, got: %s", out)
	}
	if !strings.Contains(out, "[ERROR] Connection failed") {
		t.Error("unique lines should be preserved")
	}
	if len(out) > 200 {
		t.Errorf("expected compact output, got len=%d", len(out))
	}
}

// ---------------------------------------------------------------------------
// Strategy matching
// ---------------------------------------------------------------------------

func TestStrategy_NoMatchForReadFile(t *testing.T) {
	// Most strategies only apply to run_shell.
	s := &GoTestStrategy{}
	if s.Match("read_file", "", "anything") {
		t.Error("GoTestStrategy should not match read_file")
	}
	s2 := &CompileErrorStrategy{}
	if s2.Match("read_file", "", "anything") {
		t.Error("CompileErrorStrategy should not match read_file")
	}
}

func TestStrategy_GoTestMatchesVariants(t *testing.T) {
	s := &GoTestStrategy{}
	cases := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"go test -v -count=1 ./internal/...", true},
		{"cd foo && go test", true},
		{"go build ./...", false},
		{"npm test", false},
	}
	for _, tc := range cases {
		got := s.Match("run_shell", tc.cmd, "")
		if got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// LintOutputStrategy
// ---------------------------------------------------------------------------

func TestLint_GolangciLint(t *testing.T) {
	input := `internal/foo/bar.go:15:3: exported function Foo should have comment or be unexported (golint)
internal/foo/bar.go:23:7: exported function Bar should have comment or be unexported (golint)
internal/foo/bar.go:30:1: line is 125 characters (lll)
internal/baz/qux.go:8:2: SA1029: should not use built-in type string as key for value; define your own type (staticcheck)
internal/baz/qux.go:12:5: exported function Baz should have comment or be unexported (golint)`

	c := New()
	out := c.Compress("run_shell", "golangci-lint run ./...", input, 2000, "")
	if !strings.Contains(out, "lint: 5 issues") {
		t.Errorf("expected 'lint: 5 issues', got: %s", out)
	}
	if !strings.Contains(out, "golint") {
		t.Errorf("expected 'golint' rule group, got: %s", out)
	}
	if !strings.Contains(out, "3 rules") {
		t.Errorf("expected '3 rules', got: %s", out)
	}
}

func TestLint_Eslint(t *testing.T) {
	input := `/src/a.ts:5:1  error  no-unused-vars  'x' is defined but never used
/src/a.ts:12:1  error  no-unused-vars  'y' is defined but never used
/src/b.ts:3:1  error  semi  Missing semicolon
/src/b.ts:8:1  error  semi  Missing semicolon
/src/c.ts:1:1  error  no-unused-vars  'z' is defined but never used`

	c := New()
	out := c.Compress("run_shell", "eslint .", input, 2000, "")
	if !strings.Contains(out, "lint: 5 issues") {
		t.Errorf("expected 'lint: 5 issues', got: %s", out)
	}
	if !strings.Contains(out, "no-unused-vars") {
		t.Errorf("expected 'no-unused-vars' rule, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// GitDiffStrategy
// ---------------------------------------------------------------------------

func TestGitDiff_Basic(t *testing.T) {
	input := `diff --git a/foo.go b/foo.go
index abc123..def456 100644
--- a/foo.go
+++ b/foo.go
@@ -10,7 +10,9 @@ func main() {
     unchanged
+    newLine1()
+    newLine2()
     unchanged
diff --git a/bar.go b/bar.go
index 111222..333444 100644
--- a/bar.go
+++ b/bar.go
@@ -5,10 +5,8 @@ func handler() {
-    oldLine1()
-    oldLine2()
-    oldLine3()
+    replacement()
@@ -30,3 +28,5 @@ func init() {
+    added1()
+    added2()`

	c := New()
	out := c.Compress("run_shell", "git diff", input, 2000, "")
	if !strings.Contains(out, "git diff: 2 files changed") {
		t.Errorf("expected file count, got: %s", out)
	}
	if !strings.Contains(out, "foo.go: +2 -0") {
		t.Errorf("expected foo.go stats, got: %s", out)
	}
	if !strings.Contains(out, "bar.go:") {
		t.Errorf("expected bar.go stats, got: %s", out)
	}
	if !strings.Contains(out, "func main") {
		t.Errorf("expected hunk context, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// GitStatusStrategy
// ---------------------------------------------------------------------------

func TestGitStatus_ShortFormat(t *testing.T) {
	input := ` M internal/foo.go
 M internal/bar.go
?? new_file.txt
?? another_new.txt
A  staged_file.go`

	c := New()
	out := c.Compress("run_shell", "git status --short", input, 2000, "")
	if !strings.Contains(out, "git status:") {
		t.Errorf("expected 'git status:', got: %s", out)
	}
	if !strings.Contains(out, "5 files") {
		t.Errorf("expected '5 files', got: %s", out)
	}
	if !strings.Contains(out, "untracked") {
		t.Errorf("expected 'untracked' category, got: %s", out)
	}
}

func TestGitStatus_Clean(t *testing.T) {
	input := `On branch main
nothing to commit, working tree clean`

	c := New()
	out := c.Compress("run_shell", "git status", input, 2000, "")
	if out != "git status: clean" {
		t.Errorf("expected 'git status: clean', got: %s", out)
	}
}

func TestGitStatus_VerboseFormat(t *testing.T) {
	input := `On branch feature-x
Changes to be committed:
  new file:   api/handler.go

Changes not staged for commit:
  modified:   internal/core/kernel.go
  modified:   internal/llm/worker.go
  deleted:    old_file.go

Untracked files:
  tmp/debug.log`

	c := New()
	out := c.Compress("run_shell", "git status", input, 2000, "")
	if !strings.Contains(out, "feature-x") {
		t.Errorf("expected branch name, got: %s", out)
	}
	if !strings.Contains(out, "staged") {
		t.Errorf("expected staged category, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// GitLogStrategy
// ---------------------------------------------------------------------------

func TestGitLog_VerboseFormat(t *testing.T) {
	input := `commit abc1234567890abcdef1234567890abcdef123456
Author: Alice <alice@example.com>
Date:   Mon Apr 20 10:00:00 2026 +0800

    feat: add output compressor framework

commit def4567890abcdef1234567890abcdef12345678
Author: Bob <bob@example.com>
Date:   Sun Apr 19 15:00:00 2026 +0800

    fix: correct truncation logic

commit 789abcdef1234567890abcdef1234567890abcdef
Author: Alice <alice@example.com>
Date:   Sat Apr 18 09:00:00 2026 +0800

    refactor: extract strategy interface`

	c := New()
	out := c.Compress("run_shell", "git log", input, 2000, "")
	if !strings.Contains(out, "git log: 3 commits") {
		t.Errorf("expected '3 commits', got: %s", out)
	}
	if !strings.Contains(out, "abc1234") {
		t.Errorf("expected short hash, got: %s", out)
	}
	if !strings.Contains(out, "add output compressor") {
		t.Errorf("expected commit subject, got: %s", out)
	}
	// Should NOT contain author/date metadata.
	if strings.Contains(out, "Author:") || strings.Contains(out, "Date:") {
		t.Error("should strip metadata")
	}
}

// ---------------------------------------------------------------------------
// Strategy matching — Phase 2
// ---------------------------------------------------------------------------

func TestStrategy_LintMatchesVariants(t *testing.T) {
	s := &LintOutputStrategy{}
	cases := []struct {
		cmd  string
		want bool
	}{
		{"golangci-lint run ./...", true},
		{"eslint .", true},
		{"npx eslint --fix src/", true},
		{"ruff check .", true},
		{"go test ./...", false},
		{"git diff", false},
	}
	for _, tc := range cases {
		got := s.Match("run_shell", tc.cmd, "")
		if got != tc.want {
			t.Errorf("LintMatch(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestStrategy_GitMatchesVariants(t *testing.T) {
	cases := []struct {
		strategy Strategy
		cmd      string
		want     bool
	}{
		{&GitDiffStrategy{}, "git diff", true},
		{&GitDiffStrategy{}, "git diff --cached", true},
		{&GitDiffStrategy{}, "git status", false},
		{&GitStatusStrategy{}, "git status", true},
		{&GitStatusStrategy{}, "git status --short", true},
		{&GitStatusStrategy{}, "git diff", false},
		{&GitLogStrategy{}, "git log", true},
		{&GitLogStrategy{}, "git log --oneline -10", true},
		{&GitLogStrategy{}, "git diff", false},
	}
	for _, tc := range cases {
		got := tc.strategy.Match("run_shell", tc.cmd, "")
		if got != tc.want {
			t.Errorf("%s.Match(%q) = %v, want %v", tc.strategy.Name(), tc.cmd, got, tc.want)
		}
	}
}
