package secretary

// Policy encodes the Secretary's configurable rules.
// Loaded from ~/.coden/settings.json and/or CLI flags.
type Policy struct {
	// SkillPromotions maps skill names to elevated source labels.
	// e.g. {"team-rules": "user"} promotes a project skill to user trust.
	// Only "user" promotion is supported (cannot promote to builtin).
	SkillPromotions map[string]string

	// FailurePolicy determines behavior when a task fails after retries.
	// Values: "stop" (default), "skip", "replan" (future).
	FailurePolicy string

	// MaxSkillsPerWorker caps how many skills inject into one worker. Default: 5.
	MaxSkillsPerWorker int

	// MaxSkillBytesPerWorker caps total injected text per worker. Default: 20480.
	MaxSkillBytesPerWorker int

	// MaxContentBySource caps a single skill's content length by source.
	// Keys: "builtin", "user", "project", "plugin", "mcp".
	// Values: max bytes. 0 = unlimited.
	MaxContentBySource map[string]int

	// AllowMCPWrite permits mutation MCP tools from project .mcp.json.
	AllowMCPWrite bool

	// AllowPluginHooks enables Plugin lifecycle hooks.
	AllowPluginHooks bool
}

// DefaultPolicy returns a policy with sensible defaults.
func DefaultPolicy() Policy {
	return Policy{
		SkillPromotions:        make(map[string]string),
		FailurePolicy:          "stop",
		MaxSkillsPerWorker:     5,
		MaxSkillBytesPerWorker: 20480,
		MaxContentBySource: map[string]int{
			"builtin": 0,     // unlimited
			"user":    0,     // unlimited
			"project": 10240, // 10KB
			"plugin":  5120,  // 5KB
			"mcp":     2048,  // 2KB
		},
	}
}

// EffectiveTrustLevel returns the trust level for a skill, accounting
// for any promotions in the policy.
func (p *Policy) EffectiveTrustLevel(skillName string, originalSource string) (string, int) {
	if promoted, ok := p.SkillPromotions[skillName]; ok {
		// Only "user" promotion is valid (trust level 70).
		if promoted == "user" {
			return "user", 70
		}
	}
	return originalSource, sourceTrustLevel(originalSource)
}

// ResolvedFailureAction converts the policy string to a FailureAction.
func (p *Policy) ResolvedFailureAction() FailureAction {
	switch p.FailurePolicy {
	case "skip":
		return ActionSkip
	case "replan":
		return ActionReplan
	default:
		return ActionStop
	}
}

// sourceTrustLevel maps source string to numeric trust level.
func sourceTrustLevel(source string) int {
	switch source {
	case "kernel":
		return 100
	case "builtin":
		return 90
	case "user":
		return 70
	case "project":
		return 50
	case "plugin":
		return 30
	case "mcp":
		return 10
	default:
		return 0
	}
}
