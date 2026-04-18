package shelltool

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/tool/toolserver"
)

func NewProcessRPCExecutor(ctx context.Context, moduleRoot, workspaceRoot string) (toolruntime.Executor, func(), error) {
	return toolserver.NewProcessRPCExecutor(ctx, moduleRoot, workspaceRoot, "coden-tool-shell", "./cmd/coden-tool-shell")
}
