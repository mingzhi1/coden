// Package toolserver provides a generic JSON-RPC tool server that wraps
// any toolruntime.Executor behind the CodeN tool protocol.
package toolserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// Server exposes a toolruntime.Executor over JSON-RPC.
type Server struct {
	executor     toolruntime.Executor
	name         string
	capability   string
	timeoutClass string
	commands     []string
}

// New creates a Server with the given identity and command set.
func New(name, capability, timeoutClass string, commands []string, executor toolruntime.Executor) *Server {
	if executor == nil {
		panic("toolserver: executor is required")
	}
	if name == "" {
		name = "coden-tool"
	}
	if capability == "" {
		capability = "generic"
	}
	if timeoutClass == "" {
		timeoutClass = "short"
	}
	return &Server{
		executor:     executor,
		name:         name,
		capability:   capability,
		timeoutClass: timeoutClass,
		commands:     append([]string(nil), commands...),
	}
}

func (s *Server) ServeConn(ctx context.Context, rwc io.ReadWriteCloser) {
	codec := transport.NewCodec(rwc)
	defer codec.Close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		raw, err := codec.ReadMessage()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			return
		}

		var req protocol.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			_ = codec.WriteMessage(protocol.NewError(nil, protocol.CodeParseError, "parse error"))
			continue
		}
		if req.JSONRPC != protocol.Version {
			_ = codec.WriteMessage(protocol.NewError(req.ID, protocol.CodeInvalidRequest, "invalid jsonrpc version"))
			continue
		}

		resp := s.dispatch(ctx, req)
		if !req.IsNotification() {
			_ = codec.WriteMessage(resp)
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req protocol.Request) protocol.Response {
	if !protocol.SupportsToolServer(req.Method) {
		return protocol.NewErrorFromErr(req.ID, protocol.MethodNotFoundError(req.Method))
	}

	switch req.Method {
	case protocol.MethodPing:
		return protocol.NewResult(req.ID, protocol.PingResult{Status: "pong"})
	case protocol.MethodToolDescribe:
		return protocol.NewResult(req.ID, protocol.ToolDescribeResult{
			Name:           s.name,
			Capability:     s.capability,
			Version:        "mvp",
			SupportsCancel: false,
			TimeoutClass:   s.timeoutClass,
			Commands:       append([]string(nil), s.commands...),
		})
	case protocol.MethodToolCancel:
		return protocol.NewResult(req.ID, protocol.AckResult{Status: "not_supported"})
	case protocol.MethodToolExec:
		result, err := s.handleExec(ctx, req.Params)
		if err != nil {
			return protocol.NewErrorFromErr(req.ID, err)
		}
		return protocol.NewResult(req.ID, result)
	default:
		return protocol.NewError(req.ID, protocol.CodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) handleExec(ctx context.Context, raw json.RawMessage) (protocol.ToolExecResult, error) {
	var params protocol.ToolExecParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return protocol.ToolExecResult{}, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if params.ToolName != "" && !s.supportsCommand(params.ToolName) {
		return protocol.ToolExecResult{}, protocol.InvalidParamsError(fmt.Sprintf("unsupported tool: %s", params.ToolName))
	}

	var req toolruntime.Request
	if len(params.Args) == 0 {
		return protocol.ToolExecResult{}, protocol.InvalidParamsError("args are required")
	}
	if err := json.Unmarshal(params.Args, &req); err != nil {
		return protocol.ToolExecResult{}, protocol.InvalidParamsError(fmt.Sprintf("invalid args: %v", err))
	}
	if req.Kind == "" {
		if params.ToolName != "" {
			req.Kind = params.ToolName
		} else {
			return protocol.ToolExecResult{}, protocol.InvalidParamsError("tool kind is required")
		}
	}
	if !s.supportsCommand(req.Kind) {
		return protocol.ToolExecResult{}, protocol.InvalidParamsError(fmt.Sprintf("unsupported tool: %s", req.Kind))
	}
	if params.ToolName != "" && req.Kind != params.ToolName {
		return protocol.ToolExecResult{}, protocol.InvalidParamsError(fmt.Sprintf("tool kind mismatch: tool_name=%s kind=%s", params.ToolName, req.Kind))
	}

	result, err := s.executor.Execute(ctx, req)
	if err != nil {
		return protocol.ToolExecResult{}, err
	}

	return protocol.ToolExecResult{
		ArtifactPath: result.ArtifactPath,
		Summary:      result.Summary,
		Status:       "ok",
		Stdout:       result.Output,
		Stderr:       result.Stderr,
		ExitCode:     result.ExitCode,
		FilesTouched: filesTouched(req),
		Metadata: &protocol.ToolExecutionMeta{
			Tool:   s.name,
			Status: statusFor(req.Kind),
		},
	}, nil
}

func (s *Server) supportsCommand(kind string) bool {
	for _, command := range s.commands {
		if command == kind {
			return true
		}
	}
	return false
}

func filesTouched(req toolruntime.Request) []string {
	switch req.Kind {
	case "write_file", "read_file":
		if req.Path != "" {
			return []string{req.Path}
		}
	}
	return nil
}

func statusFor(kind string) string {
	switch kind {
	case "write_file":
		return "written"
	case "read_file":
		return "read"
	case "list_dir":
		return "listed"
	case "search":
		return "searched"
	case "run_shell":
		return "executed"
	case "edit_file":
		return "edited"
	case "grep_context":
		return "searched"
	default:
		return "ok"
	}
}
