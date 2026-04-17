package config

import "strings"

// mergeConfigs merges src into dst. Non-zero values in src override dst.
// Map fields are merged key-by-key; struct fields are replaced when present.
func mergeConfigs(dst, src *Config) {
	if src == nil {
		return
	}
	mergeCoreConfig(&dst.Core, &src.Core)
	mergeLLMConfig(&dst.LLM, &src.LLM)
	dst.Tools.MergeWith(&src.Tools)
	mergeStorageConfig(&dst.Storage, &src.Storage)
	mergeUIConfig(&dst.UI, &src.UI)
	mergeLoggingConfig(&dst.Logging, &src.Logging)
}

func mergeCoreConfig(dst, src *CoreConfig) {
	// Workflow
	if src.Workflow.MaxRetries > 0 {
		dst.Workflow.MaxRetries = src.Workflow.MaxRetries
	}
	if src.Workflow.Timeout > 0 {
		dst.Workflow.Timeout = src.Workflow.Timeout
	}
	if src.Workflow.ParallelTasks {
		dst.Workflow.ParallelTasks = true
	}
	if src.Workflow.FailurePolicy != "" {
		dst.Workflow.FailurePolicy = src.Workflow.FailurePolicy
	}

	// Context
	if src.Context.MaxHistoryTurns > 0 {
		dst.Context.MaxHistoryTurns = src.Context.MaxHistoryTurns
	}
	if src.Context.FileTreeLimit > 0 {
		dst.Context.FileTreeLimit = src.Context.FileTreeLimit
	}
	if src.Context.AutoCompactThreshold > 0 {
		dst.Context.AutoCompactThreshold = src.Context.AutoCompactThreshold
	}

	// Security — AllowShell is a bool, always merge when src section is present.
	if src.Security.AllowShell {
		dst.Security.AllowShell = true
	}
	if len(src.Security.DangerousCommands) > 0 {
		dst.Security.DangerousCommands = src.Security.DangerousCommands
	}
	// CommandSubstitutionCheck: false means "disable" so we only merge when true.
	if src.Security.CommandSubstitutionCheck {
		dst.Security.CommandSubstitutionCheck = true
	}
}

func mergeLLMConfig(dst, src *LLMConfig) {
	// Providers — key-by-key merge
	if len(src.Providers) > 0 {
		if dst.Providers == nil {
			dst.Providers = make(map[string]ProviderEntry)
		}
		for name, prov := range src.Providers {
			existing := dst.Providers[name]
			if prov.Type != "" {
				existing.Type = prov.Type
			}
			if prov.APIKey != "" {
				existing.APIKey = prov.APIKey
			}
			if prov.BaseURL != "" {
				existing.BaseURL = prov.BaseURL
			}
			if prov.DefaultModel != "" {
				existing.DefaultModel = prov.DefaultModel
			}
			if prov.Command != "" {
				existing.Command = prov.Command
			}
			if len(prov.Args) > 0 {
				existing.Args = prov.Args
			}
			if len(prov.Env) > 0 {
				if existing.Env == nil {
					existing.Env = make(map[string]string)
				}
				for k, v := range prov.Env {
					existing.Env[k] = v
				}
			}
			dst.Providers[name] = existing
		}
	}

	// Pool tiers — replace entirely when specified
	if len(src.Pool.Primary) > 0 {
		dst.Pool.Primary = src.Pool.Primary
	}
	if len(src.Pool.Light) > 0 {
		dst.Pool.Light = src.Pool.Light
	}

	if src.TokenBudget.MaxInputTokens > 0 {
		dst.TokenBudget.MaxInputTokens = src.TokenBudget.MaxInputTokens
	}
	if src.TokenBudget.FileTreeRatio > 0 {
		dst.TokenBudget.FileTreeRatio = src.TokenBudget.FileTreeRatio
	}
	if src.TokenBudget.HistoryRatio > 0 {
		dst.TokenBudget.HistoryRatio = src.TokenBudget.HistoryRatio
	}
	if src.TokenBudget.RetryRatio > 0 {
		dst.TokenBudget.RetryRatio = src.TokenBudget.RetryRatio
	}
}

func mergeStorageConfig(dst, src *StorageConfig) {
	if strings.TrimSpace(src.BaseDir) != "" {
		dst.BaseDir = src.BaseDir
	}
	if src.SQLite.BusyTimeout > 0 {
		dst.SQLite.BusyTimeout = src.SQLite.BusyTimeout
	}
	if src.SQLite.JournalMode != "" {
		dst.SQLite.JournalMode = src.SQLite.JournalMode
	}
	if src.ObjectStore.MaxObjectSize > 0 {
		dst.ObjectStore.MaxObjectSize = src.ObjectStore.MaxObjectSize
	}
	if src.ObjectStore.Compression {
		dst.ObjectStore.Compression = true
	}
}

func mergeUIConfig(dst, src *UIConfig) {
	if src.TUI.Theme != "" {
		dst.TUI.Theme = src.TUI.Theme
	}
	if src.TUI.Mouse {
		dst.TUI.Mouse = true
	}
	if src.TUI.ShowTokenUsage {
		dst.TUI.ShowTokenUsage = true
	}
	if src.Plain.Color {
		dst.Plain.Color = true
	}
	if src.Plain.Verbose {
		dst.Plain.Verbose = true
	}
}

func mergeLoggingConfig(dst, src *LoggingConfig) {
	if src.Level != "" {
		dst.Level = src.Level
	}
	if src.Format != "" {
		dst.Format = src.Format
	}
	if src.Output != "" {
		dst.Output = src.Output
	}
	if src.MaxSize > 0 {
		dst.MaxSize = src.MaxSize
	}
	if src.MaxBackups > 0 {
		dst.MaxBackups = src.MaxBackups
	}
	if src.MaxAge > 0 {
		dst.MaxAge = src.MaxAge
	}
}
