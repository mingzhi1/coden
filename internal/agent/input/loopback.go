package input

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

func NewLoopbackRPCInputter(ctx context.Context) (workflow.Inputter, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalInputter())

	rpcCtx, cancel := context.WithCancel(ctx)
	go srv.ServeConn(rpcCtx, serverRWC)

	client := NewRPCInputter(clientRWC)
	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = serverRWC.Close()
	}
	return client, cleanup, nil
}

func NewLoopbackRPCInputterAdapter(ctx context.Context, _ string) (workflow.Inputter, func(), error) {
	return NewLoopbackRPCInputter(ctx)
}
