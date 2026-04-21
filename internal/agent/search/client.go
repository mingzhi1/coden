package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	rpcclient "github.com/mingzhi1/coden/internal/rpc/client"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

// RPCSearcher is a workflow.Searcher backed by a JSON-RPC worker.
type RPCSearcher struct {
	client *rpcclient.Client
}

func NewRPCSearcher(rwc io.ReadWriteCloser) *RPCSearcher {
	return &RPCSearcher{client: rpcclient.New(rwc)}
}

func (s *RPCSearcher) Close() error {
	return s.client.Close()
}

func (s *RPCSearcher) Describe(ctx context.Context) (protocol.WorkerDescribeResult, error) {
	return s.client.DescribeWorker(ctx)
}

func (s *RPCSearcher) Search(ctx context.Context, intent model.IntentSpec, tasks []model.Task) (model.DiscoveryContext, error) {
	return s.execute(ctx, SearcherInput{
		Op:     OpSearch,
		Intent: &intent,
		Tasks:  tasks,
	}, intent.SessionID)
}

func (s *RPCSearcher) Refine(ctx context.Context, current model.DiscoveryContext, hints []string) (model.DiscoveryContext, error) {
	return s.execute(ctx, SearcherInput{
		Op:      OpRefine,
		Current: &current,
		Hints:   hints,
	}, "")
}

func (s *RPCSearcher) execute(ctx context.Context, in SearcherInput, sessionID string) (model.DiscoveryContext, error) {
	input, err := json.Marshal(in)
	if err != nil {
		return model.DiscoveryContext{}, fmt.Errorf("marshal searcher input: %w", err)
	}
	result, err := s.client.ExecuteWorker(ctx, protocol.WorkerExecuteParams{
		SessionID: sessionID,
		TaskID:    "task-search",
		Role:      string(workflow.RoleSearcher),
		Input:     input,
	})
	if err != nil {
		return model.DiscoveryContext{}, err
	}
	if len(result.Discovery) == 0 {
		return model.DiscoveryContext{}, nil
	}
	var dc model.DiscoveryContext
	if err := json.Unmarshal(result.Discovery, &dc); err != nil {
		return model.DiscoveryContext{}, fmt.Errorf("unmarshal discovery: %w", err)
	}
	return dc, nil
}
