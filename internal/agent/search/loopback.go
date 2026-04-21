package search

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// NewLoopbackRPCSearcher starts an in-memory pipe between a Server (wrapping s)
// and an RPCSearcher client. The returned cleanup tears down both ends.
func NewLoopbackRPCSearcher(ctx context.Context, s workflow.Searcher) (workflow.Searcher, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(s)

	rpcCtx, cancel := context.WithCancel(ctx)
	go srv.ServeConn(rpcCtx, serverRWC)

	client := NewRPCSearcher(clientRWC)
	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = serverRWC.Close()
	}
	return client, cleanup, nil
}
