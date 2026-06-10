package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

type buildInput struct {
	Intent model.IntentSpec `json:"intent"`
	Tasks  []model.Task     `json:"tasks"`
}

// Server exposes the executor role over JSON-RPC.
type Server struct {
	executor workflow.Executor
}

func NewServer(executor workflow.Executor) *Server {
	if executor == nil {
		executor = workflow.NewLocalExecutor()
	}
	return &Server{executor: executor}
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
	if !protocol.SupportsWorkerServer(req.Method) {
		return protocol.NewErrorFromErr(req.ID, protocol.MethodNotFoundError(req.Method))
	}

	switch req.Method {
	case protocol.MethodPing:
		return protocol.NewResult(req.ID, protocol.PingResult{Status: "pong"})
	case protocol.MethodWorkerDescribe:
		return protocol.NewResult(req.ID, protocol.WorkerDescribeResult{
			Name:           "coden-agent-executor",
			Role:           string(workflow.RoleExecutor),
			Version:        "mvp",
			SupportsCancel: false,
			MaxConcurrency: 1,
		})
	case protocol.MethodWorkerCancel:
		return protocol.NewResult(req.ID, protocol.AckResult{Status: "not_supported"})
	case protocol.MethodWorkerExecute:
		result, err := s.handleExecute(ctx, req.Params)
		if err != nil {
			return protocol.NewErrorFromErr(req.ID, err)
		}
		return protocol.NewResult(req.ID, result)
	default:
		return protocol.NewError(req.ID, protocol.CodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) handleExecute(ctx context.Context, raw json.RawMessage) (protocol.WorkerExecuteResult, error) {
	var params protocol.WorkerExecuteParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}
	if params.Role != "" && params.Role != string(workflow.RoleExecutor) {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("unsupported role: %s", params.Role))
	}

	var input buildInput
	if len(params.Input) == 0 {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError("input is required")
	}
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("invalid build input: %v", err))
	}

	plan, err := s.executor.Build(ctx, params.WorkflowID, input.Intent, input.Tasks)
	if err != nil {
		return protocol.WorkerExecuteResult{}, err
	}
	calls := plan.Calls()
	if len(calls) == 0 {
		return protocol.WorkerExecuteResult{}, io.ErrUnexpectedEOF
	}
	toolCalls := make([]protocol.ToolCall, 0, len(calls))
	for _, call := range calls {
		args, err := protocol.MarshalRaw(call.Request)
		if err != nil {
			return protocol.WorkerExecuteResult{}, err
		}
		toolCalls = append(toolCalls, protocol.ToolCall{
			ID:       call.ToolCallID,
			ToolName: call.Request.Kind,
			Args:     args,
		})
	}

	return protocol.WorkerExecuteResult{
		Status: "ok",
		Messages: []protocol.WorkerMessage{
			{Kind: "info", Role: string(workflow.RoleExecutor), Content: "executor produced tool call"},
		},
		ToolCalls: toolCalls,
		Metadata: &protocol.WorkerExecutionMeta{
			Worker: "coden-agent-executor",
			Role:   string(workflow.RoleExecutor),
		},
	}, nil
}
