# Goal 09：讨论组、mention、reply 与 inbox

本 goal 实现 Mission 原生讨论组、@mention、线程回复、待响应 inbox 和 DiscussionDigest。讨论组不是旧 issue comment 的兼容壳，也不是全量上下文仓库；它是 Mission 项目作战室，用于维护团队共享状态、定向唤醒成员、沉淀可审计协作记录，并向 BaseAgent 提供压缩后的讨论上下文。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)。讨论消息、mention、inbox 和 digest 是本 goal 语义；持久化、outbox 通知、artifact 附件、日志、配置、secret、health、metrics 和 testkit 使用 platform。讨论组可以维护共同目标和决策记录，但不得绕过 MissionEvent 直接改任务、人员、QA 或排程状态；附件只保存 FileStore artifact ref，不保存本地绝对路径。

## 顶层设计

每个 Mission 天然拥有一个 DiscussionGroup。讨论组的最终目标是维护团队共享状态：

```text
Mission 最终目标
当前讨论组共同目标
已确认决策
任务责任边界
成员依赖关系
证据索引
阻塞与开放问题
待响应 mention
下一步需要谁固化结构化变更
```

讨论组不是事实源本身。事实源是 Goal 04 的 MissionEvent；讨论组消息是协作记录，可以触发 InboxItem、MemoryCandidate 或结构化变更申请，但不能直接修改任务、人员、QA 或排程状态。

## 生产级职责边界

本 goal 负责：

```text
DiscussionGroup
DiscussionMember
Message
MessageVisibility
ReplyThread
Mention
MentionParser
InboxItem
NotificationState
DiscussionDigest
ContextDigest
MessagePolicy
InboxService
DiscussionService
```

本 goal 不负责：

```text
MissionTeam 成员调配
BaseAgent runtime 执行
TaskLifecycle / QA 状态修改
MissionEvent 物理存储
MemoryStore 长期记忆写入
前端消息组件
旧 issue comment 兼容
```

禁止事项：

```text
非成员直接发消息。
讨论组默认保存 Agent internal trace / full memory / full runtime log。
mention 直接改变结构化 Mission 状态。
reply 自动授权任务变更。
旧 comment.issue_id 成为 discussion_group_id。
@all 广播默认创建每个成员的强制执行任务。
```

## 中层模块规划

代码归集：

```text
internal/mission/discussion/
  group.go
  member.go
  message.go
  reply_thread.go
  mention.go
  mention_parser.go
  inbox.go
  notification.go
  digest.go
  context_digest.go
  policy.go
  service.go
  repository.go
  events.go
  errors.go
  testkit.go
```

与 app/projection 的边界：

```text
internal/mission/discussion 负责领域操作和存储接口。
internal/mission/projection 负责从 MessagePosted / AgentMentioned / AgentReplied / InboxItemClosed 投影 DiscussionView。
internal/app/missioncontrol 负责聚合 DiscussionView / InboxView DTO。
```

依赖方向：

```text
discussion -> mission/team member refs
discussion -> agent/core refs
discussion -> agent/lifecycle SignalBus interface for reply signals
discussion -> memory candidate interface
discussion 不依赖 runtime provider、transport、frontend
```

## 数据设计

### DiscussionGroup

```text
DiscussionGroup {
  discussion_group_id
  mission_id
  goal
  status
  created_at
  updated_at
}
```

`goal` 是讨论组共同目标，来自 Mission final goal 和当前阶段目标，可随结构化事件更新。

### DiscussionMember

```text
DiscussionMember {
  discussion_group_id
  agent_instance_id
  mission_assignment_ref
  member_kind
  visibility_scope
  can_post
  can_mention
  joined_at
  left_at
}
```

成员来自 Goal 08 的 MissionTeam / MissionAssignment。组织上级不因 reporting line 自动成为讨论组成员。

### Message

```text
Message {
  message_id
  mission_id
  discussion_group_id
  reply_to_message_id
  thread_id
  author_ref
  author_mission_role
  content
  summary
  message_kind
  mentions[]
  related_task_id
  related_evidence_id
  goal_ref
  memory_candidate_ref
  context_scope
  visibility
  source_refs[]
  created_at
}
```

`message_kind`：

```text
summary
deliverable
evidence
decision
blocker
request
reply
clarification
system_notice
```

规则：

```text
content 应是面向组内的压缩表达。
summary 可用于列表和 digest。
internal_trace_ref 只能作为附件/source ref，不进入默认 content。
```

### Mention

```text
Mention {
  mention_id
  message_id
  target_kind
  target_ref
  target_role
  created_by
  created_at
}
```

`target_kind`：

```text
agent
mission_role
user
team
all
```

### InboxItem

```text
InboxItem {
  inbox_item_id
  mission_id
  discussion_group_id
  message_id
  mention_id
  recipient_ref
  required_response
  status
  due_at
  responded_by_message_id
  created_at
  closed_at
}
```

状态：

```text
pending
read
responded
expired
dismissed
cancelled
```

### DiscussionDigest

```text
DiscussionDigest {
  digest_id
  mission_id
  discussion_group_id
  mission_final_goal
  discussion_group_goal
  current_phase
  active_decisions[]
  responsibility_map[]
  worker_goal_refs[]
  open_questions[]
  blockers[]
  evidence_index[]
  pending_mentions[]
  recent_summary
  source_message_refs[]
  source_event_refs[]
  visibility
  created_at
  expires_at
}
```

`DiscussionDigest` 进入 ContextPack。它不是全量消息流，只提供目标、决策、责任、阻塞、证据和待响应事项。

## 底层实现指导

### DiscussionService

接口建议：

```go
type DiscussionService interface {
    CreateGroup(ctx context.Context, missionID MissionID, goal string) (DiscussionGroup, error)
    AddMember(ctx context.Context, groupID DiscussionGroupID, member DiscussionMember) error
    PostMessage(ctx context.Context, cmd PostMessageCommand) (Message, error)
    Reply(ctx context.Context, cmd ReplyCommand) (Message, error)
    BuildDigest(ctx context.Context, groupID DiscussionGroupID, viewer ViewerRef) (DiscussionDigest, error)
}
```

要求：

```text
PostMessage 校验 author 是 group member。
PostMessage 解析 mentions。
PostMessage 写 Message 后创建 InboxItem。
Reply 必须挂回 thread_id。
Reply 可以关闭对应 InboxItem。
PostMessage / Reply 产生 MessagePosted / AgentMentioned / AgentReplied event candidate。
```

### MentionParser

复用 Orrery 现有 mention parser 经验，但目标语义需要调整：

```text
mention://agent/<agent_instance_id>
mention://role/<mission_role>
mention://user/<user_id>
mention://team/<team_id>
mention://all/all
```

规则：

```text
无法解析的 mention 作为 plain text 或 validation warning。
非成员 mention 需要权限策略决定是否允许邀请/旁听。
@all 默认只创建低优先级通知，不创建强制执行任务。
```

### InboxService

```go
type InboxService interface {
    ListInbox(ctx context.Context, recipient ViewerRef, q InboxQuery) (InboxPage, error)
    MarkRead(ctx context.Context, itemID InboxItemID, viewer ViewerRef) error
    CloseWithReply(ctx context.Context, itemID InboxItemID, reply MessageRef) error
    Expire(ctx context.Context, now time.Time) error
}
```

### MemoryCandidate

Discussion 可以生成 MemoryCandidate，但不直接写 MemoryRecord：

```text
decision message -> MemoryCandidate(proposed_kind=collective)
blocker resolved -> MemoryCandidate(proposed_kind=mission/discussion)
QA lesson -> MemoryCandidate(proposed_kind=project/organization)
```

MemoryCandidate 的接受、合并、拒绝由 Goal 05 处理。

## 现有实现复用计划

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `internal/util/mention.go`、`internal/mention/expand.go` | 迁入 MentionParser 的语法/解析经验 | target 映射为 Mission member / role，不再 issue-centered |
| Orrery | `internal/handler/comment.go`、reply parent/thread tests | 迁入 Message / ReplyThread 测试经验 | 不保留 issue_id 作为核心外键 |
| Orrery | `comment_trigger`、mention enqueue tests | 作为 InboxItem 创建和触发规则参考 | 不再直接 enqueue agent_task_queue |
| Orrery | `internal/handler/inbox.go`、inbox migrations | 迁入 InboxItem 状态和列表经验 | recipient 使用 Agent/User/Mission role refs |
| Orrery | `notification_listeners_02_notify_mentioned_members.go` | 作为 notifier 集成参考 | 通知不是讨论组事实源 |
| Orrery | comment access / private agent tests | 作为 message visibility 和 mention permission 测试参考 | 权限裁剪需服务 Mission Team 语义 |

## 运行流程

发消息：

```text
Member posts message
-> validate membership and visibility
-> parse mentions
-> store Message
-> create InboxItem for targeted mentions
-> emit MessagePosted / AgentMentioned event candidates
-> create MemoryCandidate if message kind is decision / blocker / lesson
```

回复 mention：

```text
Recipient opens InboxItem
-> reads original message, thread, task/evidence refs, DiscussionDigest
-> replies in same thread
-> store reply Message
-> close InboxItem as responded
-> emit AgentReplied / InboxItemClosed event candidates
```

构建 DiscussionDigest：

```text
Load recent visible messages
-> merge MissionEvent projection data: decisions, blockers, evidence, task responsibility
-> include pending mentions
-> include worker_goal_refs only as responsibility/dependency refs
-> output digest with source_message_refs and source_event_refs
```

## 测试与验收

建议测试文件：

```text
internal/mission/discussion/group_test.go
internal/mission/discussion/message_test.go
internal/mission/discussion/mention_parser_test.go
internal/mission/discussion/inbox_test.go
internal/mission/discussion/digest_test.go
internal/mission/discussion/policy_test.go
```

独立验收场景：

1. 每个 Mission 可以创建一个 DiscussionGroup。
2. MissionTeam member 可以发送 Message。
3. 非成员不能发消息。
4. Message 可以 reply，并保持 thread_id。
5. Message 可以解析 agent / role / user / all mention。
6. mention 会生成 InboxItem。
7. reply 可以关闭对应 InboxItem。
8. @all 不会默认创建强制执行任务。
9. DiscussionDigest 包含 mission goal、discussion goal、decisions、responsibility map、worker_goal_refs、blockers、evidence index、pending mentions。
10. DiscussionDigest 不包含 full internal trace、full memory、full runtime log。
11. decision / blocker / QA lesson 可以生成 MemoryCandidate，但不直接写 MemoryRecord。
12. mention/reply 不直接修改 TaskLifecycle、QAResult 或 MissionAssignment。

完成证明：

```text
go test ./internal/mission/discussion
rg "agent_task_queue|issue_id" orrery_v2/internal/mission/discussion 返回仅 migration_notes 或 legacy adapter 说明
测试证明 DiscussionDigest 可以进入 ContextPack 且不包含全量消息流
```

## CROSS_GOAL

```text
CROSS_GOAL: 03-lifecycle-signals
需要：BeforeAgentReply / AfterAgentReply signal。
当前假设：讨论组服务发消息时可调用 SignalBus。
后续约定：Agent 自动回复和真人手动回复的统一入口。
```

```text
CROSS_GOAL: 04-event-ledger-projection
需要：MessagePosted、AgentMentioned、AgentReplied、InboxItemCreated、InboxItemClosed、DecisionRecorded、BlockerRaised。
当前假设：本 goal 产生 event candidate，由 Goal 04 投影 DiscussionView / InboxView。
后续约定：message visibility 和 projection 字段。
```

```text
CROSS_GOAL: 05-resource-registry-memory
需要：MemoryCandidate 创建接口。
当前假设：discussion 只产生候选，不写长期 MemoryRecord。
后续约定：DiscussionDigest 与 MemoryDigest 的边界。
```

```text
CROSS_GOAL: 07-base-agent-runtime
需要：BaseAgent sendMessage / mention / reply / readContext 通过 DiscussionService 实现。
当前假设：BaseAgent 不直接存 message。
后续约定：自动回复策略和 human handoff 的统一接口。
```

```text
CROSS_GOAL: 08-organization-staffing
需要：MissionTeamMember / MissionAssignment / AgentInstance 成员来源。
当前假设：本 goal 只依赖 GroupMemberRef 和 membership projection。
后续约定：成员权限、退出任务团队后的消息可见性、组织上级是否能旁听。
```

```text
CROSS_GOAL: 10-api-transport
需要：DiscussionView / InboxView / PostMessage / Reply API。
当前假设：transport 只调用 app service 或 DiscussionService facade。
后续约定：分页、实时 patch、错误码。
```


