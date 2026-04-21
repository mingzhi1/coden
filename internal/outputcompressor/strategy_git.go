package outputcompressor

import (
	"fmt"
	"regexp"
	"strings"
)

// GitDiffStrategy compresses `git diff` output to a stats summary plus
// the most relevant hunks. Preserves file names and change statistics
// while removing verbose context lines.
type GitDiffStrategy struct{}

func (s *GitDiffStrategy) Name() string { return "git_diff" }

func (s *GitDiffStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	cmd := strings.TrimSpace(command)
	return strings.HasPrefix(cmd, "git diff") ||
		strings.Contains(cmd, " git diff")
}

// diffFileRe matches "diff --git a/file b/file" headers.
var diffFileRe = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)

// hunkHeaderRe matches "@@ -10,7 +10,9 @@ func name" hunk headers.
var hunkHeaderRe = regexp.MustCompile(`^@@\s+.+?\s+@@\s*(.*)$`)

func (s *GitDiffStrategy) Compress(output string, budget int) string {
	lines := strings.Split(output, "\n")

	type fileDiff struct {
		name      string
		added     int
		removed   int
		hunkCtxs  []string // function/context from @@ headers
	}

	var files []fileDiff
	var current *fileDiff

	for _, line := range lines {
		// New file header.
		if m := diffFileRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				files = append(files, *current)
			}
			current = &fileDiff{name: m[2]}
			continue
		}
		if current == nil {
			continue
		}
		// Hunk header — extract context.
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			ctx := strings.TrimSpace(m[1])
			if ctx != "" {
				current.hunkCtxs = append(current.hunkCtxs, ctx)
			}
			continue
		}
		// Count additions/deletions (skip --- and +++ headers).
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			current.added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			current.removed++
		}
	}
	if current != nil {
		files = append(files, *current)
	}

	if len(files) == 0 {
		return output
	}

	totalAdded, totalRemoved := 0, 0
	for _, f := range files {
		totalAdded += f.added
		totalRemoved += f.removed
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("git diff: %d files changed, +%d -%d\n", len(files), totalAdded, totalRemoved))

	for _, f := range files {
		sb.WriteString(fmt.Sprintf("  %s: +%d -%d", f.name, f.added, f.removed))
		if len(f.hunkCtxs) > 0 {
			// Show up to 3 hunk contexts.
			ctxs := f.hunkCtxs
			if len(ctxs) > 3 {
				ctxs = ctxs[:3]
			}
			sb.WriteString(fmt.Sprintf(" (%s)", strings.Join(ctxs, ", ")))
		}
		sb.WriteString("\n")
	}

	result := sb.String()
	if len(result) > budget {
		return truncate(result, budget)
	}
	return result
}

// ---------------------------------------------------------------------------

// GitStatusStrategy compresses `git status` output to a compact summary
// showing file counts by status category.
type GitStatusStrategy struct{}

func (s *GitStatusStrategy) Name() string { return "git_status" }

func (s *GitStatusStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	cmd := strings.TrimSpace(command)
	return strings.HasPrefix(cmd, "git status") ||
		strings.Contains(cmd, " git status")
}

func (s *GitStatusStrategy) Compress(output string, budget int) string {
	lines := strings.Split(output, "\n")

	var staged, modified, untracked, deleted []string
	branch := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Branch info.
		if strings.HasPrefix(trimmed, "On branch ") {
			branch = strings.TrimPrefix(trimmed, "On branch ")
			continue
		}

		// Short format: "XY filename" — use raw line (not trimmed) to preserve XY columns.
		if len(line) >= 4 && line[2] == ' ' {
			x, y := line[0], line[1]
			file := strings.TrimSpace(line[3:])
			if file == "" {
				continue
			}
			switch {
			case x == '?' && y == '?':
				untracked = append(untracked, file)
			case x == 'D' || y == 'D':
				deleted = append(deleted, file)
			case x == 'M' || x == 'A' || x == 'R':
				staged = append(staged, file)
			case y == 'M':
				modified = append(modified, file)
			default:
				if x != ' ' || y != ' ' {
					modified = append(modified, file)
				}
			}
			continue
		}

		// Verbose format parsing.
		if strings.HasPrefix(trimmed, "modified:") {
			file := strings.TrimSpace(strings.TrimPrefix(trimmed, "modified:"))
			modified = append(modified, file)
		} else if strings.HasPrefix(trimmed, "new file:") {
			file := strings.TrimSpace(strings.TrimPrefix(trimmed, "new file:"))
			staged = append(staged, file)
		} else if strings.HasPrefix(trimmed, "deleted:") {
			file := strings.TrimSpace(strings.TrimPrefix(trimmed, "deleted:"))
			deleted = append(deleted, file)
		}
	}

	total := len(staged) + len(modified) + len(untracked) + len(deleted)
	if total == 0 {
		// Clean or can't parse.
		if strings.Contains(output, "nothing to commit") {
			return "git status: clean"
		}
		return output
	}

	var sb strings.Builder
	if branch != "" {
		sb.WriteString(fmt.Sprintf("git status [%s]: %d files\n", branch, total))
	} else {
		sb.WriteString(fmt.Sprintf("git status: %d files\n", total))
	}

	writeFileList := func(label string, files []string) {
		if len(files) == 0 {
			return
		}
		if len(files) <= 8 {
			sb.WriteString(fmt.Sprintf("  %s (%d): %s\n", label, len(files), strings.Join(files, ", ")))
		} else {
			shown := strings.Join(files[:8], ", ")
			sb.WriteString(fmt.Sprintf("  %s (%d): %s, ... +%d more\n", label, len(files), shown, len(files)-8))
		}
	}

	writeFileList("staged", staged)
	writeFileList("modified", modified)
	writeFileList("untracked", untracked)
	writeFileList("deleted", deleted)

	result := sb.String()
	if len(result) > budget {
		return truncate(result, budget)
	}
	return result
}

// ---------------------------------------------------------------------------

// GitLogStrategy compresses `git log` output to compact one-line-per-commit
// summaries. Strips verbose commit metadata, diffs, and decoration.
type GitLogStrategy struct{}

func (s *GitLogStrategy) Name() string { return "git_log" }

func (s *GitLogStrategy) Match(kind, command, output string) bool {
	if kind != "run_shell" {
		return false
	}
	cmd := strings.TrimSpace(command)
	return strings.HasPrefix(cmd, "git log") ||
		strings.Contains(cmd, " git log")
}

// commitRe matches "commit <hash>" lines.
var commitRe = regexp.MustCompile(`^commit ([0-9a-f]{7,40})`)

func (s *GitLogStrategy) Compress(output string, budget int) string {
	lines := strings.Split(output, "\n")

	type commit struct {
		hash    string
		subject string
	}

	var commits []commit
	var current *commit

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if m := commitRe.FindStringSubmatch(line); m != nil {
			if current != nil && current.subject != "" {
				commits = append(commits, *current)
			}
			current = &commit{hash: m[1][:7]} // short hash
			continue
		}
		if current == nil {
			continue
		}
		// Skip metadata lines.
		if strings.HasPrefix(trimmed, "Author:") || strings.HasPrefix(trimmed, "Date:") ||
			strings.HasPrefix(trimmed, "Merge:") {
			continue
		}
		// First non-empty content line is the subject.
		if trimmed != "" && current.subject == "" {
			current.subject = trimmed
		}
	}
	if current != nil && current.subject != "" {
		commits = append(commits, *current)
	}

	if len(commits) == 0 {
		// Might already be oneline format or unparseable.
		return output
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("git log: %d commits\n", len(commits)))
	for _, c := range commits {
		sb.WriteString(fmt.Sprintf("  %s %s\n", c.hash, c.subject))
	}

	result := sb.String()
	if len(result) > budget {
		return truncate(result, budget)
	}
	return result
}
