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

## 8 角色 Worker 流水线

```
用户输入
  → Intent    意图解析 → IntentSpec + Kind          [Light]
  └─ question → Coder 直接回答 → 结束
  → Plan      WHAT：任务 DAG + 依赖关系              [Strong]
  → Discovery WHERE：grep / LSP / RAG 并行搜索
  → Critic    REVIEW：异构 Provider 审查，反自恋     [Strong, 不同厂商]
  → RePlan    HOW：基于真实代码细化到函数/行号        [Strong]
  → Kernel 调度（按 DAG 并行）
      ├─→ Coder × N   执行 patch                   [Light]
      ├─→ Tool Runtime write / edit / shell
      └─→ Acceptor    pass/fail + FixGuidance       [Strong]
            ├─ pass → task.passed
            └─ fail → inject FixGuidance → Coder retry
  → Checkpoint 存档 + Secretary AfterTurn → MEMORY.md
```

**模型分层原则**

| Worker | 档次 | 原因 |
|--------|------|------|
| Planner / Critic / Replanner / Acceptor | **Strong** | 决策点，错误代价高 |
| Intent / Searcher / Coder / Secretary | Light | 执行点，速度优先 |
| Critic | 异构 Provider | 与 Planner 不同厂商，消除盲区 |

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
| **L2** Task DAG | `runOneTask()` | 按依赖图并行调度，失败重试 | `maxTaskRetries=2` |
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
| Kernel & 状态核心 | `█████████░` 95% | Session/Turn/Task/Checkpoint/Event Bus 全部完成，M13 Artifact 接入中 |
| RPC 协议层 | `████████░░` 85% | JSON-RPC 2.0，21 个方法，部分 handler 未接入（session.list / workspace.read 等） |
| Workflow Engine | `█████████░` 90% | 6 阶段流水线完成，任务状态机完成，L2 Regression 尚未实现 |
| LLM Broker | `████████░░` 85% | per-role pool、provider fallback、usage stats 完成，Sidecar 模式接入完成 |
| Tool Runtime | `█████████░` 90% | 14 工具完成，MCP 动态发现完成，tool_search 延迟注册完成 |
| Search Agent | `█████████░` 95% | SA-01~09 全部完成，meso-level discovery 完成 |
| 三层检索 | `████████░░` 85% | grep/LSP/RAG 全部实现，RAG stale 标记完成，写后同步完成 |
| Secretary | `███████░░░` 75% | ContextGate/ExecGate/AfterTurn 完成，MEMORY.md 写入完成，权限模型待强化 |
| TUI | `████████░░` 80% | 3 栏布局、事件驱动、History Tab 完成，slash command 扩展中 |
| LLM Server Sidecar | `████████░░` 80% | TCP sidecar、ACP/Anthropic/OpenAI/DeepSeek 完成，crash 监控待实现 |
| Artifact 管理 | `███░░░░░░░` 30% | M13 Phase 1 骨架完成，存储/查询/生命周期管理进行中 |
| Web Kanban | `██░░░░░░░░` 20% | HTTP/WS server 骨架完成，前端看板未开始 |
| 多 Agent 协作 | `█░░░░░░░░░` 10% | BoardStore 数据模型设计完成，AgentPool 调度未实现 |

---

## 未来愿景

### Web Kanban 看板

看板不是只读展示，而是调度入口——拖动卡片即触发 Agent 执行。

```
┌─────────────────────────────────────────────────────────────┐
│                    Web Kanban UI                             │
│  Backlog │ Ready │ In Progress │ Review │ Done │ Blocked    │
│          │       │  [Agent A]  │        │      │            │
│          │  ●    │  [Agent B]  │   ●    │  ●   │            │
└──────────┴───────┴─────────────┴────────┴──────┴────────────┘
           │ WebSocket / HTTP
┌──────────┴──────────────────────────────────────────────────┐
│  Kanban HTTP/WS Server (REST API + Event Bridge)            │
└──────────┬──────────────────────────────────────────────────┘
           │
┌──────────┴──────────────────────────────────────────────────┐
│  CodeN Core                                                  │
│  ├── AgentPool（Agent 注册 · 任务路由 · 冲突检测）            │
│  ├── BoardStore（Board / Column / Card · 图状依赖）           │
│  └── Kernel（Session · Workflow · Event Bus）                │
└─────────────────────────────────────────────────────────────┘
```

**设计原则**：
- `Board Is a View` — 看板是任务系统的可视化投影，不绕过状态机
- `Drag = Submit` — 拖动到 In Progress 本质上是触发一次 `workflow.submit`
- `Events Drive UI` — UI 由 Event Bus 驱动，不轮询
- `Kernel Owns State` — 最终状态仍由 Kernel 控制，UI 不直接拥有真相

Card 数据模型支持层级任务（Epic / Task / Sub-task）、图状依赖（blocks / relates_to / supersedes）、原子 claim 防止多 Agent 抢占。

---

### 多人 / 多 Agent 协作

```
User A ──┐
User B ──┤── ClientAPI ──→ AgentPool ──→ Agent Session A (Workflow A)
User C ──┘                           └──→ Agent Session B (Workflow B)
                                              │
                                         Kernel（单写者）
                                         冲突检测 · 文件锁
                                         事件广播给所有订阅者
```

**核心问题**：多 Agent 并行时的文件冲突。设计策略：
- **Conflict First**：调度前先检测文件重叠，冲突任务串行化
- **原子 Claim**：Card 被某 Agent claim 后，其他 Agent 不可抢占
- **按 Agent 过滤 Diff**：每个变更追踪到 Session/Card，支持独立 review 和 commit
- **Session 恢复**：Agent 崩溃后可通过 Checkpoint + Event replay 重新 attach

---

### 其他规划

- **L2 Regression**：验收后自动运行测试套件，Acceptor 分析结果
- **Memory 演进**：5 层记忆（工作记忆 → 会话摘要 → 洞察 → 项目知识 → 长期记忆）
- **多语言 LSP**：Go / TypeScript / Python / Rust 同时在线
- **分布式 Worker**：Worker 跨机器执行，Kernel 保持单写者

---

## License

MIT
