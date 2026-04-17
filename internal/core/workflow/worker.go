package workflow

import (
	"context"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/model"
)

type Role string

const (
	RoleInput    Role = "input"
	RolePlanner  Role = "planner"
	RoleCoder    Role = "coder"
	RoleAcceptor Role = "acceptor"
)

type WorkerMetadata struct {
	Worker string
	Role   Role
}

type WorkerInput struct {
	SessionID     string
	WorkflowID    string
	TaskID        string
	Prompt        string
	Intent        model.IntentSpec
	Tasks         []model.Task
	Artifact      model.Artifact
	RetryFeedback string // non-empty when retrying after acceptor rejection
}

type WorkerOutput struct {
	Intent     *model.IntentSpec
	Tasks      []model.Task
	CodePlan   *CodePlan
	Checkpoint *model.CheckpointResult
	Messages   []model.WorkerMessage
	Metadata   WorkerMetadata
}

type Worker interface {
	Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error)
}

type messageSource interface {
	TakeMessages() []model.WorkerMessage
}

type metadataSource interface {
	Metadata() WorkerMetadata
}

type inputterWorker struct {
	inputter Inputter
}

type plannerWorker struct {
	planner Planner
}

type coderWorker struct {
	coder Coder
}

type acceptorWorker struct {
	acceptor Acceptor
}

func NewInputterWorker(inputter Inputter) Worker {
	return inputterWorker{inputter: inputter}
}

func NewPlannerWorker(planner Planner) Worker {
	return plannerWorker{planner: planner}
}

func NewCoderWorker(coder Coder) Worker {
	return coderWorker{coder: coder}
}

func NewAcceptorWorker(acceptor Acceptor) Worker {
	return acceptorWorker{acceptor: acceptor}
}

func (w inputterWorker) Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error) {
	intent, err := w.inputter.Build(ctx, input.SessionID, input.Prompt)
	if err != nil {
		return WorkerOutput{}, err
	}
	return WorkerOutput{
		Intent:   &intent,
		Messages: takeMessages(w.inputter),
		Metadata: metadataFor(RoleInput, w.inputter),
	}, nil
}

func (w plannerWorker) Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error) {
	tasks, err := w.planner.Plan(ctx, input.WorkflowID, input.Intent)
	if err != nil {
		return WorkerOutput{}, err
	}
	return WorkerOutput{
		Tasks:    tasks,
		Messages: takeMessages(w.planner),
		Metadata: metadataFor(RolePlanner, w.planner),
	}, nil
}

func (w coderWorker) Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error) {
	plan, err := w.coder.Build(ctx, input.WorkflowID, input.Intent, input.Tasks)
	if err != nil {
		return WorkerOutput{}, err
	}
	return WorkerOutput{
		CodePlan: &plan,
		Messages: takeMessages(w.coder),
		Metadata: metadataFor(RoleCoder, w.coder),
	}, nil
}

func (w acceptorWorker) Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error) {
	checkpoint, err := w.acceptor.Accept(ctx, input.WorkflowID, input.Intent, input.Artifact)
	if err != nil {
		return WorkerOutput{}, err
	}
	return WorkerOutput{
		Checkpoint: &checkpoint,
		Messages:   takeMessages(w.acceptor),
		Metadata:   metadataFor(RoleAcceptor, w.acceptor),
	}, nil
}

func takeMessages(source any) []model.WorkerMessage {
	msgSource, ok := source.(messageSource)
	if !ok {
		return nil
	}
	return msgSource.TakeMessages()
}

func metadataFor(role Role, source any) WorkerMetadata {
	if metaSource, ok := source.(metadataSource); ok {
		meta := metaSource.Metadata()
		if meta.Role == "" {
			meta.Role = role
		}
		if meta.Worker == "" {
			meta.Worker = fmt.Sprintf("local-%s", role)
		}
		return meta
	}
	return WorkerMetadata{
		Worker: fmt.Sprintf("local-%s", role),
		Role:   role,
	}
}
