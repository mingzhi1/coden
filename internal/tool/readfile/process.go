package readfile

import (
	"context"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/tool/writefile"
)

func NewProcessRPCExecutor(ctx context.Context, moduleRoot, workspaceRoot string) (toolruntime.Executor, func(), error) {
	return writefile.NewScopedProcessRPCExecutor(ctx, moduleRoot, workspaceRoot, "coden-tool-readfile", "./cmd/coden-tool-readfile")
}
