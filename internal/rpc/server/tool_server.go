package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// ToolServer handles kernel→tool RPC calls inside a tool process.
// It wraps a toolruntime.Executor and exposes it over JSON-RPC 2.0.
type ToolServer struct {
	executor toolruntime.Executor
	desc     protocol.ToolDescribeResult
	handlers map[string]Handler
}

// NewToolServer creates a ToolServer that wraps exec and advertises desc on
// tool.describe.
func NewToolServer(exec toolruntime.Executor, desc protocol.ToolDescribeResult) *ToolServer {
	s := &ToolServer{
		executor: exec,
		desc:     desc,
		handlers: make(map[string]Handler),
	}
	s.handlers[protocol.MethodToolDescribe] = s.handleToolDescribe
	s.handlers[protocol.MethodToolExec] = s.handleToolExec
	s.handlers[protocol.MethodToolCancel] = s.handleToolCancel
	return s
}

// ServeConn serves a single kernel connection until EOF or ctx cancellation.
func (s *ToolServer) ServeConn(ctx context.Context, rwc io.ReadWriteCloser) {
	codec := transport.NewCodec(rwc)
	defer codec.Close()

	for {
		raw, err := codec.ReadMessage()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			return
		}

		var req protocol.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			if wErr := codec.WriteMessage(protocol.NewError(nil, protocol.CodeParseError, "parse error")); wErr != nil {
				log.Printf("rpc/tool: write error response: %v", wErr)
				return
			}
			continue
		}

		if req.JSONRPC != protocol.Version {
			if wErr := codec.WriteMessage(protocol.NewError(req.ID, protocol.CodeInvalidRequest, "invalid jsonrpc version")); wErr != nil {
				log.Printf("rpc/tool: write error response: %v", wErr)
				return
			}
			continue
		}

		s.serveRequest(ctx, codec, req)
	}
}

func (s *ToolServer) serveRequest(ctx context.Context, codec *transport.Codec, req protocol.Request) {
	if !protocol.SupportsToolServer(req.Method) {
		if !req.IsNotification() {
			_ = codec.WriteMessage(protocol.NewErrorFromErr(req.ID, protocol.MethodNotFoundError(req.Method)))
		}
		return
	}

	handler, ok := s.handlers[req.Method]
	if !ok {
		if !req.IsNotification() {
			_ = codec.WriteMessage(protocol.NewErrorFromErr(req.ID, protocol.MethodNotFoundError(req.Method)))
		}
		return
	}

	reqCtx, reqCancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer reqCancel()

	result, err := handler(reqCtx, req.Params)
	if req.IsNotification() {
		return
	}
	if err != nil {
		_ = codec.WriteMessage(protocol.NewErrorFromErr(req.ID, err))
		return
	}
	_ = codec.WriteMessage(protocol.NewResult(req.ID, result))
}

func (s *ToolServer) handleToolDescribe(_ context.Context, _ json.RawMessage) (any, error) {
	return s.desc, nil
}

func (s *ToolServer) handleToolExec(ctx context.Context, raw json.RawMessage) (any, error) {
	var params protocol.ToolExecParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}

	var req toolruntime.Request
	if len(params.Args) > 0 {
		if err := json.Unmarshal(params.Args, &req); err != nil {
			return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid args: %v", err))
		}
	}
	if req.Kind == "" && params.ToolName != "" {
		req.Kind = params.ToolName
	}

	result, err := s.executor.Execute(ctx, req)
	if err != nil {
		return &protocol.ToolExecResult{
			Status: "failed",
			Stderr: err.Error(),
			Metadata: &protocol.ToolExecutionMeta{
				Tool:   params.ToolName,
				Status: "failed",
			},
		}, nil
	}
	return toolResultToProto(params.ToolName, result), nil
}

func (s *ToolServer) handleToolCancel(_ context.Context, _ json.RawMessage) (any, error) {
	// Cancellation is context-driven: the kernel cancels by closing the connection.
	return map[string]string{"status": "ok"}, nil
}

// toolResultToProto maps a toolruntime.Result to the protocol wire type.
func toolResultToProto(toolName string, r toolruntime.Result) *protocol.ToolExecResult {
	status := "ok"
	if r.ExitCode != 0 {
		status = "failed"
	}
	return &protocol.ToolExecResult{
		ArtifactPath: r.ArtifactPath,
		Summary:      r.Summary,
		Status:       status,
		Stdout:       r.Output,
		Stderr:       r.Stderr,
		ExitCode:     r.ExitCode,
		Metadata: &protocol.ToolExecutionMeta{
			Tool:   toolName,
			Status: status,
		},
	}
}
