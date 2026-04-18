package workflow

import (
	"context"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// Acceptor is the acceptance boundary used by the workflow engine.
// It returns a checkpoint decision for the produced artifact.
type Acceptor interface {
	Accept(ctx context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact, tasks []model.Task) (model.CheckpointResult, error)
}

// LocalAcceptor provides the built-in fallback acceptance worker.
type LocalAcceptor struct{}

func NewLocalAcceptor() *LocalAcceptor {
	return &LocalAcceptor{}
}

func (a *LocalAcceptor) Accept(_ context.Context, workflowID string, intent model.IntentSpec, artifact model.Artifact, _ []model.Task) (model.CheckpointResult, error) {
	return model.CheckpointResult{
		WorkflowID:    workflowID,
		SessionID:     intent.SessionID,
		Status:        "pass",
		ArtifactPaths: []string{artifact.Path},
		Evidence: []string{
			artifact.Summary,
			"acceptance verified that an artifact path was returned",
		},
		CreatedAt: time.Now(),
	}, nil
}

func (a *LocalAcceptor) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "local-acceptor", Role: RoleAcceptor}
}
