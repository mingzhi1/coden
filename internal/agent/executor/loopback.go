package executor

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// NewLoopbackRPCExecutor starts the code worker over an in-memory RPC pipe.
func NewLoopbackRPCExecutor(ctx context.Context) (workflow.Executor, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalExecutor())

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

func NewLoopbackRPCExecutorAdapter(ctx context.Context, _ string) (workflow.Executor, func(), error) {
	return NewLoopbackRPCExecutor(ctx)
}
