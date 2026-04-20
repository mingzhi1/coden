package writefile

import (
	"io"

	"github.com/mingzhi1/coden/internal/tool/toolserver"
)

// RPCExecutor re-exports toolserver.RPCExecutor for backward compatibility.
type RPCExecutor = toolserver.RPCExecutor

func NewRPCExecutor(rwc io.ReadWriteCloser) *RPCExecutor {
	return toolserver.NewRPCExecutor(rwc)
}
