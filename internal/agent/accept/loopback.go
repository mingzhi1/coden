package accept

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// NewLoopbackRPCAcceptor starts an accept worker over an in-memory pipe.
func NewLoopbackRPCAcceptor(ctx context.Context) (workflow.Acceptor, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalAcceptor())

	rpcCtx, cancel := context.WithCancel(ctx)
	go srv.ServeConn(rpcCtx, serverRWC)

	client := NewRPCAcceptor(clientRWC)
	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = serverRWC.Close()
	}
	return client, cleanup, nil
}

func NewLoopbackRPCAcceptorAdapter(ctx context.Context, _ string) (workflow.Acceptor, func(), error) {
	return NewLoopbackRPCAcceptor(ctx)
}
