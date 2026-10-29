# Goal 06：任务交付、证据与 QA 契约

本 goal 建立 Worker 独立责任单元的交付和验收闭环。它把 Goal 02 的 TaskContract / AgentResult / Deliverable / Evidence 变成可执行的任务生命周期，并把 QA 建模为 AgentProfile 可装配能力，而不是独立顶层业务中心。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)。任务生命周期、交付、证据和 QA 语义属于本 goal；持久化、queue / outbox、claim、lock、FileStore、日志、config、health、metrics 和 testkit 由 platform 提供。Deliverable / Evidence 只能保存 artifact ref 和审计元数据，不把本地绝对路径、sidecar 文件或完整 secret / prompt payload 作为业务契约。

## 顶层设计

Agent 公司模型的核心价值不是“跑出一个结果”，而是每个 Worker 的责任、成果、证据和验收都可追踪、可返工、可审计。总体验收不能替代 Worker 独立验收；summary 不能替代 evidence；QA rejected 必须能回到具体 Task 和负责人。

本 goal 解决：

```text
1. TaskContract 如何进入 Assigned / Accepted / InProgress / Submitted / InReview / Approved / Rejected。
2. AgentResult 如何拆成 Summary、Deliverable、Evidence 并绑定任务。
3. EvidenceRequirement 如何阻止无证据的虚假通过。
4. QAContract / QAPolicy 如何作为 Agent 组件执行验收。
5. ReworkRequest 如何回到原 Task，形成闭环。
```

## 生产级职责边界

本 goal 负责：

```text
TaskLifecycle
TaskAssignment
TaskStatus
TaskSubmission
DeliverableSubmission
EvidenceSubmission
ResultPolicyChecker
EvidencePolicy
QAContract
QAPolicy
QACheck
QAResult
AcceptanceReport
ReworkRequest
TaskDeliveryService
TaskQAService
```

本 goal 不负责：

```text
Agent runtime 执行
SignalBus 实现
AgentAssembler 装配
MissionTeam 招募
DiscussionGroup message
MissionEvent 物理存储
前端图
HTTP handler
独立顶层 QA package
```

禁止事项：

```text
新增 internal/agent/capabilities/qa。
把 QA 做成和 Agent / Mission 平级的顶层业务模块。
用总体 Mission QA 替代 Worker task QA。
QA 基于 summary 直接通过，不检查 Deliverable / Evidence。
QA rejected 不生成 ReworkRequest。
任务状态用 nullable 大字段随意表示。
BaseAgent 绕过 TaskDeliveryService 直接写任务结果。
```

## 中层模块规划

代码归集：

```text
internal/mission/task/
  lifecycle.go
  status.go
  assignment.go
  submission.go
  delivery_service.go
  qa_service.go
  rework.go
  repository.go
  events.go
  validation.go
  errors.go

internal/agent/component/delivery/
  contract.go
  collector.go
  validator.go
  mapper.go
  errors.go

internal/agent/component/evidence/
  contract.go
  requirement.go
  validator.go
  artifact_binding.go
  errors.go

internal/agent/component/qa/
  contract.go
  policy.go
  checker.go
  result.go
  errors.go
  testkit.go
```

依赖方向：

```text
mission/task -> agent/core contracts, platform uow/event sink interfaces
agent/component/delivery -> agent/core contracts
agent/component/evidence -> agent/core contracts, platform filestore refs
agent/component/qa -> agent/core contracts, mission/task read interfaces
mission/task 不依赖 runtime provider、transport、frontend
```

## 数据与状态设计

### TaskStatus

状态建议：

```text
Draft
Assigned
Accepted
InProgress
Blocked
Submitted
InReview
Approved
Rejected
ReworkRequested
Cancelled
```

合法转移：

```text
Draft -> Assigned
Assigned -> Accepted / Cancelled
Accepted -> InProgress / Blocked / Cancelled
InProgress -> Submitted / Blocked / Cancelled
Blocked -> InProgress / ReworkRequested / Cancelled
Submitted -> InReview
InReview -> Approved / Rejected
Rejected -> ReworkRequested
ReworkRequested -> Assigned / Accepted / Cancelled
Approved -> terminal
Cancelled -> terminal
```

### TaskAssignment

```text
TaskAssignment {
  task_id
  mission_id
  assignee_agent_id
  mission_assignment_ref
  assigned_by
  assigned_at
  status
}
```

规则：

```text
assignee 必须是 AgentInstance / MissionAssignment。
裸 Tool / RuntimeAdapter / CLI 不能成为 assignee。
任务个人目标来自 TaskContract.worker_personal_goal，不由 Worker 自行发明。
```

### TaskSubmission

```text
TaskSubmission {
  submission_id
  task_id
  agent_id
  run_id
  agent_result_ref
  summary
  deliverable_refs[]
  evidence_refs[]
  submitted_at
  idempotency_key
}
```

规则：

```text
同一 run_id / idempotency_key 重复提交不得产生重复 submission。
缺少 summary 的 AgentResult 不能提交。
缺少 expected deliverable 的提交不能进入 InReview。
缺少 required evidence 的提交不能 Approved。
```

### QAContract / QAPolicy

```text
QAContract {
  qa_contract_id
  acceptance_criteria[]
  evidence_requirements[]
  pass_policy
  rejection_policy
  max_rework_attempts
  required_reviewer_ref
}

QAPolicy {
  check_mode
  evidence_strictness
  allow_human_override
  require_reason_on_reject
  require_acceptance_report
}
```

QA 是 Agent 组件：

```text
QACheckAgent = AgentProfile + QAContract + QA component + RuntimeAdapter or HumanRuntimeAdapter
```

Mission task 记录 QA 结果，但不拥有 QA 引擎。

### QAResult

```text
QAResult {
  qa_result_id
  task_id
  checked_by
  status
  checked_criteria[]
  evidence_results[]
  missing_evidence[]
  rejection_reasons[]
  acceptance_report_ref
  rework_request_ref
  checked_at
}
```

状态：

```text
approved
rejected
needs_rework
blocked
```

### ReworkRequest

```text
ReworkRequest {
  rework_request_id
  original_task_id
  target_assignee
  reason
  failed_criteria[]
  missing_evidence[]
  requested_by
  created_at
  status
}
```

## 底层实现指导

### TaskDeliveryService

接口建议：

```go
type TaskDeliveryService interface {
    AcceptTask(ctx context.Context, taskID TaskID, agent AgentInstanceRef) error
    StartTask(ctx context.Context, taskID TaskID, agent AgentInstanceRef) error
    SubmitAgentResult(ctx context.Context, taskID TaskID, result AgentResult, opts SubmitOptions) (TaskSubmission, error)
}
```

要求：

```text
所有状态变化在 UnitOfWork 内完成。
提交结果时写 TaskSubmission、DeliverableSubmission、EvidenceSubmission。
提交成功后产生 AgentResultSubmitted / DeliverableSubmitted / EvidenceSubmitted event candidate。
重复提交使用 idempotency_key 去重。
```

### ResultPolicyChecker

用于 Goal 03 的 `BeforeResultCommit` hook 和 Goal 07 BaseAgent 提交前校验：

```text
CheckSummaryRequired
CheckExpectedDeliverables
CheckRequiredEvidence
CheckEvidenceTraceability
CheckTaskAssignee
CheckReworkLimit
```

### TaskQAService

接口建议：

```go
type TaskQAService interface {
    RequestQA(ctx context.Context, taskID TaskID, requestedBy AgentRef) error
    SubmitQAResult(ctx context.Context, taskID TaskID, result QAResult) error
}
```

要求：

```text
QA 只能读取 Deliverable / Evidence / AcceptanceCriterion。
QA approve 生成 AcceptanceReport 并把 task 置为 Approved。
QA reject 生成 ReworkRequest 并把 task 置为 ReworkRequested。
QA 结果必须产生 QAApproved / QARejected / ReworkRequested event candidate。
```

### 并发与幂等

必须处理：

```text
两个 Worker 对同一 task 重复提交。
QA 对旧 submission 验收。
ReworkRequest 已存在时重复 reject。
任务已 Approved 后继续提交。
任务已 Cancelled 后继续提交。
```

建议使用：

```text
task version
submission idempotency_key
qa result idempotency_key
state transition guard
```

## 现有实现复用计划

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `workerconfig.Config` 的 responsibilities、deliverables、QAConfig | 映射到 TaskContract、ExpectedDeliverable、QAContract seed | 不把 worker_config 当新生命周期状态 |
| Orrery | `qa_contract`、`qa_plan`、`qa_check`、`qa_review` | 抽象 QAContract / QAResult / AcceptanceReport | QA 执行变为 Agent 组件 |
| Orrery | `mission_evidence`、`mission_validation_result`、review packet | 映射 EvidenceSubmission / AcceptanceReport | 不让 review packet 成为事实源 |
| Orrery | `agent_task_queue.result`、task messages | 作为 RuntimeResult -> AgentResult normalizer 的输入参考 | 旧 result 不直接作为 AgentResult |
| Orrery | PM task breakdown / QA plan prompts | 作为 Planner / QA Agent skill seed | Prompt 不定义领域状态 |
| Orrery | permission review BDD | 作为 QA human override / approval policy 参考 | 复杂权限留给 policy，不塞进 QA 状态机 |

## 运行流程

任务执行提交：

```text
TaskAssigned event exists
-> Worker accepts task
-> TaskDeliveryService.AcceptTask: Assigned -> Accepted
-> Worker starts work
-> Accepted -> InProgress
-> BaseAgent produces AgentResult
-> BeforeResultCommit calls ResultPolicyChecker
-> SubmitAgentResult writes submission / deliverables / evidence
-> InProgress -> Submitted -> InReview
-> emit event candidates
```

QA 通过：

```text
QARequested
-> QACheckAgent reads TaskSubmission, Deliverables, Evidence, AcceptanceCriteria
-> SubmitQAResult approved
-> create AcceptanceReport
-> InReview -> Approved
-> emit QAApproved / AcceptanceReportCreated
```

QA 驳回：

```text
QARequested
-> QACheckAgent finds failed criteria or missing evidence
-> SubmitQAResult rejected
-> create ReworkRequest
-> InReview -> ReworkRequested
-> emit QARejected / ReworkRequested
-> Coordinator or Planner routes rework to assignee
```

## 测试与验收

建议测试文件：

```text
internal/mission/task/lifecycle_test.go
internal/mission/task/delivery_service_test.go
internal/mission/task/qa_service_test.go
internal/mission/task/rework_test.go
internal/agent/component/delivery/validator_test.go
internal/agent/component/evidence/validator_test.go
internal/agent/component/qa/checker_test.go
```

独立验收场景：

1. Task 从 Assigned 进入 Accepted。
2. Accepted 任务可以进入 InProgress。
3. Worker 提交 AgentResult 后生成 TaskSubmission、DeliverableSubmission、EvidenceSubmission。
4. 缺少 summary 的 AgentResult 被拒绝。
5. 缺少 expected deliverable 的提交不能进入 InReview。
6. 缺少 required evidence 时无法 Approved。
7. 重复提交同一 idempotency_key 不产生重复 submission。
8. QA approve 后生成 AcceptanceReport，并把 task 置为 Approved。
9. QA reject 后生成 ReworkRequest，并把 task 置为 ReworkRequested。
10. ReworkRequest 可以关联原 Task 和失败 criteria。
11. 已 Approved / Cancelled 的 task 拒绝继续提交。
12. QACheckAgent 被建模为 AgentInstance，QA checker / policy 来自 AgentProfile 的 QAContract。

完成证明：

```text
go test ./internal/mission/task ./internal/agent/component/delivery ./internal/agent/component/evidence ./internal/agent/component/qa
rg "internal/agent/capabilities|internal/qa" orrery_v2/internal 无新增顶层 QA package
测试证明每个 Worker Task 可独立验收和返工
```

## CROSS_GOAL

```text
CROSS_GOAL: 02-core-contracts
需要：TaskContract、AcceptanceCriterion、EvidenceRequirement、AgentResult、Deliverable、Evidence。
当前假设：本 goal 扩展生命周期，不改变静态契约字段语义。
后续约定：TaskStatus 是否进入 core contract 或仅在 mission/task。
```

```text
CROSS_GOAL: 03-lifecycle-signals
需要：BeforeResultCommit / BeforeResultValidate hook 可调用 ResultPolicyChecker。
当前假设：本 goal 提供 checker，lifecycle 只执行 hook。
后续约定：缺 evidence 是 validation error 还是 blocked error。
```

```text
CROSS_GOAL: 04-event-ledger-projection
需要：TaskSubmitted、DeliverableSubmitted、EvidenceSubmitted、QAApproved、QARejected、ReworkRequested、AcceptanceReportCreated。
当前假设：本 goal 产生 event candidate，由 event sink 持久化。
后续约定：事务内 event append 和 outbox enqueue 的接口。
```

```text
CROSS_GOAL: 07-base-agent-runtime
需要：BaseAgent 提交 AgentResult 后进入 TaskDeliveryService。
当前假设：BaseAgent 不直接写 task state。
后续约定：SubmitAgentResult 的重试、幂等和 runtime failure 分类。
```

```text
CROSS_GOAL: 08-organization-staffing
需要：assignee 必须是 MissionAssignment / AgentInstance。
当前假设：本 goal 只校验引用形态，成员可用性由 staffing 提供。
后续约定：成员离组后未完成 task 的处理策略。
```

```text
CROSS_GOAL: 09-discussion-inbox
需要：QA reject、ReworkRequest、Blocker 可以产生讨论组消息或 mention。
当前假设：本 goal 不写 DiscussionGroup，只产生事件或调用通知接口。
后续约定：哪些 QA 结果自动 @PM / @Worker / @Coordinator。
```


