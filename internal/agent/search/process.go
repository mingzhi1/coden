package search

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/subprocess"
)

// NewProcessRPCSearcher launches the coden-agent-search subprocess and returns
// a workflow.Searcher backed by its stdio JSON-RPC pipe. The subprocess is
// configured to operate against workspaceRoot.
func NewProcessRPCSearcher(ctx context.Context, moduleRoot, workspaceRoot string) (workflow.Searcher, func(), error) {
	args := []string{}
	if workspaceRoot != "" {
		args = append(args, "-workspace", workspaceRoot)
	}
	clientRWC, err := subprocess.Start(ctx, subprocess.Spec{
		ModuleRoot: moduleRoot,
		BinaryName: "coden-agent-search",
		GoPackage:  "./cmd/coden-agent-search",
		Args:       args,
	})
	if err != nil {
		return nil, nil, err
	}
	client := NewRPCSearcher(clientRWC)
	describeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.Describe(describeCtx); err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, func() { _ = client.Close() }, nil
}
