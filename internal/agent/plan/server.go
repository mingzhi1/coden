package plan

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

// Server exposes the planner role over JSON-RPC.
type Server struct {
	planner workflow.Planner
}

func NewServer(planner workflow.Planner) *Server {
	if planner == nil {
		planner = workflow.NewLocalPlanner()
	}
	return &Server{planner: planner}
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
			Name:           "coden-agent-plan",
			Role:           string(workflow.RolePlanner),
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
	if params.Role != "" && params.Role != string(workflow.RolePlanner) {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("unsupported role: %s", params.Role))
	}

	var intent model.IntentSpec
	if len(params.Input) == 0 {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError("input is required")
	}
	if err := json.Unmarshal(params.Input, &intent); err != nil {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("invalid intent input: %v", err))
	}

	tasks, err := s.planner.Plan(ctx, params.WorkflowID, intent)
	if err != nil {
		return protocol.WorkerExecuteResult{}, err
	}

	proposed := make([]protocol.TaskProposal, 0, len(tasks))
	for _, task := range tasks {
		proposed = append(proposed, protocol.TaskProposal{
			ID:     task.ID,
			Title:  task.Title,
			Status: task.Status,
		})
	}

	return protocol.WorkerExecuteResult{
		Status: "ok",
		Messages: []protocol.WorkerMessage{
			{Kind: "info", Role: string(workflow.RolePlanner), Content: "planner produced task proposals"},
		},
		ProposedTasks: proposed,
		Metadata: &protocol.WorkerExecutionMeta{
			Worker: "coden-agent-plan",
			Role:   string(workflow.RolePlanner),
		},
	}, nil
}
