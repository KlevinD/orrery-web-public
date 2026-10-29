# Agent 公司 Mission Control 项目结构设计

本文是所有 implementation goal 的顶层代码结构指导。后续任何 goal 的实现都必须按这里的边界归集功能，不能各自发明目录、基础设施、日志方式或凑合式临时结构。

本文主要约束 Go 服务端内部包边界。SaaS repo 级客户端、服务端和协议边界，以 [SaaS 客户端、服务端与协议边界规划](./saas-monorepo-boundaries.md) 为准。迁移到目标目录后，本文中的 `cmd/*` 与 `internal/*` 分别对应 `server/api`、`server/worker`、`server/tools/*` 与 `server/internal/*`。

## 1. 核心判断

上一版目录把 `runtime`、`qa`、`task`、`agent` 平铺在顶层，仍然偏产品概念拆分，不够像代码层面的归属。

新的代码归集原则是：

```text
Agent 是能力主体。
runtime 是 Agent 的执行能力。
QA 是 Agent 的验收能力。
tools / skills / signals / policies / deliverables / evidence / approval 都是 AgentProfile 声明的能力配置。
Mission 负责目标、团队、协作、事件和最终汇总。
基础设施提供公共服务。
```

也就是说：

```text
不要把 runtime 独立成业务中心。
不要把 QA 独立成和 Agent 平级的业务中心。
不要让 Mission 吞掉 Agent 的内部能力。
```

## 2. 总原则

```text
合理解耦优先。
优雅解耦优先：解耦不是为了拆分而拆分，而是为了让业务更灵活、实现更健壮、代码更易读易懂。
清晰边界优先。
功能稳定优先。
排错能力优先。
禁止补丁式堆叠。
禁止把目标降级成 MVP 或演示版。
实现前必须先比较 Multica / 原 Orrery 是否已有成熟实现；能直接复制或移动迁入后修改的，优先迁移适配。
没有可迁移实现时，优先引入成熟开源包或标准库能力；自研只能作为最后选择，并说明原因。
```

这里的“禁止降级成 MVP 或演示版”不是说不能分阶段交付，而是：

```text
可以分阶段实现。
但每个阶段都必须按生产级系统的标准设计和验收。
设计必须考虑完整生命周期、错误处理、日志、测试、扩展点和失败恢复。
不能为了先跑起来就省掉关键边界。
拆包、建接口、抽象层都必须服务于业务灵活性、健壮性和可读性；如果拆分只增加跳转和认知负担，就应合并或推迟。
```

## 3. 单文件限制

硬性规则：

```text
单个代码文件必须小于 1000 行。
超过 800 行时就应该主动拆分。
超过 1000 行不得继续追加功能。
```

拆分方向：

```text
types.go          类型、枚举、接口、DTO
errors.go         错误类型、错误码
service.go        应用服务或领域服务
repository.go     repository 接口
store.go          持久化实现
handler.go        HTTP handler 或 RPC handler
middleware.go     中间件
validator.go      校验逻辑
mapper.go         DTO / domain 转换
events.go         事件定义
projector.go      投影逻辑
testkit.go        测试夹具
```

类型定义和业务逻辑必须分离。业务逻辑通过 import 使用类型，不要把大段逻辑塞进 `types.go`。

## 4. 后端推荐目录

目标结构：

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
    clock/
    idgen/
    metrics/
    tracing/
    health/
    testkit/

  agent/
    core/
      types.go
      identity.go
      persona.go
      profile.go
      base_agent.go
      result.go
      context_pack.go
      errors.go
    assembly/
      assembler.go
      component_registry.go
      binding.go
      validation.go
    component/
      policy/
        types.go
        set.go
        checker.go
      delivery/
        contract.go
        collector.go
      evidence/
        contract.go
        validator.go
      qa/
        contract.go
        checker.go
      summary/
        generator.go
      notifier/
        notifier.go
    lifecycle/
      signals.go
      bus.go
      middleware.go
      hooks.go
    runtime/
      core/
        backend.go
        registry.go
        session.go
        message.go
        result.go
        options.go
        errors.go
      process/
        command.go
        stderr_tail.go
        version.go
        env.go
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
        multica.md

  resource/
    tool/
      types.go
      registry.go
      resolver.go
      mcp.go
      approval_tool.go
    skill/
      types.go
      registry.go
      resolver.go

  memory/
    core/
      types.go
      profile.go
      policy.go
      digest.go
    store/
      repository.go
      search.go
    retrieval/
      retriever.go
      compressor.go
      context_builder.go
    audit/
      events.go

  mission/
    core/
    task/
    team/
    discussion/
    event/
    projection/

  app/
    missioncontrol/
      command_service.go
      query_service.go
      stream_service.go
      dto.go
      mapper.go
      permissions.go
      errors.go
    resourceadmin/
      query_service.go
      command_service.go

  transport/
    http/
      handler.go
      routes.go
    websocket/
      hub.go
      handler.go

pkg/
  publicapi/
```

命名说明：

```text
platform = 公共基础设施，不带业务语义。
agent = Agent 薄内核、组件装配与运行体系。core 只放 Agent 身份/profile/BaseAgent/Result/ContextPack；component 放 Agent 内置可组装能力；assembly 负责装配。
resource = 可注册资源库，包含 Tool / MCP / Skill 定义、版本、权限声明和 resolver。
memory = 独立长期记忆与上下文系统，负责存储、检索、压缩、权限过滤和 digest。
mission = 用户目标、任务团队、任务分配、讨论协作、事件事实、投影汇总。
app = 应用服务层，对前端和外部 API 提供用例、查询、DTO、权限裁剪和流式订阅。
transport = HTTP / WebSocket 适配层，只处理协议、鉴权入口、参数绑定和响应编码。
cmd = 进程入口，只做装配。
```

## 5. Agent 能力体系

Agent 模块内聚这些能力：

```text
core:
  Agent interface
  BaseAgent
  AgentProfile
  AgentInstance
  AgentIdentity
  AgentPersona
  ToolRef / SkillRef / MemoryProfileRef
  AgentResult
  ContextPack

assembly:
  AgentAssembler
  ComponentRegistry
  ResourceBinding
  RuntimeBinding
  MemoryBinding

component:
  policy component
  delivery component
  evidence component
  QA component
  summary component
  notifier component

lifecycle:
  Signal
  SignalBus
  Hook
  Middleware

runtime:
  RuntimeBackend
  RuntimeAdapter
  RuntimeSession
  RuntimeResult
  process runner
  provider adapters
  execution environment
  daemon / local worker
  human runtime
  HTTP runtime

identity attributes:
  OrganizationRef
  DepartmentRef
  PositionRef
  RoleRef
  ReportingLineRef
  ManagerAgentRef
  AuthorityScope
  EmploymentType
```

AgentProfile 必须包含这些基础字段：

```text
真实姓名 / real_name：
  用于真人化管理、团队展示、审计和责任归属。

背景故事 / persona / backstory：
  描述 Agent 的人设、工作风格、沟通方式、专业背景和行为边界。

记忆 / memory：
  指向独立 Memory 模块的 MemoryProfileRef，用于检索长期偏好、历史经验、已知上下文、团队协作记录和可复用知识。

掌握技能 / skills：
  Agent 引用的 SkillRef。SkillDefinition 可以从资源库注册，AgentProfile 只引用。

手头工具 / tools：
  Agent 引用的 ToolRef。ToolDefinition / MCP server 可以从资源库注册，AgentProfile 只引用。

组织身份 / organization identity：
  Agent 所属组织、部门、岗位、角色、直属上级、汇报线和权限范围。
```

这些字段属于 AgentProfile / AgentIdentity 的基础设计，不是前端展示装饰。

建议归集：

```text
internal/agent/core/identity.go
internal/agent/core/persona.go
internal/agent/core/org_identity.go
internal/agent/core/profile.go
internal/agent/core/base_agent.go
internal/agent/core/result.go
internal/agent/core/context_pack.go
internal/agent/assembly/*
internal/agent/component/policy/*
internal/agent/component/delivery/*
internal/agent/component/evidence/*
internal/agent/component/qa/*
internal/agent/component/summary/*
internal/agent/component/notifier/*
internal/resource/tool/*
internal/resource/skill/*
internal/memory/*
```

组织目录不是独立业务中心。它本质是按 AgentProfile / AgentIdentity 的组织身份属性做索引、过滤和查询；实现时应优先作为 `internal/agent/core` 的 repository/query 能力，或作为 mission/team 调配时的查询接口，而不是新增 `internal/agent/organization/*` 这种平级模块。

Agent assembly 的位置：

```text
AgentAssembler 是唯一的 Agent 装配入口，归集到 internal/agent/assembly。
它读取 AgentProfile，解析 identity、persona、ToolRef、SkillRef、MemoryProfileRef、policy、delivery/evidence/QA contract 和 runtime_spec。
它通过 ToolResolver / SkillResolver / MemoryBinder 绑定外部资源。
它通过 ComponentRegistry 装配 policy、delivery、evidence、QA、summary、notifier 等 Agent 内置组件。
它产出可运行的 AgentInstance / BaseAgent。
具体 Agent 不应绕过 AgentAssembler 自己拼装 runtime、toolset、skillset、memory 或 QA 规则。
```

QA / tools / skills / approval 的位置：

```text
QA 不是独立 package，而是 AgentProfile 上的 QAContract / QA policy。
QACheckAgent 是由 AgentAssembler 装配出来的一种 Agent。
tools 是 AgentProfile.tool_refs[] 中的 ToolRef；ToolDefinition / MCPServerDefinition 在 resource/tool 注册。
skills 是 AgentProfile.skill_refs[] 中的 SkillRef；SkillDefinition 在 resource/skill 注册。
发起审批、请求审批、人工确认都属于 ToolRef。
memory 是独立 `internal/memory/*` 模块；AgentProfile 只保存 MemoryProfileRef。
policy、deliverable、evidence、QA、summary、notifier 是 Agent 内置可组装组件，放在 `internal/agent/component/*`。
Mission 只引用 AgentResult、QAResult、Deliverable、Evidence 和 MissionEvent，不拥有 Agent 内部能力。
```

Runtime 的位置：

```text
runtime 是 Agent 子能力。
Codex / Claude / Browser / Human / HTTP 都是 Agent runtime adapter。
runtime adapter 不能直接成为团队成员。
团队成员只能是 AgentInstance。
```

## 6. Multica Runtime 借鉴方式

Multica 的 runtime 体系可以大幅借鉴，甚至直接迁入后再按本项目结构切分。

任何 runtime、执行、存储、队列、文件、健康检查、liveness、sweeper、日志或后台任务能力开工前，都必须先对照 Multica 是否已有成熟实现。默认路线是直接复制或移动迁入，再按本项目目标接口改名、切分、去耦和补测试；只有当 Multica 实现确实不适合目标边界、许可、依赖或运行约束时，才进入成熟包评估或自研。

建议吸收三层：

```text
multica/server/pkg/agent
  -> internal/agent/runtime/core
  -> internal/agent/runtime/process
  -> internal/agent/runtime/providers/{codex,claude,cursor,gemini,...}
  价值：统一 CLI Backend、Session、Message、Result、ExecOptions、provider 实现。

multica/server/internal/daemon/execenv
  -> internal/agent/runtime/execenv
  价值：每次 run 的隔离目录、context files、skills 注入、CodexHome、OpenClaw config、工作目录管理。

multica/server/internal/daemon
  -> internal/agent/runtime/daemon
  价值：本地 runtime 进程、CLI 探测、heartbeat、health、GC、concurrency、timeout、idle watchdog。
```

迁移说明、来源对照和未处理差异只放在：

```text
internal/agent/runtime/migration_notes/multica.md
```

`multica` 不是 provider，也不是 runtime 类型。

但必须重构掉 Multica 业务耦合：

```text
issue -> Mission / TaskContract
comment trigger -> Discussion message / Signal
agent_task_queue -> AgentRunLease
TaskResult.Comment -> AgentResult.Summary / Deliverables / Evidence
WorkerConfig -> AgentProfile refs / runtime spec
```

禁止直接保留：

```text
issue_id
comment_id
squad_id
autopilot_id
chat_session_id
```

这些应该在适配层映射成新系统的 Mission / Task / Discussion / AgentRun 概念。

## 7. Mission 模块职责

Mission 不拥有 Agent 内部能力。

Mission 负责：

```text
Mission 创建与状态
TaskContract 分配
DiscussionGroup
MissionEvent
Projection
Mission summary / final outcome
```

Mission 可以调用 Agent app service，但不能直接操作 runtime backend。

正确：

```text
internal/app/missioncontrol 分配 TaskContract
-> agent/core BaseAgent 接收
-> agent/runtime 执行
-> agent/core QAContract / QA policy 验收
-> mission/event 记录事实
```

错误：

```text
mission/task 直接调用 codex backend
mission/qa 直接解析 runtime output
mission/discussion 直接修改 Agent 内部状态
```

## 8. Application Service 与对外接口

前端展示服务不写在 domain 模块里，也不写在 `transport/http` handler 里。

统一归集到：

```text
internal/app/missioncontrol
```

这一层负责把后端领域模型、事件投影、讨论组视图、任务视图、团队视图、资源引用状态整理成前端可直接消费的 DTO。

职责边界：

```text
internal/mission/projection:
  构建后端 read model / projection。
  只表达业务投影，不关心 HTTP、WebSocket、前端组件。

internal/app/missioncontrol:
  对前端提供 QueryService / CommandService / StreamService。
  做权限裁剪、分页、过滤、DTO 映射、聚合多个 projection。
  返回 MissionControlView、TeamView、TaskBoardView、DiscussionView、InboxView 等 DTO。

internal/transport/http:
  只做 route、request binding、response encoding、错误码映射。
  不写业务规则，不直接查 DB，不直接读 eventstore。

internal/transport/websocket:
  只做连接、订阅、推送协议适配。
  订阅数据来自 app stream service。

pkg/publicapi:
  只放需要给外部 SDK / CLI 复用的稳定公开类型。
  不放内部 domain 类型，不放数据库模型。
```

推荐接口形态：

```go
type MissionControlQueryService interface {
    GetMissionControlView(ctx context.Context, missionID MissionID, viewer ViewerRef) (MissionControlViewDTO, error)
    ListMissionTimeline(ctx context.Context, missionID MissionID, q TimelineQuery) (TimelinePageDTO, error)
    GetTaskBoard(ctx context.Context, missionID MissionID, viewer ViewerRef) (TaskBoardViewDTO, error)
    GetDiscussionView(ctx context.Context, missionID MissionID, viewer ViewerRef) (DiscussionViewDTO, error)
    GetInboxView(ctx context.Context, viewer ViewerRef, q InboxQuery) (InboxViewDTO, error)
}

type MissionControlCommandService interface {
    CreateMission(ctx context.Context, cmd CreateMissionCommand) (MissionDTO, error)
    AssignTask(ctx context.Context, cmd AssignTaskCommand) error
    PostMessage(ctx context.Context, cmd PostMessageCommand) error
    SubmitAgentResult(ctx context.Context, cmd SubmitAgentResultCommand) error
    RequestApproval(ctx context.Context, cmd RequestApprovalCommand) error
}

type MissionControlStreamService interface {
    SubscribeMission(ctx context.Context, missionID MissionID, viewer ViewerRef) (<-chan MissionControlPatchDTO, error)
}
```

禁止：

```text
HTTP handler 直接调用 repository。
HTTP handler 直接查询 projection 表。
前端直接消费 MissionEvent 原始事件。
前端根据 event 自己推断业务状态。
DTO 直接复用 domain struct。
projection 为了某个页面组件写 UI 状态。
```

## 9. 平台基础设施

公共基础设施统一放入：

```text
internal/platform/*
```

而不是 `internal/infra/*`。

原因：

```text
platform 更明确表示“平台公共能力”。
infra 容易被理解成数据库和部署脚本，范围太窄。
```

平台层提供：

```text
logging
config
secret
db
migration
uow
eventstore
outbox
queue
claim
liveness
sweeper
filestore
lock
clock
idgen
metrics
tracing
health
testkit
```

业务模块不得私自创建数据库连接、事务、队列、文件存储或日志体系。

## 10. 依赖方向

允许：

```text
agent -> platform interfaces
mission -> agent interfaces
mission -> platform interfaces
app -> mission / agent / resource / memory interfaces
transport -> internal/app/*
cmd -> everything for wiring
```

禁止：

```text
platform import agent
platform import mission
agent/runtime adapter 直接写 mission 状态
mission 直接 import 具体 CLI backend
transport 直接写 SQL
domain types import storage / db driver
```

## 11. 日志系统优先

最先实现：

```text
internal/platform/logging
internal/platform/config
```

所有后续模块必须使用统一 logger。

Config 的结构边界与 logger 一样属于平台契约。`internal/platform/config` 只放基础设施公共字段和 section 注册/解码/热加载机制；平台自己的 database、logging、filestore、queue、lock、liveness、metrics、sweeper、health 是首批注册 section，用来验证这套机制。业务模块需要额外配置时，在自己的 package 定义配置 struct 并注册自己的 YAML section，不能为了业务字段修改平台 Config 成为巨型总配置。

最低要求：

```text
支持 debug / info / warn / error。
支持结构化字段。
支持 component 字段。
支持 correlation_id。
支持 mission_id、agent_id、task_id、run_id。
支持 error_code、duration_ms、attempt、status。
支持开发环境可读输出。
支持生产环境 JSON 输出。
```

日志级别原则：

```text
debug:
  详细执行过程、分支判断、hook 调用、payload 摘要、claim 细节、runtime 消息、重试计划。

info:
  重要状态变化、启动完成、迁移完成、任务被接收、runtime started、结果提交、QA 通过/驳回、sweeper 执行摘要。

warn:
  可恢复异常、降级、重试、外部依赖不稳定、缓存失效、Redis 不可用但已退回 DB。

error:
  请求失败、事务失败、不可恢复执行失败、数据不变量破坏。
```

禁止：

```text
fmt.Println 作为业务日志。
吞 error 不记录。
只记录 error 字符串而不带上下文 ID。
debug 日志输出完整密钥、token、用户隐私或超大 payload。
```

## 12. 功能归集规则

每个 goal 的代码归集：

```text
01-infrastructure-foundation
  -> internal/platform/*

02-core-contracts
  -> internal/agent/core
  -> internal/mission/core
  -> internal/mission/task

03-lifecycle-signals
  -> internal/agent/lifecycle

04-event-ledger-projection
  -> internal/mission/event
  -> internal/mission/projection
  -> internal/app/missioncontrol/query_service.go
  -> internal/app/missioncontrol/dto.go
  -> internal/app/missioncontrol/mapper.go
  -> internal/platform/eventstore 只放存储实现

05-resource-registry-memory
  -> internal/resource/tool
  -> internal/resource/skill
  -> internal/memory

06-task-delivery-qa
  -> internal/mission/task
  -> internal/agent/component/delivery
  -> internal/agent/component/evidence
  -> internal/agent/component/qa

07-base-agent-runtime
  -> internal/agent/core
  -> internal/agent/assembly
  -> internal/agent/component/*
  -> internal/agent/runtime/*

08-organization-staffing
  -> internal/agent/core/org_identity.go
  -> internal/agent/core/profile_repository.go
  -> internal/mission/team

09-discussion-inbox
  -> internal/mission/discussion
  -> internal/app/missioncontrol 中的 DiscussionView / InboxView 查询聚合

10-api-transport
  -> internal/transport/http
  -> internal/transport/websocket
  -> pkg/publicapi 只放稳定外部类型

99-deferred-frontend-mission-control
  -> frontend only
  -> 只消费 internal/app/missioncontrol 暴露的 HTTP / WebSocket DTO
```

如果一个 goal 需要修改其他 goal 的目录，必须先写清楚原因和交互契约，不能顺手改。

## 13. 接口与实现分离

业务层依赖接口，平台层提供实现。

推荐：

```text
internal/agent/...       Agent 能力和接口
internal/mission/...     Mission 协作和事实
internal/app/...         应用服务、DTO、查询、命令、流式订阅
internal/transport/...   HTTP / WebSocket 协议适配
internal/platform/...    具体基础设施实现
```

例子：

```go
// mission/event
type EventSink interface {
    Append(ctx context.Context, events ...MissionEvent) error
}

// platform/eventstore
type SQLEventStore struct {
    db DB
}
```

禁止 app 层到处直接依赖 SQL driver。

## 14. 错误处理

必须定义稳定错误类型：

```text
validation_error
not_found
conflict
permission_denied
dependency_unavailable
timeout
cancelled
internal_error
```

错误必须能携带：

```text
code
message
cause
field
retryable
metadata
```

日志记录错误时必须带：

```text
error_code
operation
correlation_id
mission_id / agent_id / task_id / run_id
```

## 15. 测试要求

每个 goal 至少提供：

```text
单元测试
关键不变量测试
错误路径测试
testkit 或 fake 实现
```

基础设施必须提供：

```text
NewTestLogger
NewTestConfig
NewTestDB
NewTestUnitOfWork
NewTestEventStore
NewTestFileStore
NewFakeClock
NewDeterministicIDGenerator
```

禁止只测 happy path。

## 16. 文档同步

每个 goal 开发时必须维护自己的 README：

```text
已实现内容
未实现内容
公开接口
关键不变量
测试入口
CROSS_GOAL 变更
```

如果实际实现改变了目录结构，必须同步更新本文档或在 goal README 中记录偏离原因。
