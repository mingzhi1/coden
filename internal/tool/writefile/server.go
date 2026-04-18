package writefile

import (
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/tool/toolserver"
)

var defaultCommands = []string{"write_file", "read_file", "list_dir", "search", "run_shell", "edit_file", "grep_context"}

// NewServer creates a tool server with the default writefile command set.
func NewServer(executor toolruntime.Executor) *toolserver.Server {
	return toolserver.New("coden-tool-writefile", "filesystem", "short", defaultCommands, executor)
}

// NewScopedServer creates a tool server with a custom identity and command set.
// Deprecated: Use toolserver.New directly for non-writefile tools.
func NewScopedServer(name, capability, timeoutClass string, commands []string, executor toolruntime.Executor) *toolserver.Server {
	return toolserver.New(name, capability, timeoutClass, commands, executor)
}
