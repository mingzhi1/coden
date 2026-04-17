package code

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// NewLoopbackRPCCoder starts the code worker over an in-memory RPC pipe.
func NewLoopbackRPCCoder(ctx context.Context) (workflow.Coder, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalCoder())

	rpcCtx, cancel := context.WithCancel(ctx)
	go srv.ServeConn(rpcCtx, serverRWC)

	client := NewRPCCoder(clientRWC)
	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = serverRWC.Close()
	}
	return client, cleanup, nil
}

func NewLoopbackRPCCoderAdapter(ctx context.Context, _ string) (workflow.Coder, func(), error) {
	return NewLoopbackRPCCoder(ctx)
}
