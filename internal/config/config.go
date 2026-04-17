package config

import "time"

// Config is the unified root configuration structure.
// It subsumes the legacy ToolsConfig under the "tools:" key and adds
// core, llm, storage, ui, and logging sections.
type Config struct {
	Core    CoreConfig    `yaml:"core"`
	LLM     LLMConfig     `yaml:"llm"`
	Tools   ToolsConfig   `yaml:"tools"`
	Storage StorageConfig `yaml:"storage"`
	UI      UIConfig      `yaml:"ui"`
	Logging LoggingConfig `yaml:"logging"`
}

// CoreConfig holds workflow, context, and security settings.
type CoreConfig struct {
	Workflow WorkflowConfig `yaml:"workflow"`
	Context  ContextConfig  `yaml:"context"`
	Security SecurityConfig `yaml:"security"`
}

// WorkflowConfig controls workflow execution behaviour.
type WorkflowConfig struct {
	MaxRetries    int           `yaml:"max_retries"`
	Timeout       time.Duration `yaml:"timeout"`
	ParallelTasks bool          `yaml:"parallel_tasks"`
	FailurePolicy string        `yaml:"failure_policy"` // "stop" or "skip"
}

// ContextConfig controls context window management.
type ContextConfig struct {
	MaxHistoryTurns      int `yaml:"max_history_turns"`
	FileTreeLimit        int `yaml:"file_tree_limit"`
	AutoCompactThreshold int `yaml:"auto_compact_threshold"`
}

// SecurityConfig controls shell and command security.
type SecurityConfig struct {
	AllowShell               bool     `yaml:"allow_shell"`
	DangerousCommands        []string `yaml:"dangerous_commands"`
	CommandSubstitutionCheck bool     `yaml:"command_substitution_check"`
}

// LLMConfig holds provider definitions, pool tiers, and token budget settings.
type LLMConfig struct {
	Server      ServerConfig             `yaml:"server"`
	Providers   map[string]ProviderEntry `yaml:"providers"`
	Pool        PoolConfig               `yaml:"pool"`
	Routing     map[string][]string      `yaml:"routing"`
	TokenBudget TokenBudgetConfig        `yaml:"token_budget"`
}

// ServerConfig controls the standalone llm-server sidecar.
type ServerConfig struct {
	Enabled bool   `yaml:"enabled"`       // CodeN auto-launches llm-server if true
	Addr    string `yaml:"addr"`          // listen address (default ":7533")
}

// ProviderEntry represents a single LLM provider (HTTP API or ACP subprocess).
// The Type field discriminates between the two transports.
type ProviderEntry struct {
	Type         string            `yaml:"type"`          // "http" (default) | "acp"
	APIKey       string            `yaml:"api_key"`       // HTTP only
	BaseURL      string            `yaml:"base_url"`      // HTTP only
	DefaultModel string            `yaml:"default_model"` // HTTP: model name; ACP: ignored
	Command      string            `yaml:"command"`       // ACP only: executable command
	Args         []string          `yaml:"args"`          // ACP only: CLI arguments
	Env          map[string]string `yaml:"env"`           // ACP only: environment variables
}

// EffectiveType returns the provider type, defaulting to "http".
func (p ProviderEntry) EffectiveType() string {
	if p.Type == "" {
		return "http"
	}
	return p.Type
}

// PoolConfig defines which providers to use for each tier, in priority order.
// Names reference keys in the Providers map.
type PoolConfig struct {
	Primary []string `yaml:"primary"` // Strong tier: Planner, Critic, Acceptor
	Light   []string `yaml:"light"`   // Light tier: Intent, Coder, Secretary
}

// TokenBudgetConfig defines context window partitioning.
type TokenBudgetConfig struct {
	MaxInputTokens int     `yaml:"max_input_tokens"`
	FileTreeRatio  float64 `yaml:"file_tree_ratio"`
	HistoryRatio   float64 `yaml:"history_ratio"`
	RetryRatio     float64 `yaml:"retry_ratio"`
}

// StorageConfig controls persistence paths and tuning.
type StorageConfig struct {
	BaseDir     string            `yaml:"base_dir"`
	SQLite      SQLiteStoreConfig `yaml:"sqlite"`
	ObjectStore ObjectStoreConfig `yaml:"object_store"`
}

// SQLiteStoreConfig tunes SQLite parameters.
type SQLiteStoreConfig struct {
	BusyTimeout int    `yaml:"busy_timeout"`
	JournalMode string `yaml:"journal_mode"`
}

// ObjectStoreConfig tunes the object store.
type ObjectStoreConfig struct {
	Compression   bool  `yaml:"compression"`
	MaxObjectSize int64 `yaml:"max_object_size"`
}

// UIConfig controls TUI and plain output settings.
type UIConfig struct {
	TUI   TUIUIConfig   `yaml:"tui"`
	Plain PlainUIConfig `yaml:"plain"`
}

// TUIUIConfig controls TUI appearance.
type TUIUIConfig struct {
	Theme          string `yaml:"theme"` // auto, light, dark
	Mouse          bool   `yaml:"mouse"`
	ShowTokenUsage bool   `yaml:"show_token_usage"`
}

// PlainUIConfig controls plain (non-TUI) output.
type PlainUIConfig struct {
	Color   bool `yaml:"color"`
	Verbose bool `yaml:"verbose"`
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	Level      string `yaml:"level"`  // debug, info, warn, error
	Format     string `yaml:"format"` // json, text
	Output     string `yaml:"output"`
	MaxSize    int    `yaml:"max_size"`    // MB
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"` // days
}
