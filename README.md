# CodeN

> **⚠️ Work In Progress** — 核心架构稳定，部分功能仍在开发中

AI 编程系统。不是 AI wrapper，不是平权 Swarm——是一个拥有唯一状态的 Core，调度多个无状态 Worker，通过真实工具触达代码库。

---

## 与其他 AI 编程工具的差异

| | CodeN | Claude Code / Cursor | AutoGPT / CrewAI |
|---|---|---|---|
| 架构 | 单 Kernel + 无状态 Worker | 单体 Agent Loop | 平权 Swarm |
| 状态归属 | Kernel 唯一写入者 | Agent 自持状态 | 各 Agent 自持 |
| LLM 角色 | 分层（强/轻/异构 Critic） | 单模型兜底 | 角色平权 |
| 调度权 | 代码控制，LLM 不可干预 | LLM 决定下一步 | LLM 互相调用 |
| 工具执行 | Kernel 子系统，零 LLM 成本 | LLM 发起工具调用 | LLM 发起工具调用 |
| Critic | 强制异构 Provider（反自恋） | 无 | 无 |
| 验收 | 独立 Acceptor Worker | 自我验证 | 自我验证 |

**核心哲学**：LLM 只能产出 spec/plan/patch，不能决定调度、提交、验收。调度权永远在代码里。

---

## 架构

```
Clients: TUI / CLI / Web
         │ JSON-RPC 2.0 (Command API + Event Stream)
         v
┌────────────────────────────────────────────────────────────┐
│                        CodeN Core                          │
│                                                            │
│  RPC Gateway                                               │
│       │                                                    │
│  Kernel（单写者）                                           │
│  ├── Workflow Engine                                       │
│  │   Intent → Plan → Discovery → Critic                   │
│  │   → RePlan → [Code → Accept] × N → Checkpoint         │
│  │                                                         │
│  ├── Secretary（策略引擎）                                  │
│  │   ContextGate · ExecGate · AfterTurn → MEMORY.md       │
│  │                                                         │
│  ├── Tool Runtime（零 LLM 成本）                            │
│  │   Shell · FS · LSP · grep · RAG · MCP · Web            │
│  │                                                         │
│  └── LLM Broker                                           │
│      per-role pool · provider fallback · usage stats      │
└──────────────────────────┬─────────────────────────────────┘
                           │
         ┌─────────────────┼──────────────────┐
         v                 v                  v
   LLM Server (TCP)   LSP / MCP         Shell / FS / Git
   ├─ ACP (Claude)
   ├─ Anthropic API
   ├─ OpenAI API
   └─ DeepSeek / MiniMax / Copilot
```

---

## Workflow 流水线

```
用户输入
  → Intent    意图解析 → IntentSpec + Kind          [Light LLM]
  └─ question → Coder 直接回答 → 结束
  → Plan      WHAT：任务 DAG + 依赖关系              [Strong LLM]
  → Discovery WHERE：grep / LSP / RAG 并行搜索       [零 LLM 成本]
  → Critic    REVIEW：异构 Provider 审查，反自恋     [Strong LLM, 不同厂商]
  → RePlan    HOW：基于真实代码细化到函数/行号        [Strong LLM]
  → Kernel 调度（按 DAG 并行）
      ├─→ Coder × N   执行 patch                   [Light LLM]
      ├─→ Tool Runtime write / edit / shell
      └─→ Acceptor    pass/fail + FixGuidance       [Strong LLM]
            ├─ pass → task.passed
            └─ fail → inject FixGuidance → Coder retry
  → Checkpoint 存档 + Secretary AfterTurn → MEMORY.md
```

**流水线组件分类**

| 类别 | 组件 | 说明 |
|------|------|------|
| **Dispatched Workers**（经 `executeWorker` 调度） | Intent / Plan / Coder / Acceptor | 标准 Worker 生命周期，产生事件与 tracing |
| **Inline Components**（Kernel 直接调用） | Discovery / Critic / RePlan | Kernel 内部同步调用，不经过 Worker dispatch |
| **Background Service** | Secretary | 异步执行，策略引擎 + MEMORY.md 写入 |

**LLM 模型分层原则**

| 组件 | 档次 | 原因 |
|------|------|------|
| Planner / Critic / Replanner / Acceptor | **Strong** | 决策点，错误代价高 |
| Intent / Coder | **Light** | 执行点，速度优先 |
| Critic | **异构 Provider** | 与 Planner 不同厂商，消除盲区 |
| Discovery | **零 LLM** | 纯代码工具（grep / LSP / RAG），不调用 LLM |
| Secretary | **条件性 Light** | AfterTurn 阶段可选调用 LLM 提取 insight |

---

## RPC 进程拓扑

全系统统一使用 **JSON-RPC 2.0**，只有两个合法 RPC 方向：

```
TUI / CLI / Web
    │  Pattern A: client → kernel
    │  session.attach · workflow.submit · event.subscribe
    v
┌─────────────────────────────────────┐
│           coden-kernel              │  ← 唯一状态写入者
│  (Session · Turn · Task · Event Bus)│
└──────────────┬──────────────────────┘
               │  Pattern B: kernel → worker / tool
               │  worker.execute · tool.exec · tool.cancel
    ┌──────────┼──────────────────────┐
    v          v                      v
coden-agent-plan   coden-agent-code   coden-agent-accept
                        │
               ┌────────┼────────┐
               v        v        v
         tool-shell  tool-lsp  tool-mcp
```

**铁律**：Worker 之间不能直接通信，Tool 之间不能直接通信。所有跨角色协调必须经过 Kernel。

Worker 的输出只是"提案"——Kernel 决定什么最终成为状态。

---

## LLM Server Sidecar 与 ACP

LLM 调用链有两种模式，通过配置切换：

```
内嵌模式（默认）                    Sidecar 模式
────────────────                   ──────────────────────────────────
Kernel                             Kernel
  └→ Broker.Chat(role, msgs)         └→ LLMServerClient.Chat()
      └→ Pool (本地 provider)              └→ TCP JSON-RPC → coden-llm-server
                                                 └→ router.go（role → provider chain）
                                                     ├─ provider_acp.go      (ACP)
                                                     ├─ provider_anthropic.go
                                                     ├─ provider_openai.go
                                                     └─ provider_others.go   (DeepSeek/MiniMax/Copilot)
```

**ACP（Agent Communication Protocol）** 是 Claude Code 原生协议：

```
coden-llm-server
  └→ provider_acp.go
      └→ acp_conn.go  ──ndJSON stdio──→  claude 子进程
                                          （复用 Claude Code 本地鉴权，无需 API Key）
```

ACP 模式的优势：复用 Claude Code 已登录的会话，本地开发零配置接入 Claude。

Sidecar 启用方式：

```yaml
llm:
  server:
    enabled: true
    addr: "127.0.0.1:7533"
```

---

## 三层 ReAct 循环

控制权始终在 Go 代码，LLM 只做推理：

| 层 | 控制者 | 职责 | 循环上限 |
|---|--------|------|---------|
| **L1** Workflow | `runWorkflow()` | 线性流水线调度 | 1（线性） |
| **L2** Task DAG | `runOneTask()` | 按依赖图并行调度，失败重试 | `maxTaskRetries=1`（共 2 次尝试） |
| **L3** Agentic | `agenticBuild()` | LLM 多轮工具循环（Coder） | `maxCoderRounds=5` |

---

## 三层检索

**`grep` 保底，`LSP` 定锚，`RAG` 扩展。**

| 层 | 擅长 |
|---|------|
| grep | 字符串/标识符，零索引，始终可用 |
| LSP | definition / references / symbols，结构事实 |
| RAG | 语义召回，BM25，大仓库跨文件检索 |

RAG 索引只包含验收通过的代码，写入期间标记 stale。

---

## Hook System（全阶段钩子）

Hook 是可配置的 shell 命令，在工作流生命周期的 **9 个阶段** 自动执行。用于质量门、审计、通知、自动提交等场景。

```
 用户输入
      │
 ❶ pre_intent     输入预处理、敏感词过滤
      │
   Intent Worker
      │
 ❷ post_intent    意图审计、路由覆盖
      │
   Plan Worker
      │
 ❸ post_plan      任务数上限检查、DAG 验证
      │
   ┌──┴──┐ Per Task
   │ ❹ pre_code    分支保护、快照保存
   │     │
   │  Code Worker → Tool Execution
   │     │          ❺ pre_tool_use   权限检查、审计
   │     │          ❻ post_tool_use  diff 验证、日志
   │     │
   │ ❼ post_code   go vet / test / lint 质量门
   │     │
   │  Accept Worker
   │     │
   │ ❽ post_accept 自动提交、通知
   └─────┘
      │
 ❾ post_workflow   清理、统计、CI 触发
```

**Hook 分类**

| 分类 | Hook Points | 可阻断工作流 |
|------|-------------|-------------|
| **Workflow-level** | pre_intent, post_intent, post_plan, post_workflow | ✓ (blocking) |
| **Task-level** | pre_code, post_code, post_accept | ✓ (blocking) |
| **Tool-level** | pre_tool_use, post_tool_use | ✓ (可拒绝 tool) |

**执行模型**：同一阶段内串行按 priority 执行。`blocking: true` 的 hook 失败会短路剩余 hook 并阻断工作流。

**配置示例**（`~/.coden/config.yaml` 或 `<workspace>/.coden/config.yaml`）：

```yaml
tools:
  hooks:
    post_code:
      - name: go_vet
        command: "go vet ./..."
        blocking: true
        timeout: 30s
      - name: golint
        command: "golangci-lint run"
        blocking: false
        timeout: 60s
        priority: 10

    pre_tool_use:
      - name: shell_audit
        command: "echo \"AUDIT: $CODEN_HOOK_TOOL_NAME $CODEN_HOOK_TOOL_INPUT\" >> .coden/audit.log"
        blocking: false
        timeout: 5s

    post_workflow:
      - name: cleanup
        command: "rm -rf .coden/tmp/*"
        blocking: false
        timeout: 5s
```

**环境变量**：Hook 通过 `CODEN_HOOK_*` 环境变量接收上下文：

| 变量 | 说明 | 可用阶段 |
|------|------|---------|
| `CODEN_HOOK_SESSION_ID` | 会话 ID | 全部 |
| `CODEN_HOOK_WORKFLOW_ID` | 工作流 ID | 全部 |
| `CODEN_HOOK_WORKSPACE` | 工作区根目录 | 全部 |
| `CODEN_HOOK_PROMPT` | 用户原始输入 | pre/post_intent |
| `CODEN_HOOK_TASK_ID` | 当前任务 ID | pre/post_code, post_accept |
| `CODEN_HOOK_TASK_TITLE` | 当前任务标题 | pre/post_code, post_accept |
| `CODEN_HOOK_ATTEMPT` | 当前重试次数 | pre/post_code, post_accept |
| `CODEN_HOOK_TOOL_NAME` | 工具名称 | pre/post_tool_use |
| `CODEN_HOOK_TOOL_INPUT` | 工具输入摘要 | pre/post_tool_use |
| `CODEN_HOOK_STATUS` | 最终状态 (pass/fail) | post_workflow |
| `CODEN_HOOK_CHANGED_FILES` | 变更文件列表 (换行分隔) | post_workflow |

**RPC 动态管理**：运行时可通过 JSON-RPC 动态注册/移除/查询 hook：

- `hook.list` — 列出已注册 hook
- `hook.register` — 动态注册 hook
- `hook.remove` — 按名称移除 hook

---

## 快速开始

```bash
# 依赖：Go 1.25+

# TUI 交互模式
go run ./cmd/coden -workspace ./my-project -allow-shell

# 单次执行
go run ./cmd/coden -workspace ./my-project -prompt "修复 kernel 中的 bug"

# Server 模式（持久化多 session）
go run ./cmd/coden -serve 127.0.0.1:7100 -workspace ./my-project
go run ./cmd/coden -connect 127.0.0.1:7100 -prompt "hello"

# CI / 脚本模式
go run ./cmd/coden -plain -prompt "bootstrap CodeN" -allow-shell
```

配置 LLM（`~/.coden/config.yaml`）：

```yaml
llm:
  providers:
    - name: anthropic
      api_key: $ANTHROPIC_API_KEY
  routing:
    primary: anthropic/claude-opus-4
    light: anthropic/claude-haiku-4-5
    critic_provider: openai  # 异构 Critic
```

---

## 当前进度

| 模块 | 进度 | 说明 |
|------|------|------|
| Kernel & 状态核心 | `█████████░` 95% | Session/Turn/Task/Checkpoint/Event Bus 全部完成，Artifact 接入完成 |
| RPC 协议层 | `█████████░` 95% | JSON-RPC 2.0，34 个方法，handler 全部接入 |
| Workflow Engine | `█████████░` 95% | 6 阶段流水线完成，任务状态机完成，L2 Regression 尚未实现 |
| Hook System | `█████████░` 90% | 9 阶段统一框架完成，Config/RPC/Event Bus 全部接入，Filter/Webhook 待实现 |
| LLM Broker | `█████████░` 90% | per-role pool、provider fallback、usage stats 完成，Sidecar 模式接入完成 |
| Tool Runtime | `█████████░` 90% | 14 工具完成，MCP 动态发现完成，tool_search 延迟注册完成 |
| Search Agent | `█████████░` 95% | SA-01~09 全部完成，meso-level discovery 完成 |
| 三层检索 | `████████░░` 85% | grep/LSP/RAG 全部实现，RAG stale 标记完成，写后同步完成 |
| Secretary | `███████░░░` 75% | ContextGate/ExecGate/AfterTurn 完成，MEMORY.md 写入完成，权限模型待强化 |
| TUI | `████████░░` 80% | 双栏四面板布局（Chat+Input / Workers+Changed）、事件驱动、History Tab 完成，slash command 扩展中 |
| LLM Server Sidecar | `█████████░` 90% | TCP sidecar、ACP/Anthropic/OpenAI/DeepSeek 完成，crash 监控待实现 |
| Artifact 管理 | `████████░░` 85% | M13 Phase 1-3 完成：存储/查询/引用/GC，Phase 4（导出/TUI）待完善 |
| Web Kanban | `███████░░░` 70% | HTTP/WS server + 完整 UI、Board/Card CRUD API、Session API（列表/创建/变更/Submit）完成，Event 回写 Card 状态待完成 |

---

## 未来愿景

### Web Kanban 看板

看板不是只读展示，而是调度入口——拖动卡片即触发 Workflow 执行。

```
┌─────────────────────────────────────────────────────────────┐
│                    Web Kanban UI                             │
│  Backlog │ Ready │ In Progress │ Review │ Done │ Blocked    │
│          │       │  [Sess-1]   │        │      │            │
│          │  ●    │  [Sess-2]   │   ●    │  ●   │            │
└──────────┴───────┴─────────────┴────────┴──────┴────────────┘
           │ WebSocket / HTTP
┌──────────┴──────────────────────────────────────────────────┐
│  Kanban HTTP/WS Server (REST API + Event Bridge)            │
└──────────┬──────────────────────────────────────────────────┘
           │
┌──────────┴──────────────────────────────────────────────────┐
│  CodeN Core                                                  │
│  ├── BoardStore（Board / Column / Card · 图状依赖）           │
│  └── Kernel（多 Session 并行 · Workflow · Event Bus）        │
└─────────────────────────────────────────────────────────────┘
```

**设计原则**：
- `Board Is a View` — 看板是任务系统的可视化投影，不绕过状态机
- `Session Is Execution` — Card 执行时绑定 Session，复用现有基础设施，不引入额外抽象
- `Drag = Submit` — 拖动到 In Progress + 选择 Session，本质上是触发一次 `Submit()`
- `Events Drive UI` — UI 由 Event Bus 驱动，不轮询
- `Kernel Owns State` — 最终状态仍由 Kernel 控制，UI 不直接拥有真相

Card 数据模型支持层级任务（Epic / Task / Sub-task）、图状依赖（blocks / relates_to / supersedes）。并行执行直接复用 Kernel 多 Session 能力，无需独立编排层。

---

### 多 Session 并行

```
User ── ClientAPI ──→ Kernel（单写者）
                       ├── Session A → Workflow A (Card X)
                       ├── Session B → Workflow B (Card Y)
                       └── Session C → Workflow C (Card Z)
                              │
                         Event Bus 广播给所有订阅者
```

Kernel 原生支持多 Session 并行执行。每个 Card 绑定一个 Session，多个 Card 同时执行时自然并行。不需要独立的 Agent 编排层——Session 就是执行单元。

---

### 其他规划

- **L2 Regression**：验收后自动运行测试套件，Acceptor 分析结果
- **Memory 演进**：5 层记忆（工作记忆 → 会话摘要 → 洞察 → 项目知识 → 长期记忆）
- **多语言 LSP**：Go / TypeScript / Python / Rust 同时在线
- **分布式 Worker**：Worker 跨机器执行，Kernel 保持单写者

---

## License

MIT
