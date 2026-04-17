package gitstate

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Service queries the git repository state for a workspace root.
// All operations are timeout-guarded to avoid blocking the workflow.
type Service struct {
	root string
}

// New creates a git state service rooted at the given workspace directory.
func New(root string) *Service {
	return &Service{root: root}
}

// Snapshot captures the current git state. Returns a zero State with
// IsRepo=false when the workspace is not inside a git repo.
// This function is safe to call even when git is not installed.
func (s *Service) Snapshot() State {
	if !s.isGitRepo() {
		return State{}
	}
	st := State{IsRepo: true}
	st.Branch = s.runGit("rev-parse", "--abbrev-ref", "HEAD")
	st.StatusSummary = truncate(s.runGit("status", "--porcelain"), 2048)
	st.DiffStat = truncate(s.runGit("diff", "--stat"), 2048)
	st.RecentCommits = s.runGit("log", "--oneline", "-5")
	return st
}

// isGitRepo returns true when `git rev-parse --git-dir` succeeds.
func (s *Service) isGitRepo() bool {
	return s.runGit("rev-parse", "--git-dir") != ""
}

// runGit executes a git command with a 5s timeout, returning trimmed
// stdout. Returns "" on any error (git not installed, not a repo, etc).
func (s *Service) runGit(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = s.root
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n... (truncated, %d bytes total)", len(s))
}

// FormatForPrompt renders the git state as a markdown section suitable
// for injection into an LLM system prompt. Returns "" if not a git repo.
func (st State) FormatForPrompt() string {
	if !st.IsRepo {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Git status\n\n")
	if st.Branch != "" {
		sb.WriteString("Branch: `")
		sb.WriteString(st.Branch)
		sb.WriteString("`\n\n")
	}
	if st.StatusSummary != "" {
		sb.WriteString("### Uncommitted changes\n```\n")
		sb.WriteString(st.StatusSummary)
		sb.WriteString("\n```\n\n")
	} else {
		sb.WriteString("Working tree clean.\n\n")
	}
	if st.DiffStat != "" {
		sb.WriteString("### Diff stat\n```\n")
		sb.WriteString(st.DiffStat)
		sb.WriteString("\n```\n\n")
	}
	if st.RecentCommits != "" {
		sb.WriteString("### Recent commits\n```\n")
		sb.WriteString(st.RecentCommits)
		sb.WriteString("\n```\n")
	}
	return sb.String()
}
