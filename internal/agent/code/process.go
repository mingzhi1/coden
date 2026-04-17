package code

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/subprocess"
)

func NewProcessRPCCoder(ctx context.Context, moduleRoot string) (workflow.Coder, func(), error) {
	clientRWC, err := subprocess.Start(ctx, subprocess.Spec{
		ModuleRoot: moduleRoot,
		BinaryName: "coden-agent-code",
		GoPackage:  "./cmd/coden-agent-code",
	})
	if err != nil {
		return nil, nil, err
	}
	client := NewRPCCoder(clientRWC)
	describeCtx, cancelDescribe := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDescribe()
	if _, err := client.Describe(describeCtx); err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, func() { _ = client.Close() }, nil
}
