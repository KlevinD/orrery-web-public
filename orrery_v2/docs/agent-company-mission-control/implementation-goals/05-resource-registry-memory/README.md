# Goal 05：资源注册与记忆系统

本 goal 建立 Agent 可引用资源的注册体系，以及独立的长期记忆系统。它承接主文档中的 Tool / MCP / Skill / Memory 边界：Tool、MCP、CLI、API、RPA、Skill 都不是团队成员；Memory 不是 prompt 片段。AgentProfile 只保存引用，BaseAgent 只消费解析后的 Toolset、Skillset 和 MemoryDigest / ContextPack。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)。资源注册、个人记忆、集体记忆、Mission 记忆和讨论组记忆可以定义自己的 repository、索引和检索策略，但存储、分库事务、artifact 引用、日志、配置、密钥、健康检查和测试夹具必须走 platform。记忆内容不得直接进入日志、config dump 或 runtime trace；进入 Agent 上下文前必须压缩成可审计的 `MemoryDigest` / `ContextPack` 引用。

## 顶层设计

Agent 公司模型里，进入组织目录和 Mission Team 的是 Agent，不是工具。工具、MCP server、CLI、API、RPA、审批系统、人工确认入口都是 Agent 的可绑定资源。技能是 Agent 的可复用能力描述和执行材料。记忆是独立长期上下文系统，分为个人记忆和集体记忆。

本 goal 解决四个问题：

```text
1. Tool / MCP / Skill 有统一注册、版本、权限和禁用机制。
2. AgentProfile 只保存 ToolRef / SkillRef / MemoryProfileRef，不复制资源实现。
3. Memory 独立存储、检索、压缩、过滤和审计，不塞进 agent/core。
4. AgentAssembler 通过接口解析引用，不能直接读写 registry / memory store。
```

## 生产级职责边界

本 goal 负责：

```text
ToolDefinition / MCPServerDefinition / ApprovalToolDefinition
SkillDefinition
ToolRegistry / ToolResolver
SkillRegistry / SkillResolver
ResourceBinding
MemoryProfile
PersonalMemory / CollectiveMemory / MissionMemory / DiscussionMemory
MemoryCandidate
MemoryStore
MemoryRetriever
MemoryCompressor
MemoryPolicy
MemoryDigest
MemoryBinder
MemoryAuditEvent
```

本 goal 不负责：

```text
AgentProfile 静态契约定义
AgentAssembler 具体装配流程
BaseAgent pipeline
RuntimeAdapter 执行
TaskLifecycle / QA
Discussion message 存储
MissionEvent 投影
前端资源市场
```

禁止事项：

```text
Tool / MCP / Skill 直接成为 AgentInstance 或 MissionTeam member。
AgentProfile 内嵌 ToolDefinition / SkillDefinition / full memory。
MemoryStore 被 BaseAgent 直接读取全量历史。
审批建成独立 ApprovalCapability；审批和人工确认都应是 ToolRef。
MemoryDigest 无来源引用或无可见性边界。
讨论组消息自动全部写入长期记忆。
```

## 中层模块规划

代码归集：

```text
internal/resource/tool/
  types.go
  registry.go
  resolver.go
  mcp.go
  approval_tool.go
  permission.go
  binding.go
  errors.go
  testkit.go

internal/resource/skill/
  types.go
  registry.go
  resolver.go
  version.go
  evidence.go
  errors.go
  testkit.go

internal/memory/core/
  types.go
  profile.go
  scope.go
  policy.go
  candidate.go
  digest.go
  visibility.go
  errors.go

internal/memory/store/
  repository.go
  search.go
  sqlite_store.go
  fake_store.go

internal/memory/retrieval/
  retriever.go
  compressor.go
  context_builder.go
  ranker.go
  filters.go

internal/memory/audit/
  events.go
  recorder.go
```

依赖方向：

```text
resource/tool -> agent/core refs, platform logging/db interfaces
resource/skill -> agent/core refs, platform logging/db interfaces
memory/core -> agent/core refs, mission refs
memory/retrieval -> memory/core, memory/store
agent/assembly -> ToolResolver / SkillResolver / MemoryBinder interfaces
resource/memory 不依赖 agent/runtime、mission/task lifecycle、transport
```

## 数据与资源设计

### ToolDefinition

```text
ToolDefinition {
  tool_id
  kind
  name
  description
  version
  runtime_requirements
  input_schema
  output_schema
  permission_declarations[]
  secret_refs[]
  enabled
  risk_level
  created_at
  updated_at
}
```

`kind` 至少包含：

```text
mcp_server
api_tool
cli_tool
rpa_tool
browser_tool
approval_tool
human_confirmation_tool
internal_service_tool
```

### MCPServerDefinition

```text
MCPServerDefinition {
  tool_id
  server_name
  transport
  command_or_url
  env_secret_refs[]
  exposed_tools[]
  permission_declarations[]
  health_check_policy
}
```

### SkillDefinition

```text
SkillDefinition {
  skill_id
  name
  version
  description
  domain_tags[]
  task_type_tags[]
  prompt_assets[]
  required_tools[]
  evidence_refs[]
  enabled
}
```

### Resolver 输出

```text
ResolvedTool {
  ref
  definition
  granted_permissions
  secret_binding_refs[]
  binding_warnings[]
}

ResolvedSkill {
  ref
  definition
  matched_version
  binding_warnings[]
}
```

Resolver 只解析和校验，不执行工具。

## 记忆体系设计

### MemoryProfile

```text
MemoryProfile {
  memory_profile_id
  owner_ref
  memory_kind
  default_scope
  retrieval_policy
  compression_policy
  retention_policy
  visibility_policy
  enabled
}
```

`memory_kind`：

```text
personal
collective
mission
discussion
project
department
organization
```

### MemoryRecord

```text
MemoryRecord {
  memory_id
  profile_id
  scope
  source_refs[]
  source_evidence_refs[]
  content
  summary
  facts[]
  preferences[]
  warnings[]
  decisions[]
  tags[]
  confidence
  visibility
  valid_from
  expires_at
  created_by
  created_at
  updated_at
  disabled_at
}
```

### MemoryCandidate

```text
MemoryCandidate {
  candidate_id
  mission_id
  discussion_group_id
  source_type
  source_ref
  proposed_scope
  proposed_kind
  summary
  evidence_refs[]
  created_by
  status
}
```

状态：

```text
proposed
accepted
rejected
merged
expired
```

讨论组消息、summary、deliverable、evidence、QA 结果和结构化事件只能产生候选记忆。候选记忆必须经过分类、去重、冲突检测、权限确认和证据绑定后才能成为长期记忆。

### MemoryDigest

```text
MemoryDigest {
  digest_id
  agent_id
  mission_id
  task_id
  scope
  source_memory_refs[]
  source_evidence_refs[]
  summary
  facts[]
  preferences[]
  warnings[]
  decisions[]
  confidence
  visibility
  created_at
  expires_at
}
```

规则：

```text
PersonalMemoryDigest 默认只给所属 Agent / RealHumanAgent 使用。
CollectiveMemoryDigest 可按 organization / department / project / mission / discussion scope 授权给成员。
MemoryDigest 必须带 source refs。
MemoryDigest 不能覆盖用户明确指令、TaskContract、AcceptanceCriterion 或 MissionEvent。
低 confidence 记忆只能作为提示，不作为验收依据。
```

## 底层实现指导

### Registry

Registry 至少支持：

```text
Register
UpdateDefinition
Disable
Resolve
ListByTag / ListByKind
CheckPermission
```

实现要求：

```text
版本约束解析必须可测试。
禁用资源不能被 resolver 返回为可绑定状态。
权限不足返回 permission_denied，不返回 not_found。
secret 只返回 secret ref，不返回 secret value。
```

### Memory retrieval

检索流程：

```text
BuildRetrievalQuery(agent, mission, task, context policy)
-> filter by visibility and scope
-> search candidates / records
-> rank by relevance, recency, confidence, source strength
-> compress to digest
-> attach source refs
```

压缩要求：

```text
按当前 task 和 role 生成摘要。
保留事实、偏好、警告、决策四类结构化片段。
保留 source_memory_refs / source_evidence_refs。
限制 token / size。
不输出 raw private content。
```

### MemoryBinder

供 AgentAssembler 使用的接口：

```go
type MemoryBinder interface {
    Bind(ctx context.Context, req MemoryBindRequest) (MemoryBinding, error)
}

type MemoryBinding struct {
    PersonalDigest   MemoryDigest
    CollectiveDigests []MemoryDigest
    Warnings         []BindingWarning
}
```

MemoryBinder 不构建完整 ContextPack；ContextPack 由 Goal 07 装配，但 digest 和策略由本 goal 提供。

### 审计

必须记录：

```text
memory_candidate_created
memory_candidate_accepted
memory_record_created
memory_record_updated
memory_record_disabled
memory_digest_generated
memory_access_denied
tool_resolved
tool_binding_denied
skill_resolved
```

审计事件可以后续映射到 MissionEvent 或独立 audit log，但本 goal 至少定义事件语义和 recorder 接口。

## 现有实现复用计划

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `agenttmpl` templates / bundled skills | 映射为 SkillDefinition seed | 不把 template 当 AgentProfile 本身 |
| Orrery | market agent catalog | 拆分为 AgentProfile seed、ToolDefinition、SkillDefinition 参考 | `kind=tool` 必须迁出组织成员语义 |
| Orrery | agent `mcp_config`、custom env、custom args | 映射为 ToolDefinition / MCPServerDefinition / RuntimeSpec 引用 | secret 只能通过 Secret Provider |
| Orrery | QA plan/check templates | 作为 SkillDefinition 或 QAContractRef 的种子 | QA 执行归 Goal 06 |
| Orrery | comments / timeline / evidence summaries | 作为 MemoryCandidate 来源参考 | 不自动写长期记忆 |
| Multica / Orrery | CLI provider config、skills hydration | 作为 ToolDefinition / SkillDefinition resolver 参考 | runtime 执行归 Goal 07 |

## 运行流程

工具绑定：

```text
AgentAssembler requests ToolResolver with AgentProfile.tool_refs
-> resolver loads ToolDefinition
-> checks enabled, version, permission, secret refs
-> returns ResolvedTool / binding warnings
```

技能绑定：

```text
AgentAssembler requests SkillResolver with AgentProfile.skill_refs
-> resolver loads SkillDefinition
-> checks version and task tags
-> returns ResolvedSkill
```

记忆注入：

```text
AgentAssembler / BaseAgent requests MemoryBinder
-> MemoryRetriever filters personal + collective scopes
-> MemoryCompressor creates MemoryDigest
-> MemoryBinding returns digests and source refs
-> Goal 07 inserts digest refs into ContextPack
```

记忆沉淀：

```text
Message / summary / deliverable / evidence / QA / decision creates MemoryCandidate
-> classify personal / collective / mission / discussion scope
-> validate permission and evidence
-> accept / reject / merge
-> create MemoryRecord
```

## 测试与验收

建议测试文件：

```text
internal/resource/tool/registry_test.go
internal/resource/tool/resolver_test.go
internal/resource/skill/registry_test.go
internal/resource/skill/resolver_test.go
internal/memory/core/policy_test.go
internal/memory/store/repository_test.go
internal/memory/retrieval/retriever_test.go
internal/memory/retrieval/compressor_test.go
internal/memory/retrieval/context_builder_test.go
internal/memory/audit/events_test.go
```

独立验收场景：

1. 可以注册 ToolDefinition，包括 MCP server、approval tool、human confirmation tool。
2. 可以注册 SkillDefinition，并声明版本、适用任务、证据引用和所需工具。
3. AgentProfile 可以引用 ToolRef / SkillRef / MemoryProfileRef，但不内嵌资源实现。
4. 禁用的 ToolDefinition 无法被绑定。
5. 缺少权限的 ToolRef 无法被绑定，并返回 permission_denied。
6. Resolver 不返回 secret value，只返回 secret binding ref。
7. 可以创建 personal MemoryProfile 和 collective MemoryProfile。
8. Discussion / QA / Evidence 只能生成 MemoryCandidate，不能绕过审核直接写 MemoryRecord。
9. MemoryRetriever 按 policy 返回 MemoryDigest，而不是全量记忆。
10. MemoryCompressor 生成带 source refs、visibility、confidence 的 digest。
11. 个人记忆默认不对其他 Worker 可见。
12. 集体记忆按 organization / department / mission / discussion scope 授权。
13. 发起审批被建模为 ToolRef，而不是独立 capability。

完成证明：

```text
go test ./internal/resource/tool ./internal/resource/skill ./internal/memory/...
rg "internal/agent/runtime|internal/mission/task|internal/transport" orrery_v2/internal/resource orrery_v2/internal/memory 无非法依赖
测试证明 MemoryDigest 不包含 raw private memory
```

## CROSS_GOAL

```text
CROSS_GOAL: 02-core-contracts
需要：ToolRef、SkillRef、MemoryProfileRef、MemoryDigestRef、ContextPack 字段。
当前假设：本 goal 扩展解析与绑定，不改 TaskContract 核心语义。
后续约定：Ref 字段命名、版本约束格式、policy 枚举。
```

```text
CROSS_GOAL: 04-event-ledger-projection
需要：MemoryCandidateCreated、MemoryRecordCreated 等事件可被投影或审计。
当前假设：本 goal 定义 audit event，MissionEvent 映射由 Goal 04 决定。
后续约定：哪些 memory candidate 进入 DiscussionView / MissionControlView。
```

```text
CROSS_GOAL: 07-base-agent-runtime
需要：AgentAssembler 解析 ToolRef、SkillRef、MemoryProfileRef。
当前假设：本 goal 提供 ToolResolver、SkillResolver、MemoryBinder 接口。
后续约定：绑定失败错误码、digest 注入位置、runtime request 展示方式。
```

```text
CROSS_GOAL: 09-discussion-inbox
需要：讨论组消息、决策、阻塞、回复产生 MemoryCandidate。
当前假设：本 goal 不存 discussion message，只接收候选来源引用。
后续约定：DiscussionDigest 与 MemoryDigest 的边界。
```
