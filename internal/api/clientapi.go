package api

import (
	"context"
	"encoding/json"

	"github.com/mingzhi1/coden/internal/core/kernel"
	"github.com/mingzhi1/coden/internal/core/model"
)

// ClientAPI is the command boundary for CodeN, inspired by Neovim's API layer.
//
// Two implementations exist:
//   - Service:     in-process, calls kernel directly (MVP default)
//   - rpc.Client:  over JSON-RPC 2.0, for when kernel runs as a server
//
// The kernel should not care whether callers are local or remote.
type ClientAPI interface {
	CreateSession(ctx context.Context, sessionID string) (model.Session, error)
	ListSessions(ctx context.Context, limit int) ([]model.Session, error)
	RenameSession(ctx context.Context, sessionID, name string) (model.Session, error)
	Attach(ctx context.Context, sessionID, clientName, view string) error
	Detach(ctx context.Context, sessionID, clientName string) error
	// Submit enqueues a workflow and returns the workflowID immediately.
	// The final CheckpointResult is delivered via a checkpoint.updated event.
	Submit(ctx context.Context, sessionID, prompt string) (string, error)
	CancelWorkflow(ctx context.Context, sessionID, workflowID string) error
	GetWorkflowRun(ctx context.Context, sessionID, workflowID string) (model.WorkflowRun, error)
	ListWorkflowRuns(ctx context.Context, sessionID string, limit int) ([]model.WorkflowRun, error)
	ListWorkflowRunObjects(ctx context.Context, sessionID, workflowID string) ([]model.Object, error)
	ReadWorkflowRunObject(ctx context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error)
	ListMessages(ctx context.Context, sessionID string, limit int) ([]model.Message, error)
	GetLatestIntent(ctx context.Context, sessionID string) (model.IntentSpec, error)
	WorkspaceChanges(ctx context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error)
	WorkspaceRead(ctx context.Context, sessionID, path string) ([]byte, error)
	WorkspaceWrite(ctx context.Context, sessionID, path string, content []byte) (string, error)
	GetCheckpoint(ctx context.Context, sessionID, workflowID string) (model.CheckpointResult, error)
	ListCheckpoints(ctx context.Context, sessionID string, limit int) ([]model.CheckpointResult, error)
	GetWorkflowWorkers(ctx context.Context, sessionID, workflowID string) ([]model.WorkerState, error)
	// M11-05: Task management — skip a task or undo the last task operation.
	SkipTask(ctx context.Context, sessionID, taskID string) error
	UndoTask(ctx context.Context, sessionID string) (string, error)
	// SessionSnapshot returns an atomic snapshot of session state, paired with
	// LastEventSeq for zero-gap event replay (R-01/R-02).
	SessionSnapshot(ctx context.Context, sessionID string, messageLimit int) (model.SessionSnapshot, error)
	Subscribe(ctx context.Context, sessionID string) (<-chan model.Event, func(), error)
}

// Service is the in-process implementation of ClientAPI.
type Service struct {
	kernel *kernel.Kernel
}

func New(k *kernel.Kernel) *Service {
	return &Service{kernel: k}
}

func (s *Service) CreateSession(ctx context.Context, sessionID string) (model.Session, error) {
	return s.kernel.CreateSession(ctx, sessionID)
}

func (s *Service) ListSessions(ctx context.Context, limit int) ([]model.Session, error) {
	return s.kernel.ListSessions(ctx, limit)
}

func (s *Service) Attach(ctx context.Context, sessionID, clientName, view string) error {
	return s.kernel.Attach(ctx, sessionID, clientName, view)
}

func (s *Service) Detach(ctx context.Context, sessionID, clientName string) error {
	return s.kernel.Detach(ctx, sessionID, clientName)
}

func (s *Service) Submit(ctx context.Context, sessionID, prompt string) (string, error) {
	return s.kernel.Submit(ctx, sessionID, prompt)
}

func (s *Service) CancelWorkflow(ctx context.Context, sessionID, workflowID string) error {
	return s.kernel.CancelWorkflow(ctx, sessionID, workflowID)
}

func (s *Service) GetWorkflowRun(ctx context.Context, sessionID, workflowID string) (model.WorkflowRun, error) {
	return s.kernel.GetWorkflowRun(ctx, sessionID, workflowID)
}

func (s *Service) ListWorkflowRuns(ctx context.Context, sessionID string, limit int) ([]model.WorkflowRun, error) {
	return s.kernel.ListWorkflowRuns(ctx, sessionID, limit)
}

func (s *Service) ListWorkflowRunObjects(ctx context.Context, sessionID, workflowID string) ([]model.Object, error) {
	return s.kernel.ListWorkflowRunObjects(ctx, sessionID, workflowID)
}

func (s *Service) ReadWorkflowRunObject(ctx context.Context, sessionID, workflowID, objectID string) (json.RawMessage, error) {
	return s.kernel.ReadWorkflowRunObject(ctx, sessionID, workflowID, objectID)
}

func (s *Service) ListMessages(ctx context.Context, sessionID string, limit int) ([]model.Message, error) {
	return s.kernel.ListMessages(ctx, sessionID, limit)
}

func (s *Service) GetLatestIntent(ctx context.Context, sessionID string) (model.IntentSpec, error) {
	return s.kernel.GetLatestIntent(ctx, sessionID)
}

func (s *Service) WorkspaceChanges(ctx context.Context, sessionID string) ([]model.WorkspaceChangedPayload, error) {
	return s.kernel.WorkspaceChanges(ctx, sessionID)
}

func (s *Service) WorkspaceRead(ctx context.Context, sessionID, path string) ([]byte, error) {
	return s.kernel.WorkspaceRead(ctx, sessionID, path)
}

func (s *Service) WorkspaceWrite(ctx context.Context, sessionID, path string, content []byte) (string, error) {
	return s.kernel.WorkspaceWrite(ctx, sessionID, path, content)
}

func (s *Service) GetCheckpoint(ctx context.Context, sessionID, workflowID string) (model.CheckpointResult, error) {
	return s.kernel.GetCheckpoint(ctx, sessionID, workflowID)
}

func (s *Service) ListCheckpoints(ctx context.Context, sessionID string, limit int) ([]model.CheckpointResult, error) {
	return s.kernel.ListCheckpoints(ctx, sessionID, limit)
}

func (s *Service) Subscribe(_ context.Context, sessionID string) (<-chan model.Event, func(), error) {
	events, cancel := s.kernel.Subscribe(sessionID)
	return events, cancel, nil
}

func (s *Service) SessionSnapshot(ctx context.Context, sessionID string, messageLimit int) (model.SessionSnapshot, error) {
	return s.kernel.Snapshot(ctx, sessionID, messageLimit)
}

func (s *Service) GetWorkflowWorkers(ctx context.Context, sessionID, workflowID string) ([]model.WorkerState, error) {
	return s.kernel.GetWorkflowWorkers(ctx, sessionID, workflowID)
}

func (s *Service) SkipTask(ctx context.Context, sessionID, taskID string) error {
	return s.kernel.SkipTask(ctx, sessionID, taskID)
}

func (s *Service) UndoTask(ctx context.Context, sessionID string) (string, error) {
	return s.kernel.UndoTask(ctx, sessionID)
}

func (s *Service) RenameSession(ctx context.Context, sessionID, name string) (model.Session, error) {
	return s.kernel.RenameSession(ctx, sessionID, name)
}
