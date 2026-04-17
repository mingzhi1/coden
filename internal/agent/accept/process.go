package accept

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/subprocess"
)

func NewProcessRPCAcceptor(ctx context.Context, moduleRoot string) (workflow.Acceptor, func(), error) {
	clientRWC, err := subprocess.Start(ctx, subprocess.Spec{
		ModuleRoot: moduleRoot,
		BinaryName: "coden-agent-accept",
		GoPackage:  "./cmd/coden-agent-accept",
	})
	if err != nil {
		return nil, nil, err
	}
	client := NewRPCAcceptor(clientRWC)
	describeCtx, cancelDescribe := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDescribe()
	if _, err := client.Describe(describeCtx); err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, func() { _ = client.Close() }, nil
}
