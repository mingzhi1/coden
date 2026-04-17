package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
	"github.com/mingzhi1/coden/internal/rpc/transport"
)

// WorkerServer handles kernel→worker RPC calls inside a worker process.
// It wraps a workflow.Worker and exposes it over JSON-RPC 2.0.
// Following the Neovim pattern: the server is the "remote plugin" side of
// the connection; the kernel is the caller.
type WorkerServer struct {
	worker   workflow.Worker
	desc     protocol.WorkerDescribeResult
	handlers map[string]Handler
}

// NewWorkerServer creates a WorkerServer that wraps w and advertises desc on
// worker.describe.
func NewWorkerServer(w workflow.Worker, desc protocol.WorkerDescribeResult) *WorkerServer {
	s := &WorkerServer{
		worker:   w,
		desc:     desc,
		handlers: make(map[string]Handler),
	}
	s.handlers[protocol.MethodWorkerDescribe] = s.handleWorkerDescribe
	s.handlers[protocol.MethodWorkerExecute] = s.handleWorkerExecute
	s.handlers[protocol.MethodWorkerCancel] = s.handleWorkerCancel
	return s
}

// ServeConn serves a single kernel connection until EOF or ctx cancellation.
// Blocks until the connection is closed.
func (s *WorkerServer) ServeConn(ctx context.Context, rwc io.ReadWriteCloser) {
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
				log.Printf("rpc/worker: write error response: %v", wErr)
				return
			}
			continue
		}

		if req.JSONRPC != protocol.Version {
			if wErr := codec.WriteMessage(protocol.NewError(req.ID, protocol.CodeInvalidRequest, "invalid jsonrpc version")); wErr != nil {
				log.Printf("rpc/worker: write error response: %v", wErr)
				return
			}
			continue
		}

		s.serveRequest(ctx, codec, req)
	}
}

func (s *WorkerServer) serveRequest(ctx context.Context, codec *transport.Codec, req protocol.Request) {
	if !protocol.SupportsWorkerServer(req.Method) {
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

func (s *WorkerServer) handleWorkerDescribe(_ context.Context, _ json.RawMessage) (any, error) {
	return s.desc, nil
}

func (s *WorkerServer) handleWorkerExecute(ctx context.Context, raw json.RawMessage) (any, error) {
	var params protocol.WorkerExecuteParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid params: %v", err))
	}

	var input workflow.WorkerInput
	if len(params.Input) > 0 {
		if err := json.Unmarshal(params.Input, &input); err != nil {
			return nil, protocol.InvalidParamsError(fmt.Sprintf("invalid input: %v", err))
		}
	}

	output, err := s.worker.Execute(ctx, input)
	if err != nil {
		return nil, err
	}
	return workerOutputToResult(output), nil
}

func (s *WorkerServer) handleWorkerCancel(_ context.Context, _ json.RawMessage) (any, error) {
	// Cancellation is context-driven: the kernel cancels by closing the connection
	// or by cancelling the context passed to ServeConn.
	return map[string]string{"status": "ok"}, nil
}

// workerOutputToResult maps a workflow.WorkerOutput to the protocol wire type.
func workerOutputToResult(out workflow.WorkerOutput) protocol.WorkerExecuteResult {
	r := protocol.WorkerExecuteResult{
		Status: "ok",
	}

	if len(out.Messages) > 0 {
		r.Messages = make([]protocol.WorkerMessage, len(out.Messages))
		for i, m := range out.Messages {
			r.Messages[i] = protocol.WorkerMessage{Kind: m.Kind, Role: m.Role, Content: m.Content}
		}
	}

	r.Metadata = &protocol.WorkerExecutionMeta{
		Worker: out.Metadata.Worker,
		Role:   string(out.Metadata.Role),
	}

	if out.Intent != nil {
		r.Intent = &protocol.IntentProposal{
			ID:              out.Intent.ID,
			SessionID:       out.Intent.SessionID,
			Goal:            out.Intent.Goal,
			SuccessCriteria: out.Intent.SuccessCriteria,
		}
	}

	if len(out.Tasks) > 0 {
		r.ProposedTasks = make([]protocol.TaskProposal, len(out.Tasks))
		for i, t := range out.Tasks {
			r.ProposedTasks[i] = protocol.TaskProposal{ID: t.ID, Title: t.Title, Status: t.Status}
		}
	}

	if out.Checkpoint != nil {
		r.Checkpoint = &protocol.CheckpointProposal{
			Status:        out.Checkpoint.Status,
			ArtifactPaths: out.Checkpoint.ArtifactPaths,
			Evidence:      out.Checkpoint.Evidence,
		}
	}

	if out.CodePlan != nil {
		calls := out.CodePlan.Calls()
		r.ToolCalls = make([]protocol.ToolCall, 0, len(calls))
		for _, c := range calls {
			args, _ := json.Marshal(c.Request)
			r.ToolCalls = append(r.ToolCalls, protocol.ToolCall{
				ID:       c.ToolCallID,
				ToolName: c.Request.Kind,
				Args:     json.RawMessage(args),
			})
		}
	}

	return r
}
