package workflow

import (
	"context"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/model"
)

type Role string

const (
	RoleInput     Role = "input"
	RolePlanner   Role = "planner"
	RoleCritic    Role = "critic"       // Reviews plan quality (anti-narcissism)
	RoleSearcher  Role = "searcher"     // SA-10: Search phase as a first-class worker role
	RoleReplanner Role = "replanner"    // Refines tasks with concrete steps after discovery
	RoleCoder     Role = "coder"
	RoleAcceptor  Role = "acceptor"
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
	Critique      *model.CritiqueResult // critic feedback for replanner
	Snippets      []model.FileSnippet   // discovery snippets for replanner
}

type WorkerOutput struct {
	Intent     *model.IntentSpec
	Tasks      []model.Task
	Discovery  *model.DiscoveryContext  // populated by SearcherWorker (SA-10)
	Critique   *model.CritiqueResult    // populated by CriticWorker
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

type criticWorker struct {
	critic Critic
}

type replannerWorker struct {
	replanner Replanner
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

func NewCriticWorker(critic Critic) Worker {
	return criticWorker{critic: critic}
}

func NewReplannerWorker(replanner Replanner) Worker {
	return replannerWorker{replanner: replanner}
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

func (w criticWorker) Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error) {
	result, err := w.critic.Critique(ctx, input.WorkflowID, input.Intent, input.Tasks)
	if err != nil {
		return WorkerOutput{}, err
	}
	return WorkerOutput{
		Critique: &result,
		Messages: takeMessages(w.critic),
		Metadata: metadataFor(RoleCritic, w.critic),
	}, nil
}

func (w replannerWorker) Execute(ctx context.Context, input WorkerInput) (WorkerOutput, error) {
	tasks, err := w.replanner.RePlan(ctx, input.Intent, input.Tasks, input.Snippets)
	if err != nil {
		return WorkerOutput{}, err
	}
	return WorkerOutput{
		Tasks:    tasks,
		Messages: takeMessages(w.replanner),
		Metadata: metadataFor(RoleReplanner, w.replanner),
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
