// Package search provides the RPC boundary and standalone implementation
// for the workflow.Searcher role (SA-10).
//
// It mirrors the structure of internal/agent/plan: a Server that exposes
// any workflow.Searcher over JSON-RPC, and an RPCSearcher client that
// satisfies workflow.Searcher by talking to a remote worker. The
// subprocess binary lives in cmd/coden-agent-search.
package search

import (
	"encoding/json"

	"github.com/mingzhi1/coden/internal/core/model"
)

// SearcherInput is the payload carried in WorkerExecuteParams.Input for
// the searcher role. Op selects between the Search and Refine entry points
// of workflow.Searcher.
type SearcherInput struct {
	Op      string                   `json:"op"` // "search" or "refine"
	Intent  *model.IntentSpec        `json:"intent,omitempty"`
	Tasks   []model.Task             `json:"tasks,omitempty"`
	Current *model.DiscoveryContext  `json:"current,omitempty"`
	Hints   []string                 `json:"hints,omitempty"`
}

const (
	OpSearch = "search"
	OpRefine = "refine"
)

// MarshalInput is a small helper for callers that want to encode a SearcherInput
// without depending on the protocol package directly.
func MarshalInput(in SearcherInput) (json.RawMessage, error) {
	return json.Marshal(in)
}
