package search

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

// Server exposes a workflow.Searcher over JSON-RPC.
type Server struct {
	searcher workflow.Searcher
}

func NewServer(s workflow.Searcher) *Server {
	if s == nil {
		s = noopSearcher{}
	}
	return &Server{searcher: s}
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
			Name:           "coden-agent-search",
			Role:           string(workflow.RoleSearcher),
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
	if params.Role != "" && params.Role != string(workflow.RoleSearcher) {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("unsupported role: %s", params.Role))
	}
	if len(params.Input) == 0 {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError("input is required")
	}

	var in SearcherInput
	if err := json.Unmarshal(params.Input, &in); err != nil {
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("invalid searcher input: %v", err))
	}

	var (
		dc  model.DiscoveryContext
		err error
	)
	switch in.Op {
	case "", OpSearch:
		intent := model.IntentSpec{}
		if in.Intent != nil {
			intent = *in.Intent
		}
		dc, err = s.searcher.Search(ctx, intent, in.Tasks)
	case OpRefine:
		current := model.DiscoveryContext{}
		if in.Current != nil {
			current = *in.Current
		}
		dc, err = s.searcher.Refine(ctx, current, in.Hints)
	default:
		return protocol.WorkerExecuteResult{}, protocol.InvalidParamsError(fmt.Sprintf("unknown op: %s", in.Op))
	}
	if err != nil {
		return protocol.WorkerExecuteResult{}, err
	}

	rawDC, mErr := json.Marshal(dc)
	if mErr != nil {
		return protocol.WorkerExecuteResult{}, fmt.Errorf("marshal discovery: %w", mErr)
	}

	return protocol.WorkerExecuteResult{
		Status:    "ok",
		Discovery: rawDC,
		Metadata: &protocol.WorkerExecutionMeta{
			Worker: "coden-agent-search",
			Role:   string(workflow.RoleSearcher),
		},
	}, nil
}

// noopSearcher is a safe fallback that returns an empty DiscoveryContext.
type noopSearcher struct{}

func (noopSearcher) Search(_ context.Context, _ model.IntentSpec, _ []model.Task) (model.DiscoveryContext, error) {
	return model.DiscoveryContext{}, nil
}

func (noopSearcher) Refine(_ context.Context, current model.DiscoveryContext, _ []string) (model.DiscoveryContext, error) {
	return current, nil
}
