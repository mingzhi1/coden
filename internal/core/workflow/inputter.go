package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

// Inputter is the prompt-to-intent boundary used by the workflow engine.
type Inputter interface {
	Build(ctx context.Context, sessionID, prompt string) (model.IntentSpec, error)
}

// LocalInputter provides the built-in fallback input worker.
type LocalInputter struct{}

var intentIDSeq atomic.Uint64

func NewLocalInputter() *LocalInputter {
	return &LocalInputter{}
}

func (i *LocalInputter) Build(_ context.Context, sessionID, prompt string) (model.IntentSpec, error) {
	return model.IntentSpec{
		ID:        fmt.Sprintf("intent-%d-%d", time.Now().UTC().UnixMilli(), intentIDSeq.Add(1)),
		SessionID: sessionID,
		Goal:      strings.TrimSpace(prompt),
		SuccessCriteria: []string{
			"artifact is generated inside workspace/artifacts",
			"checkpoint contains evidence",
		},
		CreatedAt: time.Now(),
	}, nil
}

func (i *LocalInputter) Metadata() WorkerMetadata {
	return WorkerMetadata{Worker: "local-input", Role: RoleInput}
}
