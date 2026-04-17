package kernel

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/mingzhi1/coden/internal/config"
	"github.com/mingzhi1/coden/internal/core/artifact"
	"github.com/mingzhi1/coden/internal/core/checkpoint"
	"github.com/mingzhi1/coden/internal/core/events"
	"github.com/mingzhi1/coden/internal/core/gitstate"
	"github.com/mingzhi1/coden/internal/core/insight"
	"github.com/mingzhi1/coden/internal/core/intent"
	"github.com/mingzhi1/coden/internal/core/message"
	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/objectstore"
	"github.com/mingzhi1/coden/internal/core/session"
	"github.com/mingzhi1/coden/internal/core/storagepath"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
	"github.com/mingzhi1/coden/internal/core/turn"
	"github.com/mingzhi1/coden/internal/core/turnsummary"
	"github.com/mingzhi1/coden/internal/core/workflow"
	"github.com/mingzhi1/coden/internal/core/workspace"
	"github.com/mingzhi1/coden/internal/core/workspacestore"
	"github.com/mingzhi1/coden/internal/plugin"
	"github.com/mingzhi1/coden/internal/secretary"
	"github.com/mingzhi1/coden/internal/skill"
	"github.com/mingzhi1/coden/internal/tool/inventory"
)

// busEmitAdapter wraps *events.Bus to satisfy secretary.EventEmitter.
// events.Bus.Emit returns model.Event; the interface expects no return value.
type busEmitAdapter struct{ bus *events.Bus }

func (a busEmitAdapter) Emit(sessionID, topic string, payload any) {
	a.bus.Emit(sessionID, topic, payload)
}

// New 创建使用内存存储的新 Kernel。
func New(workspaceRoot string) *Kernel {
	return NewWithDependencies(workspaceRoot, nil, nil, nil)
}

// NewWithPlanner 创建带自定义 planner 的新 Kernel。
func NewWithPlanner(workspaceRoot string, planner workflow.Planner) *Kernel {
	return NewWithDependencies(workspaceRoot, planner, nil, nil)
}

// NewWithDependencies 创建带自定义依赖的新 Kernel。
func NewWithDependencies(workspaceRoot string, planner workflow.Planner, coder workflow.Coder, executor toolruntime.Executor, acceptor ...workflow.Acceptor) *Kernel {
	return NewWithWorkflowDependencies(workspaceRoot, nil, planner, coder, executor, acceptor...)
}

// NewWithWorkflowDependencies 创建带完整工作流依赖的新 Kernel。
func NewWithWorkflowDependencies(workspaceRoot string, inputter workflow.Inputter, planner workflow.Planner, coder workflow.Coder, executor toolruntime.Executor, acceptor ...workflow.Acceptor) *Kernel {
	return NewWithStores(workspaceRoot, "", session.NewStore(), intent.NewStore(), message.NewStore(), checkpoint.NewStore(), turn.NewStore(), turnsummary.NewStore(), objectstore.NewStore(), insight.NewStore(), inputter, planner, coder, executor, acceptor...)
}

// NewPersistentWithWorkflowDependencies 创建带持久化存储的新 Kernel。
func NewPersistentWithWorkflowDependencies(workspaceRoot, stateDBPath string, inputter workflow.Inputter, planner workflow.Planner, coder workflow.Coder, executor toolruntime.Executor, acceptor ...workflow.Acceptor) (*Kernel, error) {
	workspaceRegistry, err := workspacestore.NewSQLiteStore(stateDBPath)
	if err != nil {
		return nil, fmt.Errorf("create workspace registry: %w", err)
	}
	defer workspaceRegistry.Close()

	workspaceRef, err := workspaceRegistry.EnsureByRoot(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("ensure workspace metadata: %w", err)
	}

	workspaceDBPath := storagepath.WorkspaceDBPath(stateDBPath, workspaceRef.ID)
	sessionStore, err := session.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		return nil, fmt.Errorf("create workspace session store: %w", err)
	}
	intentStore, err := intent.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		_ = sessionStore.Close()
		return nil, fmt.Errorf("create workspace intent store: %w", err)
	}
	messageStore, err := message.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		_ = sessionStore.Close()
		_ = intentStore.Close()
		return nil, fmt.Errorf("create workspace message store: %w", err)
	}
	checkpointStore, err := checkpoint.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		_ = sessionStore.Close()
		_ = intentStore.Close()
		_ = messageStore.Close()
		return nil, fmt.Errorf("create workspace checkpoint store: %w", err)
	}
	turnStore, err := turn.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		_ = sessionStore.Close()
		_ = intentStore.Close()
		_ = messageStore.Close()
		_ = checkpointStore.Close()
		return nil, fmt.Errorf("create workspace turn store: %w", err)
	}
	turnSummaryStore, err := turnsummary.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		_ = sessionStore.Close()
		_ = intentStore.Close()
		_ = messageStore.Close()
		_ = checkpointStore.Close()
		_ = turnStore.Close()
		return nil, fmt.Errorf("create workspace turn summary store: %w", err)
	}
	objectStore, err := objectstore.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		_ = sessionStore.Close()
		_ = intentStore.Close()
		_ = messageStore.Close()
		_ = checkpointStore.Close()
		_ = turnStore.Close()
		_ = turnSummaryStore.Close()
		return nil, fmt.Errorf("create workspace object store: %w", err)
	}
	insightStore, err := insight.NewSQLiteStore(workspaceDBPath)
	if err != nil {
		_ = sessionStore.Close()
		_ = intentStore.Close()
		_ = messageStore.Close()
		_ = checkpointStore.Close()
		_ = turnStore.Close()
		_ = turnSummaryStore.Close()
		_ = objectStore.Close()
		return nil, fmt.Errorf("create workspace insight store: %w", err)
	}

	k := NewWithStores(workspaceRoot, stateDBPath, sessionStore, intentStore, messageStore, checkpointStore, turnStore, turnSummaryStore, objectStore, insightStore, inputter, planner, coder, executor, acceptor...)

	// M13-01d: wire artifact manager into tool runtime for automatic persistence.
	artifactDataDir := storagepath.ArtifactDataDir(stateDBPath, workspaceRef.ID)
	if mgr, err := artifact.NewManager(artifactDataDir); err == nil {
		k.artifactMgr = mgr
		k.tools.SetArtifactManager(mgr)
	} else {
		slog.Warn("[kernel] artifact manager init failed, artifact persistence disabled", "error", err)
	}

	return k, nil
}

// NewWithStores 使用指定存储创建新 Kernel。
func NewWithStores(workspaceRoot, mainDBPath string, sessionStore session.Store, intentStore intent.Store, messageStore message.Store, checkpointStore checkpoint.Store, turnStore turn.Store, turnSummaryStore turnsummary.Store, objectStore objectstore.Store, insightStore insight.Store, inputter workflow.Inputter, planner workflow.Planner, coder workflow.Coder, executor toolruntime.Executor, acceptor ...workflow.Acceptor) *Kernel {
	ws := workspace.New(workspaceRoot)

	// ★ Tool discovery: detect project languages and probe available tools.
	var inv *inventory.Inventory
	if executor == nil {
		cfg, cfgErr := config.LoadConfig(workspaceRoot)
		if cfgErr == nil && cfg.Discovery.AutoCheck {
			inv = inventory.Discover(workspaceRoot, cfg)
			// Auto-write workspace config if new tools were discovered.
			if inventory.HasNewFindings(inv, cfg) {
				if writeErr := inventory.WriteWorkspaceConfig(workspaceRoot, inventory.GenerateConfig(inv), "merge"); writeErr != nil {
					slog.Warn("[kernel] failed to write discovered config", "error", writeErr)
				}
			}
		}
	}

	var tools *toolruntime.Runtime
	if executor == nil {
		// Config may have been updated by discovery; reload for runtime.
		if t, err := toolruntime.NewWithConfig(ws, workspaceRoot); err == nil {
			tools = t
		} else {
			tools = toolruntime.New(ws)
		}
	} else {
		var rtErr error
		tools, rtErr = toolruntime.NewWithExecutor(executor)
		if rtErr != nil {
			panic(rtErr)
		}
	}
	var a workflow.Acceptor
	if len(acceptor) > 0 {
		a = acceptor[0]
	}

	// Initialize Secretary Agent with Skill Registry.
	skills := skill.NewRegistry()
	// Load project skills (.coden/skills/) and user skills (~/.coden/skills/).
	_ = skills.LoadFromDir(skill.ProjectSkillsDir(workspaceRoot), skill.SourceProject)
	_ = skills.LoadFromDir(skill.UserSkillsDir(), skill.SourceUser)
	// Load project rules file (.coden/RULES.md) if present.
	_ = skills.LoadRules(skill.ProjectRulesPath(workspaceRoot), skill.SourceProject)
	// Register builtin skills.
	skill.RegisterBuiltins(skills)

	// Load plugins and inject their skills.
	plugins := plugin.NewRegistry()
	plugins.LoadFromDirs(plugin.ScopeUser, plugin.UserPluginDir())
	plugins.LoadFromDirs(plugin.ScopeProject, plugin.ProjectPluginDir(workspaceRoot))
	for _, dir := range plugins.SkillDirs() {
		_ = skills.LoadFromDir(dir, skill.SourcePlugin)
	}

	evBus := events.NewBus()
	sec := secretary.New(skills, secretary.DefaultPolicy(), busEmitAdapter{bus: evBus})

	return &Kernel{
		sessionMus:             make(map[string]*sync.Mutex),
		sessions:               make(map[string]map[string]string),
		activeWorkflows:        make(map[string]*activeWorkflow),
		activeSessionWorkflows: make(map[string]string),
		workflowGeneration:     make(map[string]uint64),
		workspaceChanges:       make(map[string][]model.WorkspaceChangedPayload),
		events:                 evBus,
		sessionStore:           sessionStore,
		intents:                intentStore,
		messages:               messageStore,
		checkpoints:            checkpointStore,
		turns:                  turnStore,
		turnSummaries:          turnSummaryStore,
		objects:                objectStore,
		insights:               insightStore,
		mainDBPath:             mainDBPath,
		workspace:              ws,
		tools:                  tools,
		git:                    gitstate.New(workspaceRoot), // M8-04
		workflow:               workflow.NewWithInputter(inputter, planner, coder, a),
		secretary:              sec,
		maxTaskRetries:         1,      // N-06: default 1 = two total attempts per task
		failurePolicy:          "stop", // M11-04: default = abandon remaining on failure
	}
}
