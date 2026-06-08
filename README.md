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
│  │   Intent → Profile → Dispatcher(WorkflowPlan)          │
│  │   → Discovery → Plan → Critic → RePlan                 │
│  │   → [Code → Accept] × N → Checkpoint                   │
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
  → Intent       意图解析 → IntentSpec + Kind                        [Light LLM]
  → ProjectProfile  载入缓存(语言/工具链/概览/风格);失效则重建        [缓存命中零成本 / 一次 Light LLM]
  → Dispatcher   设计本次 workflow → WorkflowPlan                     [Strong LLM]
  │   { Mode, 参与角色集, 每角色 Objective, CoderMode }
  │   解析失败 → 回退静态策略表 (LocalDispatcher: Kind → 阶段集)
  │
  ├─ 按 Mode 路由（所有路径收口 Responder）:
  │   answer    → Responder                                          （直答 / 问答 / 闲聊）
  │   analyze   → Discovery → Analyzer(只读) → Responder              （读代码出分析,零修改）
  │   execute   → 下方完整流水线;plan_only = execute 但不含 Coder/Accept
  │
  → Discovery  WHERE：grep / LSP / RAG 捞片段                        [零 LLM 成本]
  → Plan       WHAT：任务 DAG + 依赖（注入 Dispatcher 下发的 Objective）[Strong LLM]
  → Critic     REVIEW：异构 Provider 审查,反自恋                      [Strong LLM, 不同厂商]
  → RePlan     HOW：细化到函数/行号                                  [Strong LLM]
  → Kernel 调度（按 DAG 并行）
      ├─→ Coder × N   执行 patch（注入 Objective）                   [Light LLM]
      ├─→ Tool Runtime write / edit / shell
      └─→ Acceptor    pass/fail + FixGuidance                        [Strong LLM]
            ├─ pass → task.passed
            └─ fail → inject FixGuidance → Coder retry
  → Responder  收口：成功→简洁总结;失败/部分→进展 + 下一步建议        [Light LLM]
  → Checkpoint 存档 + Secretary AfterTurn → MEMORY.md（并判断 Profile 是否过期）
```

> **设计要点（Dispatcher + WorkflowPlan）**：每轮由 **Dispatcher(LLM)** 设计 workflow,产出
> `WorkflowPlan{ Mode, 参与角色, 每角色 Objective, CoderMode }` —— 运行时单一真相源;Dispatcher
> 解析失败则**回退**到静态策略表 `policyForKind`(LocalDispatcher),保证永不卡死。角色不混用:
> - **Dispatcher 给每个参与角色一个明确 Objective**(有边界的目的+完成判据),让 Analyzer/Planner/Coder
>   收敛而非空转——弱目的曾让 Analyzer 无限读到超时;
> - **Analyzer 只服务 `analyze`**(只读、零修改);**Coder 纯写**、**Responder 纯收口**、**Discovery 纯检索**;
> - **`plan_only`** = execute 去掉 Coder/Accept(只出并审核计划,不执行);
> - 简单意图(`hi`)→ `answer` 直达 Responder;
> - **ProjectProfile 缓存**:语言/工具链/概览/风格按 manifest hash + Secretary 的结构性判断失效,
>   跨轮复用,让各 agent 开局即懂项目,不必每轮重新发现基础事实。
>
> 详见 `docs/ARCHITECTURE.md`。

**流水线组件分类**

| 类别 | 组件 | 说明 |
|------|------|------|
| **Dispatched Workers**（经 `executeWorker` 调度） | Intent / Plan / Coder / Acceptor | 标准 Worker 生命周期，产生事件与 tracing |
| **Inline Components**（Kernel 直接调用） | Dispatcher / Discovery / Analyzer / Critic / RePlan / Responder | Kernel 内部同步调用，不经过 Worker dispatch |
| **Background Service** | Secretary（含 ProjectProfile 失效判断） / Profiler | 异步执行，策略引擎 + MEMORY.md 写入 + 一次性项目画像 |

**LLM 模型分层原则**

| 组件 | 档次 | 原因 |
|------|------|------|
| Dispatcher / Planner / Critic / Replanner / Acceptor / Analyzer | **Strong** | 决策 / 流程设计 / 分析点，错误代价高 |
| Intent / Coder / Responder / Profiler | **Light** | 执行 / 收口 / 摘要画像，速度优先 |
| Critic | **异构 Provider** | 与 Planner 不同厂商，消除盲区 |
| Discovery | **零 LLM** | 纯代码工具（grep / LSP / RAG），不调用 LLM |
| Secretary | **条件性 Light** | AfterTurn 提取 insight + 判断 ProjectProfile 是否过期 |

---

## RPC 进程拓扑

全系统统一使用 **JSON-RPC 2.0**，只有两个合法 RPC 方向：

```
                    TUI / CLI / Web
                        │  Pattern A: client → kernel
                        │  session.attach · workflow.submit · event.subscribe
                        v
         ┌─────────────────────────────────────┐
         │   coden  (内嵌 Kernel，-serve 模式)   │  ← 唯一状态写入者
         │  (Session · Turn · Task · Event Bus)│
         └──────────────┬──────────────────────┘
                        │  Pattern B: kernel → worker / tool
                        │  worker.execute · tool.exec · tool.cancel
    ┌───────────────────┼─────────────┐
    v                   v             v
coden-agent-plan   coden-agent-code   coden-agent-accept
                        │
          ┌─────────────┼─────────────┐
          v             v             v
   coden-tool-shell  coden-tool-lsp   coden-tool-{read,write}file
```

> Kernel 没有独立二进制——它内嵌在 `coden` 进程里（`coden -serve` 即以 Kernel 角色运行）。
> MCP 不是独立进程，而是 `internal/mcp` 内部模块，经 `coden-llm-server` 接入。

**铁律**：Worker 之间不能直接通信，Tool 之间不能直接通信。所有跨角色协调必须经过 Kernel。

Worker 的输出只是"提案"——Kernel 决定什么最终成为状态。

---

## LLM Server Sidecar 与 ACP

LLM 调用链有两种模式，配置切换:
- **内嵌(默认)**:`Kernel → Broker.Chat → Pool(本地 provider)`。
- **Sidecar**:`Kernel → LLMServerClient → TCP JSON-RPC → coden-llm-server`(按 role 路由到 ACP / Anthropic / OpenAI / DeepSeek / MiniMax / Copilot)。

**接入 Claude(复用 Claude Code 订阅,无需 API Key)**:用 `claude-cli` provider —— 子进程跑
`claude -p --disallowed-tools <全部> --output-format json`,把工具移出模型上下文,agent 循环塌成
单轮纯补全;coden 只要「纯大脑」,工具由自己的 executor 跑。(旧的 `claude-agent-acp` 已弃用——
它强发 tool_call 导致 reasoning 桥死循环;详见 `docs/`。)

```yaml
llm:
  server: { enabled: true, addr: "127.0.0.1:7533" }   # 启用 sidecar
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
| RAG | SQLite **FTS5 持久化索引**（bm25 排序），大仓库跨文件检索 |

RAG 索引持久化在 `~/.coden/workspace/<key>/rag.sqlite`(不污染工作树),重启按 mtime/size + 内容 hash
增量 reconcile;checkpoint 通过后增量更新变更文件。

> 缺口:目前只有增量更新、没有全量/按需构建——已有仓库**首次运行**时索引为空,`rag_search` 暂失效(仅靠 grep/LSP),按需构建待补。

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

**分类与执行**：workflow 级(pre_intent / post_intent / post_plan / post_workflow)、task 级(pre_code / post_code / post_accept)、tool 级(pre / post_tool_use,可拒绝该 tool)均可 `blocking`。同阶段按 priority 串行;`blocking: true` 失败则短路并阻断工作流。Hook 经 `CODEN_HOOK_*` 环境变量拿上下文(session / workflow / workspace / task / tool / status / changed_files),运行时也可经 JSON-RPC `hook.list / register / remove` 动态管理。

**配置示例**(`<workspace>/.coden/config.yaml`):

```yaml
tools:
  hooks:
    post_code:
      - { name: go_vet, command: "go vet ./...", blocking: true, timeout: 30s }
    pre_tool_use:
      - { name: audit, command: "echo $CODEN_HOOK_TOOL_NAME >> .coden/audit.log", blocking: false }
```

> 完整阶段语义、环境变量表与 RPC 方法见 `docs/`。

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
  # providers 是 map（key = provider 名），不是数组
  providers:
    claude-opus:              # 复用 Claude Code 订阅，无需 API Key
      type: claude-cli        # http（默认）| claude-cli | anthropic | acp(弃用)
      command: claude
      default_model: opus
    mimo-pro:
      type: http              # 任意 OpenAI 兼容端点
      base_url: https://.../v1
      api_key: $MIMO_API_KEY
      default_model: mimo-v2-pro

  # pool 按档次声明 provider 优先级链（名字引用上面 providers 的 key）
  pool:
    primary: [claude-opus, mimo-pro]   # Strong 档：Planner / Critic / Acceptor / Analyzer
    light:   [mimo-pro]                # Light 档：Inputter / Responder / Secretary

  # routing 按角色覆盖 pool
  routing:
    coder:     [claude-opus, mimo-pro]
    secretary: [mimo-pro]              # 后台抽取,用快的 HTTP(别走 claude-cli 的 spawn)
    critic:    [mimo-pro]              # 异构 critic（与 planner 不同 provider，"反自恋"）

  # 语义记忆:配置后启用 dense 检索 + 语义去重；省略则退回纯词法。
  # 任意 OpenAI 兼容 /embeddings 端点（DashScope / SiliconFlow / OpenAI…）。
  embedding:
    base_url: https://dashscope.aliyuncs.com/compatible-mode/v1
    api_key: $DASHSCOPE_API_KEY
    model: text-embedding-v4
```

> 异构 Critic 是**优先级偏好**而非硬约束：若只配了一个 provider，critic 会回退到同一家。
> 要强制异构，请确保 `routing.critic` 的首选 provider 与 `pool.primary` 的首选不同。

---

## 当前进度

| 模块 | 进度 | 说明 |
|------|------|------|
| Kernel & 状态核心 | `█████████░` 95% | Session/Turn/Task/Checkpoint/Event Bus 全部完成，Artifact 接入完成 |
| RPC 协议层 | `█████████░` 95% | JSON-RPC 2.0，客户端面 29 个方法接入 handler（protocol 共定义 59 个方法常量，含 worker/tool 方向） |
| Workflow Engine | `█████████░` 95% | Dispatcher(LLM)→WorkflowPlan 驱动路由（策略表回退）、每角色 Objective（Analyzer/Planner/Coder 已消费）、任务状态机完成，L2 Regression 尚未实现 |
| Hook System | `█████████░` 90% | 9 阶段统一框架完成，Config/RPC/Event Bus 全部接入，Filter/Webhook 待实现 |
| LLM Broker | `█████████░` 90% | per-role pool、provider fallback、usage stats 完成，Sidecar 模式接入完成 |
| Tool Runtime | `█████████░` 90% | 14 工具完成，MCP 动态发现完成，tool_search 延迟注册完成 |
| Search Agent | `█████████░` 95% | SA-01~09 全部完成，meso-level discovery 完成 |
| 三层检索 | `████████░░` 85% | grep/LSP/RAG 全部实现，RAG stale 标记完成，写后同步完成 |
| ProjectProfile 缓存 | `████████░░` 80% | 语言/工具链(自探编译器)/概览/风格,manifest hash + Secretary 结构性判断失效,跨轮复用;finding 粒度增量分析未做 |
| Secretary | `███████░░░` 75% | ContextGate/ExecGate/AfterTurn 完成，MEMORY.md 写入完成，新增 ProjectProfile 过期判断，权限模型待强化 |
| TUI | `████████░░` 82% | 双栏四面板布局（Chat+Input / Workers+Changed）、事件驱动、History Tab 完成；每轮中间「思考过程」运行时实时显示、完成后折叠为一行摘要（`⋯ N steps · N tools · N files · 时长`），聊天保持干净的 YOU↔CODE 对话；slash command 扩展中 |
| LLM Server Sidecar | `█████████░` 90% | TCP sidecar、ACP/Anthropic/OpenAI/DeepSeek 完成，crash 监控完成（自动重启，上限 3 次，指数退避 500ms/1s/2s） |
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
