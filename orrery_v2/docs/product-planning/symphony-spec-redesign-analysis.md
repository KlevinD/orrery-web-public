# 参考 Symphony SPEC 的系统改造设计文档

> **来源**: [openai/symphony SPEC.md](https://github.com/openai/symphony/blob/main/SPEC.md)
> **日期**: 2026-06-16
> **目标**: 对比分析 Symphony 多 Agent 编排规范与 Orrery 现有架构，产出可落地的改造方案与实施路径。

---

## 目录

1. [当前架构分析](#1-当前架构分析)
2. [Symphony SPEC 可借鉴的关键设计](#2-symphony-spec-可借鉴的关键设计)
3. [差距分析与改造机会](#3-差距分析与改造机会)
4. [改造方案建议](#4-改造方案建议)
5. [实施路径](#5-实施路径)
6. [风险与注意事项](#6-风险与注意事项)

---

## 1. 当前架构分析

### 1.1 整体分层

Orrery v2 采用严格的 DDD 分层架构（ports & adapters），依赖方向内聚：

```
transport → app → domain → platform
   (http,       (CQRS-lite,   (agent,       (db, eventstore,
    websocket,   services,     mission,      config, logging,
    mcp)         dto)          resource,     secrets, metrics)
                               memory)
```

**核心原则**：Agent 是能力主体，runtime/QA/tools/skills 是装配到 Agent 上的资源，Mission 负责目标、团队、协作和事件汇总。

### 1.2 Agent 生命周期系统

**22 信号 pipeline**，覆盖 Agent 完整执行周期：

| 阶段 | Before 信号 | After 信号 |
|------|-------------|------------|
| 任务接收 | `BeforeTaskReceived` | `AfterTaskReceived` |
| 任务接受 | `BeforeTaskAccepted` | `AfterTaskAccepted` |
| 上下文构建 | `BeforeContextBuild` | `AfterContextBuild` |
| 运行时执行 | `BeforeRuntimeStart` / `RuntimeStarted` / `RuntimeProgress` | `BeforeRuntimeFinish` / `AfterRuntimeFinish` |
| 结果规范化 | `BeforeResultNormalize` | `AfterResultNormalize` |
| 结果验证 | `BeforeResultValidate` | `AfterResultValidate` |
| 结果提交 | `BeforeResultCommit` | `AfterResultCommit` |
| 回复 | `BeforeAgentReply` | `AfterAgentReply` |

**异常信号**: `AgentBlocked`, `AgentFailed`, `AgentCancelled`, `AgentTimeout`

**SignalBus 特性**:
- Pub/sub 模式，`InMemorySignalBus` 实现
- 四种失败策略：`Stop` / `Continue` / `Degrade` / `Retry`
- Retry 支持指数退避 + 最终 fallback 策略
- Panic 恢复和 metrics 采集
- Soft failure 聚合与上报

**PipelineExecutor**:
- 按步骤顺序执行（`RunPlan`）
- 每步骤：emit Before → 执行 pre-hooks → 执行 step（经 middleware 包装）→ 执行 post-hooks → emit After
- 步骤失败分类：DeadlineExceeded → `AgentTimeout`, Canceled → `AgentCancelled`, Stopped → `AgentBlocked`, 其他 → `AgentFailed`

### 1.3 任务系统（11-State FSM）

```
Draft → Assigned → Accepted → InProgress → Submitted → InReview → Approved
                    ↓             ↓                         ↓
                  Cancelled    Blocked                   Rejected
                    ↑             ↓                         ↓
                    └── ReworkRequested ←───────────────────┘
```

- 乐观并发控制（version-based `If-Match`）
- 终端状态：`Approved` / `Cancelled`
- 从终端状态不可再转换
- 版本冲突检测（`ExpectedVersion` 比对）

### 1.4 Agent 组装系统（AgentAssembler）

```
AgentProfile
  ├── ComponentRegistry
  │     ├── Delivery (ResultCommitter)
  │     ├── Evidence (ResultValidator)
  │     ├── QA (ResultValidator)
  │     ├── Summary (SummaryGenerator)
  │     ├── Notifier (Notifier)
  │     └── Policy (ResultValidator)
  ├── Resource Binding (tools, skills, MCP)
  ├── Memory Binding
  └── Runtime Adapter (codex, claude, cursor, gemini, openclaw, human, browser, HTTP)
```

- 组件注册表支持 `DefaultComponentRegistry` 和 `ProductionComponentRegistry`
- 验证器链模式（Evidence → QA → Policy 顺序执行）
- 依赖缺失时返回明确的 `componentDependencyMissing` 错误

### 1.5 运行时执行系统

**Daemon + Worker 模式**:

```go
type Daemon struct {
    Worker       *Worker
    PollInterval time.Duration
}
// Run: 循环 RunOnce → 等待 poll interval
```

- `RecoverySweeper`: 恢复僵尸任务（`RecoverStale`）
- `Watchdog`: 硬超时控制（`context.WithTimeout`）
- 进程执行器：命令构建、环境变量、stderr tail、版本管理

### 1.6 事件系统

- 65+ 领域事件类型
- Append-only event store with stream versioning
- 投影（projections）重建读模型
- 事务性 outbox 保证最终一致性
- 事件适配器（TaskAdapter）连接 task 和 event 层

### 1.7 现有架构优势总结

| 维度 | 优势 |
|------|------|
| **领域建模** | 严格的 DDD，类型安全的值对象 ID，清晰边界 |
| **生命周期** | 细粒度 22-signal pipeline，灵活的失败策略 |
| **可扩展性** | ComponentRegistry + AgentAssembler 插件式组装 |
| **数据一致性** | Event sourcing + outbox + optimistic concurrency |
| **可观测性** | 结构化日志（correlation/mission/agent/task/run ID），metrics sink |
| **多租户** | SaaS-first 信任边界，租户/工作空间/角色/审计 |
| **任务协作** | 讨论组、mention 系统、inbox、staffing |

### 1.8 现有架构待改进领域

| 维度 | 不足 |
|------|------|
| **自主循环** | Daemon 过于简单，缺少 claim/dispatch/reconcile 的显式状态机 |
| **工作空间隔离** | 未抽象为显式的 WorkspaceManager；隔离粒度不够 |
| **并发控制** | 无显式的全局 max_concurrent 和 per-state 并发上限 |
| **重试策略** | Retry 只在 SignalBus handler 级别，无任务级别的指数退避重试 |
| **停滞检测** | Watchdog 仅做硬超时，无输出停滞检测（stall detection） |
| **工作流定义** | 无 WORKFLOW.md 式的版本化声明式工作流合约 |
| **恢复机制** | RecoverySweeper 较简单，缺少 tracker reconciliation 模式 |
| **规范表达** | 缺少语言无关的 formal specification 文档 |

---

## 2. Symphony SPEC 可借鉴的关键设计

### 2.1 总体理念：Spec-First, Product-Second

Symphony 的核心哲学：

> 不是产品，是规范。任何团队可用任何语言实现。

这是一个**协议标准**式的思维方式，而非又一个框架或平台。Orrery 也可以从这个角度重新审视自己的定位——定义一个"Agent Mission Control 协议"，然后提供 Go 参考实现。

### 2.2 四大核心循环：Poll → Claim → Dispatch → Reconcile

Symphony 的 Orchestrator 将所有工作抽象为四个标准化操作：

```
┌──────────────────────────────────────────┐
│              Orchestrator                │
│                                          │
│  ┌──────┐   ┌───────┐   ┌──────────┐   │
│  │ Poll │──▶│ Claim │──▶│ Dispatch │   │
│  └──────┘   └───────┘   └──────────┘   │
│       ▲                        │        │
│       │                        ▼        │
│       │    ┌────────────┐  ┌──────┐    │
│       └────│ Reconcile  │◀─│ Run  │    │
│            └────────────┘  └──────┘    │
└──────────────────────────────────────────┘
```

**每个操作的精确定义**:

| 操作 | 职责 | 输入 | 输出 |
|------|------|------|------|
| **Poll** | 从 tracker 拉取候选 issue | tracker query | candidate list |
| **Claim** | 检查并发上限，标记 issue 为己有 | candidate + runtime state | claimed set |
| **Dispatch** | 为每个 claimed issue 创建工作空间并启动 agent | claimed issue + workspace | running sessions |
| **Reconcile** | 检测崩溃/停滞的 run，重新入队 | running sessions + stall detector | retry queue / release |

### 2.3 Orchestrator Runtime State（单一权威内存状态）

Symphony 的 Orchestrator 维护一份完整的内存状态快照：

```
Fields:
- poll_interval_ms          // 轮询间隔
- max_concurrent_agents     // 全局并发上限
- running                   // map[issue_id → RunningEntry]
- claimed                   // set[issue_id]
- retry_attempts            // map[issue_id → RetryEntry]
- completed                 // set[issue_id]
- codex_totals              // 聚合 token + 运行秒数
- codex_rate_limits         // 速率限制状态
```

**关键设计选择**：内存优先，不额外引入数据库。Tracker（Linear）本身就是持久化状态存储。重启时通过 reconciliation 从 tracker + filesystem 重建状态。

### 2.4 WORKFLOW.md 合约

版本化在代码仓库中的声明式工作流定义：

```yaml
---
# YAML front matter
tracker:
  kind: linear
  endpoint: https://api.linear.app/graphql
  api_key: ${LINEAR_API_KEY}
  project_slug: my-project
  states:
    active: [Todo, InProgress]
    terminal: [Done, Cancelled]

polling:
  interval_ms: 30000

workspace:
  root: /tmp/symphony-workspaces
  # 每个 issue 获得隔离的工作目录

hooks:
  after_create: "npm install"
  before_run: "git pull origin main"
  after_run: "echo 'done'"
  before_remove: "rm -rf node_modules"

agent:
  max_concurrent_agents: 4
  max_turns: 50
  retry:
    max_attempts: 3
    backoff_base_ms: 10000
    max_backoff_ms: 300000

codex:
  command: codex exec
  approval_policy: never
  timeout:
    total_seconds: 1800
    stall_seconds: 300
---
# Markdown body = agent prompt template
You are a coding agent working on: {{issue.title}}

{{issue.description}}

Instructions:
1. Create a plan
2. Implement the changes
3. Write tests
4. Verify everything passes
```

**核心价值**:
- 工作流定义与代码同版本，消除版本偏差
- YAML front matter 提供结构化配置，Markdown body 提供自然语言指令
- Liquid 模板变量注入运行时上下文（issue 字段等）
- Shell hooks 允许自定义前后处理

### 2.5 重试与停滞检测

**指数退避**:
```
delay = min(10000 × 2^(attempt-1), max_retry_backoff_ms)
```
默认 max_backoff = 300s，可配置。

**停滞检测**:
- 如果子进程在 `stall_timeout_ms`（默认 5 分钟）内无任何输出 → 判定为停滞
- 停滞的 worker 被终止，issue 进入 retry queue
- 与硬超时（total timeout）独立运作

### 2.6 Per-Issue Workspace Isolation

- 每个 issue 获得独立的文件系统工作目录
- Agent 命令的执行范围限定在工作目录内
- 工作空间生命周期由 WorkspaceManager 管理：创建 → 使用 → 清理
- 避免了共享 checkout 的锁竞争问题

### 2.7 Tracker as State Store

Symphony 不引入额外数据库：

> Linear IS the state store.

- Issue 状态流转由 Agent 驱动
- 重启时通过 re-read tracker + scan filesystem 重建状态
- Reconciliation 是 first-class 操作，不是事后补救

### 2.8 多回合连续执行（Continuation Turns）

Symphony 的一个关键设计是 Agent 不会在一次 turn 后立即退出：

```
Turn 1 (完整 prompt) → 检查 issue 状态 → 仍活跃且未达 max_turns →
Turn 2 (仅发送 continuation 提示) → 检查 → ... → 退出
```

- **首个 turn** 发送完整的渲染后的任务 prompt
- **后续 continuation turns** 仅发送简短指引（不重发原始 prompt，避免上下文膨胀）
- **Worker 退出后**，Orchestrator 仍会安排一个短延迟（~1s）的 continuation retry，重新检查 issue 是否仍需处理
- 这意味着一个 issue 的生命周期是 **多个 worker session × 每个 session 多个 turns** 的嵌套循环

Orrery 的 `PipelineExecutor.RunPlan` 当前是线性的一次性执行。借鉴这个模式可以让单个 Agent 在一个 session 内执行多个回合，每回合检查任务状态和依赖变化。

### 2.9 语言无关的 SPEC 表达

SPEC.md 使用 RFC 2119 关键词（MUST, SHOULD, MAY），定义：
- 各组件的接口契约
- 数据格式和字段
- 行为语义和边界条件
- 错误处理策略

这使得不同语言的实现可以互操作，也使得规范本身可以被 review、fork、迭代。

### 2.10 安全加固策略

Symphony SPEC Section 15 定义了明确的安全基线，部分可直接适配到 Orrery：

| 原则 | Symphony 机制 | Orrery 可适配方式 |
|------|-------------|-----------------|
| 工作空间隔离 | `cwd` 强制限定在 per-issue workspace 内 | WorkspaceManager 隔离 + `RuntimeWorkspacePolicy` 校验 |
| 路径安全 | workspace path 必须在 workspace root 前缀内 | `internal/agent/runtime/core/` 已有的 `WorkspacePolicy` 扩展 |
| Secret 处理 | `$VAR` 间接引用，禁止日志输出 | 增强 `internal/platform/secret/` + SignalEnvelope metadata 过滤（已有 `isSensitiveText`） |
| Hook 安全 | 脚本超时 + 输出截断 | `internal/agent/runtime/process/` 已有 stderr tail + 超时控制 |
| 信任边界 | 显式声明部署信任级别 | 扩展 `RuntimeSandboxProfile` 和 `RuntimeWorkspacePolicy` 值类型 |

---

## 3. 差距分析与改造机会

### 3.1 成熟度对比矩阵

| 能力维度 | Orrery 现状 | Symphony SPEC | 差距 | 优先级 |
|----------|------------|---------------|------|--------|
| **自主编排循环** | 简单 Daemon poll loop | Poll-Claim-Dispatch-Reconcile 四阶段显式状态机 | 中等 | 🔴 高 |
| **并发控制** | 隐式，无全局上限 | 显式 `max_concurrent_agents` + per-state 限制 | 中等 | 🔴 高 |
| **工作空间隔离** | 隐式在 runtime 层 | 显式 WorkspaceManager，per-issue 目录隔离 | 中等 | 🟡 中 |
| **重试策略** | SignalBus handler 级别 | 任务级别指数退避 + 可配置上限 | 较大 | 🔴 高 |
| **停滞检测** | 仅 Watchdog 硬超时 | 独立 stall detector + 输出监控 | 较大 | 🟡 中 |
| **工作流定义** | AgentProfile + Mission Brief | WORKFLOW.md 版本化声明式合约 | 大 | 🔴 高 |
| **状态恢复** | RecoverySweeper + event store | Tracker reconciliation + filesystem scan | 中等 | 🟡 中 |
| **规范文档** | CLAUDE.md + 结构指南 | 正式 SPEC.md (RFC 2119) | 大 | 🟡 中 |
| **生命周期** | 22-signal pipeline | 5-state orchestration (简化) | Orrery 更优 | - |
| **事件溯源** | 65+ events + projections | 无（依赖 tracker 状态） | Orrery 更优 | - |
| **组件装配** | ComponentRegistry + Assembler | 无显式装配层 | Orrery 更优 | - |
| **协作系统** | Discussion + mention + inbox | 无 | Orrery 更优 | - |
| **多人审批** | QA → Review → Approve/Reject | 无 | Orrery 更优 | - |

### 3.2 Symphony ↔ Orrery 领域实体映射

将 Symphony 的核心实体对应到 Orrery 当前领域模型，明确改造的"翻译层"：

| Symphony 实体 | Orrery 对应 | 映射说明 |
|--------------|-----------|---------|
| `Issue` | `TaskContract` (`internal/mission/task/`) | Symphony 的 issue 是 tracker 中的工作项；Orrery 的 TaskContract 是更丰富的任务契约（含 evidence/QA/delivery 要求） |
| `Issue.id` / `Issue.identifier` | `TaskID` / `TaskContract.Title` | Orrery 使用类型化 ID 值对象，比 raw string 更安全 |
| `Issue.state` (tracker state) | `TaskStatus` (11-state FSM) | Orrery 的 FSM 更精细，terminal 状态为 Approved/Cancelled |
| `WorkflowDefinition` (`WORKFLOW.md`) | `AgentProfile` + `Mission` | 尚无直接等价物；`AgentProfile` 表达 Agent 能力，`Mission` 表达目标，但要新增 `WorkflowDefinition` 来桥接 |
| `Orchestrator Runtime State` | `internal/agent/runtime/daemon/` 的状态 | Daemon 当前仅有简单 poll loop，无显式 claimed/running/retry maps |
| `Workspace` (per-issue dir) | 隐式在 runtime adapter 的 `ExecOptions.Cwd` | 未提升为独立领域概念 |
| `Run Attempt` | `AgentRunState` (`internal/agent/lifecycle/state.go`) | 已跟踪 `Attempt` 字段，但缺少 attempt 级别的 retry policy |
| `Live Session` | `RuntimeSession` (`internal/agent/runtime/core/`) | 已有 `Messages` channel + `Result` channel，但缺少 `turn_count` 和事件时间戳 |
| `Retry Entry` | `SignalBus` 的 `RetryPolicy` | 仅在 handler 级别，非任务级别；无独立的 retry queue |
| `Config Layer` (typed getters) | `internal/platform/config/` | 已有配置基础设施，可扩展 |
| `Agent Runner` | `runtime + PipelineExecutor` | 已有时序流水线（22 signals + middleware），可增加 turn 循环 |

### 3.3 关键发现

1. **Symphony 是简化版的执行引擎** —— 它把多 Agent 协作简化为 "issue → workspace → agent → PR" 的线性流程，而 Orrery 支持更复杂的任务图（DAG）、团队协作、多人审批。

2. **Orrery 的领域模型远更丰富** —— 事件溯源、投影、CQRS、组件装配、讨论系统等都是 Symphony 不具备的。改造应增强而非替换。

3. **Symphony 强在执行闭环的工程化** —— Poll-Claim-Dispatch-Reconcile 循环、stall detection、exponential backoff、workspace isolation 都是生产级执行系统必需的，Orrery 在这些方面有明确的提升空间。

4. **WORKFLOW.md 是最有价值的借鉴模式** —— 将工作流定义版本化、声明式、模板化的理念可以直接映射到 Orrery 的 Mission Brief / AgentProfile 体系。

5. **Symphony 的简洁是一种选择** —— 它刻意不引入数据库，依赖 tracker 作为唯一状态存储。Orrery 的 event store 是更强大的方案，但可以参考 Symphony 的恢复哲学（reconciliation-first）。

---

## 4. 改造方案建议

### 4.1 方案 A：引入 MissionWorkflow 声明式定义（推荐优先实施）

**目标**: 将 Symphony 的 WORKFLOW.md 理念映射到 Orrery 体系，创建版本化的 Mission 工作流模板。

**设计**:

```yaml
# MISSION_WORKFLOW.yaml（版本化在项目仓库中）
---
mission:
  kind: design_document    # 任务类型
  title_template: "{{.topic}} 系统改造设计文档"

team:
  lead_profile: lead-agent
  worker_pool:
    - profile: profile-108b661386c13b9b0d55cb117efff2f0  # claudeCode worker
      selector: priority
  reviewers:
    - user: superadmin

task_graph:
  stages:
    - name: research
      description: "阅读 {{.source_url}}，分析现有体系"
      agent_profile: profile-108b661386c13b9b0d55cb117efff2f0
      tools: [web_fetch, web_search, glob, grep, read]
      expected_artifacts: [research_notes]
      timeout_seconds: 1800

    - name: draft
      description: "编写设计文档"
      depends_on: [research]
      agent_profile: profile-108b661386c13b9b0d55cb117efff2f0
      expected_artifacts: [design_document]
      timeout_seconds: 3600

    - name: review
      description: "人工评审"
      depends_on: [draft]
      assignee: user:superadmin
      review_criteria:
        - "架构分析准确"
        - "借鉴要点合理"
        - "实施方案可行"

execution:
  max_concurrent_tasks: 1
  retry:
    max_attempts: 3
    backoff_base_ms: 10000
    max_backoff_ms: 300000
  stall_timeout_ms: 300000
  workspace:
    isolation: per_task

hooks:
  before_stage: "echo 'stage starting'"
  after_stage: "echo 'stage complete'"
  on_failure: "notify_lead"
```

**需要改动的模块**:

| 模块 | 改动 |
|------|------|
| `internal/mission/core/` | 新增 `WorkflowDefinition` 类型 |
| `internal/mission/task/` | TaskGraph → 支持 DAG 依赖 |
| `internal/agent/assembly/` | AgentAssembler 支持从 Workflow 装配 |
| `internal/app/missioncontrol/` | WorkflowLoader service |

### 4.2 方案 B：强化 Orchestrator 执行循环

**目标**: 将 Symphony 的 Poll-Claim-Dispatch-Reconcile 四阶段循环显式化到 Orrery 的 Daemon 中。

**设计**:

```go
// 改造后的 Orchestrator
type Orchestrator struct {
    Poller       TaskPoller       // 从 task repository 拉取候选
    Claimer      TaskClaimer      // 并发控制 + 乐观锁 claim
    Dispatcher   TaskDispatcher   // 创建 workspace + 启动 agent
    Reconciler   TaskReconciler   // 停滞检测 + 恢复 + 重试
    State        *OrchestratorState // 内存状态快照
}

type OrchestratorState struct {
    PollInterval       time.Duration
    MaxConcurrent      int
    Running            map[TaskID]*RunningEntry
    Claimed            map[TaskID]bool
    RetryAttempts      map[TaskID]*RetryEntry
    Completed          map[TaskID]bool
}

type RunningEntry struct {
    TaskID      TaskID
    AgentID     AgentID
    StartedAt   time.Time
    LastOutput  time.Time   // 停滞检测用
    Workspace   string
    Session     SessionHandle
}

type RetryEntry struct {
    Attempt     int
    LastError   error
    NextRetryAt time.Time
}
```

**需要改动的模块**:

| 模块 | 改动 |
|------|------|
| `internal/agent/runtime/daemon/` | 重构为四阶段 Orchestrator |
| `internal/agent/runtime/daemon/` | 新增 `orchestrator.go`, `claimer.go`, `dispatcher.go`, `reconciler.go` |
| `internal/platform/` | 可能新增 workspace manager |
| `internal/mission/task/` | RetryEntry 持久化 |

### 4.3 方案 C：显式 Workspace 管理

**目标**: 将工作空间隔离抽象为独立领域概念。

**设计**:

```go
// internal/agent/workspace/manager.go
type WorkspaceManager interface {
    Create(ctx context.Context, taskID TaskID) (*Workspace, error)
    Prepare(ctx context.Context, ws *Workspace, hooks []Hook) error
    Snapshot(ctx context.Context, ws *Workspace) (*ArtifactPack, error)
    Cleanup(ctx context.Context, ws *Workspace) error
    List(ctx context.Context) ([]*Workspace, error)
}

type Workspace struct {
    ID        WorkspaceID
    TaskID    TaskID
    RootPath  string
    State     WorkspaceState  // Creating, Ready, Active, Snapshotting, Cleaned
    CreatedAt time.Time
    Hooks     []Hook
}

type WorkspaceState string
const (
    WorkspaceCreating    WorkspaceState = "creating"
    WorkspaceReady       WorkspaceState = "ready"
    WorkspaceActive      WorkspaceState = "active"
    WorkspaceSnapshotting WorkspaceState = "snapshotting"
    WorkspaceCleaned     WorkspaceState = "cleaned"
)
```

**需要改动的模块**:

| 模块 | 改动 |
|------|------|
| `internal/agent/workspace/` | **新建包**：manager, types, errors, hooks |
| `internal/agent/runtime/` | 适配 WorkspaceManager 接口 |
| `internal/platform/filestore/` | 可能需要扩展 |

### 4.4 方案 D：任务级指数退避重试

**目标**: 将重试策略从 SignalBus handler 级别提升到任务级别。

**设计**:

```go
// internal/mission/task/retry.go
type RetryPolicy struct {
    MaxAttempts    int           // 最大重试次数（默认 3）
    BaseBackoff    time.Duration // 基础退避时间（默认 10s）
    MaxBackoff     time.Duration // 最大退避上限（默认 300s）
    StallTimeout   time.Duration // 停滞检测超时（默认 300s）
    TotalTimeout   time.Duration // 总执行超时（默认 1800s）
}

func (p RetryPolicy) BackoffForAttempt(attempt int) time.Duration {
    delay := time.Duration(float64(p.BaseBackoff) * math.Pow(2, float64(attempt-1)))
    if delay > p.MaxBackoff {
        return p.MaxBackoff
    }
    return delay
}
```

**需要改动的模块**:

| 模块 | 改动 |
|------|------|
| `internal/mission/task/` | 新增 `retry.go`，RetryPolicy 值类型 |
| `internal/agent/runtime/daemon/` | 整合停滞检测 + 退避调度 |
| `internal/mission/event/` | 新增 `TaskRetryScheduled`, `TaskRetryExhausted` 事件 |

### 4.5 方案 E：编写 Orrery 正式 SPEC 规范

**目标**: 参照 Symphony SPEC.md 的格式，为 Orrery 编写语言无关的 Agent Mission Control 协议规范。

**SPEC 大纲**:

```
ORRERY_SPEC.md
├── 1. Problem Statement
├── 2. Goals & Non-Goals
├── 3. System Overview
│   ├── 3.1 Core Abstractions (Mission, Task, Agent, Team, Discussion)
│   ├── 3.2 Component Architecture
│   └── 3.3 Trust & Security Boundaries
├── 4. Core Domain Model
│   ├── 4.1 Mission & MissionStatus
│   ├── 4.2 Task & TaskContract (11-state FSM)
│   ├── 4.3 Agent & AgentProfile
│   ├── 4.4 Team & Staffing
│   ├── 4.5 Discussion & Mentions
│   └── 4.6 Evidence & QA
├── 5. Agent Lifecycle Protocol
│   ├── 5.1 22-Signal Pipeline
│   ├── 5.2 SignalBus Contract
│   ├── 5.3 Failure Policies
│   └── 5.4 Component Assembly
├── 6. Execution Runtime
│   ├── 6.1 Orchestrator Loop (Poll-Claim-Dispatch-Reconcile)
│   ├── 6.2 Workspace Isolation
│   ├── 6.3 Concurrency Model
│   ├── 6.4 Retry & Backoff
│   └── 6.5 Stall Detection & Recovery
├── 7. Event System
│   ├── 7.1 Event Store Contract
│   ├── 7.2 Projection Model
│   └── 7.3 Outbox & Delivery Guarantees
├── 8. Mission Workflow Template
│   ├── 8.1 MISSION_WORKFLOW.yaml Format
│   ├── 8.2 Template Variables
│   └── 8.3 Hook Contracts
├── 9. Transport & API
│   ├── 9.1 HTTP API (OpenAPI)
│   ├── 9.2 WebSocket Protocol
│   └── 9.3 MCP Integration
└── 10. Operational Concerns
    ├── 10.1 Multi-Tenancy
    ├── 10.2 Audit Trail
    ├── 10.3 Rate Limiting
    └── 10.4 Disaster Recovery
```

---

## 5. 实施路径

### 5.1 分阶段路线图

```
Phase 1 (2-3 sprints): 执行循环强化
  ├── 方案 B: 重构 Orchestrator 为四阶段循环
  ├── 方案 D: 任务级指数退避重试 + 停滞检测
  └── 方案 C: WorkspaceManager 抽象

Phase 2 (2-3 sprints): 工作流声明化
  ├── 方案 A: MISSION_WORKFLOW.yaml 格式定义
  ├── WorkflowLoader + Template Engine
  └── 现有 Mission Brief 迁移到 WORKFLOW 格式

Phase 3 (1-2 sprints): 规范与文档
  ├── 方案 E: ORRERY_SPEC.md 正式规范
  ├── 社区可用的参考实现标注
  └── RFC 2119 语言风格

Phase 4 (持续): 生态与集成
  ├── WORKFLOW 模板库
  ├── 多 tracker 适配（GitHub Issues, Linear, Jira）
  └── 跨实例编排协议
```

### 5.2 Phase 1 详细任务分解

| # | 任务 | 涉及文件 | 估时 |
|---|------|---------|------|
| 1.1 | 定义 `OrchestratorState` 结构体（含 running/claimed/retry maps） | `internal/agent/runtime/daemon/orchestrator.go` (新建) | 0.5d |
| 1.2 | 实现 `TaskPoller` 接口 — 从 `internal/mission/task/repository.go` 拉取候选 | `internal/agent/runtime/daemon/poller.go` (新建) | 1d |
| 1.3 | 实现 `TaskClaimer` — 全局 `max_concurrent` + per-state 上限 + `claimed` set 去重 | `internal/agent/runtime/daemon/claimer.go` (新建) | 1d |
| 1.4 | 实现 `TaskDispatcher` — 集成 WorkspaceManager + `RuntimeBackend.Execute` | `internal/agent/runtime/daemon/dispatcher.go` (新建) | 1.5d |
| 1.5 | 实现 `TaskReconciler` — stall detection（基于 `last_codex_timestamp`）+ 恢复 | `internal/agent/runtime/daemon/reconciler.go` (新建) | 1.5d |
| 1.6 | 实现 `RetryPolicy` + 指数退避计算 `BackoffForAttempt(n)` | `internal/mission/task/retry.go` (新建) | 0.5d |
| 1.7 | 定义 `WorkspaceManager` 接口 + 基础文件系统实现 | `internal/agent/workspace/` (新建包: `manager.go`, `types.go`, `errors.go`, `hooks.go`) | 1.5d |
| 1.8 | 集成测试 + 迁移现有 `Daemon.RunOnce` 调用方到新 Orchestrator | `internal/agent/runtime/daemon/*_test.go` | 1d |
| 1.9 | 停滞检测 + 指数退避 + workspace 隔离 边界场景测试 | `*_test.go` | 1d |

**Phase 1 总估算**: ~9.5 工作日

### 5.3 Phase 2 详细任务分解

| # | 任务 | 涉及文件 | 估时 |
|---|------|---------|------|
| 2.1 | 设计 `MISSION_WORKFLOW.yaml` schema（对齐 Symphony WORKFLOW.md 结构） | 设计文档 | 1d |
| 2.2 | 实现 YAML 解析 + 校验（参考 `agent/assembly/` 的校验模式） | `internal/mission/core/workflow_definition.go` (新建) | 1d |
| 2.3 | 实现 Liquid 模板引擎集成（严格模式：未知变量/过滤器 → 失败） | `internal/mission/core/workflow_template.go` (新建) | 1d |
| 2.4 | 实现 TaskGraph DAG → TaskContract 映射（依赖解析 + 拓扑排序） | `internal/mission/task/taskgraph/` (新建子包: `mapper.go`, `dag.go`) | 1d |
| 2.5 | WorkflowLoader — 从 repo/registry 加载并缓存 `MISSION_WORKFLOW.yaml` | `internal/app/missioncontrol/workflow_loader.go` (新建) | 1d |
| 2.6 | 扩展 `AgentAssembler.Build` 支持从 Workflow 装配（新增 `workflow_binding.go`） | `internal/agent/assembly/workflow_binding.go` (新建) | 1d |
| 2.7 | 改造 `MissionControlCommandService` 支持从 WORKFLOW 创建 Mission | `internal/app/missioncontrol/command_service.go` (修改) | 1d |
| 2.8 | 集成测试（WORKFLOW → Mission → TaskGraph → AgentAssembly 全链路） | `*_test.go` + `docs/product-planning/` | 1d |

**Phase 2 总估算**: ~8 工作日

### 5.4 兼容性保证

所有改造必须保持向后兼容：

- **Phase 1**: 新 Orchestrator 通过 feature flag 启用，旧 Daemon 保留为 fallback
- **Phase 2**: MISSION_WORKFLOW.yaml 是可选增强，现有 Mission Brief JSON API 不变
- **Phase 3**: SPEC 是增量文档，不影响代码
- 所有新接口遵循 Go 的 interface 隔离原则，不破坏现有实现
- 新增事件追加到 event store，不修改已有事件 schema

---

## 6. 风险与注意事项

### 6.1 架构差异风险

| 风险 | 说明 | 缓解措施 |
|------|------|---------|
| **过度简化** | Symphony 的线性 issue 模型可能不适合 Orrery 的复杂任务图 | 保留 TaskGraph DAG 能力，WORKFLOW 仅作为快速启动模板 |
| **状态分裂** | Orchestrator 内存状态 + Event Store 可能产生不一致 | Orchestrator 状态仅作运行时缓存，event store 保持为唯一真相源 |
| **性能开销** | 四阶段循环 + 停滞检测增加系统复杂度 | 通过 feature flag 渐进式启用，基准测试对比 |

### 6.2 实施风险

| 风险 | 说明 | 缓解措施 |
|------|------|---------|
| **范围蔓延** | SPEC 编写可能演变为长期项目 | 先写核心章节（1-5），后续迭代补充 |
| **兼容性断裂** | 新 Orchestrator 可能破坏现有 agent 合约 | 严格的接口隔离 + 集成测试 |
| **过度工程化** | 为未到来的规模做优化 | 聚焦当前痛点（重试、并发、恢复），按需扩展 |

### 6.3 不做的事项

- ❌ **不替换 Event Store**: Symphony 不引入数据库是适用于其简单场景的选择；Orrery 的事件溯源架构是核心优势，保留
- ❌ **不照搬 Elixir/OTP 模型**: Go 的并发模型不同；用 Go 惯用方式实现
- ❌ **不引入外部 tracker 依赖**: Orrery 自带完整的 Mission/Task 管理，不需要依赖 Linear/Jira
- ❌ **不简化 Agent 生命周期**: 22-signal pipeline 比 Symphony 的 5-state 更精细，不需要降级
- ❌ **不放弃协作功能**: Discussion/Mention/Inbox 是 Orrery 的差异化优势，保持独立演进

---

## 附录 A: Symphony SPEC 关键配置参数速查

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `poll_interval_ms` | 30000 | 轮询间隔 |
| `max_concurrent_agents` | (required) | 全局并发上限 |
| `max_turns` | 50 | Agent 最大轮次 |
| `retry.max_attempts` | 3 | 最大重试次数 |
| `retry.backoff_base_ms` | 10000 | 退避基数 |
| `retry.max_backoff_ms` | 300000 | 退避上限 |
| `stall_timeout_ms` | 300000 | 停滞检测超时 |
| `total_timeout_seconds` | 1800 | 总执行超时 |

## 附录 B: Orrery 22-Signal 与 Symphony 5-State 映射

| Orrery Signal | Symphony State |
|---------------|----------------|
| `BeforeTaskReceived` | (workflow parsing) |
| `AfterTaskReceived` | Unclaimed |
| `BeforeTaskAccepted` + `AfterTaskAccepted` | Claimed |
| `BeforeRuntimeStart` + `RuntimeStarted` | Running |
| `RuntimeProgress` | (output streaming) |
| `AgentFailed` / `AgentTimeout` | RetryQueued |
| `AgentBlocked` / `AgentCancelled` | Released |
| `AfterResultCommit` | (N/A - Symphony uses PR merge as terminal) |

## 附录 C: 参考资料

- [Symphony SPEC.md](https://github.com/openai/symphony/blob/main/SPEC.md)
- [Symphony GitHub Repository](https://github.com/openai/symphony)
- [InfoQ: OpenAI Open-Sources Symphony](https://www.infoq.com/news/2026/05/openai-symphony-agents/)
- [Verdent AI: Symphony Architecture Deep Dive](https://www.verdent.ai/tr/guides/openai-symphony-architecture-deep-dive)
- [Tessl: Symphony Analysis](https://tessl.io/blog/openai-open-sources-symphony-a-spec-for-orchestrating-codex-agents)
- Orrery `CLAUDE.md` — 项目结构指南
- Orrery `docs/agent-company-mission-control/project-structure-guidelines.md` — 架构约束
- Orrery `docs/product-planning/orrery-saas-product-architecture-plan.md` — SaaS 产品架构
