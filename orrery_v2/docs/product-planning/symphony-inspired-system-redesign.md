# 参考 Symphony SPEC 的系统改造设计文档

> **版本**: v1.0
> **日期**: 2026-06-16
> **参考**: [OpenAI Symphony SPEC](https://github.com/openai/symphony/blob/main/SPEC.md) (Draft v1)
> **范围**: Orrery v2 多 Agent 编排体系的改造升级

---

## 目录

1. [当前架构分析](#1-当前架构分析)
2. [Symphony 可借鉴的关键设计](#2-symphony-可借鉴的关键设计)
3. [改造方案建议](#3-改造方案建议)
4. [实施路径](#4-实施路径)
5. [风险评估与缓解](#5-风险评估与缓解)
6. [附录：术语对照表](#6-附录术语对照表)

---

## 1. 当前架构分析

### 1.1 Orrery v2 架构总览

Orrery v2 采用严格的 DDD（领域驱动设计）+ Ports & Adapters 架构，依赖方向从外向内：

```
transport → app → domain → platform
   (HTTP/WS/MCP)  (CQRS)  (agent/mission)  (DB/eventstore/queue)
```

**核心组件关系图：**

```
┌──────────────────────────────────────────────────────┐
│                   Transport Layer                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │ HTTP API │  │WebSocket │  │   MCP Gateway    │   │
│  │ (manual  │  │   Hub    │  │ (tool discovery, │   │
│  │  router) │  │          │  │  auth, routing)  │   │
│  └────┬─────┘  └────┬─────┘  └────────┬─────────┘   │
└───────┼──────────────┼─────────────────┼─────────────┘
        │              │                 │
┌───────┼──────────────┼─────────────────┼─────────────┐
│       ▼              ▼                 ▼              │
│                  App Layer (CQRS)                     │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐  │
│  │ CommandService│ │ QueryService │ │ Discussion   │  │
│  │ (mission,    │ │ (projection  │ │ Session Svc  │  │
│  │  resource,   │ │  reads)      │ │              │  │
│  │  operations) │ │              │ │              │  │
│  └──────┬───────┘ └──────┬───────┘ └──────┬───────┘  │
└─────────┼─────────────────┼─────────────────┼─────────┘
          │                 │                 │
┌─────────┼─────────────────┼─────────────────┼─────────┐
│         ▼                 ▼                 ▼          │
│                   Domain Layer                        │
│  ┌──────────────────────────────────────────────┐    │
│  │              Agent System                     │    │
│  │  ┌────────┐ ┌──────────┐ ┌────────────────┐  │    │
│  │  │ Core   │→│ Assembly │→│   Lifecycle    │  │    │
│  │  │Profile │ │(compose) │ │ (22 signals,   │  │    │
│  │  │Identity│ │          │ │  SignalBus,    │  │    │
│  │  │BaseAgent│ │          │ │  Pipeline)     │  │    │
│  │  └────────┘ └──────────┘ └───────┬────────┘  │    │
│  │                                   │           │    │
│  │  ┌────────────────────────────────┘           │    │
│  │  │  Runtime Adapters                         │    │
│  │  │  (Claude, Gemini, Codex, Eino, Human)     │    │
│  │  └───────────────────────────────────────────┘    │
│  └──────────────────────────────────────────────────┘    │
│                                                         │
│  ┌──────────────────────────────────────────────┐    │
│  │            Mission System                     │    │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────────┐  │    │
│  │  │ Mission  │ │  Task    │ │   Event      │  │    │
│  │  │(aggregate│ │(11-state │ │ (56 types,   │  │    │
│  │  │ root)    │ │ FSM)     │ │  eventstore) │  │    │
│  │  └──────────┘ └──────────┘ └──────┬───────┘  │    │
│  │                                    │          │    │
│  │  ┌─────────────────────────────────┘          │    │
│  │  │  Projection (ReadModel builder)           │    │
│  │  └───────────────────────────────────────────┘    │
│  └──────────────────────────────────────────────────┘    │
│                                                         │
│  ┌──────────────────────────────────────────────┐    │
│  │           Resource Layer                      │    │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────────┐  │    │
│  │  │  Tools   │ │  Skills  │ │ MCP Servers  │  │    │
│  │  │(registry,│ │(registry,│ │(config,      │  │    │
│  │  │ resolver)│ │ resolver)│ │ discovery)   │  │    │
│  │  └──────────┘ └──────────┘ └──────────────┘  │    │
│  └──────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────┘
          │
┌─────────┼─────────────────────────────────────────────┐
│         ▼                                              │
│               Platform Layer                           │
│  ┌────────┐ ┌──────────┐ ┌────────┐ ┌─────────────┐  │
│  │  DB    │ │EventStore│ │  UoW   │ │ Outbox/Queue│  │
│  │(SQLite)│ │(append-  │ │(tx     │ │(at-least-   │  │
│  │        │ │ only)    │ │ coord) │ │ once)       │  │
│  └────────┘ └──────────┘ └────────┘ └─────────────┘  │
│  ┌────────┐ ┌──────────┐ ┌────────┐ ┌─────────────┐  │
│  │ Config │ │ Logging  │ │ Metrics│ │ FileStore   │  │
│  └────────┘ └──────────┘ └────────┘ └─────────────┘  │
└───────────────────────────────────────────────────────┘
```

### 1.2 各层深度分析

#### 1.2.1 Agent 系统 — 优势与不足

**优势：**

| 特性 | 描述 | 文件位置 |
|------|------|----------|
| 多运行时适配 | Claude, Gemini, Codex, Eino, Human 等多运行时统一接口 | `agent/runtime/` |
| 组装器模式 | `AgentAssembler` 从 Profile + Tools + Skills + Memory 组合 Agent 实例 | `agent/assembly/assembler.go` |
| 22 信号生命周期 | 完整的信号驱动流水线：Task接收→接受→上下文构建→运行时执行→结果处理→回复 | `agent/lifecycle/names.go` |
| 失败策略 | 每个信号处理器支持 Stop/Continue/Degrade/Retry 四种失败策略 | `agent/lifecycle/handler.go` |
| 资源绑定 | Tools、Skills、MCP 作为资源绑定到 Agent，非独立角色 | `agent/assembly/bindings.go` |

**不足：**

| 问题 | 影响 | 严重程度 |
|------|------|----------|
| `BaseAgent.Handle()` 硬编码流水线 | 无法按任务类型定制执行流程，Agent 行为不可通过配置调整 | 🔴 高 |
| SignalBus 纯内存实现 | 进程重启丢失所有运行时状态，无恢复机制 | 🔴 高 |
| 无任务隔离工作区 | Agent 在共享文件系统中执行，安全边界模糊 | 🟡 中 |
| 无停滞检测 | 长时间无响应的 Agent 会话不会被自动终止 | 🟡 中 |
| PipelineExecutor 与 BaseAgent 脱节 | lifecycle 包中存在 PipelineExecutor 但 BaseAgent 未使用，存在两套流水线抽象 | 🟡 中 |

#### 1.2.2 Mission/Task 系统 — 优势与不足

**优势：**

| 特性 | 描述 |
|------|------|
| 11 状态任务 FSM | Draft→Assigned→Accepted→InProgress→Submitted→InReview→Approved/Rejected→Rework，加 Blocked/Cancelled |
| 丰富的任务契约 | TaskContract 包含证据要求、验收标准、QA 策略、个人目标 |
| 乐观并发控制 | 基于版本号的任务状态转换，HTTP 层支持 If-Match 头 |
| 领域事件 | 56+ 事件类型覆盖 Mission 生命周期、任务流转、讨论、记忆等 |

**不足：**

| 问题 | 影响 | 严重程度 |
|------|------|----------|
| 无聚合根清晰边界 | Mission 和 Task 分离但边界模糊，缺少显式的领域服务 | 🟡 中 |
| 投影无快照 | ReadModel 必须从事件流头部重放，随事件增长性能下降 | 🟡 中 |
| 无 Saga/Process Manager | 跨聚合根的长时间业务流程无显式协调者 | 🟡 中 |
| 事件版本仅 v1 | 无事件 schema 演进策略，领域模型变更时迁移困难 | 🟢 低 |

#### 1.2.3 编排与调度 — 与 Symphony 的核心差距

这是 Orrery 相比 Symphony 最需要改进的领域：

| Symphony 能力 | Orrery 现状 | 差距 |
|---------------|-------------|------|
| **Poll-loop 编排器** | 无独立编排器，任务通过 API 手动触发 | ❌ 缺失 |
| **仓库级工作流契约 (WORKFLOW.md)** | 无标准化的任务执行契约文件 | ❌ 缺失 |
| **每任务隔离工作区** | 共享文件系统，无工作区概念 | ❌ 缺失 |
| **指数退避重试队列** | execution/queue 有基本重试，但无标准退避公式 | 🟡 部分 |
| **动态配置热加载** | 配置需重启生效 | ❌ 缺失 |
| **协调-执行分离** | 与 Agent 生命周期混合 | 🟡 部分 |
| **停滞检测与自动终止** | 无 | ❌ 缺失 |
| **启动恢复（无 DB 依赖）** | 依赖 SQLite 持久化 | N/A（设计选择不同） |
| **可选 HTTP 扩展** | 有完整的 HTTP API + WebSocket + MCP | ✅ 优于 Symphony |
| **多租户 SaaS** | 内置租户边界 | ✅ 优于 Symphony |
| **Web 前端** | 完整 SvelteKit SPA | ✅ 优于 Symphony |
| **事件溯源** | 完整事件存储 + 投影 | ✅ 优于 Symphony |

### 1.3 架构优势总结

Orrery v2 在以下方面显著优于 Symphony 的定位：

1. **领域模型深度**: TaskContract 远超 Symphony 的简单 Issue 模型，包含证据、QA、验收标准
2. **多 Agent 组装**: AgentAssembler 比 Symphony 的单 Agent 模型灵活得多
3. **事件溯源**: 完整的审计追踪和时间旅行调试能力
4. **组织建模**: 部门/职位/角色/监督策略的完整层次结构
5. **多租户 SaaS**: 从第一天就内置租户隔离
6. **Web 前端 + 实时更新**: Symphony 明确声明 "Rich web UI" 是非目标

### 1.4 架构不足总结

Orrery v2 在以下方面可以借鉴 Symphony：

1. **缺少工作流契约层**: 没有类似 WORKFLOW.md 的版本化、仓库级执行策略
2. **缺少隔离工作区**: Agent 执行缺乏文件系统级安全边界
3. **缺少独立编排器**: 轮询-调度-协调-重试 的逻辑分散在多处
4. **流水线硬编码**: Agent 执行流程不可配置
5. **缺少热加载**: 配置变更需要重启
6. **缺少停滞检测**: 长时间运行的 Agent 无人看管

---

## 2. Symphony 可借鉴的关键设计

### 2.1 五层抽象模型

Symphony 定义了清晰的五层抽象，这是最值得借鉴的架构思想：

```
┌─────────────────────────────────┐
│  1. Policy Layer (仓库定义)      │  ← WORKFLOW.md 提示词 + 团队规则
├─────────────────────────────────┤
│  2. Configuration Layer (类型化) │  ← YAML front matter → 类型化运行时设置
├─────────────────────────────────┤
│  3. Coordination Layer (编排器)  │  ← 轮询循环、调度、重试、协调
├─────────────────────────────────┤
│  4. Execution Layer (工作区+Agent)│  ← 文件系统生命周期、Agent 子进程
├─────────────────────────────────┤
│  5. Integration Layer (适配器)   │  ← 外部 API 调用和数据规范化
├─────────────────────────────────┤
│  6. Observability Layer (可观测) │  ← 结构化日志 + 可选状态面板
└─────────────────────────────────┘
```

**对 Orrery 的启示**: 当前 Orrery 的 Coordination 和 Execution 层耦合在一起（都发生在 Agent 生命周期内）。借鉴此模型可以将 "谁来做"（Execution）和 "做什么、何时做、做错了怎么办"（Coordination）清晰分离。

### 2.2 编排器状态机（Section 7）

Symphony 的编排器拥有唯一权威的运行时状态，所有 Worker 结果向其汇报：

```
状态转换:
  Unclaimed → Claimed → Running → Released
                ↓           ↓
            RetryQueued → (重试计时器触发)
                              ↓
                        重新调度或释放
```

**关键洞察**:
- **单一权威状态源**: 编排器是唯一修改调度状态的组件
- **正常退出 ≠ 完成**: Worker 正常退出后，编排器仍安排 1 秒的延续重试以检查 Issue 是否仍需处理
- **运行尝试生命周期**: 10 个阶段（PreparingWorkspace → Succeeded/Failed/TimedOut/Stalled/Canceled）

**对 Orrery 的启示**: 当前 Orrery 没有这种显式的编排状态机。Mission/Task 的状态机关注业务状态，而非执行调度状态。需要在任务执行层面引入类似的调度状态跟踪。

### 2.3 工作区隔离与安全（Section 9）

Symphony 的三条安全不变量：

1. **工作区路径约束**: Agent 只能在 per-issue 工作区路径下运行
2. **根目录包含**: 工作区路径必须在配置的 workspace root 内
3. **路径名消毒**: 仅允许 `[A-Za-z0-9._-]` 字符

**对 Orrery 的启示**: 当前 Orrery Agent 执行时没有文件系统级别的隔离。虽然 Agent 运行时有权限声明，但缺乏实际的 OS 级别工作区隔离。这对于 SaaS 多租户场景尤为重要。

### 2.4 仓库级工作流契约 — WORKFLOW.md（Section 5）

这是 Symphony 最创新的设计：

```yaml
---
tracker:
  kind: linear
  project_slug: my-project
polling:
  interval_ms: 30000
agent:
  max_concurrent_agents: 5
  max_turns: 20
  max_retry_backoff_ms: 300000
codex:
  command: codex app-server
  turn_timeout_ms: 3600000
---

# 以下是 Agent 提示词模板

You are working on issue {{ issue.identifier }}: {{ issue.title }}

## Issue Description
{{ issue.description }}

## Instructions
...
```

**设计亮点**:
- **版本控制**: 工作流策略与代码同在 Git 中
- **YAML 前置元数据 + Markdown 提示词体**: 结构化配置与自然语言提示词合一
- **严格模板渲染**: 未知变量/过滤器必须失败，不静默降级
- **动态热加载**: 文件变更自动检测并应用，无需重启

**对 Orrery 的启示**: 这是 Orrery 最应该直接采用的设计。当前 Agent Profile 中的 system prompt 和工具绑定存储在数据库中，缺乏版本控制和仓库级管理。引入 `MISSION.md` 或 `WORKFLOW.md` 概念可以让团队将任务执行策略与代码一起版本化。

### 2.5 重试与退避策略（Section 8.4）

Symphony 的重试模型区分两种场景：

| 场景 | 延迟 | 目的 |
|------|------|------|
| 延续重试（正常退出后） | 固定 1000ms | 快速检查 Issue 是否仍需处理 |
| 失败重试（异常退出） | `min(10000 × 2^(attempt-1), max_retry_backoff_ms)` | 指数退避，默认上限 5 分钟 |

**对 Orrery 的启示**: Orrery 的 execution/queue 有基本的重试机制，但没有区分 "正常完成但需继续" 和 "异常失败需重试" 两种场景。这个区分在生产中非常关键。

### 2.6 停滞检测（Section 8.5）

Symphony 在每个 Poll Tick 执行停滞检测：

```
elapsed_ms = now - last_event_timestamp (或 started_at)
if elapsed_ms > stall_timeout_ms: 终止 worker，安排重试
```

**对 Orrery 的启示**: 这是关键的安全保障。Orrery 当前没有自动检测 Agent 会话是否停滞的机制。

### 2.7 动态配置热加载（Section 6.2）

核心原则：
- 检测 `WORKFLOW.md` 文件变更
- 重新读取并应用配置，无需重启
- 影响未来的调度决策，不强制终止运行中的会话
- 无效配置不崩溃服务，保持上一个有效配置

**对 Orrery 的启示**: 生产环境中，修改 Agent Profile、工具绑定、运行时设置等需要重启服务。引入热加载能力可以大幅提升运维体验。

### 2.8 扩展模型（Section 13.7 + Appendix A）

Symphony 的扩展设计干净：

- **HTTP Server 扩展**: 可选，通过 `server.port` 或 `--port` 启用
- **SSH Worker 扩展**: 可选，通过 `worker.ssh_hosts` 启用远程执行
- **linear_graphql 工具扩展**: 可选，通过实现特定工具接口扩展

扩展遵循 "opt-in by config" 原则，核心不依赖任何扩展。

**对 Orrery 的启示**: Orrery 的 MCP Gateway 和 Tool Router 已经有类似的可插拔设计，但配置化程度不如 Symphony。可以将更多能力（如远程执行、额外监控面板）作为可选扩展模块。

---

## 3. 改造方案建议

### 3.1 总体改造策略

**原则**: **增强而非重写**。Orrery v2 在领域模型深度、多 Agent 组装、事件溯源方面已经超越 Symphony 的定位。改造的核心是在现有架构上**补充编排层和工作流契约层**，而非替换现有系统。

**改造优先级矩阵**:

```
                    高影响
                      │
        ② 工作区隔离   │   ① 编排器 + 工作流契约
        ④ 停滞检测    │   ③ 动态热加载
                      │
    ──────────────────┼──────────────────
                      │
        ⑤ 流水线配置化 │   ⑥ 远程执行扩展
        ⑦ 快照策略    │   ⑧ 重试策略增强
                      │
                    低影响
     低复杂度                      高复杂度
```

### 3.2 方案一：引入工作流契约层（P0 — 最高优先级）

**目标**: 在 Orrery 中引入类似 `WORKFLOW.md` 的仓库级任务执行策略文件。

**设计**:

在 Mission 或仓库根目录新增 `MISSION.md`（或复用现有 Skill 包结构）：

```yaml
---
# MISSION.md — 仓库级任务执行策略
version: "1.0"

# 编排设置
orchestration:
  max_concurrent_tasks: 5
  max_retry_backoff_sec: 300
  poll_interval_sec: 30
  stall_timeout_sec: 300

# Agent 设置
agent:
  max_turns: 20
  turn_timeout_sec: 3600
  approval_policy: auto_approve_commands
  runtime: claude_code  # 覆盖默认运行时

# 工作区设置
workspace:
  root: "./.orrery/workspaces"
  hooks:
    after_create: |
      git clone {{ repo_url }} .
      npm install
    before_run: |
      git pull origin main
    after_run: |
      git status > .orrery/run-status.txt

# 任务路由规则
routing:
  required_labels: ["ready-for-agent"]
  active_states: ["Todo", "In Progress"]
  terminal_states: ["Done", "Cancelled"]

# 提示词模板（Markdown 正文）
---

# {{ mission.title }}

## 任务目标
{{ task.goal }}

## 个人目标
{{ task.worker_personal_goal }}

## 预期交付物
{{ task.expected_deliverables | join:"\n" }}

## 验收标准
{{ task.acceptance_criteria | join:"\n" }}

## 证据要求
{{ task.evidence_requirements | join:"\n" }}

## 上下文
- Mission: {{ mission.id }}
- Task: {{ task.id }}
- Attempt: {{ attempt | default:"首次" }}
```

**实现要点**:

1. **新增 `internal/orchestration/workflow/` 包**
   - `loader.go` — 文件发现和路径解析（优先显式路径 → 默认 `./MISSION.md`）
   - `parser.go` — YAML front matter + Markdown body 分离
   - `validator.go` — 工作流配置验证
   - `watcher.go` — 文件变更检测 + 动态重载

2. **新增 `internal/orchestration/config/` 包**
   - `config.go` — 类型化 getter，默认值 + 环境变量展开
   - `reloader.go` — 动态重载信号发射

3. **扩展 Agent Profile**
   - 添加 `WorkflowRef` 字段指向工作流文件
   - Profile 中的 system prompt 可以引用工作流模板

**改造影响**:
- `agent/core/profile.go`: 新增 `WorkflowRef` 字段
- `agent/assembly/assembler.go`: 支持从工作流加载 prompt
- 新增 CLI flag: `--workflow ./path/to/MISSION.md`

### 3.3 方案二：引入独立编排器（P0 — 最高优先级）

**目标**: 在 Agent 生命周期之外，新增一个独立的编排协调层。

**设计**:

```
┌─────────────────────────────────────────────┐
│            Orchestrator (新增)               │
│                                              │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  │
│  │ Poll     │  │ Dispatch │  │ Reconcile │   │
│  │ Manager  │→ │ Manager  │→ │ Manager   │   │
│  └──────────┘  └──────────┘  └───────────┘  │
│       │              │              │        │
│  ┌────┴──────┐ ┌─────┴──────┐ ┌────┴─────┐  │
│  │ Task      │ │ Concurrency│ │ Stall    │   │
│  │ Candidate │ │ Controller │ │ Detector │   │
│  │ Selector  │ │            │ │          │   │
│  └───────────┘ └────────────┘ └──────────┘  │
│                                              │
│  运行时状态（内存）:                           │
│  ┌──────────────────────────────────────┐    │
│  │ running: map[TaskID]RunningEntry     │    │
│  │ claimed: set[TaskID]                 │    │
│  │ retry_attempts: map[TaskID]RetryEntry│    │
│  │ completed: set[TaskID]               │    │
│  └──────────────────────────────────────┘    │
└─────────────────────────────────────────────┘
         │                    │
         ▼                    ▼
┌─────────────┐    ┌─────────────────────┐
│ 现有的       │    │ 现有的              │
│ Agent       │    │ Event Store         │
│ Assembly +  │    │ (持久化事件 +       │
│ Runtime     │    │  投影 ReadModel)    │
└─────────────┘    └─────────────────────┘
```

**核心接口**:

```go
// internal/orchestration/orchestrator.go

// Orchestrator 拥有唯一的调度状态权威
type Orchestrator interface {
    // 启动编排循环
    Start(ctx context.Context) error
    // 停止（优雅关闭）
    Stop(ctx context.Context) error
    // 手动触发一次轮询
    TriggerPoll(ctx context.Context) error
    // 获取运行时快照
    Snapshot() RuntimeSnapshot
}

// TaskCandidate 调度候选任务
type TaskCandidate struct {
    TaskID     mission.TaskID
    MissionID  mission.MissionID
    Priority   int
    State      task.Status
    CreatedAt  time.Time
    Assignee   agent.AgentInstanceID
}

// RuntimeSnapshot 运行时快照
type RuntimeSnapshot struct {
    Running   []RunningEntry
    Retrying  []RetryEntry
    Completed int
    Totals    TokenTotals
}
```

**调度算法（参考 Symphony Section 8 + 16）**:

```
每个 Poll Tick:
  1. 协调运行中的任务 (Reconcile):
     a. 停滞检测: 遍历 running，elapsed > stall_timeout → 终止 + 重试
     b. 状态刷新: 查询任务当前状态，terminal → 清理，non-active → 终止
  2. 调度前验证: 工作流配置有效？
  3. 获取候选任务: 查询 status IN active_states 且 assignee 匹配
  4. 排序: priority ASC → created_at ASC → task_id 字典序
  5. 调度:
     while slots > 0 and candidates > 0:
       task = candidates.pop()
       if eligible(task) and not claimed(task):
         dispatch(task)
         slots--
  6. 通知可观测消费者
```

**改造影响**:
- `新增 internal/orchestration/`: orchestrator, poll, dispatch, reconcile, retry, stall
- `新增 internal/orchestration/workspace/`: 工作区管理（从零创建）
- `扩展 internal/agent/lifecycle/`: 编排器事件接入 SignalBus
- `扩展 internal/app/missioncontrol/`: CommandService 接入编排器

### 3.4 方案三：工作区隔离（P1 — 高优先级）

**目标**: 为每个任务执行创建独立的文件系统工作区，确保 Agent 操作被限制在安全边界内。

**设计**:

```go
// internal/orchestration/workspace/manager.go

type WorkspaceManager interface {
    // 为任务创建或重用工作区
    CreateForTask(taskID mission.TaskID) (*Workspace, error)
    // 清理终端任务的工作区
    Cleanup(taskID mission.TaskID) error
    // 启动时清理所有终端状态任务的工作区
    StartupCleanup(ctx context.Context) error
}

type Workspace struct {
    Path       string     // 绝对路径: <root>/<sanitized_mission_id>/<sanitized_task_id>
    TaskID     mission.TaskID
    CreatedNow bool       // true = 本次新创建
    Root       string     // 工作区根目录
}

// 三条安全不变量（参考 Symphony Section 9.5）:
// 1. Agent cwd MUST == workspace_path
// 2. workspace_path MUST 在 workspace_root 内
// 3. 目录名仅允许 [A-Za-z0-9._-]
```

**工作区生命周期钩子**:

```yaml
# MISSION.md 中的钩子配置
workspace:
  hooks:
    after_create: |
      # 工作区首次创建时运行（如 git clone, npm install）
    before_run: |
      # 每次 Agent 尝试前运行（如 git pull）
    after_run: |
      # 每次 Agent 尝试后运行（如保存日志）
    before_remove: |
      # 工作区删除前运行（如上传产物）
    timeout_ms: 60000
```

**安全加固**（参考 Symphony Section 15）:
- 多租户场景下，每个租户使用独立的 workspace root
- 推荐在 SaaS 部署中使用独立 OS 用户运行 Agent 子进程
- 工作区根目录权限最小化
- 配合现有的 `agent/runtime/` 权限声明做双重约束

### 3.5 方案四：动态配置热加载（P1 — 高优先级）

**目标**: `MISSION.md` 文件变更时，编排器和 Agent 运行时自动应用新配置，无需重启。

**设计**:

```go
// internal/orchestration/workflow/watcher.go

type WorkflowWatcher interface {
    // 启动文件监控
    Start(ctx context.Context) error
    // 注册变更回调
    OnChange(handler func(newConfig WorkflowConfig, newPrompt string))
    // 强制重新加载
    Reload() error
}

// 重载语义（参考 Symphony Section 6.2）:
// - 影响: poll interval, concurrency limits, active/terminal states,
//         agent settings, workspace paths/hooks, prompt content
// - 不影响: 已在运行的 Agent 会话
// - 错误处理: 无效配置保持上一个有效配置，发出 operator-visible error
```

**实现方式**:
- 跨平台文件监控（fsnotify library 或 polling fallback）
- 在 `dispatch preflight validation` 时也进行防御性重读
- 通过 SignalBus 发出 `WorkflowReloaded` 信号，通知所有订阅者

### 3.6 方案五：停滞检测与自动恢复（P1）

**目标**: 自动检测并处理无响应的 Agent 会话。

**设计**:

```go
// internal/orchestration/stall/detector.go

type StallDetector struct {
    stallTimeout time.Duration // 默认 5 分钟
}

// 在每个 poll tick 中运行（参考 Symphony Section 8.5 Part A）:
func (d *StallDetector) Check(running map[TaskID]RunningEntry) []StallEvent {
    var events []StallEvent
    for taskID, entry := range running {
        lastActivity := entry.LastEventAt
        if lastActivity.IsZero() {
            lastActivity = entry.StartedAt
        }
        if time.Since(lastActivity) > d.stallTimeout {
            events = append(events, StallEvent{
                TaskID:     taskID,
                StalledFor: time.Since(lastActivity),
                LastEvent:  entry.LastEvent,
            })
        }
    }
    return events
}
```

**处理策略**:
1. 发送 `AgentStalled` 信号 → SignalBus 通知所有订阅者
2. 终止 Agent worker 子进程
3. 安排指数退避重试
4. 记录 `RuntimeStalled` 事件到事件存储

### 3.7 方案六：Agent 流水线可配置化（P2 — 中等优先级）

**目标**: 将 `BaseAgent.Handle()` 的硬编码 22 步流水线改造为 `MISSION.md` 可配置。

**设计**:

```yaml
# MISSION.md 中的流水线配置
agent:
  pipeline:
    # 默认流水线（22 个信号的子集）
    default:
      - BeforeTaskReceived
      - AfterTaskReceived
      - BeforeContextBuild
      - ApplyBoundMemory
      - AfterContextBuild
      - BeforeRuntimeStart
      - RunRuntime
      - AfterRuntimeFinish
      - BeforeResultNormalize
      - NormalizeResult
      - AfterResultNormalize
      - BeforeResultCommit
      - CommitResult
      - AfterResultCommit
      - BeforeAgentReply
      - Notify
      - AfterAgentReply

    # QA 增强流水线（在提交前插入 QA 检查）
    with_qa:
      - ... # 默认步骤
      - BeforeQAReview    # 在 CommitResult 前插入
      - QAPlanCompleted
      - QAApproved
      - ... # 继续

    # 快速执行流水线（跳过某些步骤）
    fast:
      - BeforeTaskReceived
      - RunRuntime
      - CommitResult
      - Notify
```

**改造影响**:
- 让 `BaseAgent.Handle()` 使用 `lifecycle.PipelineExecutor`
- 步骤注册为可插拔的 `PipelineStep`
- 工作流配置决定使用哪个流水线变体

### 3.8 方案七：重试策略增强（P2）

**目标**: 区分正常延续和异常重试，实施标准指数退避。

**设计**:

```go
// internal/orchestration/retry/policy.go

type RetryPolicy struct {
    MaxBackoff time.Duration // 默认 5 分钟
}

// 延续重试（正常退出后快速检查是否需要下一轮）
func (p *RetryPolicy) ContinuationDelay() time.Duration {
    return 1 * time.Second
}

// 指数退避重试（异常退出）
func (p *RetryPolicy) BackoffDelay(attempt int) time.Duration {
    delay := time.Duration(10000*math.Pow(2, float64(attempt-1))) * time.Millisecond
    if delay > p.MaxBackoff {
        return p.MaxBackoff
    }
    return delay
}
```

### 3.9 方案八：远程执行扩展（P3 — 低优先级）

**目标**: 支持 SSH 远程 Worker 执行（参考 Symphony Appendix A）。

这是可选的扩展能力，服务于以下场景：
- SaaS 部署中的客户私有环境执行
- 需要 GPU/高配硬件的 Agent 任务
- 客户数据不出本地的合规需求

**设计概要**:
- `worker.ssh_hosts` 配置远程主机列表
- 编排器保留在 Orrery 服务端
- 工作区和 Agent 在远程主机上执行
- 通过 SSH stdio 传递 Agent 协议消息

### 3.10 改造后的目标架构

```
┌──────────────────────────────────────────────────────────┐
│                     Transport Layer                       │
│  HTTP API │ WebSocket Hub │ MCP Gateway │ (不变)          │
└──────────────────────────────────────────────────────────┘
                            │
┌───────────────────────────┼──────────────────────────────┐
│                           ▼                               │
│                      App Layer                            │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────────┐  │
│  │ CommandService│ │ QueryService │ │ OrchestratorAPI  │  │
│  │ (不变)        │ │ (不变)       │ │ (新增)           │  │
│  └──────────────┘ └──────────────┘ └────────┬─────────┘  │
└─────────────────────────────────────────────┼────────────┘
                                              │
┌─────────────────────────────────────────────┼────────────┐
│                                             ▼             │
│              ★ 新增: Orchestration Layer ★               │
│  ┌──────────────────────────────────────────────────┐    │
│  │                 Orchestrator                      │    │
│  │  ┌────────┐ ┌──────────┐ ┌──────────┐           │    │
│  │  │Poll    │ │Dispatch  │ │Reconcile │           │    │
│  │  │Manager │ │Manager   │ │Manager   │           │    │
│  │  └────────┘ └──────────┘ └──────────┘           │    │
│  │  ┌────────┐ ┌──────────┐ ┌──────────┐           │    │
│  │  │Retry   │ │Stall     │ │Concur-   │           │    │
│  │  │Policy  │ │Detector  │ │rency Ctrl│           │    │
│  │  └────────┘ └──────────┘ └──────────┘           │    │
│  └──────────────────────┬───────────────────────────┘    │
│                          │                                │
│  ┌──────────────────────┼───────────────────────────┐    │
│  │              Workflow Layer                       │    │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────────────┐  │    │
│  │  │ MISSION  │ │ Config   │ │ Workspace        │  │    │
│  │  │ .md      │ │ Resolver │ │ Manager (新增)   │  │    │
│  │  │ Loader   │ │          │ │                  │  │    │
│  │  │ (新增)   │ │ (新增)   │ │                  │  │    │
│  │  └──────────┘ └──────────┘ └──────────────────┘  │    │
│  └──────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────┘
                            │
┌───────────────────────────┼──────────────────────────────┐
│                           ▼                               │
│                     Domain Layer                           │
│  ┌──────────────────────────────────────────────────┐    │
│  │  Agent System (增强)                              │    │
│  │  - 可配置流水线 (PipelineExecutor 替代硬编码)      │    │
│  │  - 工作区隔离执行                                  │    │
│  │  - 停滞信号响应                                    │    │
│  └──────────────────────────────────────────────────┘    │
│  ┌──────────────────────────────────────────────────┐    │
│  │  Mission System (不变核心)                        │    │
│  │  - 新增 OrchestrationStarted/Stalled 等事件       │    │
│  └──────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────┘
```

---

## 4. 实施路径

### 4.1 阶段划分

```
Phase 1 (2-3 周)           Phase 2 (3-4 周)           Phase 3 (2-3 周)
┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐
│ 工作流契约层     │       │ 编排器核心       │       │ Agent 流水线    │
│ + 配置热加载     │  ──►  │ + 工作区隔离     │  ──►  │ 可配置化        │
│                 │       │ + 停滞检测       │       │ + 重试增强      │
└─────────────────┘       └─────────────────┘       └─────────────────┘
                                                             │
                                                             ▼
Phase 4 (按需)                                           ┌─────────────────┐
┌─────────────────┐                                      │ 集成测试 +      │
│ 远程执行扩展     │ ◄── 按需 ──                          │ 文档 + 示例     │
│ (SSH Worker)    │                                      └─────────────────┘
└─────────────────┘
```

### 4.2 Phase 1: 工作流契约 + 配置热加载（第 1-3 周）

**目标**: 建立 `MISSION.md` 文件规范，实现加载、解析、验证和热加载。

**任务分解**:

| # | 任务 | 产出 | 依赖 |
|---|------|------|------|
| 1.1 | 设计 `MISSION.md` 完整 schema | `docs/specs/mission-md-spec.md` | 无 |
| 1.2 | 实现 `internal/orchestration/workflow/loader.go` | 文件发现 + YAML 解析 | 1.1 |
| 1.3 | 实现 `internal/orchestration/workflow/parser.go` | Front matter 解析 + 提示词提取 | 1.1 |
| 1.4 | 实现 `internal/orchestration/workflow/validator.go` | 配置验证 + 错误分类 | 1.2 |
| 1.5 | 实现 `internal/orchestration/config/config.go` | 类型化 getter + 默认值 | 1.2 |
| 1.6 | 实现 `internal/orchestration/config/env_resolver.go` | `$VAR` 和环境变量展开 | 1.5 |
| 1.7 | 实现 `internal/orchestration/workflow/watcher.go` | 文件变更检测 + 动态重载 | 1.2 |
| 1.8 | 实现 `internal/orchestration/workflow/template.go` | 严格 Liquid 兼容模板渲染 | 1.2 |
| 1.9 | 单元测试 + 工作流验证矩阵 | 覆盖 Section 17.1 测试用例 | 1.2-1.8 |
| 1.10 | 集成到 Agent Profile / Assembler | Profile 支持 workflow ref | 1.2 |

### 4.3 Phase 2: 编排器 + 工作区 + 停滞检测（第 4-7 周）

**目标**: 实现独立编排器，任务级工作区隔离，停滞检测。

**任务分解**:

| # | 任务 | 产出 | 依赖 |
|---|------|------|------|
| 2.1 | 设计编排器接口和状态模型 | `internal/orchestration/orchestrator.go` | Phase 1 |
| 2.2 | 实现 Poll Manager | 轮询循环 + Tick 序列 | 2.1 |
| 2.3 | 实现 Candidate Selector | 任务候选排序和资格检查 | 2.1 |
| 2.4 | 实现 Dispatch Manager | 调度决策 + 并发控制 | 2.2, 2.3 |
| 2.5 | 实现 Reconcile Manager | 运行中任务状态刷新 | 2.2 |
| 2.6 | 实现 Retry Policy | 延续重试 + 指数退避 | 2.1 |
| 2.7 | 实现 Stall Detector | 停滞检测 + 自动终止 | 2.5 |
| 2.8 | 实现 Workspace Manager | 工作区创建/重用/清理/钩子 | 2.1 |
| 2.9 | 实现 Workspace Safety | 路径消毒 + 根包含检查 | 2.8 |
| 2.10 | 编排器-事件系统集成 | 新增编排事件类型 | 2.2-2.7 |
| 2.11 | 单元测试 + 编排器集成测试 | 覆盖 Section 17.4 测试用例 | 2.2-2.9 |

### 4.4 Phase 3: Agent 流水线可配置化 + 重试增强（第 8-10 周）

**目标**: 硬编码流水线 → 可配置流水线，重试策略完善。

**任务分解**:

| # | 任务 | 产出 | 依赖 |
|---|------|------|------|
| 3.1 | 让 BaseAgent 使用 PipelineExecutor | 统一流水线抽象 | Phase 1 |
| 3.2 | 步骤注册表 + 可插拔步骤 | PipelineStep Registry | 3.1 |
| 3.3 | 工作流配置驱动流水线选择 | MISSION.md pipeline 字段 | 3.1, 3.2 |
| 3.4 | 停滞信号处理集成 | Agent 响应 StallSignal | Phase 2 |
| 3.5 | 工作区 cwd 验证集成 | Agent 启动前验证 workspace | Phase 2 |
| 3.6 | 端到端测试 | 从 MISSION.md 到 Agent 执行的完整流程 | 3.1-3.5 |

### 4.5 Phase 4: 远程执行（按需启动）

**前提条件**: 有明确的客户需求或生产场景需要。

| # | 任务 | 产出 |
|---|------|------|
| 4.1 | SSH Worker 协议设计 | SSH stdio 传输 Agent 协议消息 |
| 4.2 | 远程主机池管理 | 主机发现、健康检查、容量管理 |
| 4.3 | 远程工作区同步 | 代码拉取/同步策略 |
| 4.4 | 安全加固 | SSH 密钥管理、网络隔离 |

### 4.6 测试策略

参照 Symphony Section 17 的验证矩阵：

**Core Conformance 测试**（必须通过）:
- 工作流文件路径优先级
- 动态重载 + 无效配置不崩溃
- `$VAR` / `~` 展开
- 严格模板渲染（未知变量失败）
- 工作区路径确定性 + 安全不变量
- 编排器调度排序（priority → created_at → 字典序）
- 阻塞规则（Todo + 非终端阻塞者）
- 正常退出 → 延续重试（1s）
- 异常退出 → 指数退避
- 停滞检测 + 终止 + 重试

**Extension Conformance 测试**（按实现）:
- 远程 SSH Worker
- 自定义钩子执行
- HTTP Dashboard 扩展

**Real Integration 测试**（生产前推荐）:
- 使用真实任务端到端运行
- 验证工作区钩子在目标 OS/shell 环境
- 验证租户隔离边界

### 4.7 迁移兼容性

**向后兼容承诺**:
- 现有 API 接口不变
- 现有 Agent Profile 继续工作（无 workflow ref 时使用当前行为）
- 事件存储 schema 仅追加新事件类型
- MISSION.md 是可选的增强功能，不强制使用
- 编排器默认禁用，通过配置启用

**渐进式采用路径**:
1. 不使用 MISSION.md → 行为完全不变
2. 使用 MISSION.md 但不启用编排器 → 仅工作流配置生效
3. 启用编排器 → 完整新能力激活

---

## 5. 风险评估与缓解

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| 编排器引入单点故障 | 中 | 高 | 编排器状态可通过事件存储恢复；无状态设计 + 健康检查 + 自动重启 |
| 工作区隔离性能开销 | 低 | 中 | 按需创建，复用已有工作区；符号链接避免大文件复制 |
| 热加载导致配置不一致 | 中 | 中 | 原子性读写 + 防御性重读 + 无效配置回退到上一个有效配置 |
| 两套流水线系统并存 | 中 | 中 | Phase 3 统一为 PipelineExecutor；过渡期通过 Feature Flag 控制 |
| Agent pipeline 可配置化过于复杂 | 低 | 低 | 提供合理的默认流水线；高级用户才需要自定义 |
| 远程执行安全风险 | 低 | 高 | SSH 密钥轮换；网络隔离；仅在有明确需求时实现 |
| 数据库与内存状态同步 | 中 | 高 | 事件存储为真值源；编排器内存状态为派生缓存；启动时从事务日志恢复 |

---

## 6. 附录：术语对照表

| Symphony 术语 | Orrery 对应概念 | 说明 |
|---------------|----------------|------|
| Issue | Task (TaskContract) | Orrery 的 Task 比 Issue 丰富得多（证据、QA、验收标准） |
| Issue Tracker (Linear) | Mission/Task 管理系统 | Orrery 自建而非依赖外部 tracker |
| WORKFLOW.md | MISSION.md（新增） | 仓库级任务执行策略契约 |
| Orchestrator | Orchestrator（新增） | 独立的任务编排协调器 |
| Workspace | Workspace（新增） | 每任务隔离文件系统工作区 |
| Agent Runner | Agent Assembly + Runtime | Orrery 已有更丰富的组装器 |
| Poll Tick | Orchestrator Poll Loop（新增） | 定时轮询 + 调度循环 |
| Retry Queue | Retry Policy（增强） | 现有 execution/queue + 增强退避策略 |
| Stall Detection | Stall Detector（新增） | 停滞检测 |
| Live Session | AgentRunState | Orrery 已有更丰富的运行时状态 |
| Signal (absent in Symphony) | SignalBus (22 named signals) | Orrery 独有的信号驱动生命周期 |
| Domain Events (absent) | Event Sourcing (56 types) | Orrery 独有的事件溯源架构 |
| Codex app-server | Runtime Adapters | Orrery 支持多种运行时 |
| Extension | MCP Gateway + Plugin | Orrery 已有可扩展架构 |
| Status Surface | Web Frontend | Orrery 已有完整的 SPA 前端 |

---

> **文档状态**: 已完成初稿
> **下一步**: [@superadmin](mention://user/superadmin) [@顾明澈](mention://agent/lead-agent) 请评审
> **产出位置**: `docs/product-planning/symphony-inspired-system-redesign.md`
