# Goal 03：Agent 生命周期信号系统

本 goal 定义 BaseAgent 执行过程中的生命周期信号、hook 和 middleware。它不是业务事实源，不保存 MissionEvent，也不执行 RuntimeAdapter；它负责把 Agent run 的关键时机稳定下来，让上下文构建、权限检查、审计、summary 生成、证据校验、通知和事件输出能以插件化方式接入。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)。SignalBus、hook 和 middleware 可以消费 platform logger、clock、config snapshot、metrics 和 testkit，但不得自建事件存储、队列、锁或运行时资源。热加载配置通过 `ConfigManager` 当前内存快照读取；订阅只用于调整可热更新的 handler 参数或日志级别，不作为业务状态来源。

## 顶层设计

Agent 公司模型要求 PM、HR、Planner、Worker、QA、RealHumanAgent 都走同一套 BaseAgent 协议。不同 Agent 的差异来自 profile、tool、skill、memory、runtime、policy 和局部小流程，而不是每个 Agent 自己写一套执行生命周期。

生命周期信号系统解决三个问题：

```text
1. BaseAgent 不膨胀成巨型流程函数。
2. 横切能力有稳定插入点，例如权限、日志、审计、summary、evidence、QA 前置校验。
3. Signal 与 MissionEvent 分离，避免把运行时内部噪声全部变成业务事实。
```

Signal 是运行时通知。Event 是业务事实。Signal 可以触发 Event，但不能默认持久化为 Event。

## 生产级职责边界

本 goal 负责：

```text
SignalName
SignalEnvelope
SignalPayload
AgentRunState
SignalBus
SignalHandler
HookRegistry
PreHook / PostHook
AgentMiddleware
PipelineExecutor
FailurePolicy
SignalRecorder test fake
```

本 goal 不负责：

```text
Mission / Agent / TaskContract 契约定义
BaseAgent 具体执行 pipeline
RuntimeAdapter 执行
Tool / Skill / Memory 绑定
TaskLifecycle 状态机
QA 验收
MissionEvent 持久化
Projection / DTO
DiscussionGroup 消息存储
HTTP / WebSocket
```

禁止事项：

```text
SignalBus 直接写 eventstore。
Signal handler 直接调用 runtime provider。
Signal payload 携带全量 memory、全量 runtime trace 或完整 secret。
把 Signal 当成前端 timeline 事实源。
handler 失败策略不明确，导致同一 run 有时继续有时停止。
```

## 中层模块规划

代码归集：

```text
internal/agent/lifecycle/
  names.go
  signal.go
  payload.go
  state.go
  bus.go
  handler.go
  hook.go
  middleware.go
  pipeline.go
  failure_policy.go
  recorder.go
  testkit.go
  errors.go
```

文件职责：

```text
names.go：所有 SignalName 常量和阶段分组。
signal.go：Signal envelope、metadata、correlation id、causation id。
payload.go：常见 payload 类型，禁止 any 到处扩散。
state.go：AgentRunState，引用 Goal 02 的 MissionID / AgentID / TaskID / ContextPack / AgentResult。
bus.go：SignalBus 接口、EmitReport 与 in-memory 实现。
handler.go：SignalHandler、HandlerFunc、HandlerRegistration。
hook.go：PreHook / PostHook 抽象。
middleware.go：AgentMiddleware、AgentStep。
pipeline.go：按固定阶段执行 step + signal 的编排器。
failure_policy.go：Stop / Continue / Degrade / Retry 的策略。
recorder.go：测试和审计用 signal recorder。
testkit.go：NewTestSignalBus、NewRecordingHandler、NewFailingHandler。
errors.go：handler_failed、pipeline_stopped、invalid_signal、missing_correlation_id。
```

依赖方向：

```text
lifecycle -> agent/core 契约类型
lifecycle -> platform/logging
lifecycle 不依赖 mission/event、runtime、resource、memory/store、transport
```

## 数据与信号设计

### SignalEnvelope

```text
SignalEnvelope {
  signal_id
  name
  mission_id
  agent_id
  task_id
  run_id
  correlation_id
  causation_id
  attempt
  emitted_at
  payload
  metadata
}
```

要求：

```text
correlation_id 必须存在，用于串起一次 Agent run。
run_id 必须存在，用于区分同一 Agent 同一 Task 的多次尝试。
payload 必须是小对象或引用，不能是全量 runtime trace。
metadata 只能放可审计的小字段，不放 secret。
```

### AgentRunState

```text
AgentRunState {
  mission_id
  agent_id
  task_id
  run_id
  attempt
  task_contract
  context_pack
  runtime_request_ref
  runtime_response_ref
  agent_result
  errors[]
  warnings[]
  metadata
  started_at
  deadline_at
}
```

`AgentRunState` 是 pipeline 内部状态。它可以被 hook 补充 metadata、warnings、normalized result，但不应被任意 handler 改写核心身份字段。

### SignalName

最小信号集：

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

阶段分组：

```text
task intake:
  BeforeTaskReceived -> AfterTaskReceived

acceptance:
  BeforeTaskAccepted -> AfterTaskAccepted

context:
  BeforeContextBuild -> AfterContextBuild

runtime:
  BeforeRuntimeStart -> RuntimeStarted -> RuntimeProgress -> BeforeRuntimeFinish -> AfterRuntimeFinish

result:
  BeforeResultNormalize -> AfterResultNormalize
  BeforeResultValidate -> AfterResultValidate
  BeforeResultCommit -> AfterResultCommit

communication:
  BeforeAgentReply -> AfterAgentReply

exception:
  AgentBlocked / AgentFailed / AgentCancelled / AgentTimeout
```

## 底层实现指导

### SignalBus

接口建议：

```go
type SignalBus interface {
    Emit(ctx context.Context, signal SignalEnvelope) (EmitReport, error)
    Subscribe(name SignalName, handler SignalHandler, opts HandlerOptions) (SubscriptionID, error)
    Unsubscribe(id SubscriptionID) error
}

type EmitReport struct {
    SoftFailures []HandlerFailure
}
```

行为要求：

```text
同一个 signal 的 handler 默认按注册顺序执行。
Subscribe 必须校验 signal name、handler 非空、handler name 非空，并在注册失败时返回 error。
handler 可以声明 FailurePolicy。
Emit 必须写 debug 日志：signal name、run_id、correlation_id、handler count。
Continue / Degrade 的 handler 失败属于 soft failure，必须进入 EmitReport.SoftFailures，供 PipelineExecutor 记录 warnings / degraded_capabilities。
Emit 仅在 invalid signal、Stop、Retry 后 Stop、context cancellation 等硬失败时返回 error。
Emit 返回 error 时必须包含失败 handler、signal name、failure policy。
```

### FailurePolicy

建议策略：

```text
Stop：handler 失败时停止 pipeline，并触发 AgentFailed。
Continue：记录 warn，继续执行后续 handler，并将 soft failure 写入 EmitReport。
Degrade：记录 warn，继续执行后续 handler，并将 degraded capability 写入 EmitReport。
Retry：按小次数重试 handler，仍失败后按 Then 策略处理；Then=Stop 返回硬错误，Then=Degrade / Continue 写入 EmitReport。
```

默认策略：

```text
权限、契约、证据、结果提交前校验 -> Stop
日志、metrics、非关键审计 -> Continue
memory digest 预热、成本估算 -> Degrade
通知投递 -> Retry then Degrade
```

### PipelineExecutor

PipelineExecutor 不执行具体 runtime，只负责提供执行模板：

```text
emit BeforeX
run step X
emit AfterX
if step error -> classify -> emit AgentFailed / AgentBlocked / AgentCancelled / AgentTimeout
```

Goal 07 的 BaseAgent 可以组合这个 executor：

```text
ReceiveTask
AcceptTask
BuildContext
RunRuntime
NormalizeResult
ValidateResult
CommitResult
ReplyOrNotify
```

### Signal -> Event 映射边界

本 goal 只定义映射建议，不直接写 eventstore：

```text
AfterTaskAccepted -> TaskAccepted candidate
AfterResultCommit -> DeliverableSubmitted / EvidenceSubmitted candidate
AgentBlocked -> AgentBlocked candidate, only when blocker affects Mission coordination
AgentFailed -> AgentFailed candidate, only when failure affects Task / Mission status
AfterAgentReply -> AgentReplied candidate
```

实际 MissionEvent 类型和写入由 Goal 04 / 07 决定。

## 现有实现复用计划

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `internal/service/managed_runtime*` 中的 worker 执行阶段经验 | 提取阶段划分、错误分类、runtime 输出处理经验 | 不继承旧 `agent_task_queue` 为生命周期模型 |
| Orrery | `internal/handler/comment_02_enqueue_mentioned_agent_tasks.go`、comment trigger 流程 | 作为 `BeforeAgentReply / AfterAgentReply`、mention 响应 signal 的参考 | 不让旧 comment 成为事实源 |
| Orrery | `cmd/server/runtime_sweeper*`、runtime liveness tests | 作为 timeout / stale / failed signal 的触发经验 | stale lease 属于 platform / runtime，Signal 只表达通知 |
| Orrery | QA gate / permission review 测试 | 映射为 `BeforeResultCommit`、`BeforeTaskAccepted` handler 的测试参考 | QA 执行仍归 Goal 06 |
| Multica | runtime daemon lifecycle、heartbeat、watchdog 机制 | 作为 AgentTimeout / AgentFailed / RuntimeProgress 的设计参考 | 不引入 issue/comment/autopilot 字段 |

复用原则：复用执行经验、测试用例和错误分类；不复用旧业务对象作为 Signal 模型。

## 运行流程

正常路径：

```text
BaseAgent receives TaskContract
-> emit BeforeTaskReceived / AfterTaskReceived
-> emit BeforeTaskAccepted
-> run acceptance checks
-> emit AfterTaskAccepted
-> emit BeforeContextBuild
-> build ContextPack
-> emit AfterContextBuild
-> emit BeforeRuntimeStart / RuntimeStarted
-> runtime reports RuntimeProgress
-> emit BeforeRuntimeFinish / AfterRuntimeFinish
-> emit BeforeResultNormalize / AfterResultNormalize
-> emit BeforeResultValidate / AfterResultValidate
-> emit BeforeResultCommit
-> commit AgentResult through delivery service
-> emit AfterResultCommit
-> optional BeforeAgentReply / AfterAgentReply
```

失败路径：

```text
handler returns error
-> apply FailurePolicy
-> Continue / Degrade: append soft failure to EmitReport
-> PipelineExecutor records warnings / degraded_capabilities and continues
-> Stop: append error to AgentRunState
-> emit AgentFailed with cause
-> BaseAgent returns classified error
```

超时路径：

```text
context deadline exceeded or watchdog timeout
-> emit AgentTimeout
-> BaseAgent cancels runtime through Goal 07 runtime adapter
-> later Goal 04 may persist AgentTimedOut / TaskBlocked if it affects Mission status
```

## 测试与验收

建议测试文件：

```text
internal/agent/lifecycle/bus_test.go
internal/agent/lifecycle/pipeline_test.go
internal/agent/lifecycle/failure_policy_test.go
internal/agent/lifecycle/state_test.go
internal/agent/lifecycle/testkit_test.go
```

独立验收场景：

1. 注册 `BeforeTaskAccepted` handler 后，Agent 接受任务前会触发该 handler。
2. 前处理返回 Stop 策略错误时，执行管线停止，并触发 `AgentFailed`。
3. Continue 策略 handler 失败时，pipeline 继续并记录 warn。
4. `AfterRuntimeFinish` 可以读取 runtime response ref 并追加 metadata。
5. `BeforeResultCommit` 可以拒绝缺少 evidence 的结果。
6. 同一个 signal 可以注册多个 handler，并按注册顺序执行。
7. signal 必须包含 correlation id；缺失时 `Emit` 返回 validation error。
8. payload 不允许携带 full memory / full trace 标记字段，测试应覆盖拒绝策略。
9. `NewTestSignalBus` 和 recorder 能记录 signal 顺序，供 Goal 07 测试使用。

完成证明：

```text
go test ./internal/agent/lifecycle
rg "internal/mission/event|internal/agent/runtime|internal/transport" orrery_v2/internal/agent/lifecycle 无结果
生命周期测试能断言完整 signal 顺序
```

## CROSS_GOAL

```text
CROSS_GOAL: 02-core-contracts
需要：MissionID、AgentID、TaskID、ContextPack、AgentResult、ValidationError。
当前假设：lifecycle 只引用契约类型，不重复定义。
后续约定：AgentRunState 最终是否归 lifecycle 或 agent/core，由 Goal 07 实现时确认。
```

```text
CROSS_GOAL: 04-event-ledger-projection
需要：Signal -> MissionEvent 映射规则。
当前假设：SignalBus 不直接写 eventstore，只产生可被 BaseAgent / app service 消费的 signal。
后续约定：哪些 AgentFailed / AgentBlocked / AgentTimeout 必须升级为 MissionEvent。
```

```text
CROSS_GOAL: 06-task-delivery-qa
需要：BeforeResultCommit / BeforeResultValidate 可调用 ResultPolicyChecker。
当前假设：lifecycle 不知道 QA 规则，只承载 hook。
后续约定：QA hook 的 failure policy 默认为 Stop。
```

```text
CROSS_GOAL: 07-base-agent-runtime
需要：BaseAgent 在每个生命周期时机调用 SignalBus，并使用 PipelineExecutor。
当前假设：runtime 只依赖 lifecycle interface。
后续约定：RuntimeProgress payload、timeout 分类、cancel 行为。
```

```text
CROSS_GOAL: 09-discussion-inbox
需要：BeforeAgentReply / AfterAgentReply 支持 Agent 自动回复和真人手动回复统一入口。
当前假设：discussion 服务可以调用 SignalBus，但不通过 signal 直接修改结构化状态。
后续约定：mention response 与 InboxItem closing 的事件边界。
```
