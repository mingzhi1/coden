package workflow

import "context"

// ProfileInput is the cheap, already-available context handed to the Profiler to
// produce a one-time project overview + style summary. No agentic reading — it is
// a single shot over manifests, the README head, and the file tree.
type ProfileInput struct {
	Languages []string // detected from the tool inventory
	GoMod     string   // primary manifest content (go.mod / package.json / …)
	Readme    string   // README head (truncated by the caller)
	FileTree  []string // workspace-relative paths (capped by the caller)
}

// ProfileResult is the semantic half of a ProjectProfile: a prose overview of
// what the project is and a short description of its code style/conventions.
type ProfileResult struct {
	Overview string
	Style    string
}

// Profiler produces the semantic project-profile fields in a single LLM call.
// It is invoked at most once per profile (cached until manifests change), so it
// can afford a richer model without per-run cost.
type Profiler interface {
	Profile(ctx context.Context, in ProfileInput) (ProfileResult, error)
}
