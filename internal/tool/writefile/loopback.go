package writefile

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// NewLoopbackRPCExecutor starts the write-file tool over an in-memory RPC pipe.
func NewLoopbackRPCExecutor(ctx context.Context, workspaceRoot string) (toolruntime.Executor, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(toolruntime.NewLocalExecutor(workspace.New(workspaceRoot)))

	rpcCtx, cancel := context.WithCancel(ctx)
	go srv.ServeConn(rpcCtx, serverRWC)

	client := NewRPCExecutor(clientRWC)
	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = serverRWC.Close()
	}
	return client, cleanup, nil
}

func NewLoopbackRPCExecutorAdapter(ctx context.Context, _ string, workspaceRoot string) (toolruntime.Executor, func(), error) {
	return NewLoopbackRPCExecutor(ctx, workspaceRoot)
}
