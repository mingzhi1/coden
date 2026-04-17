package code

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/workflow"
	rpcclient "github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// RPCCoder is a workflow coder backed by a JSON-RPC worker.
type RPCCoder struct {
	client   *rpcclient.Client
	msgMu    sync.Mutex
	messages []model.WorkerMessage
}

func NewRPCCoder(rwc io.ReadWriteCloser) *RPCCoder {
	return &RPCCoder{client: rpcclient.New(rwc)}
}

func (c *RPCCoder) Close() error {
	return c.client.Close()
}

func (c *RPCCoder) Describe(ctx context.Context) (protocol.WorkerDescribeResult, error) {
	return c.client.DescribeWorker(ctx)
}

func (c *RPCCoder) Build(ctx context.Context, workflowID string, intent model.IntentSpec, tasks []model.Task) (workflow.CodePlan, error) {
	input, err := protocol.MarshalRaw(buildInput{Intent: intent, Tasks: tasks})
	if err != nil {
		return workflow.CodePlan{}, err
	}

	result, err := c.client.ExecuteWorker(ctx, protocol.WorkerExecuteParams{
		SessionID:  intent.SessionID,
		WorkflowID: workflowID,
		TaskID:     "task-code",
		Role:       string(workflow.RoleCoder),
		Input:      input,
	})
	if err != nil {
		return workflow.CodePlan{}, err
	}
	if len(result.ToolCalls) == 0 {
		return workflow.CodePlan{}, io.ErrUnexpectedEOF
	}
	c.storeMessages(result.Messages)

	toolCalls := make([]workflow.ToolCall, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		var req toolruntime.Request
		if err := json.Unmarshal(call.Args, &req); err != nil {
			return workflow.CodePlan{}, err
		}
		toolCalls = append(toolCalls, workflow.ToolCall{
			ToolCallID: call.ID,
			Request:    req,
		})
	}
	first := toolCalls[0]
	return workflow.CodePlan{
		ToolCalls:  toolCalls,
		ToolCallID: first.ToolCallID,
		Request:    first.Request,
	}, nil
}

func (c *RPCCoder) TakeMessages() []model.WorkerMessage {
	c.msgMu.Lock()
	defer c.msgMu.Unlock()
	out := append([]model.WorkerMessage(nil), c.messages...)
	c.messages = nil
	return out
}

func (c *RPCCoder) storeMessages(messages []protocol.WorkerMessage) {
	if len(messages) == 0 {
		return
	}
	c.msgMu.Lock()
	defer c.msgMu.Unlock()
	c.messages = c.messages[:0]
	for _, msg := range messages {
		c.messages = append(c.messages, model.WorkerMessage{
			Kind:    msg.Kind,
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
}

func (c *RPCCoder) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "rpc-coder", Role: workflow.RoleCoder}
}
