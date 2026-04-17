package plan

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	rpcclient "github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// RPCPlanner is a workflow planner backed by a JSON-RPC worker.
type RPCPlanner struct {
	client   *rpcclient.Client
	msgMu    sync.Mutex
	messages []model.WorkerMessage
}

func NewRPCPlanner(rwc io.ReadWriteCloser) *RPCPlanner {
	return &RPCPlanner{client: rpcclient.New(rwc)}
}

func (p *RPCPlanner) Close() error {
	return p.client.Close()
}

func (p *RPCPlanner) Describe(ctx context.Context) (protocol.WorkerDescribeResult, error) {
	return p.client.DescribeWorker(ctx)
}

func (p *RPCPlanner) Plan(ctx context.Context, workflowID string, intent model.IntentSpec) ([]model.Task, error) {
	input, err := protocol.MarshalRaw(intent)
	if err != nil {
		return nil, err
	}

	result, err := p.client.ExecuteWorker(ctx, protocol.WorkerExecuteParams{
		SessionID:  intent.SessionID,
		WorkflowID: workflowID,
		TaskID:     "task-plan",
		Role:       string(workflow.RolePlanner),
		Input:      input,
	})
	if err != nil {
		return nil, err
	}
	p.storeMessages(result.Messages)

	now := time.Now()
	tasks := make([]model.Task, 0, len(result.ProposedTasks))
	for _, task := range result.ProposedTasks {
		tasks = append(tasks, model.Task{
			ID:      task.ID,
			Title:   task.Title,
			Status:  task.Status,
			Created: now,
		})
	}
	return tasks, nil
}

func (p *RPCPlanner) TakeMessages() []model.WorkerMessage {
	p.msgMu.Lock()
	defer p.msgMu.Unlock()
	out := append([]model.WorkerMessage(nil), p.messages...)
	p.messages = nil
	return out
}

func (p *RPCPlanner) storeMessages(messages []protocol.WorkerMessage) {
	if len(messages) == 0 {
		return
	}
	p.msgMu.Lock()
	defer p.msgMu.Unlock()
	p.messages = p.messages[:0]
	for _, msg := range messages {
		p.messages = append(p.messages, model.WorkerMessage{
			Kind:    msg.Kind,
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
}

func (p *RPCPlanner) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "rpc-planner", Role: workflow.RolePlanner}
}
