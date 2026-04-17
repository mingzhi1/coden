package writefile

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/rpc/subprocess"
)

func NewProcessRPCExecutor(ctx context.Context, moduleRoot, workspaceRoot string) (toolruntime.Executor, func(), error) {
	return NewScopedProcessRPCExecutor(ctx, moduleRoot, workspaceRoot, "coden-tool-writefile", "./cmd/coden-tool-writefile")
}

func NewScopedProcessRPCExecutor(ctx context.Context, moduleRoot, workspaceRoot, binaryName, goPackage string) (toolruntime.Executor, func(), error) {
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
