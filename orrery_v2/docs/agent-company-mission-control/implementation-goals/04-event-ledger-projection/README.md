# Goal 04：事件账本、后端投影与前端查询服务

本 goal 定义 Mission Control 的业务事实源和后端读模型。它把 Goal 02 的核心契约和 Goal 03 的生命周期信号边界连接起来：业务事实必须写成 MissionEvent，前端视图必须从事件投影而来，旧 timeline、comment、task queue、前端流程图都不能成为事实源。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)。业务事件语义属于本 goal，但物理 EventStore、Outbox、DBRouter、RoutedUOW、migration coordinator、logger、health 和 testkit 由平台层提供。事件表、投影表和 outbox 表必须按逻辑 role 使用 `events.*`、`core.*` 或 `jobs.*` 逻辑表名，不能绕过平台分库，也不能伪造跨库原子事务。

## 顶层设计

Agent 公司 Mission Control 不是固定流程引擎，而是一个动态协作系统。成员可以被追加、任务可以返工、QA 可以驳回、讨论组可以提出变更、Worker 可以阻塞。前端要展示的不是一张写死流程图，而是实际发生过的事实轨迹。

因此本 goal 建立三层：

```text
MissionEvent：已经发生的业务事实。
Projection：从事件流回放得到的后端 read model。
MissionControl app service：对前端提供权限裁剪后的 DTO / patch。
```

物理事件存储由 Goal 01 的 `internal/platform/eventstore` 提供。本 goal 只定义业务事件语义、事件 envelope 的业务 payload、投影规则、QueryService / StreamService 和 DTO 边界。

## 生产级职责边界

本 goal 负责：

```text
MissionEvent 类型体系
EventSink / EventReader / EventStream 领域接口
Signal -> MissionEvent 映射规则
Projector
Projection read model
MissionControlView
TeamView
TaskBoardView
EvidenceView
QAView
DiscussionView
InboxSummaryView
ProgressGraphView
MissionControlQueryService
MissionControlStreamService
MissionControlViewDTO
MissionControlPatchDTO
ViewMapper
权限裁剪规则
```

本 goal 不负责：

```text
platform eventstore 物理实现
SignalBus 实现
Agent runtime 执行
TaskLifecycle 业务操作
QA 具体验收
Discussion message 存储
HTTP / WebSocket handler
前端组件
旧 timeline 兼容接口
```

禁止事项：

```text
前端直接消费 MissionEvent 原始事件。
transport/http 直接查 projection 表。
QueryService 写入 MissionEvent。
DTO 直接复用 domain struct。
timeline / comment / task queue 成为事实源。
Projector 为某个前端组件写 UI-only 状态。
```

## 中层模块规划

代码归集：

```text
internal/mission/event/
  event.go
  envelope.go
  types.go
  payloads.go
  sink.go
  reader.go
  mapper.go
  signal_mapping.go
  idempotency.go
  errors.go

internal/mission/projection/
  projector.go
  registry.go
  checkpoint.go
  read_model.go
  team_view.go
  task_board_view.go
  evidence_view.go
  qa_view.go
  discussion_view.go
  progress_graph_view.go
  patch.go
  errors.go

internal/app/missioncontrol/
  query_service.go
  stream_service.go
  dto.go
  mapper.go
  permissions.go
  pagination.go
  filters.go
  errors.go
```

依赖方向：

```text
mission/event -> agent/core, mission/core, mission/task contracts
mission/projection -> mission/event, platform logging, optional platform db interfaces
app/missioncontrol -> mission/projection, mission/event reader, agent/resource/memory refs for display names only
transport -> app/missioncontrol
mission/event 不依赖 transport / frontend / runtime provider
```

## 数据与事件设计

### EventEnvelope

Goal 01 提供通用 envelope 存储；本 goal 定义业务 envelope 约束：

```text
MissionEventEnvelope {
  event_id
  mission_id
  stream_id
  stream_version
  event_type
  event_version
  actor_ref
  causation_id
  correlation_id
  idempotency_key
  occurred_at
  payload
  metadata
}
```

要求：

```text
mission_id 必须存在。
stream_id 默认为 mission_id；后续可支持 task / discussion 子 stream，但投影必须能按 mission_id 回放。
stream_version 必须单调递增。
idempotency_key 用于防重复业务事实。
actor_ref 记录责任来源，可以是 Agent、RealHumanAgent、system、user。
payload 必须 versioned，不能使用无约束 any。
```

### 最小事件集

Mission：

```text
MissionCreated
MissionGoalUpdated
MissionCompleted
MissionCancelled
```

Staffing / Team：

```text
StaffingRequested
AgentInvited
AgentJoined
AgentReleased
MissionAssignmentCreated
MissionAssignmentChanged
```

Task：

```text
TaskContractCreated
TaskAssigned
TaskAccepted
TaskStarted
TaskBlocked
TaskSubmitted
ReworkRequested
TaskApproved
TaskRejected
TaskCancelled
```

Delivery / Evidence / QA：

```text
AgentResultSubmitted
SummarySubmitted
DeliverableSubmitted
EvidenceSubmitted
QARequested
QAApproved
QARejected
AcceptanceReportCreated
```

Discussion：

```text
DiscussionGroupCreated
MessagePosted
AgentMentioned
AgentReplied
InboxItemCreated
InboxItemClosed
DecisionRecorded
BlockerRaised
ClarificationRequested
MemoryCandidateCreated
```

Runtime-significant events：

```text
AgentRunFailed
AgentRunTimedOut
AgentRunCancelled
AgentRunBlocked
```

不是所有 Signal 都有对应 Event。只有影响 Mission 事实、任务状态、协作责任、证据、QA 或前端投影的 Signal 才升级为 Event。

### Signal -> Event 映射

建议映射：

| Signal | MissionEvent | 条件 |
| --- | --- | --- |
| AfterTaskAccepted | TaskAccepted | 任务接受改变 Mission 任务事实 |
| RuntimeProgress | 无默认事件 | 仅作为 runtime 内部进度，除非显式上报用户可见 progress |
| AfterResultCommit | AgentResultSubmitted / DeliverableSubmitted / EvidenceSubmitted | AgentResult 已通过提交边界 |
| AgentBlocked | TaskBlocked / BlockerRaised | 阻塞影响任务或需要团队响应 |
| AgentFailed | AgentRunFailed | 失败影响任务状态或需要返工 / 调配 |
| AgentTimeout | AgentRunTimedOut | 超时影响任务状态 |
| AfterAgentReply | AgentReplied | 回复进入讨论组线程 |

## 投影设计

Projection 是后端 read model，不是前端 UI 状态。

### MissionControlView

```text
MissionControlView {
  mission
  team
  task_board
  evidence
  qa
  discussion
  inbox_summary
  progress_graph
  latest_decisions[]
  blockers[]
  updated_at
}
```

### TeamView

来自 staffing / assignment 事件：

```text
TeamView {
  members[]
  assignments[]
  staffing_requests[]
}

TeamMemberView {
  agent_id
  display_name
  real_name
  role_name
  department
  position
  mission_role
  status
  joined_at
}
```

### TaskBoardView

来自 TaskContract / Task 状态 / QA / Rework 事件：

```text
TaskBoardView {
  tasks[]
  dependencies[]
}

TaskCardView {
  task_id
  assignee
  goal
  worker_personal_goal_summary
  status
  expected_deliverables[]
  required_evidence[]
  latest_result_ref
  qa_status
  blocker_refs[]
}
```

### EvidenceView / QAView

```text
EvidenceView {
  evidence_items[]
  missing_requirements[]
}

QAView {
  checks[]
  approvals[]
  rejections[]
  rework_requests[]
}
```

### DiscussionView

DiscussionView 是压缩协作视图，不是全量内部上下文：

```text
DiscussionView {
  discussion_group_id
  messages[]
  pending_mentions[]
  decisions[]
  blockers[]
  discussion_digest_ref
}
```

### ProgressGraphView

ProgressGraphView 表达实际发生的轨迹：

```text
ProgressGraphView {
  nodes[]
  edges[]
  current_focus[]
}
```

节点来自真实事件，如 MissionCreated、AgentJoined、TaskAssigned、TaskSubmitted、QARejected、ReworkRequested、TaskApproved。不能根据固定流程模板预生成未发生节点。

## 底层实现指导

### Projector

接口建议：

```go
type Projector interface {
    Apply(ctx context.Context, view *ReadModel, event MissionEventEnvelope) error
}

type ReplayService interface {
    ReplayMission(ctx context.Context, missionID MissionID) (ReadModel, error)
}
```

要求：

```text
重复 replay 同一事件流结果稳定。
重复事件通过 event_id / idempotency_key 去重。
乱序事件默认拒绝或进入 dead letter projection error，不静默吞掉。
未知 event_version 必须返回可诊断错误。
projector 日志包含 mission_id、event_id、event_type、stream_version、correlation_id。
```

### Patch

StreamService 输出 patch，而不是原始事件：

```text
MissionControlPatchDTO {
  patch_id
  mission_id
  base_version
  next_version
  changed_views[]
  payload
  emitted_at
}
```

WebSocket handler 在 Goal 10 中只转发 patch，不自行订阅 eventstore。

### 权限裁剪

QueryService / StreamService 必须接收 viewer：

```text
ViewerRef {
  viewer_id
  viewer_kind
  organization_scope
  mission_roles[]
  permission_scope
}
```

裁剪规则：

```text
默认不暴露 Agent internal_trace_ref。
默认不暴露个人记忆内容。
只显示 viewer 可见的 evidence、discussion message、task detail。
summary 可以显示，但必须保留 source_refs 以便追溯。
```

## 现有实现复用计划

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `mission_timeline_event`、PM timeline、timelineProjection | 提取事件分类、排序、前端展示经验 | timeline 只能作为 projection 参考，不是事实源 |
| Orrery | `mission_brief/run/task/evidence/review_packet` migrations | 映射成 MissionEvent payload 和 read model 字段参考 | 不让旧表直接成为新 read model 契约 |
| Orrery | comment / reply / mention / inbox 实现 | 映射到 MessagePosted、AgentMentioned、InboxItemCreated、AgentReplied 事件 | 不兼容 legacy issue comment 作为核心模型 |
| Orrery | QA plan / QA check / review packet | 映射到 QARequested、QAApproved、QARejected、AcceptanceReportCreated | QA 执行仍归 Goal 06 |
| Orrery | mission_staffing service / migrations | 映射到 StaffingRequested、AgentJoined、MissionAssignmentCreated | staffing 规则归 Goal 08 |
| Multica / Orrery | outbox / realtime / notification 经验 | 用于 patch 投递、stream service 和投影异步刷新 | 不让 transport 直接消费 eventstore |

迁移旧数据时，只允许两种方式：

```text
一次性 backfill 生成 MissionEvent。
只读归档展示 legacy timeline。
```

禁止新内核运行时继续把旧 timeline / comment 当事实源。

## 运行流程

写入业务事实：

```text
Domain service completes a business action
-> build MissionEventEnvelope
-> append through EventSink using platform EventStore
-> enqueue projection outbox job
-> transaction commit
```

投影：

```text
Projector loads new events
-> apply event to ReadModel
-> update checkpoint
-> emit MissionControlPatchDTO to StreamService subscribers
```

查询：

```text
HTTP handler
-> MissionControlQueryService.GetMissionControlView
-> read projection
-> apply permission trimming
-> map to DTO
-> return to transport
```

replay：

```text
Load mission event stream from version 1
-> apply projector in order
-> compare checkpoint
-> rebuild read model
```

## 测试与验收

建议测试文件：

```text
internal/mission/event/envelope_test.go
internal/mission/event/signal_mapping_test.go
internal/mission/projection/projector_test.go
internal/mission/projection/replay_test.go
internal/mission/projection/progress_graph_test.go
internal/app/missioncontrol/query_service_test.go
internal/app/missioncontrol/permissions_test.go
internal/app/missioncontrol/stream_service_test.go
```

独立验收场景：

1. 可以 append MissionEventEnvelope，并按 MissionID / stream_version 读取。
2. 可以区分 Signal 和 Event；`BeforeRuntimeStart` 不会默认产生 MissionEvent。
3. `AfterResultCommit` 可以映射为 AgentResultSubmitted / DeliverableSubmitted / EvidenceSubmitted。
4. 重复投影同一事件流结果稳定。
5. 重复事件不会造成重复 TeamMember、TaskCard、EvidenceItem。
6. 乱序或未知版本事件有明确处理策略和日志。
7. 可以投影 TeamView。
8. 可以投影 TaskBoardView。
9. 可以投影 EvidenceView / QAView。
10. 可以投影 DiscussionView / InboxSummaryView。
11. 可以投影 ProgressGraphView，且不预生成未发生流程节点。
12. MissionControlQueryService 返回 MissionControlViewDTO。
13. DTO 按 viewer 权限裁剪，不暴露 internal trace、个人记忆、不可见 evidence。
14. StreamService 输出 MissionControlPatchDTO，不输出 MissionEvent 原始事件。

完成证明：

```text
go test ./internal/mission/event ./internal/mission/projection ./internal/app/missioncontrol
rg "internal/transport|frontend" orrery_v2/internal/mission/event orrery_v2/internal/mission/projection 无结果
QueryService 测试证明 handler 不需要直接读 projection 表
权限测试证明 internal_trace_ref / personal_memory 不进入 DTO
```

## CROSS_GOAL

```text
CROSS_GOAL: 01-infrastructure-foundation
需要：platform EventStore、UnitOfWork、Outbox、Logger、TestEventStore。
当前假设：Goal 01 只保存通用 envelope。
后续约定：MissionEventEnvelope 到 platform envelope 的 mapper。
```

```text
CROSS_GOAL: 02-core-contracts
需要：MissionID、AgentID、TaskID、EvidenceID、DeliverableID、AgentResult、TaskContract refs。
当前假设：事件 payload 引用契约类型，不复制 domain struct。
后续约定：事件 payload versioning 和 ref 字段命名。
```

```text
CROSS_GOAL: 03-lifecycle-signals
需要：Signal -> MissionEvent 映射规则。
当前假设：EventLedger 不订阅所有 signal，只接收显式 business event 或 BaseAgent 提交的 event candidate。
后续约定：AgentFailed / AgentBlocked / AgentTimeout 升级条件。
```

```text
CROSS_GOAL: 06-task-delivery-qa
需要：TaskSubmitted、DeliverableSubmitted、EvidenceSubmitted、QAApproved、QARejected、ReworkRequested 事件。
当前假设：本 goal 定义事件和投影，Goal 06 触发这些事件。
后续约定：QAResult payload 和 AcceptanceReport 字段。
```

```text
CROSS_GOAL: 08-organization-staffing
需要：StaffingRequested、AgentJoined、AgentReleased、MissionAssignmentCreated 事件。
当前假设：本 goal 定义事件和 TeamView 投影，Goal 08 实现调配规则。
后续约定：成员离组后的可见性和审计字段。
```

```text
CROSS_GOAL: 09-discussion-inbox
需要：MessagePosted、AgentMentioned、AgentReplied、InboxItemCreated、InboxItemClosed、DecisionRecorded、BlockerRaised。
当前假设：本 goal 定义事件和 DiscussionView 投影，Goal 09 实现消息/inbox 存储与操作。
后续约定：DiscussionDigest 和 message visibility 字段。
```

```text
CROSS_GOAL: 10-api-transport
需要：MissionControlQueryService、MissionControlStreamService、DTO、Patch envelope。
当前假设：transport 只依赖 app service interface。
后续约定：HTTP route、WebSocket subscription、error response 格式。
```

```text
CROSS_GOAL: 99-deferred-frontend-mission-control
需要：MissionControlViewDTO、MissionControlPatchDTO。
当前假设：前端先 mock DTO，最终只消费 HTTP / WebSocket 输出。
后续约定：字段命名、状态枚举、分页、增量刷新策略。
```
