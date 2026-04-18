package writefile

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/tool/toolserver"
)

// NewLoopbackRPCExecutor starts the write-file tool over an in-memory RPC pipe.
func NewLoopbackRPCExecutor(ctx context.Context, workspaceRoot string) (toolruntime.Executor, func(), error) {
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(workspaceRoot)))
	return toolserver.NewLoopbackRPCExecutor(ctx, srv)
}

func NewLoopbackRPCExecutorAdapter(ctx context.Context, _ string, workspaceRoot string) (toolruntime.Executor, func(), error) {
	return NewLoopbackRPCExecutor(ctx, workspaceRoot)
}
