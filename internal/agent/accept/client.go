package accept

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

type acceptInput struct {
	Intent   model.IntentSpec `json:"intent"`
	Artifact model.Artifact   `json:"artifact"`
	Tasks    []model.Task     `json:"tasks,omitempty"`
}

// RPCAcceptor is a workflow acceptor backed by a JSON-RPC worker.
type RPCAcceptor struct {
	client   *rpcclient.Client
	msgMu    sync.Mutex
	messages []model.WorkerMessage
}

func NewRPCAcceptor(rwc io.ReadWriteCloser) *RPCAcceptor {
	return &RPCAcceptor{client: rpcclient.New(rwc)}
}

func (a *RPCAcceptor) Close() error {
	return a.client.Close()
}

func (a *RPCAcceptor) Describe(ctx context.Context) (protocol.WorkerDescribeResult, error) {
	return a.client.DescribeWorker(ctx)
}

func (a *RPCAcceptor) Accept(ctx context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact, tasks []model.Task) (model.CheckpointResult, error) {
	input, err := protocol.MarshalRaw(acceptInput{Intent: intent, Artifact: artifact, Tasks: tasks})
	if err != nil {
		return model.CheckpointResult{}, err
	}

	result, err := a.client.ExecuteWorker(ctx, protocol.WorkerExecuteParams{
		SessionID:  intent.SessionID,
		WorkflowID: workflowID,
		TaskID:     "task-accept",
		Role:       string(workflow.RoleAcceptor),
		Input:      input,
	})
	if err != nil {
		return model.CheckpointResult{}, err
	}
	if result.Checkpoint == nil {
		return model.CheckpointResult{}, io.ErrUnexpectedEOF
	}
	a.storeMessages(result.Messages)

	return model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     intent.SessionID,
		Status:        result.Checkpoint.Status,
		ArtifactPaths: result.Checkpoint.ArtifactPaths,
		Evidence:      result.Checkpoint.Evidence,
		CreatedAt:     time.Now(),
	}, nil
}

func (a *RPCAcceptor) TakeMessages() []model.WorkerMessage {
	a.msgMu.Lock()
	defer a.msgMu.Unlock()
	out := append([]model.WorkerMessage(nil), a.messages...)
	a.messages = nil
	return out
}

func (a *RPCAcceptor) storeMessages(messages []protocol.WorkerMessage) {
	if len(messages) == 0 {
		return
	}
	a.msgMu.Lock()
	defer a.msgMu.Unlock()
	a.messages = a.messages[:0]
	for _, msg := range messages {
		a.messages = append(a.messages, model.WorkerMessage{
			Kind:    msg.Kind,
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
}

func (a *RPCAcceptor) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "rpc-acceptor", Role: workflow.RoleAcceptor}
}
