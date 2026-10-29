# Goal 99：前端 Mission Control（延期）

本 goal 定义延期实现的前端 Mission Control。前端是 Mission 项目作战室，不是流程引擎、事实源或业务状态机。它只能展示后端 app service 暴露的 DTO / patch，并把用户操作提交为 command；最终事实仍来自 MissionEvent、projection 和 app service。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 虽然后置，仍必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定) 的边界：前端不直接读取数据库、FileStore root、日志文件、config YAML 或事件原表；只消费 Goal 10 暴露的 DTO、patch、command receipt 和 artifact URL / ref。前端本地缓存不能成为事实源，也不能绕过后端 app service 写任务、讨论、人员或 QA 状态。

## 顶层设计

Mission Control 前端要服务于“Agent 公司项目管理”而不是固定流程模板。它需要让 PM、人类协作者和 Agent 成员看到：

```text
Mission final goal
Discussion group goal
Team and worker personal goals
Task board and ownership
Progress graph from actual events
Deliverables and evidence
QA state and rework
Discussion decisions and blockers
Inbox pending responses
```

前端不从 MissionEvent 原始事件自行推导业务状态，也不根据写死的阶段节点推断任务进度。它消费：

```text
HTTP snapshot: MissionControlViewDTO
WebSocket patch: MissionControlPatchEnvelope
Command response: CommandReceiptDTO
```

本 goal 延期，不阻塞 01-10 后端目标。但后端 DTO 设计必须为本前端预留清晰边界，避免后续页面被迫读取底层表或旧 timeline。

## 生产级职责边界

本 goal 负责：

```text
Mission Control shell
Mission Overview
Team Panel
Worker Goal Panel
Task Board
Progress Graph
Deliverables Panel
Evidence Panel
QA Panel
Discussion Panel
Inbox / Mention UI
Command forms and dialogs
Live patch state store
DTO fixtures and frontend contract tests
Permission-aware rendering
Responsive operational layout
```

本 goal 不负责：

```text
MissionEvent append
Projection rules
Backend app service
Runtime execution
QA business policy
Memory retrieval
Discussion digest generation
Agent internal trace parsing
Legacy timeline compatibility
Hardcoded process templates
```

禁止事项：

```text
Frontend consumes MissionEvent raw payload.
Frontend consumes projection table rows.
Frontend imports backend domain struct shape as UI state.
Frontend writes local state and treats it as server fact before patch/receipt.
Progress Graph uses fixed phase nodes instead of actual event-derived graph.
Discussion UI shows full internal trace by default.
Worker personal memory is displayed wholesale.
Page hides QA/rework/evidence behind only summary text.
Large marketing hero or landing-page shell replaces the operational workspace.
```

## 中层模块规划

推荐目录：

```text
orrery_v2/web/src/lib/mission/mission-control/
  MissionControlPage.svelte
  MissionControlShell.svelte
  api.ts
  dto.ts
  patch.ts
  store.ts
  permissions.ts
  fixtures.ts

  overview/
    MissionOverview.svelte
    MissionGoalPanel.svelte
    DecisionSummary.svelte

  team/
    TeamPanel.svelte
    TeamMemberRow.svelte
    WorkerGoalPanel.svelte
    StaffingDialog.svelte

  tasks/
    TaskBoard.svelte
    TaskCard.svelte
    TaskDetailDrawer.svelte
    AssignmentDialog.svelte

  progress/
    ProgressGraph.svelte
    ProgressNode.svelte
    ProgressEdge.svelte
    ProgressLegend.svelte

  deliverables/
    DeliverablesPanel.svelte
    DeliverableRow.svelte
    EvidencePanel.svelte
    EvidenceDetailDrawer.svelte

  qa/
    QAPanel.svelte
    QAItem.svelte
    ReworkRequestDialog.svelte

  discussion/
    DiscussionPanel.svelte
    MessageComposer.svelte
    MessageThread.svelte
    MentionInput.svelte
    DecisionMarker.svelte

  inbox/
    InboxPanel.svelte
    InboxItemRow.svelte
    InboxDetailDrawer.svelte
```

依赖方向：

```text
mission-control/api -> Goal 10 HTTP / WebSocket DTO
mission-control/store -> DTO snapshot + patch reducer
components -> store selectors and command functions
components never -> MissionEvent raw, backend SQL row, backend domain type
```

## 底层实现指导

### DTO 输入

前端只依赖 Goal 10 暴露的 DTO：

```text
MissionControlView
TeamView
TaskBoardView
ProgressGraphView
DeliverablesView
EvidenceView
QAView
DiscussionView
InboxView
MissionControlPatch
CommandReceipt
```

`MissionControlView` 至少需要：

```text
mission { id, title, final_goal, status, health, owner, created_at, updated_at }
discussion_group { id, goal, digest_summary }
team { members[], worker_goal_refs[] }
task_board { columns[], cards[] }
progress_graph { nodes[], edges[], latest_version }
deliverables { items[] }
evidence { items[] }
qa { items[], policy_summary }
discussion { messages[], decisions[], blockers[], pending_mentions[] }
inbox_summary { counts, items[] }
permissions { commands[], hidden_fields[] }
version
```

### State Store

Store 只维护 UI 可渲染状态：

```text
load snapshot
-> normalize by view section
-> apply patches by version
-> keep local UI state separately
-> submit command
-> wait for receipt / patch
-> reconcile optimistic UI if used
```

本地 UI state 包括：

```text
selected task
expanded worker
active tab
draft message
filter text
drawer open state
viewport preferences
```

本地 UI state 不能变成 Mission fact。

### View Design

Mission Control 是工作台，不是宣传页。视觉策略：

```text
信息密度高但可扫描。
顶部显示 Mission 名称、final goal、health、关键阻塞和下一步。
左侧或顶部提供 Team / Task / Progress / QA / Discussion 快速切换。
Task、QA、Evidence、Inbox 使用紧凑列表和详情抽屉。
Progress Graph 显示实际发生的节点和关系，不显示预设流程幻想。
默认展示 summary、deliverable、evidence、decision、blocker。
内部 trace、raw runtime output、full memory 只在有权限的诊断入口查看。
```

讨论组目标和 worker 个人目标注入展示：

```text
Discussion group goal 在 discussion header 和 Mission overview 中显示。
Worker personal goal 在 Team member detail / Task detail 中显示与当前 Mission 相关的版本。
其他 worker 只显示 worker_goal_refs 和协作必要摘要，不暴露完整个人记忆。
```

### Patch Reducer

Patch reducer 支持：

```text
replace_mission_summary
upsert_team_member
remove_team_member
upsert_worker_goal_ref
upsert_task_card
move_task_card
upsert_progress_node
upsert_progress_edge
upsert_deliverable
upsert_evidence
upsert_qa_item
append_discussion_message
upsert_decision
upsert_blocker
upsert_inbox_item
mark_inbox_item_closed
```

Reducer 必须检查：

```text
base_version matches current version, otherwise reload snapshot.
patch operation is known.
permission-hidden fields are not rendered.
duplicate patch_id is ignored.
```

### Commands

前端命令函数只提交 DTO：

```text
createMission
requestStaffing
assignTask
submitHumanResult
makeQADecision
requestRework
postDiscussionMessage
replyToMessage
markInboxRead
closeInboxItem
```

命令成功不直接改写事实状态；只显示 pending / submitting UI，并等待 command receipt 或 patch 更新。

## 现有实现复用计划

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `archived/OrreryApp/src/lib/features/missions/MissionsPage.svelte` | 复用 Mission 列表/详情入口和局部布局经验 | 页面主体改为 MissionControlView DTO 驱动 |
| Orrery | `archived/OrreryApp/src/lib/features/missions/missions.ts`、`missions.test.ts` | 复用 API client 和 fixture 测试模式 | 类型改为 Goal 10 DTO，不直接绑定旧后端 shape |
| Orrery | `archived/OrreryApp/src/lib/app/missionRoutes.ts`、`appRootMissionRoutes.test.ts` | 复用 route 入口 | route 命名跟随 Goal 10 |
| Orrery | `archived/OrreryApp/src/lib/features/coordination/timelineProjection.ts`、`timelineProjection.test.ts` | 作为 Progress Graph 可视化经验参考 | 不作为事实源，不再从前端自行投影 Mission 状态 |
| Orrery | `archived/OrreryApp/src/lib/features/coordination/PMTimelineModal.svelte` | 复用 timeline modal 的交互经验 | 改为读取 ProgressGraphView / EvidenceView |
| Orrery | `archived/OrreryApp/src/lib/features/chat/TimelineGraph.svelte` | 复用图可视化组件经验 | 图节点来自 ProgressGraphView，不来自 chat timeline |
| Orrery | comment / inbox / QA 后端测试 fixture | 作为前端 mock 数据语义参考 | 最终以前端 contract fixture 为准 |
| Multica | runtime session / stderr / health 展示经验 | 可用于诊断子视图 | 默认 Mission Control 不展示 raw runtime trace |

迁移说明需要记录在：

```text
orrery_v2/web/src/lib/mission/mission-control/migration_notes/orrery.md
orrery_v2/web/src/lib/mission/mission-control/migration_notes/multica.md
```

## 运行流程

页面加载：

```text
route /missions/{mission_id}/control
-> api.getMissionControlView
-> store.loadSnapshot
-> render overview/team/task/progress/qa/discussion/inbox
-> connect WebSocket stream
-> apply patches
```

任务查看：

```text
user opens task card
-> TaskDetailDrawer shows contract, assignee, worker goal ref, deliverables, evidence, QA status
-> user with permission can assign/reassign/request rework
-> command receipt marks UI pending
-> patch updates board / QA / evidence
```

讨论与 inbox：

```text
user posts message with @mention
-> postDiscussionMessage command
-> patch appends message
-> patch creates inbox item for recipient
-> recipient replies
-> patch closes inbox item and appends reply
```

进度图刷新：

```text
Projection emits patch
-> ProgressGraph reducer upserts node/edge
-> graph preserves viewport if possible
-> missing base_version triggers snapshot reload
```

## 测试与验收

建议测试文件：

```text
orrery_v2/web/src/lib/mission/mission-control/dto.test.ts
orrery_v2/web/src/lib/mission/mission-control/patch.test.ts
orrery_v2/web/src/lib/mission/mission-control/store.test.ts
orrery_v2/web/src/lib/mission/mission-control/api.test.ts
orrery_v2/web/src/lib/mission/mission-control/MissionControlPage.test.ts
orrery_v2/web/src/lib/mission/mission-control/progress/ProgressGraph.test.ts
orrery_v2/web/src/lib/mission/mission-control/discussion/DiscussionPanel.test.ts
orrery_v2/web/src/lib/mission/mission-control/inbox/InboxPanel.test.ts
```

独立验收场景：

1. 使用 mock MissionControlView 可以渲染完整页面。
2. Mission final goal 和 discussion group goal 在首屏可见。
3. Team member detail 可以展示 worker personal goal ref。
4. Task card 可以展示 assignee、status、deliverable、evidence、QA 状态。
5. Progress Graph 使用 ProgressGraphView 节点和边，不依赖固定流程节点。
6. patch 可以增量新增 Agent、Task、QA reject、Evidence、Discussion message、Inbox item。
7. base_version 不匹配时触发 snapshot reload。
8. Discussion 和 Inbox 可以显示 @ 待响应事项。
9. 默认页面不显示 full internal trace、raw runtime output、full personal memory。
10. 权限隐藏字段不会被渲染。
11. command 提交后不会直接把本地 UI state 当成 server fact。
12. 页面不依赖 MissionEvent 原始事件或 projection row。
13. 关键按钮、列表、图节点在移动端和桌面端不重叠。

完成证明：

```text
npm test -- mission-control
npm run check
rg "MissionEvent|projection row|pm_timeline" orrery_v2/web/src/lib/mission/mission-control returns only migration_notes or tests asserting absence
Playwright screenshot verifies desktop and mobile Mission Control layout after implementation
```

## CROSS_GOAL

```text
CROSS_GOAL: 04-event-ledger-projection
需要：MissionControlView、ProgressGraphView、EvidenceView、QAView、DiscussionView、InboxView、patch envelope。
当前假设：前端先用 mock DTO，最终切到 Goal 10 HTTP / WebSocket 输出。
后续约定：字段命名、状态枚举、分页、权限裁剪、增量 patch 格式。
```

```text
CROSS_GOAL: 08-organization-staffing
需要：TeamView、MissionTeamMember、MissionAssignment、worker_goal_refs。
当前假设：前端只展示 Mission 内临时编组，不编辑稳定组织身份。
后续约定：staffing dialog 的 command DTO 和权限。
```

```text
CROSS_GOAL: 09-discussion-inbox
需要：InboxView、DiscussionView、DiscussionDigest、pending mentions。
当前假设：前端只展示压缩消息、决策、阻塞和待响应项。
后续约定：未读、已读、已响应、过期、关闭状态。
```

```text
CROSS_GOAL: 10-api-transport
需要：HTTP snapshot、WebSocket patch、ErrorResponse、CommandReceipt。
当前假设：Goal 10 先稳定后端出口，前端以 contract fixture 开发。
后续约定：WebSocket 重连、snapshot reload、patch ack、错误重试策略。
```
