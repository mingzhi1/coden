package protocol

import "time"

// HookListParams filters hooks by point. Empty Point returns all.
type HookListParams struct {
	Point string `json:"point,omitempty"`
}

// HookInfo is the RPC representation of a hook configuration.
type HookInfo struct {
	Name     string            `json:"name"`
	Point    string            `json:"point"`
	Command  string            `json:"command"`
	Blocking bool              `json:"blocking"`
	Timeout  time.Duration     `json:"timeout"`
	Env      map[string]string `json:"env,omitempty"`
	Source   string            `json:"source"`
	Priority int              `json:"priority"`
}

// HookListResult is the response for hook.list.
type HookListResult struct {
	Hooks []HookInfo `json:"hooks"`
}

// HookRegisterParams registers a new hook via RPC.
type HookRegisterParams struct {
	Name     string            `json:"name"`
	Point    string            `json:"point"`
	Command  string            `json:"command"`
	Blocking bool              `json:"blocking"`
	Timeout  string            `json:"timeout,omitempty"` // e.g. "30s"
	Env      map[string]string `json:"env,omitempty"`
	Priority int              `json:"priority,omitempty"`
}

// HookRemoveParams removes a hook by name.
type HookRemoveParams struct {
	Name string `json:"name"`
}

// HookRemoveResult is the response for hook.remove.
type HookRemoveResult struct {
	Removed bool `json:"removed"`
}
