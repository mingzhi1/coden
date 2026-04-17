package plan

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// NewLoopbackRPCPlanner starts a planner worker over an in-memory pipe.
// This keeps the planner on the RPC boundary even before it becomes a real OS process.
func NewLoopbackRPCPlanner(ctx context.Context) (workflow.Planner, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
	srv := NewServer(workflow.NewLocalPlanner())

	rpcCtx, cancel := context.WithCancel(ctx)
	go srv.ServeConn(rpcCtx, serverRWC)

	client := NewRPCPlanner(clientRWC)
	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = serverRWC.Close()
	}
	return client, cleanup, nil
}

func NewLoopbackRPCPlannerAdapter(ctx context.Context, _ string) (workflow.Planner, func(), error) {
	return NewLoopbackRPCPlanner(ctx)
}
