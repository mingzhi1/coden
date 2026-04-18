package toolserver

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	rpcclient "github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// RPCExecutor is a toolruntime.Executor backed by a JSON-RPC tool process.
type RPCExecutor struct {
	client *rpcclient.Client
}

func NewRPCExecutor(rwc io.ReadWriteCloser) *RPCExecutor {
	return &RPCExecutor{client: rpcclient.New(rwc)}
}

func (e *RPCExecutor) Close() error {
	return e.client.Close()
}

func (e *RPCExecutor) Describe(ctx context.Context) (protocol.ToolDescribeResult, error) {
	return e.client.DescribeTool(ctx)
}

func (e *RPCExecutor) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	args, err := protocol.MarshalRaw(req)
	if err != nil {
		return toolruntime.Result{}, err
	}

	result, err := e.client.ExecuteTool(ctx, protocol.ToolExecParams{
		ToolCallID: fmt.Sprintf("tool-%d", time.Now().UnixNano()),
		ToolName:   req.Kind,
		Args:       args,
	})
	if err != nil {
		return toolruntime.Result{}, err
	}

	return toolruntime.Result{
		ArtifactPath: result.ArtifactPath,
		Summary:      result.Summary,
		Output:       result.Stdout,
		Stderr:       result.Stderr,
		ExitCode:     result.ExitCode,
	}, nil
}
