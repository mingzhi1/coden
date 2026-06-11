package llm

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// ExecutorDeps abstracts the I/O dependencies of the agentic coding loop,
// enabling unit tests to inject fakes without real LLM or file-system access.
type ExecutorDeps struct {
	// Chat sends messages to the LLM and returns the reply.
	// Production: RecoverableChat(ctx, broker, RoleExecutor, msgs, defaultRecoveryConfig())
	Chat func(ctx context.Context, messages []Message) (string, error)

	// Execute runs a single tool call and returns its result.
	// Production: executor.Execute(ctx, request)
	Execute func(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error)

	// Compress applies the two-layer Compact chain to the message history.
	// Production: CompactHistory (L1 structural → L2 LLM summary on overflow).
	Compress func(ctx context.Context, messages []Message, round int, budget int) []Message
}

// ProductionExecutorDeps creates a ExecutorDeps wired to the real implementations
// used in production. The chatter can be a *Broker (embedded mode) or
// *LLMServerClient (llm-server mode). The returned value can be stored on
// LLMExecutor.deps; when deps is nil the executor lazily creates one via this
// function so that existing call-sites need no changes.
func ProductionExecutorDeps(chatter Chatter, executor toolruntime.Executor, toolHistoryBudget int) ExecutorDeps {
	return ExecutorDeps{
		Chat: func(ctx context.Context, messages []Message) (string, error) {
			return RecoverableChat(ctx, chatter, RoleExecutor, messages, defaultRecoveryConfig())
		},
		Execute: func(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
			return executor.Execute(ctx, req)
		},
		Compress: func(ctx context.Context, messages []Message, round int, budget int) []Message {
			return CompactHistory(ctx, messages, round, budget, newChatSummarizer(chatter, RoleExecutor))
		},
	}
}
