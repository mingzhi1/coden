package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// builtinClaudeTools are the tools the `claude` CLI exposes by default. Passing
// them to --disallowed-tools as BARE names removes each tool from the model's
// context entirely. With no tools to call, Claude Code's agent loop collapses
// into a single-turn text/JSON completion — exactly the "pure brain" role coden
// needs. This is what ACP could NOT do: there, the agent emits tool_calls from
// its built-in toolset regardless of client capabilities, and coden loops
// declining them. Here the tools simply do not exist for the model.
var builtinClaudeTools = []string{
	"Bash", "BashOutput", "KillShell", "Read", "Edit", "Write", "NotebookEdit",
	"Glob", "Grep", "WebSearch", "WebFetch", "Task", "TodoWrite", "SlashCommand",
}

// ClaudeCLI drives the official `claude -p` (headless) CLI as a pure completion
// provider, reusing the local Claude Code subscription login (no API key). coden
// stays the orchestrator and runs its own executor/tools; Claude only reasons.
//
// Each Chat() spawns `claude -p --output-format json --permission-mode dontAsk
// --disallowed-tools <all>` with the system prompt via --system-prompt and the
// conversation on stdin, then parses the .result field from the JSON envelope.
type ClaudeCLI struct {
	providerName string
	command      string
	args         []string
	env          map[string]string
	cwd          string
}

// ClaudeCLIConfig holds claude-cli provider configuration.
type ClaudeCLIConfig struct {
	Name    string            // provider display name
	Command string            // executable (default "claude")
	Args    []string          // additional CLI arguments
	Env     map[string]string // extra environment variables
	CWD     string            // working directory for the claude process
}

// NewClaudeCLI creates a `claude -p`-backed ChatProvider.
func NewClaudeCLI(cfg ClaudeCLIConfig) *ClaudeCLI {
	name := cfg.Name
	if name == "" {
		name = "claude-cli"
	}
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		command = "claude"
	}
	return &ClaudeCLI{
		providerName: name,
		command:      command,
		args:         cfg.Args,
		env:          cfg.Env,
		cwd:          cfg.CWD,
	}
}

func (p *ClaudeCLI) Name() string       { return p.providerName }
func (p *ClaudeCLI) IsConfigured() bool { return p.command != "" }

// claudeResult is the envelope emitted by `claude -p --output-format json`.
type claudeResult struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	NumTurns  int    `json:"num_turns"`
	SessionID string `json:"session_id"`
	Usage     struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Chat runs a single-turn `claude -p` invocation and returns the assembled text.
func (p *ClaudeCLI) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	start := time.Now()

	// Split system messages (→ --system-prompt) from the conversation (→ stdin).
	var system []string
	var convo strings.Builder
	for _, m := range messages {
		switch m.Role {
		case "system":
			system = append(system, m.Content)
		case "assistant":
			convo.WriteString("Assistant: ")
			convo.WriteString(m.Content)
			convo.WriteString("\n")
		default: // user and anything else
			convo.WriteString(m.Content)
			convo.WriteString("\n")
		}
	}

	args := []string{
		"-p",
		"--output-format", "json",
		"--permission-mode", "dontAsk",
		"--disallowed-tools",
	}
	args = append(args, builtinClaudeTools...)
	if sys := strings.TrimSpace(strings.Join(system, "\n\n")); sys != "" {
		args = append(args, "--system-prompt", sys)
	}
	if m := strings.TrimSpace(model); m != "" {
		args = append(args, "--model", m)
	}
	args = append(args, p.args...)

	cmd := exec.CommandContext(ctx, p.command, args...)
	cmd.Stdin = strings.NewReader(convo.String())
	if dir := strings.TrimSpace(p.cwd); dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = p.buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("claude-cli(%s): %w", p.providerName, ctx.Err())
		}
		return "", fmt.Errorf("claude-cli(%s): run failed: %w (stderr: %s)",
			p.providerName, err, truncate(stderr.String(), 400))
	}

	var res claudeResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &res); err != nil {
		return "", fmt.Errorf("claude-cli(%s): parse output: %w (raw: %s)",
			p.providerName, err, truncate(stdout.String(), 400))
	}
	if res.IsError || (res.Subtype != "" && res.Subtype != "success") {
		return "", fmt.Errorf("claude-cli(%s): agent error (subtype=%s): %s",
			p.providerName, res.Subtype, truncate(res.Result, 400))
	}

	reply := strings.TrimSpace(res.Result)
	if reply == "" {
		return "", fmt.Errorf("claude-cli(%s): empty response", p.providerName)
	}

	slog.Info("[claude-cli] response received", "name", p.providerName,
		"duration_ms", time.Since(start).Milliseconds(),
		"input_tokens", res.Usage.InputTokens, "output_tokens", res.Usage.OutputTokens,
		"num_turns", res.NumTurns, "reply_len", len(reply))
	return reply, nil
}

// buildEnv returns the child environment. It drops ANTHROPIC_API_KEY so the CLI
// authenticates via the Claude Code subscription login (in -p mode an API key,
// if present, always wins — defeating the whole point of this provider). It also
// strips CLAUDECODE so a coden launched from inside a Claude Code session can
// still spawn the CLI (it refuses to start nested otherwise).
func (p *ClaudeCLI) buildEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(p.env))
	for _, e := range base {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") || strings.HasPrefix(e, "CLAUDECODE=") {
			continue
		}
		out = append(out, e)
	}
	for k, v := range p.env {
		out = append(out, k+"="+v)
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
