package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func TestSnapshotNonRepo(t *testing.T) {
	t.Parallel()
	svc := New(t.TempDir())
	st := svc.Snapshot()
	if st.IsRepo {
		t.Error("expected IsRepo=false for non-git directory")
	}
	if st.FormatForPrompt() != "" {
		t.Error("expected empty prompt for non-repo")
	}
}

func TestSnapshotGitRepo(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	t.Parallel()

	// Create a temp git repo with one commit.
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s\n%s", args, err, out)
		}
	}

	run("init")
	if runtime.GOOS == "windows" {
		run("config", "core.autocrlf", "false")
	}
	run("config", "user.email", "test@test")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false")
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")

	svc := New(dir)
	st := svc.Snapshot()

	if !st.IsRepo {
		t.Fatal("expected IsRepo=true")
	}
	// Branch should be main or master.
	if st.Branch == "" {
		t.Error("expected non-empty branch")
	}
	if st.RecentCommits == "" {
		t.Error("expected recent commits")
	}
	if !strings.Contains(st.RecentCommits, "init") {
		t.Errorf("expected 'init' in commits, got %q", st.RecentCommits)
	}
	// Working tree should be clean after commit.
	if st.StatusSummary != "" {
		t.Errorf("expected clean status, got %q", st.StatusSummary)
	}

	// Add an uncommitted file.
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644)
	st2 := svc.Snapshot()
	if st2.StatusSummary == "" {
		t.Error("expected non-empty status after adding file")
	}
	if !strings.Contains(st2.StatusSummary, "new.go") {
		t.Errorf("expected new.go in status, got %q", st2.StatusSummary)
	}

	prompt := st2.FormatForPrompt()
	if !strings.Contains(prompt, "## Git status") {
		t.Error("expected Git status header in prompt")
	}
	if !strings.Contains(prompt, "new.go") {
		t.Error("expected new.go in prompt")
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	short := "abc"
	if got := truncate(short, 10); got != short {
		t.Errorf("expected no truncation, got %q", got)
	}
	long := strings.Repeat("x", 100)
	got := truncate(long, 50)
	if len(got) < 50 {
		t.Error("truncated too much")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("expected truncation notice")
	}
}

func TestFormatForPromptClean(t *testing.T) {
	t.Parallel()
	st := State{
		IsRepo:        true,
		Branch:        "main",
		RecentCommits: "abc1234 initial commit",
	}
	prompt := st.FormatForPrompt()
	if !strings.Contains(prompt, "Branch: `main`") {
		t.Error("expected branch in prompt")
	}
	if !strings.Contains(prompt, "Working tree clean") {
		t.Error("expected clean notice")
	}
}
