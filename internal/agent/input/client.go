package input

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

type buildInput struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
}

// RPCInputter is a workflow inputter backed by a JSON-RPC worker.
type RPCInputter struct {
	client   *rpcclient.Client
	msgMu    sync.Mutex
	messages []model.WorkerMessage
}

func NewRPCInputter(rwc io.ReadWriteCloser) *RPCInputter {
	return &RPCInputter{client: rpcclient.New(rwc)}
}

func (i *RPCInputter) Close() error {
	return i.client.Close()
}

func (i *RPCInputter) Describe(ctx context.Context) (protocol.WorkerDescribeResult, error) {
	return i.client.DescribeWorker(ctx)
}

func (i *RPCInputter) Build(ctx context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	input, err := protocol.MarshalRaw(buildInput{
		SessionID: sessionID,
		Prompt:    prompt,
	})
	if err != nil {
		return model.IntentSpec{}, err
	}

	result, err := i.client.ExecuteWorker(ctx, protocol.WorkerExecuteParams{
		SessionID:  sessionID,
		WorkflowID: "",
		TaskID:     "task-input",
		Role:       string(workflow.RoleInput),
		Input:      input,
	})
	if err != nil {
		return model.IntentSpec{}, err
	}
	if result.Intent == nil {
		return model.IntentSpec{}, io.ErrUnexpectedEOF
	}
	i.storeMessages(result.Messages)

	return model.IntentSpec{
		ID:              result.Intent.ID,
		SessionID:       sessionID,
		Goal:            result.Intent.Goal,
		SuccessCriteria: result.Intent.SuccessCriteria,
		CreatedAt:       time.Now(),
	}, nil
}

func (i *RPCInputter) TakeMessages() []model.WorkerMessage {
	i.msgMu.Lock()
	defer i.msgMu.Unlock()
	out := append([]model.WorkerMessage(nil), i.messages...)
	i.messages = nil
	return out
}

func (i *RPCInputter) storeMessages(messages []protocol.WorkerMessage) {
	if len(messages) == 0 {
		return
	}
	i.msgMu.Lock()
	defer i.msgMu.Unlock()
	i.messages = i.messages[:0]
	for _, msg := range messages {
		i.messages = append(i.messages, model.WorkerMessage{
			Kind:    msg.Kind,
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
}

var _ workflow.Inputter = (*RPCInputter)(nil)

func (i *RPCInputter) Metadata() workflow.WorkerMetadata {
	return workflow.WorkerMetadata{Worker: "rpc-input", Role: workflow.RoleInput}
}
