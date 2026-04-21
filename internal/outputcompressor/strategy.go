package outputcompressor

// Strategy is a single output compression strategy.
type Strategy interface {
	// Name returns the strategy name (for logging/debugging).
	Name() string

	// Match checks whether this strategy applies to the given output.
	// kind is the tool type ("run_shell", "read_file", etc.);
	// command is the shell command string (empty for non-shell tools);
	// output is the raw output content.
	Match(kind, command, output string) bool

	// Compress performs the compression, returning a compressed string.
	// budget is the target maximum character count.
	Compress(output string, budget int) string
}

// DefaultStrategies returns the default strategy chain, ordered by priority.
// Higher-priority (more specific) strategies come first.
func DefaultStrategies() []Strategy {
	return []Strategy{
		// High-value, command-specific strategies.
		&GoTestStrategy{},       // go test NDJSON output → failure focus
		&CompileErrorStrategy{}, // go build / compile errors → group by file
		&LintOutputStrategy{},   // golangci-lint, eslint → group by rule
		&GitDiffStrategy{},      // git diff → stats + file summary
		&GitStatusStrategy{},    // git status → compact file list
		&GitLogStrategy{},       // git log → one-line-per-commit

		// Universal strategies (lower priority).
		&ProgressBarStrategy{},   // strip ANSI escape sequences and progress bars
		&DuplicateLineStrategy{}, // collapse repeated lines
	}
}
