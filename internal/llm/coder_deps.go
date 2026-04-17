package llm

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// CoderDeps abstracts the I/O dependencies of the agentic coding loop,
// enabling unit tests to inject fakes without real LLM or file-system access.
type CoderDeps struct {
	// Chat sends messages to the LLM and returns the reply.
	// Production: RecoverableChat(ctx, broker, RoleCoder, msgs, defaultRecoveryConfig())
	Chat func(ctx context.Context, messages []Message) (string, error)

	// Execute runs a single tool call and returns its result.
	// Production: executor.Execute(ctx, request)
	Execute func(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error)

	// Compress applies the 4-layer compression chain to the message history.
	// Production: Snip → MicroCompact → compressAgenticHistory → AutoCompact
	Compress func(messages []Message, round int, budget int) []Message
}

// ProductionCoderDeps creates a CoderDeps wired to the real implementations
// used in production. The chatter can be a *Broker (embedded mode) or
// *LLMServerClient (llm-server mode). The returned value can be stored on
// LLMCoder.deps; when deps is nil the coder lazily creates one via this
// function so that existing call-sites need no changes.
func ProductionCoderDeps(chatter Chatter, executor toolruntime.Executor, toolHistoryBudget int) CoderDeps {
	return CoderDeps{
		Chat: func(ctx context.Context, messages []Message) (string, error) {
			return RecoverableChat(ctx, chatter, RoleCoder, messages, defaultRecoveryConfig())
		},
		Execute: func(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
			return executor.Execute(ctx, req)
		},
		Compress: func(messages []Message, round int, budget int) []Message {
			messages = SnipHistory(messages, snipMaxMessages)
			messages = MicroCompact(messages, round)
			messages = compressAgenticHistory(messages, budget)
			messages = AutoCompact(messages, budget)
			return messages
		},
	}
}
