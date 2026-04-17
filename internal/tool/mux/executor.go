package mux

import (
	"context"
	"fmt"

	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

type Executor struct {
	defaultExecutor toolruntime.Executor
	byKind          map[string]toolruntime.Executor
}

func New(defaultExecutor toolruntime.Executor, byKind map[string]toolruntime.Executor) *Executor {
	copied := make(map[string]toolruntime.Executor, len(byKind))
	for kind, executor := range byKind {
		copied[kind] = executor
	}
	return &Executor{
		defaultExecutor: defaultExecutor,
		byKind:          copied,
	}
}

func (e *Executor) Execute(ctx context.Context, req toolruntime.Request) (toolruntime.Result, error) {
	if executor, ok := e.byKind[req.Kind]; ok && executor != nil {
		return executor.Execute(ctx, req)
	}
	if e.defaultExecutor == nil {
		return toolruntime.Result{}, fmt.Errorf("unsupported tool request: %s", req.Kind)
	}
	return e.defaultExecutor.Execute(ctx, req)
}
