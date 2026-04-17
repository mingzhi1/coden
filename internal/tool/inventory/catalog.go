// Package inventory provides tool auto-discovery, project language detection,
// and config generation for the CodeN workspace.
package inventory

// ToolCandidate describes a tool that may exist on the system.
// The builtin catalog contains well-known candidates; the discovery
// system probes only those relevant to the detected project languages.
type ToolCandidate struct {
	Name           string   // unique key, e.g. "gopls", "pylsp"
	Category       Category // "lsp", "formatter", etc.
	Languages      []string // languages this tool serves
	Command        string   // primary executable name
	Args           []string // default args (e.g. LSP startup args)
	VersionCmd     []string // command to check version, e.g. ["gopls","version"]
	VersionPattern string   // regex to extract version from output; empty = use raw
	InstallHint    string   // human-readable install instruction
	Priority       int      // higher = preferred when multiple candidates for same role
	Indicators     []string // project files that signal relevance, e.g. ["go.mod"]
}

// builtinCatalog is the compiled-in knowledge base of well-known tools.
// Discovery only probes tools whose Indicators match files found in the workspace.
var builtinCatalog = []ToolCandidate{
	// ── LSP Servers ─────────────────────────────────────────────
	{
		Name: "gopls", Category: CatLSP,
		Languages: []string{"go"},
		Command:   "gopls", Args: []string{"serve"},
		VersionCmd:  []string{"gopls", "version"},
		InstallHint: "go install golang.org/x/tools/gopls@latest",
		Priority:    100, Indicators: []string{"go.mod", "go.sum"},
	},
	{
		Name: "pylsp", Category: CatLSP,
		Languages:   []string{"python"},
		Command:     "pylsp",
		VersionCmd:  []string{"pylsp", "--version"},
		InstallHint: "pip install python-lsp-server",
		Priority:    100, Indicators: []string{"pyproject.toml", "setup.py", "requirements.txt", "Pipfile"},
	},
	{
		Name: "typescript-language-server", Category: CatLSP,
		Languages: []string{"typescript", "javascript"},
		Command:   "typescript-language-server", Args: []string{"--stdio"},
		VersionCmd:  []string{"typescript-language-server", "--version"},
		InstallHint: "npm install -g typescript-language-server typescript",
		Priority:    100, Indicators: []string{"package.json", "tsconfig.json"},
	},
	{
		Name: "rust-analyzer", Category: CatLSP,
		Languages:   []string{"rust"},
		Command:     "rust-analyzer",
		VersionCmd:  []string{"rust-analyzer", "--version"},
		InstallHint: "rustup component add rust-analyzer",
		Priority:    100, Indicators: []string{"Cargo.toml"},
	},
	{
		Name: "clangd", Category: CatLSP,
		Languages:   []string{"c", "cpp"},
		Command:     "clangd",
		VersionCmd:  []string{"clangd", "--version"},
		InstallHint: "apt install clangd / brew install llvm",
		Priority:    100, Indicators: []string{"CMakeLists.txt", "Makefile", "compile_commands.json"},
	},
	{
		Name: "jdtls", Category: CatLSP,
		Languages:   []string{"java"},
		Command:     "jdtls",
		VersionCmd:  []string{"jdtls", "--version"},
		InstallHint: "See https://github.com/eclipse-jdtls/eclipse.jdt.ls",
		Priority:    100, Indicators: []string{"pom.xml", "build.gradle", "build.gradle.kts"},
	},

	// ── Package Managers ────────────────────────────────────────
	{
		Name: "go-mod", Category: CatPackageManager,
		Languages:  []string{"go"},
		Command:    "go",
		VersionCmd: []string{"go", "version"},
		Priority:   100, Indicators: []string{"go.mod"},
	},
	{
		Name: "npm", Category: CatPackageManager,
		Languages:  []string{"javascript", "typescript"},
		Command:    "npm",
		VersionCmd: []string{"npm", "--version"},
		Priority:   80, Indicators: []string{"package.json", "package-lock.json"},
	},
	{
		Name: "yarn", Category: CatPackageManager,
		Languages:  []string{"javascript", "typescript"},
		Command:    "yarn",
		VersionCmd: []string{"yarn", "--version"},
		Priority:   70, Indicators: []string{"yarn.lock"},
	},
	{
		Name: "pnpm", Category: CatPackageManager,
		Languages:  []string{"javascript", "typescript"},
		Command:    "pnpm",
		VersionCmd: []string{"pnpm", "--version"},
		Priority:   75, Indicators: []string{"pnpm-lock.yaml"},
	},
	{
		Name: "pip", Category: CatPackageManager,
		Languages:  []string{"python"},
		Command:    "pip",
		VersionCmd: []string{"pip", "--version"},
		Priority:   70, Indicators: []string{"requirements.txt"},
	},
	{
		Name: "uv", Category: CatPackageManager,
		Languages:   []string{"python"},
		Command:     "uv",
		VersionCmd:  []string{"uv", "version"},
		InstallHint: "pip install uv / brew install uv",
		Priority:    90, Indicators: []string{"pyproject.toml", "uv.lock"},
	},
	{
		Name: "cargo", Category: CatPackageManager,
		Languages:  []string{"rust"},
		Command:    "cargo",
		VersionCmd: []string{"cargo", "--version"},
		Priority:   100, Indicators: []string{"Cargo.toml"},
	},
	{
		Name: "maven", Category: CatPackageManager,
		Languages:  []string{"java"},
		Command:    "mvn",
		VersionCmd: []string{"mvn", "--version"},
		Priority:   80, Indicators: []string{"pom.xml"},
	},
	{
		Name: "gradle", Category: CatPackageManager,
		Languages:  []string{"java"},
		Command:    "gradle",
		VersionCmd: []string{"gradle", "--version"},
		Priority:   80, Indicators: []string{"build.gradle", "build.gradle.kts"},
	},

	// ── Interpreters / Compilers ────────────────────────────────
	{
		Name: "go", Category: CatInterpreter,
		Languages:  []string{"go"},
		Command:    "go",
		VersionCmd: []string{"go", "version"},
		Priority:   100, Indicators: []string{"go.mod", "go.sum"},
	},
	{
		Name: "python3", Category: CatInterpreter,
		Languages:  []string{"python"},
		Command:    "python3",
		VersionCmd: []string{"python3", "--version"},
		Priority:   100, Indicators: []string{"pyproject.toml", "setup.py", "requirements.txt", "*.py"},
	},
	{
		Name: "node", Category: CatInterpreter,
		Languages:  []string{"javascript", "typescript"},
		Command:    "node",
		VersionCmd: []string{"node", "--version"},
		Priority:   100, Indicators: []string{"package.json", "tsconfig.json"},
	},
	{
		Name: "rustc", Category: CatInterpreter,
		Languages:  []string{"rust"},
		Command:    "rustc",
		VersionCmd: []string{"rustc", "--version"},
		Priority:   100, Indicators: []string{"Cargo.toml"},
	},
	{
		Name: "java", Category: CatInterpreter,
		Languages:  []string{"java"},
		Command:    "java",
		VersionCmd: []string{"java", "-version"},
		Priority:   100, Indicators: []string{"pom.xml", "build.gradle"},
	},
	{
		Name: "ruby", Category: CatInterpreter,
		Languages:  []string{"ruby"},
		Command:    "ruby",
		VersionCmd: []string{"ruby", "--version"},
		Priority:   100, Indicators: []string{"Gemfile", "*.rb"},
	},

	// ── Formatters ──────────────────────────────────────────────
	{
		Name: "gofmt", Category: CatFormatter,
		Languages: []string{"go"},
		Command:   "gofmt", Args: []string{"-w"},
		VersionCmd: []string{"go", "version"}, // gofmt ships with go
		Priority:   90, Indicators: []string{"go.mod"},
	},
	{
		Name: "goimports", Category: CatFormatter,
		Languages: []string{"go"},
		Command:   "goimports", Args: []string{"-w"},
		VersionCmd:  []string{"goimports", "-e", "/dev/null"}, // no --version flag
		InstallHint: "go install golang.org/x/tools/cmd/goimports@latest",
		Priority:    100, Indicators: []string{"go.mod"},
	},
	{
		Name: "black", Category: CatFormatter,
		Languages:   []string{"python"},
		Command:     "black",
		VersionCmd:  []string{"black", "--version"},
		InstallHint: "pip install black",
		Priority:    100, Indicators: []string{"pyproject.toml", "setup.py", "requirements.txt"},
	},
	{
		Name: "prettier", Category: CatFormatter,
		Languages: []string{"javascript", "typescript", "css", "html", "json", "yaml", "markdown"},
		Command:   "prettier", Args: []string{"--write"},
		VersionCmd:  []string{"prettier", "--version"},
		InstallHint: "npm install -g prettier",
		Priority:    100, Indicators: []string{"package.json", ".prettierrc"},
	},
	{
		Name: "rustfmt", Category: CatFormatter,
		Languages:   []string{"rust"},
		Command:     "rustfmt",
		VersionCmd:  []string{"rustfmt", "--version"},
		InstallHint: "rustup component add rustfmt",
		Priority:    100, Indicators: []string{"Cargo.toml"},
	},

	// ── Linters ─────────────────────────────────────────────────
	{
		Name: "golangci-lint", Category: CatLinter,
		Languages: []string{"go"},
		Command:   "golangci-lint", Args: []string{"run"},
		VersionCmd:  []string{"golangci-lint", "--version"},
		InstallHint: "go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest",
		Priority:    100, Indicators: []string{"go.mod", ".golangci.yml"},
	},
	{
		Name: "ruff", Category: CatLinter,
		Languages: []string{"python"},
		Command:   "ruff", Args: []string{"check"},
		VersionCmd:  []string{"ruff", "--version"},
		InstallHint: "pip install ruff",
		Priority:    100, Indicators: []string{"pyproject.toml", "ruff.toml"},
	},
	{
		Name: "eslint", Category: CatLinter,
		Languages:   []string{"javascript", "typescript"},
		Command:     "eslint",
		VersionCmd:  []string{"eslint", "--version"},
		InstallHint: "npm install -g eslint",
		Priority:    100, Indicators: []string{"package.json", ".eslintrc", ".eslintrc.js", ".eslintrc.json"},
	},
	{
		Name: "clippy", Category: CatLinter,
		Languages: []string{"rust"},
		Command:   "cargo", Args: []string{"clippy"},
		VersionCmd:  []string{"cargo", "clippy", "--version"},
		InstallHint: "rustup component add clippy",
		Priority:    100, Indicators: []string{"Cargo.toml"},
	},

	// ── Search Tools ────────────────────────────────────────────
	{
		Name: "ripgrep", Category: CatSearch,
		Languages:   nil, // language-agnostic
		Command:     "rg",
		VersionCmd:  []string{"rg", "--version"},
		InstallHint: "cargo install ripgrep / brew install ripgrep / apt install ripgrep",
		Priority:    100, Indicators: nil, // always probe
	},
	{
		Name: "fd", Category: CatSearch,
		Languages:   nil,
		Command:     "fd",
		VersionCmd:  []string{"fd", "--version"},
		InstallHint: "cargo install fd-find / brew install fd / apt install fd-find",
		Priority:    80, Indicators: nil,
	},
	{
		Name: "fzf", Category: CatSearch,
		Languages:   nil,
		Command:     "fzf",
		VersionCmd:  []string{"fzf", "--version"},
		InstallHint: "brew install fzf / apt install fzf",
		Priority:    80, Indicators: nil,
	},
}

// BuiltinCatalog returns a copy of the built-in tool candidate catalog.
func BuiltinCatalog() []ToolCandidate {
	out := make([]ToolCandidate, len(builtinCatalog))
	copy(out, builtinCatalog)
	return out
}

// FilterByLanguages returns candidates whose Indicators are nil (always probe)
// or whose Languages overlap with the given set.
func FilterByLanguages(candidates []ToolCandidate, langs []string) []ToolCandidate {
	if len(langs) == 0 {
		return candidates // no filter = probe everything
	}
	langSet := make(map[string]bool, len(langs))
	for _, l := range langs {
		langSet[l] = true
	}

	var filtered []ToolCandidate
	for _, c := range candidates {
		if len(c.Languages) == 0 {
			// Language-agnostic tools (search tools) are always included
			filtered = append(filtered, c)
			continue
		}
		for _, lang := range c.Languages {
			if langSet[lang] {
				filtered = append(filtered, c)
				break
			}
		}
	}
	return filtered
}
