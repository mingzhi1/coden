package skill

import "time"

// RegisterBuiltins adds default skills that ship with the binary.
func RegisterBuiltins(r *Registry) {
	r.Register(&Skill{
		Frontmatter: Frontmatter{
			Name:        "coden-defaults",
			Description: "Default CodeN behavior rules",
			WhenToUse:   "Always active",
		},
		Content: `When generating or modifying code:

1. **Preserve existing style** — match indentation, naming, and patterns already in the file.
2. **Handle errors explicitly** — never ignore error returns in Go; always check and propagate.
3. **Prefer simplicity** — choose the simplest correct solution over clever abstractions.
4. **Write tests** — when creating new functions, include corresponding test cases.
5. **Document public API** — exported Go functions/types must have doc comments.
`,
		LoadedFrom: SourceBuiltin,
		LoadedAt:   time.Now(),
	})
}
