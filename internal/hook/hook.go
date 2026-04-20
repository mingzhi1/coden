// Package hook provides a unified hook framework for the CodeN workflow engine.
// Hooks are user-configurable shell commands that execute at specific points
// in the workflow lifecycle. They can be registered via config files or RPC.
package hook

import (
	"fmt"
	"strings"
	"time"
)

// Point defines a hook trigger point in the workflow lifecycle.
type Point string

const (
	PreIntent   Point = "pre_intent"
	PostIntent  Point = "post_intent"
	PostPlan    Point = "post_plan"
	PreCode     Point = "pre_code"
	PostCode    Point = "post_code"
	PreToolUse  Point = "pre_tool_use"
	PostToolUse Point = "post_tool_use"
	PostAccept  Point = "post_accept"
	PostWorkflow Point = "post_workflow"
)

// AllPoints enumerates every valid hook point.
var AllPoints = []Point{
	PreIntent, PostIntent, PostPlan,
	PreCode, PostCode,
	PreToolUse, PostToolUse,
	PostAccept, PostWorkflow,
}

// ValidPoint reports whether p is a recognized hook point.
func ValidPoint(p Point) bool {
	for _, ap := range AllPoints {
		if ap == p {
			return true
		}
	}
	return false
}

// Verdict is the outcome decision of a hook execution.
type Verdict string

const (
	VerdictContinue Verdict = "continue"
	VerdictBlock    Verdict = "block"
)

// Config describes a single registered hook.
type Config struct {
	Name     string            `json:"name"`
	Point    Point             `json:"point"`
	Command  string            `json:"command"`
	Blocking bool              `json:"blocking"`
	Timeout  time.Duration     `json:"timeout"`
	Env      map[string]string `json:"env,omitempty"`
	Source   string            `json:"source"`   // "config" | "rpc" | "plugin"
	Priority int              `json:"priority"` // lower runs first
}

// Result captures the outcome of a single hook execution.
type Result struct {
	Name     string        `json:"name"`
	Point    Point         `json:"point"`
	Verdict  Verdict       `json:"verdict"`
	Output   string        `json:"output,omitempty"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

// Context carries data passed to hook executors via environment variables.
type Context struct {
	SessionID     string
	WorkflowID    string
	WorkspaceRoot string

	// Intent phase
	Prompt string // PreIntent: raw user input

	// Task phase
	TaskID    string
	TaskTitle string
	Attempt   int

	// Tool phase
	ToolName  string // PreToolUse/PostToolUse
	ToolInput string // PreToolUse

	// Post-code / post-accept / post-workflow
	ChangedFiles []string
	FinalStatus  string // PostWorkflow: "pass" | "fail"
}

// ToEnv converts the context to CODEN_HOOK_* environment variables.
func (c *Context) ToEnv() []string {
	env := []string{
		"CODEN_HOOK_SESSION_ID=" + c.SessionID,
		"CODEN_HOOK_WORKFLOW_ID=" + c.WorkflowID,
		"CODEN_HOOK_WORKSPACE=" + c.WorkspaceRoot,
	}
	if c.Prompt != "" {
		env = append(env, "CODEN_HOOK_PROMPT="+c.Prompt)
	}
	if c.TaskID != "" {
		env = append(env, "CODEN_HOOK_TASK_ID="+c.TaskID)
		env = append(env, "CODEN_HOOK_TASK_TITLE="+c.TaskTitle)
	}
	if c.ToolName != "" {
		env = append(env, "CODEN_HOOK_TOOL_NAME="+c.ToolName)
	}
	if c.Attempt > 0 {
		env = append(env, fmt.Sprintf("CODEN_HOOK_ATTEMPT=%d", c.Attempt))
	}
	if c.ToolInput != "" {
		env = append(env, "CODEN_HOOK_TOOL_INPUT="+c.ToolInput)
	}
	if c.FinalStatus != "" {
		env = append(env, "CODEN_HOOK_STATUS="+c.FinalStatus)
	}
	if len(c.ChangedFiles) > 0 {
		env = append(env, "CODEN_HOOK_CHANGED_FILES="+strings.Join(c.ChangedFiles, "\n"))
	}
	return env
}

// HasBlockingFailure reports whether any result has VerdictBlock.
func HasBlockingFailure(results []Result) bool {
	for _, r := range results {
		if r.Verdict == VerdictBlock {
			return true
		}
	}
	return false
}

// FormatBlockingErrors formats all blocking failures into a single string
// suitable for injection into the LLM conversation as retry feedback.
func FormatBlockingErrors(results []Result) string {
	var sb strings.Builder
	first := true
	for _, r := range results {
		if r.Verdict != VerdictBlock {
			continue
		}
		if !first {
			sb.WriteString("\n\n")
		}
		first = false
		sb.WriteString("Hook '")
		sb.WriteString(r.Name)
		sb.WriteString("' failed:\n")
		if r.Output != "" {
			sb.WriteString(r.Output)
		} else if r.Error != "" {
			sb.WriteString(r.Error)
		}
	}
	return sb.String()
}
