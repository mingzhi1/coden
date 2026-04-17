package protocol

// IntentGetParams selects the latest intent for a session.
type IntentGetParams struct {
	SessionID string `json:"session_id"`
}

// IntentGetResult carries the latest intent spec.
type IntentGetResult struct {
	IntentID        string   `json:"intent_id"`
	SessionID       string   `json:"session_id"`
	Goal            string   `json:"goal"`
	Kind            string   `json:"kind,omitempty"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
	CreatedAt       int64    `json:"created_at"`
}

// WorkspaceReadParams requests reading a file from workspace.
type WorkspaceReadParams struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
}

// WorkspaceReadResult returns the file content.
type WorkspaceReadResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WorkspaceWriteParams requests writing a file to workspace.
type WorkspaceWriteParams struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

// WorkspaceWriteResult reports the outcome of a workspace write.
type WorkspaceWriteResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// WorkspaceDiffParams requests the diff of workspace changes.
type WorkspaceDiffParams struct {
	SessionID string `json:"session_id"`
}

// FileDiff describes a single file diff entry.
type FileDiff struct {
	WorkflowID string `json:"workflow_id"`
	Path       string `json:"path"`
	Operation  string `json:"operation"`
}

// WorkspaceDiffResult returns the list of diffs.
type WorkspaceDiffResult struct {
	SessionID string     `json:"session_id"`
	Diffs     []FileDiff `json:"diffs,omitempty"`
}
