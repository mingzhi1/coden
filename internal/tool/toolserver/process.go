package toolserver

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/rpc/subprocess"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// NewProcessRPCExecutor launches a tool binary as a subprocess and returns
// an RPC-backed executor connected to it via stdio.
func NewProcessRPCExecutor(ctx context.Context, moduleRoot, workspaceRoot, binaryName, goPackage string) (toolruntime.Executor, func(), error) {
	clientRWC, err := subprocess.Start(ctx, subprocess.Spec{
		ModuleRoot: moduleRoot,
		BinaryName: binaryName,
		GoPackage:  goPackage,
		Args:       []string{"-workspace", workspaceRoot},
	})
	if err != nil {
		return nil, nil, err
	}
	client := NewRPCExecutor(clientRWC)
	describeCtx, cancelDescribe := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDescribe()
	if _, err := client.Describe(describeCtx); err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, func() { _ = client.Close() }, nil
}

// NewLoopbackRPCExecutor starts a tool server over an in-memory RPC pipe.
func NewLoopbackRPCExecutor(ctx context.Context, srv *Server) (toolruntime.Executor, func(), error) {
	serverRWC, clientRWC := transport.Pipe()
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
