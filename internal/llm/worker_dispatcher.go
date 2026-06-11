package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/llm/prompts"
)

// LLMDispatcher is the LLM-backed workflow.Dispatcher. Given an intent it asks a
// lightweight model to design the run — the execution mode and which optional
// roles participate — and returns a WorkflowPlan. It uses SideQuery (light tier,
// low tokens) since this is a fast routing decision, not a heavy worker call.
//
// On any failure (no SideQuery support, LLM error, or unparseable output) it
// returns an error so the Engine falls back to the deterministic policy table —
// a misbehaving dispatcher can never strand a workflow.
type LLMDispatcher struct {
	chatter Chatter
}

func NewLLMDispatcher(chatter Chatter) *LLMDispatcher {
	return &LLMDispatcher{chatter: chatter}
}

func (d *LLMDispatcher) Dispatch(ctx context.Context, intent model.IntentSpec) (workflow.WorkflowPlan, error) {
	if d.chatter == nil {
		return workflow.WorkflowPlan{}, fmt.Errorf("dispatcher: no chatter configured")
	}

	// Give the dispatcher the cached project profile (languages, toolchain,
	// overview) so its routing and objectives are project-aware: it can pick the
	// real build/test command for an acceptor brief, and tailor each objective to
	// what the project actually is instead of guessing from the bare goal.
	var ub strings.Builder
	fmt.Fprintf(&ub, "Goal: %s\nKind hint: %s", strings.TrimSpace(intent.Goal), intent.Kind)
	if pp := strings.TrimSpace(model.WorkflowContextFrom(ctx).ProjectProfile); pp != "" {
		ub.WriteString("\n\n")
		ub.WriteString(pp)
	}
	user := ub.String()
	slog.Info("[dispatcher] start", "kind_hint", intent.Kind, "goal", truncForLog(intent.Goal, 80))
	t0 := time.Now()
	// Strong tier: the dispatcher designs the flow AND writes each agent's
	// objective — reasoning-heavy work where a weak model produces vague briefs
	// that fail to make downstream agents (e.g. the Analyzer) converge. Routed
	// via the broker's "dispatcher" role (in strongRoles) rather than SideQuery,
	// which is hard-wired to the light tier.
	reply, err := RecoverableChat(ctx, d.chatter, RoleDispatcher, []Message{
		{Role: "system", Content: prompts.Dispatcher()},
		{Role: "user", Content: user},
	}, defaultRecoveryConfig())
	dur := time.Since(t0)
	if err != nil {
		slog.Warn("[dispatcher] SideQuery failed; falling back to policy table",
			"err", err, "dur_ms", dur.Milliseconds())
		return workflow.WorkflowPlan{}, fmt.Errorf("dispatcher llm: %w", err)
	}

	plan, ok := parseDispatcherPlan(reply)
	if !ok {
		slog.Warn("[dispatcher] unparseable plan; falling back to policy table",
			"reply", truncForLog(reply, 200), "dur_ms", dur.Milliseconds())
		return workflow.WorkflowPlan{}, fmt.Errorf("dispatcher: unparseable plan: %q", reply)
	}
	slog.Info("[dispatcher] plan chosen",
		"mode", plan.Mode,
		"critic", plan.Has(workflow.RoleCritic),
		"replan", plan.Has(workflow.RoleReplanner),
		"executor", plan.Has(workflow.RoleExecutor),
		"acceptor", plan.Has(workflow.RoleAcceptor),
		"dur_ms", dur.Milliseconds())
	return plan, nil
}

var _ workflow.Dispatcher = (*LLMDispatcher)(nil)

// parseDispatcherPlan converts the model's JSON reply into a sanitized
// WorkflowPlan. It returns ok=false on an unknown/missing mode so the caller can
// fall back to the policy table. Within execute mode, omitted flags default to
// the conventional careful flow (all stages on); a flag is only dropped when the
// model explicitly sets it false.
func parseDispatcherPlan(reply string) (workflow.WorkflowPlan, bool) {
	var raw struct {
		Mode         string            `json:"mode"`
		Critic       *bool             `json:"critic"`
		Replan       *bool             `json:"replan"`
		Executor     *bool             `json:"executor"`
		Accept       *bool             `json:"accept"`
		ExecutorMode string            `json:"executor_mode"`
		Objectives   map[string]string `json:"objectives"`
	}
	if err := json.Unmarshal([]byte(extractJSON(reply)), &raw); err != nil {
		return workflow.WorkflowPlan{}, false
	}

	objectives := sanitizeObjectives(raw.Objectives)

	switch strings.TrimSpace(raw.Mode) {
	case string(workflow.WorkflowModeAnswer):
		return workflow.WorkflowPlan{Mode: workflow.WorkflowModeAnswer}, true
	case string(workflow.WorkflowModeAnalyze):
		plan := workflow.WorkflowPlan{Mode: workflow.WorkflowModeAnalyze, Objectives: objectives}
		return pruneObjectives(plan), true
	case string(workflow.WorkflowModeExecute):
		executor := boolOr(raw.Executor, true)
		roles := map[workflow.Role]bool{workflow.RolePlanner: true}
		if boolOr(raw.Critic, true) {
			roles[workflow.RoleCritic] = true
		}
		if boolOr(raw.Replan, true) {
			roles[workflow.RoleReplanner] = true
		}
		if executor {
			roles[workflow.RoleExecutor] = true
			// Accept only makes sense when there is produced code to verify.
			if boolOr(raw.Accept, true) {
				roles[workflow.RoleAcceptor] = true
			}
		}
		executorMode := model.ExecutorModeReadWrite
		if strings.EqualFold(strings.TrimSpace(raw.ExecutorMode), "readonly") {
			executorMode = model.ExecutorModeReadOnly
		}
		plan := workflow.WorkflowPlan{Mode: workflow.WorkflowModeExecute, Roles: roles, ExecutorMode: executorMode, Objectives: objectives}
		return pruneObjectives(plan), true
	default:
		return workflow.WorkflowPlan{}, false
	}
}

// pruneObjectives drops any objective the dispatcher wrote for a role that won't
// run under this plan — e.g. a "executor" brief when executor=false, or an "analyzer"
// brief in execute mode. The model occasionally over-specifies; without this a
// stray brief would sit in the plan and (if a future code path read it) leak
// into an agent outside the flow. Mutates and returns the same plan for chaining.
func pruneObjectives(plan workflow.WorkflowPlan) workflow.WorkflowPlan {
	for role := range plan.Objectives {
		if !plan.Participates(role) {
			delete(plan.Objectives, role)
		}
	}
	if len(plan.Objectives) == 0 {
		plan.Objectives = nil
	}
	return plan
}

// sanitizeObjectives keeps only known roles with non-empty briefs and caps each
// brief's length so a verbose model can't bloat the per-role purpose.
func sanitizeObjectives(raw map[string]string) map[workflow.Role]string {
	if len(raw) == 0 {
		return nil
	}
	const maxObjectiveChars = 600
	known := map[string]workflow.Role{
		string(workflow.RoleAnalyzer):  workflow.RoleAnalyzer,
		string(workflow.RolePlanner):   workflow.RolePlanner,
		string(workflow.RoleExecutor):  workflow.RoleExecutor,
		string(workflow.RoleCritic):    workflow.RoleCritic,
		string(workflow.RoleReplanner): workflow.RoleReplanner,
		string(workflow.RoleAcceptor):  workflow.RoleAcceptor,
	}
	out := make(map[workflow.Role]string)
	for k, v := range raw {
		role, ok := known[strings.ToLower(strings.TrimSpace(k))]
		v = strings.TrimSpace(v)
		if !ok || v == "" {
			continue
		}
		if len(v) > maxObjectiveChars {
			v = v[:maxObjectiveChars]
		}
		out[role] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func truncForLog(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
