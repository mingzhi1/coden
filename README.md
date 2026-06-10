# CodeN

> **⚠️ Work In Progress** — 核心架构稳定，部分功能仍在开发中

AI 编程系统。不是单体 Agent Loop，不是平权 Swarm——一个拥有唯一状态的 Core：意图经确定性路由选定流程，任务进单写者黑板桶按就绪+优先级调度，再由 Core 经真实工具执行。**LLM 提议，代码裁决。**

---

## 与其他 AI 编程工具的差异

| | CodeN | Claude Code / Cursor | AutoGPT / CrewAI |
|---|---|---|---|
| 架构 | 单 Kernel + 无状态 Worker | 单体 Agent Loop | 平权 Swarm |
| 状态归属 | Kernel 唯一写入者 | Agent 自持状态 | 各 Agent 自持 |
| LLM 角色 | 分层（强/轻/异构 Critic） | 单模型兜底 | 角色平权 |
| 执行调度 | 黑板桶引擎：就绪+优先级、单写者、Guardian 守护（路由/调度是确定性代码，LLM 不驱动执行） | LLM 决定下一步 | LLM 互相调用 |
| 工具执行 | Kernel 子系统，零 LLM 成本 | LLM 发起工具调用 | LLM 发起工具调用 |
| Critic | 优先异构 Provider（plan + goal 两道，反自恋；单 provider 时回退同家） | 无 | 无 |
| 验收 | 独立 Acceptor Worker | 自我验证 | 自我验证 |

**核心哲学**：LLM 产出 spec/plan/patch；但**路由、执行、状态写入、提交、验收**始终在 Kernel 手里——意图→流程的路由是确定性单一真相表，执行是单写者黑板桶。LLM 提议，代码裁决。

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
│  │   Intent → Profile → Dispatcher(确定性路由)            │
│  │   → Discovery → Plan → Critic → RePlan                 │
│  │   → 黑板桶引擎(就绪+优先级·单写者) → Checkpoint(增量)  │
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
  → Intent       意图规整 + 跟随解析 → IntentSpec + Kind               [Light LLM]
  → ProjectProfile  载入缓存(语言/工具链/概览/风格);失效则重建        [缓存命中零成本 / 一次 Light LLM]
  → Dispatcher   确定性路由 Kind → WorkflowMode (policyForKind 单一真相表)  [零 LLM·代码裁决]
  │
  ├─ 按 Mode 路由（所有路径收口 Responder）:
  │   answer    → Responder                                          （直答 / 问答 / 闲聊）
  │   analyze   → Discovery → Analyzer(只读) → Responder              （读本仓库代码出分析,零修改）
  │   research  → 黑板桶引擎(只读·web_search) → Responder             （取外部知识:库文档/API/web）
  │   execute   → 下方完整流水线;plan_only = execute 但不含 Executor/Accept
  │
  → Discovery  WHERE：grep / LSP / RAG 捞片段                        [零 LLM 成本]
  → Plan       WHAT：任务 + 依赖；资料不齐则先产只读调研任务(gather-first) [Strong LLM]
  → Critic     plan 审查：异构 Provider,反自恋                       [Strong LLM, 不同厂商]
  → RePlan     HOW：细化到函数/行号                                  [Strong LLM]
  → 黑板桶引擎（就绪 + 优先级 · 单写者调度 · Guardian 守护）
      ├─→ Executor   执行 patch；缺外部知识先 web_search 再写         [Light LLM]
      ├─→ Tool Runtime write / edit / shell / web_search
      ├─→ Acceptor    pass/fail + FixGuidance（内层逐任务）           [Strong LLM]
      │     ├─ pass → 完成记录 + 产物留存
      │     └─ fail → 重派带反馈(#3) → 超 N 次 abandon
      ├─  依赖产物经 DepArtifacts 投影喂下游任务(#4 §11)
      └─  每 apply 发增量 checkpoint(桶+完成记录+产物,可恢复)
  → goal-critique  外层异构 Critic 审「结果是否达标」,可降级 pass    [Strong LLM]
  → Responder  收口：成功→简洁总结;失败/部分→进展 + 下一步建议        [Light LLM]
  → Checkpoint 终态 saga + Secretary AfterTurn → MEMORY.md（并判断 Profile 是否过期）
```

> **设计要点（确定性路由 + 黑板桶执行）**：详见 `docs/design/intent_routing.md` 与 `docs/design/blackboard_bucket_workflow.md`。
> - **路由是确定性代码,不是 LLM**：Intent 出 `Kind`,`policyForKind` 单一真相表把 `Kind → WorkflowMode`
>   一一映射(`AllIntentKinds` 是唯一权威列表,加意图只改一处)。曾经的 LLMDispatcher 二次粗分被退役——
>   省一段 LLM、消词表漂移、`代码裁决` 不抖不 fallback。
> - **执行是黑板桶引擎**:任务进一个总桶,按**就绪+优先级**调度(依赖一满足就跑,不再等 DAG 整层);
>   **单写者**串行 apply 状态/产物/checkpoint(无锁);**Guardian** 确定性守预算/震荡/死锁;
>   **重试带反馈**(上次为何失败投影回 Executor);**依赖产物**经 DepArtifacts 投影喂下游。
> - **资料不齐先研究再做**:Planner 信息不足时先产只读调研任务、实现任务依赖之;Executor 单轮内缺
>   外部知识也会先 `web_search/web_fetch` 再写代码,不臆测外部 API。
> - **research 是和 analyze 平级的 workflow 类型**:由意图识别选中,用**多 agent 桶引擎只读**实现,零新角色。
> - **角色不混用**:Analyzer 只读本仓库;Executor 通用工具 worker(可只读);Responder 纯收口;Discovery 纯检索。
> - **ProjectProfile 缓存**:语言/工具链/概览/风格按 manifest hash + Secretary 结构性判断失效,跨轮复用。
>
> 详见 `docs/ARCHITECTURE.md`。

**流水线组件分类**

| 类别 | 组件 | 说明 |
|------|------|------|
| **Dispatched Workers**（经 `executeWorker` 调度） | Intent / Plan / Executor / Acceptor | 标准 Worker 生命周期，产生事件与 tracing |
| **Deterministic Code**（零 LLM·`代码裁决`） | Dispatcher（路由）/ Guardian（守护）/ 桶调度器（单写者） | 确定性决策，可测、不抖、零成本 |
| **Inline Components**（Kernel 直接调用） | Discovery / Analyzer / Critic / RePlan / Responder | Kernel 内部同步调用，不经过 Worker dispatch |
| **Background Service** | Secretary（含 ProjectProfile 失效判断） / Profiler | 异步执行，策略引擎 + MEMORY.md 写入 + 一次性项目画像 |

**LLM 模型分层原则**

| 组件 | 档次 | 原因 |
|------|------|------|
| Planner / Critic / Replanner / Acceptor / Analyzer | **Strong** | 决策 / 流程设计 / 分析点，错误代价高 |
| Intent / Executor / Responder / Profiler | **Light** | 规整 / 执行 / 收口 / 摘要画像，速度优先 |
| Critic | **异构 Provider** | 与 Planner 不同厂商，消除盲区 |
| Dispatcher / Guardian / 桶调度 / Discovery | **零 LLM** | 路由/守护/调度是确定性代码；检索是纯工具（grep/LSP/RAG） |
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
coden-agent-plan   coden-agent-executor   coden-agent-accept
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
| **L1** Workflow | `runWorkflow()` | 路由(确定性) + 外层阶段调度 | 1（线性外壳） |
| **L2** 黑板桶 | `bucketScheduler.run()` | 就绪+优先级·单写者调度，重试带反馈，Guardian 守护 | `maxTaskRetries=1`（共 2 次尝试） |
| **L3** Agentic | `agenticBuild()` | LLM 多轮工具循环（Executor） | `maxExecutorRounds=5` |

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
    executor:     [claude-opus, mimo-pro]
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
| Workflow Engine | `█████████░` 95% | 确定性路由(policyForKind 单一真相表,含 research)、黑板桶执行引擎(就绪+优先级·单写者·Guardian·重试带反馈·DepArtifacts 投影·增量 checkpoint)、plan+goal 双 Critic 收口;外层 blackboard 化已评估为不追求(保守 cutover 即终态),L2 Regression 未实现 |
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
