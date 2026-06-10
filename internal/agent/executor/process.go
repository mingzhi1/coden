package executor

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/subprocess"
)

func NewProcessRPCExecutor(ctx context.Context, moduleRoot string) (workflow.Executor, func(), error) {
	clientRWC, err := subprocess.Start(ctx, subprocess.Spec{
		ModuleRoot: moduleRoot,
		BinaryName: "coden-agent-executor",
		GoPackage:  "./cmd/coden-agent-executor",
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
