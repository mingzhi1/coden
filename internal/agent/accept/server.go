package accept

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// Server exposes the acceptor role over JSON-RPC.
type Server struct {
	acceptor workflow.Acceptor
}

func NewServer(acceptor workflow.Acceptor) *Server {
	if acceptor == nil {
		acceptor = workflow.NewLocalAcceptor()
	}
	return &Server{acceptor: acceptor}
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
			Name:           "coden-agent-accept",
			Role:           string(workflow.RoleAcceptor),
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
	if params.Role != "" && params.Role != string(workflow.RoleAcceptor) {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("unsupported role: %s", params.Role))
	}

	var input acceptInput
	if len(params.Input) == 0 {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError("input is required")
	}
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("invalid accept input: %v", err))
	}

	checkpoint, err := s.acceptor.Accept(ctx, params.WorkflowID, input.Intent, input.Artifact)
	if err != nil {
		return protocol.WorkerExecuteResult{}, err
	}

	return protocol.WorkerExecuteResult{
		Status: "ok",
		Messages: []protocol.WorkerMessage{
			{Kind: "info", Role: string(workflow.RoleAcceptor), Content: "acceptor produced checkpoint decision"},
		},
		Checkpoint: &protocol.CheckpointProposal{
			Status:        checkpoint.Status,
			ArtifactPaths: checkpoint.ArtifactPaths,
			Evidence:      checkpoint.Evidence,
		},
		Metadata: &protocol.WorkerExecutionMeta{
			Worker: "coden-agent-accept",
			Role:   string(workflow.RoleAcceptor),
		},
	}, nil
}
