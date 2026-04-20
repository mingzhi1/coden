package config

import (
	"fmt"
	"time"
)

// DefaultConfig returns the built-in default unified configuration.
// This is Layer 0 of the 4-layer merge strategy.
func DefaultConfig() *Config {
	return &Config{
		Core: CoreConfig{
			Workflow: WorkflowConfig{
				MaxRetries:    3,
				Timeout:       300 * time.Second,
				ParallelTasks: false,
				FailurePolicy: "stop",
			},
			Context: ContextConfig{
				MaxHistoryTurns:      5,
				FileTreeLimit:        200,
				AutoCompactThreshold: 8000,
			},
			Security: SecurityConfig{
				AllowShell: false,
				DangerousCommands: []string{
					"rm -rf /", "> /dev", "mkfs", "dd if=",
				},
				CommandSubstitutionCheck: true,
			},
		},
		LLM: LLMConfig{
			Providers: map[string]ProviderEntry{
				"anthropic": {DefaultModel: "claude-3-5-sonnet"},
				"openai":    {DefaultModel: "gpt-4o"},
				"deepseek":  {DefaultModel: "deepseek-chat"},
			},
			Pool: PoolConfig{
				Primary: []string{"anthropic", "openai", "deepseek"},
				Light:   []string{"deepseek", "openai"},
			},
			TokenBudget: TokenBudgetConfig{
				MaxInputTokens: 128000,
				FileTreeRatio:  0.30,
				HistoryRatio:   0.60,
				RetryRatio:     0.10,
			},
		},
		Tools: *DefaultToolsConfig(),
		Storage: StorageConfig{
			BaseDir: "~/.coden",
			SQLite: SQLiteStoreConfig{
				BusyTimeout: 5000,
				JournalMode: "WAL",
			},
			ObjectStore: ObjectStoreConfig{
				Compression:   true,
				MaxObjectSize: 10 * 1024 * 1024,
			},
		},
		UI: UIConfig{
			TUI: TUIUIConfig{
				Theme:          "auto",
				Mouse:          true,
				ShowTokenUsage: true,
			},
			Plain: PlainUIConfig{
				Color:   true,
				Verbose: false,
			},
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "json",
			Output:     "~/.coden/logs/coden.log",
			MaxSize:    100,
			MaxBackups: 5,
			MaxAge:     30,
		},
	}
}

// Validate checks the entire Config tree for consistency.
func (c *Config) Validate() error {
	// Core
	if c.Core.Workflow.MaxRetries < 0 {
		return fmt.Errorf("core.workflow.max_retries must be >= 0")
	}
	if c.Core.Workflow.Timeout < 0 {
		return fmt.Errorf("core.workflow.timeout must be >= 0")
	}
	fp := c.Core.Workflow.FailurePolicy
	if fp != "" && fp != "stop" && fp != "skip" && fp != "replan" {
		return fmt.Errorf("core.workflow.failure_policy must be 'stop', 'skip', or 'replan'")
	}
	if c.Core.Context.MaxHistoryTurns < 0 {
		return fmt.Errorf("core.context.max_history_turns must be >= 0")
	}
	if c.Core.Context.FileTreeLimit < 0 {
		return fmt.Errorf("core.context.file_tree_limit must be >= 0")
	}

	// LLM
	tb := c.LLM.TokenBudget
	if tb.MaxInputTokens < 0 {
		return fmt.Errorf("llm.token_budget.max_input_tokens must be >= 0")
	}
	if tb.FileTreeRatio < 0 || tb.FileTreeRatio > 1 {
		return fmt.Errorf("llm.token_budget.file_tree_ratio must be in [0,1]")
	}
	if tb.HistoryRatio < 0 || tb.HistoryRatio > 1 {
		return fmt.Errorf("llm.token_budget.history_ratio must be in [0,1]")
	}

	// Storage
	if c.Storage.ObjectStore.MaxObjectSize < 0 {
		return fmt.Errorf("storage.object_store.max_object_size must be >= 0")
	}

	// Logging
	lvl := c.Logging.Level
	if lvl != "" && lvl != "debug" && lvl != "info" && lvl != "warn" && lvl != "error" {
		return fmt.Errorf("logging.level must be debug|info|warn|error")
	}

	// Tools (delegate to existing validator)
	if err := c.Tools.Validate(); err != nil {
		return fmt.Errorf("tools: %w", err)
	}

	return nil
}
