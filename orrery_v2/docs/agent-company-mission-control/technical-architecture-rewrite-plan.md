# Agent 公司 Mission Control 技术架构重写方案

本文把上一版“Agent 公司 / Mission Control”产品模型与新版 Go 实现方案融合，形成一份面向实现的技术方案。

所有后续实现必须遵守 [项目结构设计](./project-structure-guidelines.md)，包括优雅解耦原则、单文件小于 1000 行、类型与业务逻辑分离、日志系统优先、禁止降级成 MVP 或演示版等硬性约束。每个 goal 都应实现可测试、可观测、可恢复、可替换的生产级系统切片，而不是为了跑通流程而凑合的临时实现。

优雅是工程原则，不是表面风格。解耦不是为了拆分而拆分，而是为了让业务更灵活、实现更健壮、代码更易读易懂。任何实现开工前必须先比较 Multica 和原始 Orrery 是否已有成熟实现；能直接复制或移动迁入后修改的，优先迁移适配。没有可迁移实现时，优先评估成熟开源包或标准库能力，最后才允许自研，并在文档或实现计划中说明原因。

核心结论：

```text
保留上一版的产品语义，推翻补丁式实现路线。

系统不应该继续在现有 Mission、Agent、Market、Comment、QA 表和接口上到处补字段。
这是一个初创系统，优先级应该是优雅解耦、模型纯粹、实现健壮、易读易懂、功能稳定。
如果现有结构阻碍目标模型，应接受重建领域内核；如果 Multica 或 Orrery 的已有实现能干净落入目标模块边界，应直接复用、迁入、改名、切分或包装。
```

## 1. Go 里的“类”应该如何理解

Go 没有传统面向对象语言里的 class / extends / inheritance。

Go 的建模方式是：

- `struct` 表示数据和状态。
- `interface` 表示能力契约。
- `method` 给 `struct` 挂行为。
- `embedding` 通过组合复用字段和默认行为。

因此文档里之前写的“所有 Agent 继承 BaseAgent”，在 Go 实现里不应理解成 Java / TypeScript 式父类继承，而应改成：

```text
所有 Agent 都组合 BaseAgent，并实现 Agent interface。
BaseAgent 提供统一生命周期、上下文、消息、工具、summary、deliverable、evidence 收口。
具体 Agent 只实现局部策略和技能差异。
```

示意：

```go
type Agent interface {
    ID() AgentID
    Profile() AgentProfile
    Handle(ctx context.Context, input AgentInput) (AgentResult, error)
}

type BaseAgent struct {
    id       AgentID
    profile  AgentProfile
    runtime  RuntimeAdapter
    memory   MemoryBinding
    reporter Reporter
}

type WorkerAgent struct {
    BaseAgent
    planner WorkerPlanner
    checker WorkerChecker
}
```

这里的 `WorkerAgent` 不是继承 `BaseAgent`，而是组合 `BaseAgent`。

## 2. 上一版与新版的差异

| 维度 | 上一版文档 | 新版技术方案 |
| --- | --- | --- |
| 架构态度 | 基于现有 Mission / Agent / Market / QA 能力逐步收敛 | 推翻补丁式兼容路线，建立干净内核，同时复用可迁移资产 |
| Agent 建模 | 类图中表达为继承 `BaseAgent` | Go 中使用 `interface + struct + embedding` |
| 流程 | 描述 PM、HR、Planner、Worker、QA 的协作流程 | 不固化全局流程，只固化局部 Agent 内部小流程 |
| 讨论组 | 强调 Mission 天然拥有讨论组和 mention / reply | 讨论组成为 Mission 原生领域模型，不兼容 legacy comment 语义 |
| Agent Org Identity / Staffing | Agent 组织身份与任务调配，Tool 不能直接入组 | 组织、部门、岗位、角色、汇报线是 AgentProfile / AgentIdentity 的属性；MissionAssignment 属于 Mission team |
| Summary / Deliverable | 强调所有 Agent 必须交付总结和成果 | 由 BaseAgent 在退出阶段强制收口，不由子 Agent 自觉实现 |
| 前端图 | 从事件流投影 Mission Control | 严格以事件流为唯一事实源，不从固定流程模板生成 |
| 兼容性 | 倾向保留现有模块资产 | 不为兼容牺牲模型，但可复用实现必须优先复用 |

融合后的判断：

```text
上一版是正确的产品模型。
新版是更正确的实现路线。

产品语义继续保留：
Mission、Agent 公司、Agent 组织身份、讨论组、RealHumanAgent、Tool 包装、summary / deliverable / evidence、QA 验收。
Tool / MCP / Skill 注册库和独立 Memory 模块也是实现路线的一部分，但它们不是团队成员，也不是 Agent 子类。

实现路线必须调整：
不要在旧 schema、旧 API、旧 comment、旧 task queue 上连续打补丁。
应该重建一个干净的 Agent 公司运行时内核，同时系统化复用 Orrery 和 Multica 中能落入目标边界的代码资产。
```

## 3. 不打补丁原则

这个系统最需要避免的是“看起来省事，实际上越补越乱”。

必须坚持：

1. 不在旧 `comment` 上硬塞 Mission 讨论组语义。
2. 不在旧 `agent_task_queue` 上硬塞 Agent 生命周期。
3. 不让 `market_agent_catalog.kind = tool` 继续表达团队成员。
4. 不把 Codex / Claude / Browser / RPA 直接当团队成员。
5. 不用一堆可空字段兼容 PM、HR、Worker、QA、Human 的不同输出。
6. 不把 timeline 当事实源。
7. 不把前端流程图写死成系统流程。
8. 不把 summary 当真相，summary 必须追溯 evidence。

更好的方式：

```text
重新定义核心领域模型。
重新定义核心表。
重新定义事件流。
重新定义 Agent runtime contract。
旧代码在适合的位置作为 adapter、store、runtime provider、test fixture 或实现细节复用。
```

## 4. 目标系统内核

系统内核只承认这些一等概念：

```text
Mission
AgentProfile
AgentInstance
AgentIdentity
AgentOrgIdentity
AgentPersona
MemoryProfileRef
ToolRef
SkillRef
DiscussionGroup
Message
TaskContract
AgentResult
Deliverable
Evidence
MissionEvent
RuntimeAdapter
```

不要把 PM、HR、Worker、QA 做成系统级特殊分支。

PM、HR、Planner、Worker、QA、RealHuman 都是 Agent。

系统内核只负责：

```text
创建 Mission
按 AgentProfile 的组织身份属性查询、调配或邀请 Agent
分配 TaskContract
传递 ContextPack
接收 AgentResult
保存 Deliverable / Evidence
产生 MissionEvent
投影 Mission Control 视图
```

## 5. 推荐 Go 包结构

```text
cmd/
  server/
  migrate/
  worker/
  backfill/

internal/
  platform/
    logging/
    config/
    secret/
    db/
    migration/
    uow/
    eventstore/
    outbox/
    execution/
    filestore/
    lock/
    config/
    secret/
    logging/
    metrics/
    tracing/
    health/
    testkit/
    auth/

  agent/
    core/
      profile.go
      identity.go
      persona.go
      base_agent.go
      result.go
      context_pack.go
    assembly/
      assembler.go
      component_registry.go
      binding.go
    component/
      policy/
      delivery/
      evidence/
      qa/
      summary/
      notifier/
    lifecycle/
    runtime/
      core/
      process/
      providers/
        codex/
        claude/
        cursor/
        gemini/
        browser/
        human/
        http/
      execenv/
      daemon/
      migration_notes/

  resource/
    tool/
      registry.go
      resolver.go
      mcp.go
      approval_tool.go
    skill/
      registry.go
      resolver.go

  memory/
    core/
    store/
    retrieval/
    audit/

  mission/
    core/
    task/
    team/
    discussion/
    event/
    projection/

  app/
    missioncontrol/
      query_service.go
      command_service.go
      stream_service.go
      dto.go
      mapper.go
      permissions.go
    resourceadmin/
      query_service.go
      command_service.go

  transport/
    http/
    websocket/
```

依赖方向：

```text
platform 处理公共基础设施：数据库、迁移、事务、事件存储、outbox、队列、文件、锁、配置、密钥、日志、指标、追踪、健康检查、测试夹具、权限和外部系统。
resource 处理可注册资源：Tool / MCP / Skill 的定义、版本、权限声明和 resolver。
memory 处理长期记忆、检索、压缩、权限过滤和 digest。
agent 聚合 Agent 薄内核、组件装配与运行：core 只放身份/profile/BaseAgent/Result/ContextPack；component 放 Agent 内置可组装能力；assembly 负责把 profile、组件、resource、memory、runtime 装配成 AgentInstance。
mission 处理目标、任务团队、任务分配、讨论组、事件事实和投影。
app 处理对前端和外部 API 的应用服务、查询服务、DTO、权限裁剪和流式订阅。
transport 调用 app service，不写业务规则，不直接查 projection 或 DB。
cmd 只做装配和启动。
```

这比在 handler / service / sqlite query 之间继续加字段更稳定。

runtime 和 QA 不再作为顶层业务模块。runtime 是 Agent 的执行能力；QA 是 Agent 的验收能力。

## 6. Agent 核心接口

```go
type Agent interface {
    ID() AgentID
    Profile() AgentProfile
    Handle(ctx context.Context, input AgentInput) (AgentResult, error)
}

type AgentAssembler interface {
    Build(ctx context.Context, profile AgentProfile) (Agent, error)
}

type AgentInput struct {
    MissionID   MissionID
    Task        TaskContract
    ContextPack ContextPack
}

type AgentResult struct {
    Summary      Summary
    Deliverables []Deliverable
    Evidence     []Evidence
    Decisions    []Decision
    Requests     []AgentRequest
}
```

`AgentProfile` 必须包含真实姓名、组织身份、人设、记忆、技能和工具：

```text
real_name
display_name
role_name
org_identity {
  organization_id
  department_id
  position_id
  role_ids[]
  manager_agent_id
  reporting_line_id
  authority_scope
  employment_type
}
persona / backstory
memory_profile_ref
skill_refs[]
tool_refs[]
policy_set
delivery_contract
evidence_contract
qa_contract
runtime_spec
permissions
constraints
```

这些字段用于真人化管理、组织归属、上下级关系、团队责任归属、上下文构建、任务匹配和运行边界控制。Tool / CLI / API / RPA / MCP server 仍然不能独立入组，只能注册为 resource/tool 并由 AgentProfile.tool_refs 引用。

Agent 必须通过 AgentAssembler 装配：

```text
AgentProfile
-> AgentAssembler
-> BaseAgent
-> RuntimeAdapter
-> resolve ToolRef / SkillRef
-> Toolset / Skillset
-> PolicySet
-> bind MemoryProfileRef via memory module
-> policy / delivery / evidence / QA / summary / notifier components
-> AgentInstance
```

发起审批、请求审批、人工确认、外部审批系统调用，都应该建模为 `ToolRef`，挂在 `AgentProfile.tool_refs[]` 下，并受 `policy_set` 和 `authority_scope` 约束；不要单独做一个 ApprovalCapability package。

Tool / MCP / Skill 可以从别的地方注册。AgentProfile 只保存引用，不能把外部资源定义复制进 Agent 内部。Memory 更不是普通能力字段，它是独立模块；BaseAgent 只能拿 MemoryDigest / ContextPack，不能直接读取全量长期记忆。Agent 自身能力中属于可组合的部分放在 `internal/agent/component/*`，不要堆进单个 BaseAgent 文件。

`AgentResult` 是所有 Agent 的统一出口。

无论执行者是 Codex、Claude、浏览器自动化、外部 API、RPA，还是真人，最终都必须变成 `AgentResult`。

## 7. BaseAgent 的职责

`BaseAgent` 不负责具体业务策略。

它负责强制通用协议：

```text
加载 ContextPack
执行 RuntimeAdapter
记录 internal trace
解析工具输出
生成 Summary
收集 Deliverable
绑定 Evidence
产生 AgentResult
发出 MissionEvent
处理 mention / reply
处理 blocker / request
```

这些通用协议不应全部硬编码在一个巨大的 `Run()` 函数里。BaseAgent 必须通过生命周期信号系统暴露扩展点，让权限、审计、上下文压缩、summary 生成、evidence 校验、通知、失败恢复等横切逻辑以 hook / middleware 方式进入。

关键生命周期信号包括：

```text
BeforeTaskReceived
AfterTaskReceived
BeforeTaskAccepted
AfterTaskAccepted
BeforeContextBuild
AfterContextBuild
BeforeRuntimeStart
RuntimeStarted
RuntimeProgress
BeforeRuntimeFinish
AfterRuntimeFinish
BeforeResultNormalize
AfterResultNormalize
BeforeResultValidate
AfterResultValidate
BeforeResultCommit
AfterResultCommit
BeforeAgentReply
AfterAgentReply
AgentBlocked
AgentFailed
AgentCancelled
AgentTimeout
```

子 Agent 只能定制：

```text
role prompt
skills
tools
permissions
local workflow
acceptance interpretation
```

子 Agent 不能绕过：

```text
summary
deliverable
evidence
trace
event
```

## 8. Tool、Runtime、Agent 的边界

必须严格区分：

```text
Agent = 责任主体
RuntimeAdapter = 执行环境
Tool = 能力器官
```

正确：

```text
ResearchAgent -> uses SearchTool
CodeWorkerAgent -> uses CodexRuntimeAdapter
BrowserQAAgent -> uses BrowserRuntimeAdapter
RealHumanAgent -> uses HumanRuntimeAdapter
```

错误：

```text
SearchTool 入组
Codex CLI 入组
Browser 自动化脚本入组
Webhook 入组
```

团队成员只能是 Agent。

## 9. RealHumanAgent

真人也必须是 Agent，不是系统外的特殊备注。

```go
type HumanRuntimeAdapter struct {
    inbox HumanInbox
}
```

真人执行流程：

```text
收到 TaskContract
阅读 ContextPack
提交 summary
提交 deliverables
提交 evidence
提交 blocker / request
响应 mention
接受 QA
```

真人入口要极简。

不要让真人填复杂 Agent 表单。真人只需要像交任务一样提交：

```text
我做了什么
成果在哪里
证据是什么
卡在哪里
需要谁确认
```

系统内部把这些包装为标准 `AgentResult`。

## 10. Signal 与 Mission Event

必须区分运行时信号和业务事实事件：

```text
Signal = Agent 生命周期中的运行时通知，用于 hook、middleware、审计、拦截、补充处理。
MissionEvent = 已经发生的业务事实，用于持久化、回放、投影和前端展示。
```

不是所有 Signal 都应该持久化为 MissionEvent。

示例：

```text
BeforeRuntimeStart 是 Signal，不一定是 MissionEvent。
AfterResultCommit 是 Signal，通常会导致 DeliverableSubmitted / EvidenceSubmitted 等 MissionEvent。
TaskAssigned 是 MissionEvent。
AgentFailed 既可以是 Signal，也可以在影响 Mission 状态时升级为 MissionEvent。
```

不要让 timeline、前端图、comment、task queue 成为事实源。

事实源应该是事件：

```go
type MissionEvent interface {
    MissionID() MissionID
    OccurredAt() time.Time
}
```

核心事件：

```text
MissionCreated
AgentInvited
AgentJoined
TaskAssigned
TaskAccepted
MessagePosted
AgentMentioned
AgentReplied
DeliverableSubmitted
EvidenceSubmitted
QARequested
QAApproved
QARejected
StaffingRequested
TaskReassigned
MissionCompleted
```

前端所有进度图、团队视图、任务视图、证据视图、QA 状态都从事件流投影。

## 11. 讨论组模型

讨论组天然属于 Mission。

```go
type DiscussionGroup struct {
    ID        DiscussionGroupID
    MissionID MissionID
    Members   []AgentID
}

type Message struct {
    ID        MessageID
    MissionID MissionID
    AuthorID AgentID
    Body      string
    Mentions  []AgentID
    ReplyTo   *MessageID
}
```

`@mention` 的处理机制：

```text
Agent A 发消息 @Agent B
系统解析 mention
生成 AgentMentioned 事件
Agent B inbox 出现待响应项
Agent B 处理后生成 AgentReplied
回复挂回原消息线程
```

讨论组默认不向所有成员广播全量内部上下文。

组内只沉淀：

```text
summary
deliverable
evidence
decision
blocker
request
reply
```

## 12. TaskContract

Worker 真正执行任务，因此必须有独立责任边界。

```go
type TaskContract struct {
    ID          TaskID
    MissionID  MissionID
    AssigneeID AgentID

    Goal       string
    Scope      Scope
    Inputs     []ArtifactRef
    Expected   []DeliverableSpec
    Acceptance []AcceptanceCriterion
    Evidence   []EvidenceRequirement
}
```

Worker 内部可以有固定小流程：

```text
Plan / BDD
Do
Check / TDD
Submit summary / deliverable / evidence
```

但这个小流程只属于 Worker 内部，不应膨胀为全局流程引擎。

## 13. 前端展示

前端不展示固定流程模板。

前端展示事件投影后的 Mission Control：

```text
Mission Overview
Team
Task Board
Discussion
Deliverables
Evidence
QA
Timeline
```

“进度图”应该是实际执行轨迹：

```text
谁被邀请
谁接了什么任务
谁提交了什么成果
谁提出阻塞
谁追加了人员
谁验收通过或驳回
```

图不是系统规定的流程，而是 Mission 发生过的事实。

对外接口边界：

```text
mission/event + mission/projection
-> internal/app/missioncontrol QueryService / CommandService / StreamService
-> internal/transport/http / websocket
-> frontend
```

前端只能消费 `internal/app/missioncontrol` 暴露出来的 DTO / patch，不直接消费 domain struct、projection row 或 MissionEvent 原始事件。HTTP handler 和 WebSocket handler 也不能直接查询 projection 表，只做协议适配。

## 14. 重写路线

具体后端搭建任务已经拆分到 [implementation-goals](./implementation-goals/README.md)。这些 goal 不是完全并发关系，而是阶段式推进：先基础设施公共服务，再核心契约、生命周期信号、事件账本，然后任务交付、BaseAgent 运行时，最后接人才团队和讨论组；前端延后。需要跨 goal 协作的地方先以 `CROSS_GOAL` 标记保留，后续再补交互约定。

### Phase 1：建立基础设施公共服务

基础设施必须独立成体系，先于业务内核。

它提供：

```text
Database / Migration
UnitOfWork / Transaction
Repository 基础约定
EventStore / EventLedger 存储
Outbox
Queue / JobRunner
FileStore / ArtifactStore
Lock
Config / Secret
Logger / Metrics / Tracing
Health Check
Test Harness
```

业务模块不得私自创建数据库连接、事务、队列、文件存储或日志体系。

### Phase 2：冻结旧实现语义

目的不是继续补丁，而是明确哪些东西要被新内核吸收。

保留为认知资产：

```text
Mission-first 产品方向
Agent 公司定位
Agent 组织身份
讨论组
summary / deliverable / evidence
QA 验收
RealHumanAgent
Tool 必须被 Agent 包装
```

不保留为实现约束：

```text
现有表结构
现有 comment 兼容语义
现有 task queue 输出结构
现有 market kind = tool
现有 timeline 拼装方式
```

### Phase 3：建立新 domain model

先写纯 domain：

```text
Mission
Agent
AgentProfile
AgentIdentity
AgentOrgIdentity
MissionTeam
MissionAssignment
DiscussionGroup
TaskContract
AgentResult
Deliverable
Evidence
MissionEvent
```

这一层不连接数据库，不连接 Codex，不连接前端。

### Phase 4：建立生命周期信号和事件账本

先实现 Agent 生命周期信号系统：

```text
BeforeTaskReceived
AfterTaskReceived
BeforeTaskAccepted
AfterTaskAccepted
BeforeContextBuild
AfterContextBuild
BeforeRuntimeStart
RuntimeStarted
RuntimeProgress
BeforeRuntimeFinish
AfterRuntimeFinish
BeforeResultNormalize
AfterResultNormalize
BeforeResultValidate
AfterResultValidate
BeforeResultCommit
AfterResultCommit
AgentFailed
AgentTimeout
```

然后建立事件账本，让业务事实可持久化、可回放、可投影。

注意：

```text
Signal 是运行时 hook。
MissionEvent 是业务事实。
二者不能混为一谈。
```

### Phase 5：建立任务交付与 QA 闭环

先定义：

```text
TaskLifecycle
DeliverableSubmission
EvidenceSubmission
QACheck
QAResult
ReworkRequest
```

目标是让每个 Worker 的任务能独立提交、独立验收、独立驳回。

### Phase 6：实现 BaseAgent runtime kernel

在核心契约、生命周期信号、事件账本、任务交付规则存在后，再实现 BaseAgent。

BaseAgent 的任务是把这些能力串起来：

```text
TaskContract
-> lifecycle signals
-> RuntimeAdapter
-> AgentResult
-> Deliverable / Evidence
-> QA / MissionEvent
```

### Phase 7：建立事件流投影

先让系统能保存事件，再从事件投影：

```text
MissionControlView
TeamView
TaskBoardView
EvidenceView
DiscussionView
ProgressGraphView
```

### Phase 8：实现 RuntimeAdapter

按价值顺序接入：

```text
HumanRuntimeAdapter
CodexRuntimeAdapter
BrowserRuntimeAdapter
HTTPRuntimeAdapter
```

先接真人是为了验证统一协议，因为真人最能暴露任务契约和交付表单是否合理。

### Phase 9：实现 PM / HR / Worker / QA Agent

这些不是系统硬编码流程，而是 Agent Profile + local workflow。

第一条生产级协作闭环：

```text
PMAgent 粗拆 Mission
HRAgent 按 Agent 组织身份属性查询并选择成员
PlannerAgent 生成 TaskContract
WorkerAgent 执行并提交 AgentResult
QACheckAgent 基于 Evidence 验收
```

### Phase 99：前端 Mission Control

先完成后端 API 出口：

```text
internal/app/missioncontrol
-> internal/transport/http
-> internal/transport/websocket
```

前端只吃投影数据。

不要让前端理解后端编排细节。

## 15. 第一版生产级稳定闭环

第一版先做这条生产级稳定闭环：

```text
用户创建 Mission
PMAgent 生成需求摘要和粗拆
HRAgent 按 Agent 组织身份属性查询并选择成员，生成 MissionAssignment
PlannerAgent 生成 TaskContract
WorkerAgent 或 RealHumanAgent 执行
BaseAgent 强制生成 AgentResult
QACheckAgent 验收 Evidence
MissionControl 展示 Team / Task / Deliverable / Evidence / Progress / QA / Discussion / Inbox
```

这里的“不做”表示推迟扩展功能，不表示降低工程质量；第一版仍必须具备清晰边界、错误处理、日志、测试、观测、权限、幂等和恢复能力。这里不要做：

```text
复杂 BPMN
通用 workflow builder
行业模板市场
全自动公司
复杂组织权限
兼容旧 comment / issue / task queue
```

## 16. 最终原则

```text
产品语义可以继承。
实现结构不必继承。

Agent 公司不是旧系统上补出来的功能，
而是一个新的运行时内核。

旧代码里有价值的东西可以被搬进 adapter，
但不能让旧结构决定新系统的骨架。

解耦只在提升业务灵活性、实现健壮性和可读性时成立；
拆分本身不是目标。

实现前先比较 Multica / 原 Orrery；
能迁移就先迁移后适配，
无可迁移资产再优先选成熟包，
自研必须留下取舍说明。
```
