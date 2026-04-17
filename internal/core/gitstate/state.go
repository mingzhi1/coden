package gitstate

// State holds the summarised git state for a workspace, suitable for
// injection into LLM prompts as ambient context.
type State struct {
	// IsRepo is true when the workspace root is inside a git repository.
	IsRepo bool
	// Branch is the current branch name (empty when detached HEAD).
	Branch string
	// StatusSummary is the output of `git status --porcelain` (capped).
	StatusSummary string
	// DiffStat is the output of `git diff --stat` (capped).
	DiffStat string
	// RecentCommits is the output of `git log --oneline -5`.
	RecentCommits string
}
