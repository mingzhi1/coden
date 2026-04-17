package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// RPCWorker implements workflow.Worker over a JSON-RPC connection to a worker
// process. The kernel substitutes this for an in-process worker once that
// worker is split into its own process.
type RPCWorker struct {
	client *Client
	role   workflow.Role
}

// NewRPCWorker creates an RPCWorker that routes Execute calls through c.
func NewRPCWorker(c *Client, role workflow.Role) *RPCWorker {
	return &RPCWorker{client: c, role: role}
}

// Execute implements workflow.Worker.
func (w *RPCWorker) Execute(ctx context.Context, input workflow.WorkerInput) (workflow.WorkerOutput, error) {
	rawInput, err := json.Marshal(input)
	if err != nil {
		return workflow.WorkerOutput{}, fmt.Errorf("rpc worker: marshal input: %w", err)
	}

	params := protocol.WorkerExecuteParams{
		SessionID:  input.SessionID,
		WorkflowID: input.WorkflowID,
		TaskID:     input.TaskID,
		Role:       string(w.role),
		Input:      json.RawMessage(rawInput),
	}

	result, err := w.client.ExecuteWorker(ctx, params)
	if err != nil {
		return workflow.WorkerOutput{}, fmt.Errorf("rpc worker: %w", err)
	}

	return protoResultToWorkerOutput(result)
}

func protoResultToWorkerOutput(r protocol.WorkerExecuteResult) (workflow.WorkerOutput, error) {
	out := workflow.WorkerOutput{}

	if r.Metadata != nil {
		out.Metadata = workflow.WorkerMetadata{
			Worker: r.Metadata.Worker,
			Role:   workflow.Role(r.Metadata.Role),
		}
	}

	if len(r.Messages) > 0 {
		out.Messages = make([]model.WorkerMessage, len(r.Messages))
		for i, m := range r.Messages {
			out.Messages[i] = model.WorkerMessage{Kind: m.Kind, Role: m.Role, Content: m.Content}
		}
	}

	if r.Intent != nil {
		spec := model.IntentSpec{
			ID:              r.Intent.ID,
			SessionID:       r.Intent.SessionID,
			Goal:            r.Intent.Goal,
			SuccessCriteria: r.Intent.SuccessCriteria,
		}
		out.Intent = &spec
	}

	if len(r.ProposedTasks) > 0 {
		out.Tasks = make([]model.Task, len(r.ProposedTasks))
		for i, pt := range r.ProposedTasks {
			out.Tasks[i] = model.Task{ID: pt.ID, Title: pt.Title, Status: pt.Status}
		}
	}

	if r.Checkpoint != nil {
		cp := model.CheckpointResult{
			Status:        r.Checkpoint.Status,
			ArtifactPaths: r.Checkpoint.ArtifactPaths,
			Evidence:      r.Checkpoint.Evidence,
		}
		out.Checkpoint = &cp
	}

	if len(r.ToolCalls) > 0 {
		calls := make([]workflow.ToolCall, 0, len(r.ToolCalls))
		for _, tc := range r.ToolCalls {
			var req toolruntime.Request
			if err := json.Unmarshal(tc.Args, &req); err != nil {
				return workflow.WorkerOutput{}, fmt.Errorf("rpc worker: unmarshal tool call args for %q: %w", tc.ID, err)
			}
			calls = append(calls, workflow.ToolCall{
				ToolCallID: tc.ID,
				Request:    req,
			})
		}
		out.CodePlan = &workflow.CodePlan{ToolCalls: calls}
	}

	return out, nil
}
