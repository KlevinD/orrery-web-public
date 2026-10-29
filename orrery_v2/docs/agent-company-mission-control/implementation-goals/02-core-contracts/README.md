# Goal 02：核心契约模型

本 goal 定义 Agent 公司 Mission Control 的共同语言。它不是运行时、不是数据库、不是前端 DTO，也不是从 0 忽略既有实现；它要把主文档中的 Mission、Agent、TaskContract、AgentResult、Evidence、ContextPack 等一等概念固化成后续模块都能依赖的纯契约。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)。核心契约层只定义领域类型、验证规则和错误结构；需要时间、ID、错误码或测试夹具时使用 `internal/platform` 的接口和 testkit，不得自建 config、DB、logger、FileStore、queue 或 runtime 基础设施。若契约层需要业务配置，只注册自己的 config section 和 validator，通过 `ConfigManager` 当前内存快照读取，不直接读环境变量或本地路径。

## 顶层设计

核心契约层解决的问题是：后续 `03-lifecycle-signals`、`04-event-ledger-projection`、`05-resource-registry-memory`、`06-task-delivery-qa`、`07-base-agent-runtime`、`08-organization-staffing`、`09-discussion-inbox` 都必须使用同一套 ID、状态、引用、结果和上下文语言。

本 goal 只定义稳定模型和校验规则：

```text
Agent 是责任主体。
Mission 是目标根对象。
TaskContract 是责任边界。
AgentResult 是统一交付出口。
Deliverable / Evidence 是验收依据。
ContextPack 是运行上下文的受控注入包。
Tool / Skill / Memory 是引用，不是内嵌实现。
```

如果这个层没有定清楚，后续模块会各自发明 `task result`、`runtime output`、`message summary`、`agent config`、`memory blob`，最终无法形成统一 Mission Control。

## 生产级职责边界

本 goal 负责：

```text
ID / Ref 类型
Mission 静态契约
AgentProfile / AgentIdentity / AgentPersona / AgentOrgIdentity
ToolRef / SkillRef / MemoryProfileRef / RuntimeSpec
TaskContract / AcceptanceCriterion / EvidenceRequirement
AgentResult / Summary / Deliverable / Evidence / Decision / AgentRequest
ContextPack / MemoryDigestRef / DiscussionDigestRef
基础校验规则和错误结构
```

本 goal 不负责：

```text
BaseAgent pipeline
SignalBus / lifecycle hook
数据库 schema / repository / migration
EventLedger 持久化和投影
Tool / Skill / Memory registry 具体实现
TaskLifecycle 状态机
QA 执行逻辑
MissionTeam 调配
DiscussionGroup 消息
HTTP / WebSocket / frontend DTO
```

禁止事项：

```text
把 runtime output 当 AgentResult 契约。
把 ToolRef / RuntimeAdapter 实例化为 AgentInstance。
把 MemoryProfileRef 展开成全量记忆字段。
把旧 worker_config、market catalog、comment payload 原样作为新契约。
为了兼容旧表而加入大量 nullable 大对象。
```

## 中层模块规划

代码归集：

```text
internal/agent/core/
  ids.go
  identity.go
  persona.go
  profile.go
  refs.go
  runtime_spec.go
  result.go
  context_pack.go
  validation.go
  errors.go

internal/mission/core/
  ids.go
  mission.go
  status.go
  errors.go

internal/mission/task/
  contract.go
  acceptance.go
  evidence_requirement.go
  validation.go
  errors.go
```

文件职责：

```text
ids.go：强类型 ID、Ref、String 方法、空值判断。
identity.go：AgentIdentity、AgentOrgIdentity、组织引用。
persona.go：AgentPersona 和行为边界描述。
profile.go：AgentProfile、AgentKind、启用条件。
refs.go：ToolRef、SkillRef、MemoryProfileRef、PolicyRef。
runtime_spec.go：RuntimeKind、RuntimeSpec、配置引用，不含执行器实现。
result.go：AgentResult、Summary、Deliverable、Evidence、Decision、AgentRequest。
context_pack.go：ContextPack、目标注入、MemoryDigestRef、DiscussionDigestRef。
contract.go：TaskContract、WorkerPersonalGoal、assignee、交付要求。
validation.go：纯校验函数，不访问 DB、不调用 registry、不执行 runtime。
errors.go：稳定 validation error / conflict error。
```

依赖方向：

```text
agent/core 可以引用 platform 基础错误、clock/idgen 的接口类型，但不依赖具体实现。
mission/task 可以引用 agent/core 的 AgentInstanceRef、AgentResult、EvidenceRef 等共享类型。
mission/core 不依赖 runtime、discussion、projection、transport。
```

## 数据与契约设计

### Mission

`Mission` 是用户目标的根对象，但本 goal 只定义静态契约，不定义事件、投影或团队。

```text
Mission {
  mission_id
  title
  final_goal
  business_direction
  requester_ref
  status
  created_at
}
```

最小规则：

```text
mission_id 不能为空。
final_goal 不能为空。
business_direction 可以通用，不限定软件开发。
status 只表达静态生命周期入口，详细进度由事件投影表达。
```

### AgentProfile

`AgentProfile` 是可被组织管理和任务调配的成员定义。Tool、CLI、API、RPA、MCP、RuntimeAdapter 都不能直接成为 AgentProfile，必须被 Agent 包装。

```text
AgentProfile {
  agent_profile_id
  real_name
  display_name
  role_name
  agent_kind
  org_identity
  persona
  personal_memory_profile_ref
  collective_memory_scope_refs[]
  skill_refs[]
  tool_refs[]
  runtime_spec
  permission_policy_ref
  context_injection_policy
  summary_contract_ref
  deliverable_contract_ref
  evidence_contract_ref
  qa_contract_ref
  availability
  enabled
}
```

必填边界：

```text
real_name
agent_kind
org_identity.organization_id
org_identity.department_ref
org_identity.position_ref
org_identity.manager_agent_ref 或明确 root/supervised policy
persona.backstory 或 responsibility_boundary
personal_memory_profile_ref
runtime_spec.kind
```

`skill_refs[]` 和 `tool_refs[]` 可以为空数组，但边界必须显式存在，表示该 Agent 暂无外部 skill/tool，而不是字段缺失。

### AgentOrgIdentity

组织身份是稳定身份，不随 Mission 改变：

```text
AgentOrgIdentity {
  organization_ref
  department_ref
  position_ref
  role_refs[]
  manager_agent_ref
  reporting_line_ref
  authority_scope
  employment_type
}
```

Mission 中的临时职责由 `MissionAssignment` 在 Goal 08 定义，不能覆盖 `AgentOrgIdentity`。

### Resource Refs

资源引用只表达“这个 Agent 可以请求绑定什么”，不包含资源实现。

```text
ToolRef {
  tool_id
  version_constraint
  alias
  permission_scope
  binding_policy
}

SkillRef {
  skill_id
  version_constraint
  alias
  level
  evidence_refs[]
}

MemoryProfileRef {
  memory_profile_id
  owner_agent_id
  memory_kind
  access_scope
  retrieval_policy
  compression_policy
}
```

`memory_kind` 至少区分：

```text
personal
collective
mission
discussion
project
organization
```

### TaskContract

`TaskContract` 是独立责任单元，不是 prompt。

```text
TaskContract {
  task_id
  mission_id
  assignee
  title
  goal
  worker_personal_goal
  scope
  inputs[]
  expected_deliverables[]
  acceptance_criteria[]
  evidence_requirements[]
  dependencies[]
  constraints[]
  due_policy
  context_policy
}
```

规则：

```text
assignee 必须是 AgentInstanceRef 或可解析的 MissionAssignmentRef。
goal 必须说明任务目标。
worker_personal_goal 必须从 goal / scope / expected_deliverables 派生，供 BaseAgent 注入上下文。
expected_deliverables 至少一项。
有 acceptance_criteria 时，AgentResult 必须能映射到验收依据。
有 evidence_requirements 时，提交结果必须提供对应 Evidence。
```

### AgentResult

`AgentResult` 是所有 Agent 的唯一交付出口。Runtime output、human submission、tool output 都必须被 normalize 成 AgentResult。

```text
AgentResult {
  result_id
  mission_id
  task_id
  agent_id
  run_id
  summary
  deliverables[]
  evidence[]
  decisions[]
  requests[]
  blocker
  internal_trace_ref
  created_at
}
```

规则：

```text
summary 必须存在。
summary 不能替代 evidence。
deliverables 必须能追溯 TaskContract.expected_deliverables。
evidence 必须能追溯 ArtifactRef、工具日志、外部 URL、审批记录或人工提交。
internal_trace_ref 只作为审计引用，不进入讨论组默认上下文。
```

### ContextPack

`ContextPack` 是受控上下文注入包，由 Goal 07 的 BaseAgent / AgentAssembler 构建，但契约在本 goal 定义。

```text
ContextPack {
  context_pack_id
  mission_id
  discussion_group_id
  agent_id
  task_id
  mission_final_goal
  discussion_group_goal
  agent_organization_identity
  agent_mission_role
  worker_personal_goal
  task_contract_ref
  acceptance_criteria_refs[]
  required_evidence_refs[]
  personal_memory_digest_ref
  collective_memory_digest_refs[]
  discussion_digest_ref
  relevant_decision_refs[]
  relevant_blocker_refs[]
  permission_scope
  tool_scope
  source_refs[]
  created_at
  expires_at
}
```

规则：

```text
所有 Agent 都应看到 mission_final_goal 和 discussion_group_goal。
承担任务的 Worker 必须看到 worker_personal_goal。
个人记忆和集体记忆只能以 digest ref 或摘要字段进入。
ContextPack 必须带 source_refs，便于审计“为什么这段上下文被注入”。
ContextPack 不能覆盖用户明确指令、TaskContract、AcceptanceCriterion 或 MissionEvent。
```

## 底层实现指导

### 校验策略

本 goal 的校验只做纯内存结构校验：

```text
ValidateMission(m Mission) []ValidationError
ValidateAgentProfile(p AgentProfile) []ValidationError
ValidateTaskContract(c TaskContract) []ValidationError
ValidateAgentResult(c TaskContract, r AgentResult) []ValidationError
ValidateContextPack(p ContextPack) []ValidationError
```

不得在校验里访问 DB、registry、memory store、runtime backend 或 discussion service。跨模块存在性校验留给 app service 或后续 goal。

### 错误结构

```text
ValidationError {
  code
  field
  message
  retryable
  metadata
}
```

错误码建议：

```text
missing_required_field
invalid_ref
invalid_state
invalid_agent_kind
tool_ref_not_agent
missing_summary
missing_deliverable
missing_required_evidence
memory_digest_required
context_source_required
```

### 测试策略

每个契约至少提供：

```text
构造成功测试
缺必填字段测试
边界冲突测试
ToolRef 不能作为 AgentInstanceRef 测试
TaskContract -> AgentResult 验证测试
ContextPack 禁止全量记忆注入测试
```

测试文件建议：

```text
internal/agent/core/profile_test.go
internal/agent/core/result_test.go
internal/agent/core/context_pack_test.go
internal/mission/core/mission_test.go
internal/mission/task/contract_test.go
internal/mission/task/result_validation_test.go
```

## 现有实现复用计划

本 goal 可以复用 Orrery 的现有概念经验，但不能直接照搬旧结构。

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `mission_brief`、`mission_run`、`mission_task`、`mission_evidence` | 提取字段语义，映射到 Mission / TaskContract / Evidence | 不继承旧 run/timeline 作为事实源 |
| Orrery | `workerconfig.Config` 中 responsibilities、deliverables、QAConfig | 映射到 AgentProfile refs、TaskContract、EvidenceRequirement、QAContractRef | 不把 worker_config 当运行时真相 |
| Orrery | `agent` 表中的 person_name、role_title、runtime_id、instructions | 映射到 AgentProfile、AgentPersona、RuntimeSpec | 不把 runtime row 当 Agent identity |
| Orrery | market catalog / templates | 作为 AgentProfile seed 的参考 | `kind=tool` 必须废弃或改成 ToolDefinition |
| Orrery | QA contract / QA plan / QA check 概念 | 抽象成 EvidenceRequirement、AcceptanceCriterion、QAContractRef | QA 执行放到 Goal 06 |

如果某个旧字段无法明确映射到新契约，应先记录为 migration note，不要塞进 `metadata any` 逃避设计。

## 运行流程

本 goal 不运行业务，但定义后续流程的语言：

```text
Create Mission
-> Select / instantiate AgentProfile as AgentInstance
-> Create TaskContract with worker_personal_goal
-> Build ContextPack from mission goal, discussion goal, task contract, memory digest refs
-> Runtime output normalized into AgentResult
-> Validate AgentResult against TaskContract
-> Later goals persist MissionEvent and project MissionControlView
```

## 测试与验收

独立验收场景：

1. 可以创建一个 Mission，并校验 `mission_id`、`final_goal`、`business_direction`。
2. 可以创建一个 AgentProfile，并声明 real name、organization、department、position、manager、persona、personal memory ref、collective memory scopes、skill refs、tool refs、runtime kind。
3. 缺少 real_name / org_identity / persona / personal_memory_profile_ref / runtime_spec 的 AgentProfile 无效。
4. ToolRef、SkillRef、RuntimeSpec 不能被实例化为 AgentInstanceRef。
5. 可以创建 TaskContract，并校验 goal、assignee、worker_personal_goal、expected deliverables。
6. 缺少 summary 的 AgentResult 无效。
7. 需要 evidence 的 TaskContract，不能接受无 evidence 的 AgentResult。
8. ContextPack 必须包含 mission_final_goal、discussion_group_goal、source_refs；Worker 任务必须包含 worker_personal_goal。
9. ContextPack 只能引用 MemoryDigestRef / DiscussionDigestRef，不能包含全量记忆正文或全量消息流。

完成证明：

```text
go test ./internal/agent/core ./internal/mission/core ./internal/mission/task
契约包不 import internal/agent/runtime、internal/memory/store、internal/resource/tool store、internal/transport
README 中所有拥有物都有类型或明确 deferred 到后续 goal 的引用说明
```

## CROSS_GOAL

```text
CROSS_GOAL: 03-lifecycle-signals
需要：AgentRunState 引用 MissionID、AgentID、TaskID、ContextPack、AgentResult。
当前假设：本 goal 提供静态类型，lifecycle 定义 signal payload。
后续约定：AgentRunState 是否放在 lifecycle 或 agent/core。
```

```text
CROSS_GOAL: 04-event-ledger-projection
需要：MissionID、TaskID、AgentID、EvidenceID、DeliverableID、AgentResult 摘要字段。
当前假设：本 goal 不定义 MissionEvent envelope。
后续约定：MissionEvent payload 只引用这些契约，不复制 domain struct。
```

```text
CROSS_GOAL: 05-resource-registry-memory
需要：ToolRef、SkillRef、MemoryProfileRef、MemoryDigestRef、ContextPack 记忆入口。
当前假设：本 goal 定义 ref 结构，不实现 registry / retriever / compressor。
后续约定：绑定失败错误码、digest 格式和访问策略枚举。
```

```text
CROSS_GOAL: 06-task-delivery-qa
需要：TaskContract、AcceptanceCriterion、EvidenceRequirement、AgentResult、Deliverable、Evidence。
当前假设：本 goal 只定义静态契约和基本校验，不定义 TaskLifecycle。
后续约定：Submitted / InReview / Approved / Rejected 等状态由 Goal 06 定义。
```

```text
CROSS_GOAL: 07-base-agent-runtime
需要：AgentProfile、RuntimeSpec、ContextPack、AgentResult。
当前假设：runtime 只消费契约，不拥有领域模型。
后续约定：RuntimeResponse -> AgentResult 的 normalizer 错误结构。
```

```text
CROSS_GOAL: 08-organization-staffing
需要：AgentOrgIdentity、OrganizationRef、DepartmentRef、PositionRef、RoleRef、ReportingLineRef。
当前假设：本 goal 定义身份字段，staffing 实现查询、任命和 MissionAssignment。
后续约定：组织目录 repository 和可用性筛选接口。
```


