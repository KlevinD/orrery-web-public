# Orrery Server Runtime Bridge 设计与实施方案

## 文档状态

- 状态：Accepted，服务端 Runtime Bridge 唯一实施依据
- 日期：2026-07-25
- 适用范围：`orrery_v2` Go 服务端 Runtime 会话、Provider 接入和 Device Runtime 接入
- 客户端方案：[Orrery Desktop](../../../orrery-desktop/docs/orrery-desktop-client.md)
- 长期目标态：[Runtime Capability and Lifecycle Control Plane](../superpowers/specs/2026-07-16-runtime-capability-and-lifecycle-control-plane-design.md)

本文件同时包含架构、AionUI/AionCore 对照、Provider 验收矩阵和实施顺序。Bridge 不再维护另一份平行设计、计划、兼容矩阵或删除清单；后续行为差异和开发进度直接更新本文件。

2026-10-17 当前状态补充：生产源码 `a321b308` 已在依赖齐备时注册 managed 自研 AgentCore，Registry 也已有 OpenClaw adapter 注册路径。下文 Phase 7 的 unavailable 列表是该阶段历史状态，不能用它否定后续实现；注册不证明所有 provider 均配置可用或通过完整 parity。当前循环、工具策略、恢复限制与具体差距见 [当前 AgentCore 与 Runtime 基线](../design-baseline/02-agentcore-runtime.md)。本文的 Session 契约与适用的上游行为依据继续有效。

## 最终决策

Runtime Bridge 不再按 Orrery 当前的简化 `Run -> Progress/Done` 模型独立设计，也不重新发明一套与成熟 Agent 客户端无关的 Session 抽象。服务端以 AionUI 配套 AionCore 的 `aionui-session` 为行为参考实现，在 Go 中一比一还原其 Runtime 架构和可观察语义：

- 两级边界 `BackendConnection -> SessionBackend`；
- typed `Command` 向下、ordered `SessionEnvelope/SessionEvent` 向上；
- `ConversationSession` 的消息 FIFO、admission 和 prompt-accepted 语义；
- `Orchestrator` 的单一有序 fold lane；
- 唯一纯 reducer `step(state, event)`；
- per-turn generation/epoch、late-result fence、cancel/crash 区分和 terminal reconciliation；
- capability、model/mode/config discovery、permission pending registry、snapshot/rehydrate、lag recovery 和 idle suspend/resume；
- Codex 与 Claude Code 的完整 Provider-native 映射。

这里的“一比一”是行为和架构等价，不是逐行翻译 Rust，也不是把 Rust 类型、Tokio 或 AionCore HTTP 服务带进 Go 服务端。除明确登记的 Orrery 集成差异外，同一输入序列在 AionCore reference 和 Go Bridge 中必须得到等价的 command receipt、事件顺序、状态、能力、pending queue 和终态。

首批原生 Provider 固定为：

1. Codex app-server；
2. Claude Code persistent CLI/control protocol。

Codex 和 Claude Code 都通过 parity/conformance Gate 前，不启动 PI、ACP、Gemini、Cursor、OpenClaw 或 Device Provider 的原生迁移。现有 Provider 可暂时通过单向 legacy compatibility adapter 继续工作，但不能反向影响新 contract 的语义。

Phase 7 阶段记录：Codex、Claude、Device Gate 已通过，随后完成 PI `0.79.9` 原生 RPC 迁移；legacy compatibility、旧 parser 和 Bridge feature flag 已删除。之后完成通用 ACP stdio Provider。该阶段 Gemini、Cursor、OpenClaw、OpenCode、Hermes、Human、HTTP、Browser 和 `managed` 标为 unavailable。后续 `a321b308` 的 OpenClaw 与 managed 注册变化以上述当前状态补充及实际 Registry 为准，不存在用旧 fallback 静默代替未支持 provider 的授权。

服务端实现语言仍为 Go：

- 不引入 Rust crate、FFI、Rust sidecar 或 Cargo 构建链；
- 不把 AionCore 作为生产依赖或服务端进程；
- Codex/Claude 的进程、JSON-RPC/NDJSON、并发和生命周期由 Go adapter 实现；
- 不新增全局 gRPC 层；跨语言 sidecar 只有在官方能力确实无法由 CLI/native protocol 获得时才单独评审。

## 上游参考与版本固定

### 当前固定基线

| 项目            | 固定值                                     | 用途                                                           |
| --------------- | ------------------------------------------ | -------------------------------------------------------------- |
| AionCore        | tag `v0.1.52`                              | Runtime 行为参考                                               |
| AionCore commit | `76f5554286ba0b6d33fb74d5c2bb2b3b0b83100d` | 防止 tag 漂移                                                  |
| 核心 crate      | `crates/aionui-session`                    | Session contract、reducer、orchestrator、Codex/Claude adapters |
| 本地只读参考    | `orrery_v2/.cache/upstream/aioncore`       | 开发时逐文件对照和移植测试                                     |

`.cache/upstream/aioncore` 已被 `.gitignore` 排除。它是可随时重新拉取的上游参考 checkout，不进入构建、发布镜像、Go module、SBOM 或生产运行时。禁止从该目录做运行时动态加载，也禁止复制源码后丢失上游文件、commit 和测试用例的对应关系。

核对或刷新基线：

```powershell
git -C .cache/upstream/aioncore fetch --tags origin
git -C .cache/upstream/aioncore checkout --detach v0.1.52
git -C .cache/upstream/aioncore rev-parse HEAD
git -C .cache/upstream/aioncore describe --tags --exact-match
```

升级 reference 前必须先在本文件更新 commit、变更摘要、parity matrix 和受影响 fixture。不能让本地 checkout 的 `main` 静默成为新规范。

### 权威源码顺序

发生语义冲突时按以下顺序判断：

1. 固定 commit 的 executable tests 与 captured fixtures；
2. `aionui-session` 的公开类型、reducer 和 provider adapter 实现；
3. 同 commit 内注释和设计说明；
4. Orrery 当前 Runtime 行为。

Orrery 当前实现只是迁移输入，不是新 Bridge 的语义来源。若 AionCore 注释与测试冲突，以测试和实际 wire fixture 为准，并把结论写入本文件的差异登记。

## 改造前实现判断（Phase 0 基线）

本节记录方案接受时的迁移输入，不描述 Phase 7 完成后的当前生产结构；最终实现状态以各 Phase review、Provider matrix 和完成标准验收为准。

`internal/agent/runtime/core` 目前围绕 `RuntimeAdapter.Run/Cancel`、`RuntimeBackend.Execute`、`RuntimeSession.Messages/Result` 工作。它适合一次性任务，但不足以承载成熟交互式 Runtime：

- `accepted`、prompt 已送达和 turn 已完成没有独立语义；
- `Progress/Done/Errors` 多 channel 容易造成跨 channel 顺序和 terminal race；
- 默认 capability 会乐观声明 streaming/cancel/resume；
- multi-turn、steer、config、permission/auth、checkpoint、discovery 和 reconnect 没有统一 contract；
- Provider parser、状态推断和业务结果组装混在不同层；
- channel EOF、进程退出、cancel 和 Provider error 可能被不同路径解释；
- 没有统一 generation fence，迟到结果可能污染下一 turn；
- 没有与 UI/reload 对应的 pending permission 和 snapshot recovery。

因此不在现有 `RuntimeSession{Messages, Result}` 上继续增加布尔字段。新 contract 采用 strangler 迁移：新 Session 体系是唯一新事实源，旧接口只能从新事件单向投影，待消费者迁完后删除。

## 目标与非目标

### 目标

1. 在 Go 中复现 AionCore `aionui-session` 的边界、命令、事件、状态、并发与恢复语义。
2. Codex 和 Claude Code 成为首批完整原生实现，不再只是一次性 CLI parser。
3. Provider adapter 只处理 native wire、进程和映射，不能直接修改会话 FSM。
4. Application、Discussion、Worker 和 Device Adapter 只消费 canonical contract，不按 Provider kind 分支。
5. 用移植的 upstream tests、fixtures 和 differential tests 证明 parity，而不是凭接口名称判断“差不多”。
6. 保持 Go 服务端技术栈，不增加 Rust 的构建、供应链和运行复杂度。

### 非目标

- 不在服务端运行或改造 AionCore。
- 不把 AionUI renderer、SQLite、HTTP API 或桌面功能迁进服务端。
- 不要求所有 Provider 使用相同进程协议或 gRPC。
- 不把 provider raw JSON、任意 method 名或 `map[string]any` 变成公共业务协议。
- 不承诺未通过 parity matrix 的 Provider 已原生支持 Session contract。
- 不让 Device Runtime 接受 shell、path、argv、env 或任意本地方法。
- 不用 Bridge 代替 workspace containment、credential broker、tenant authz 或本地 OS sandbox。

## 参考架构

```text
Application / Agent / Discussion / Worker
                       |
                       v
             ConversationSession
       pending FIFO + admission + snapshot
                       |
                       v
                  Orchestrator
       lower commands + merge ordered events
                       |
             step(state, event)
             the only FSM writer
                       |
                       v
              SessionBackend actor
       dispatch(Command) / Events() / Caps
                       |
                       v
              BackendConnection factory
        one connection, many isolated sessions
                       |
       +---------------+----------------+
       v                                v
 Codex app-server               Claude Code CLI
 JSON-RPC adapter               persistent NDJSON/control
```

向下只传 canonical `Command`，向上只传 `SessionEnvelope`。Provider adapter 不得到 reducer/state 的可变引用；Orchestrator 也不读取 Provider 私有 struct。业务结果、UI projection、recording 和旧 `RuntimeResponse` 都从同一 ordered event stream 派生。

### 两级 Runtime 边界

Go 公共 port 放在 `internal/agent/core`，保持应用层依赖方向：

```go
type BackendConnection interface {
    OpenSession(context.Context, SessionSpec, SessionConfig) (SessionBackend, error)
    CloseSession(context.Context, SessionID) error
    Capabilities(context.Context) Capabilities
}

type SessionBackend interface {
    Dispatch(context.Context, Command) (CommandReceipt, error)
    Events() <-chan SessionEnvelope
    Capabilities() Capabilities
    PendingPermissionRequests() []PendingPermissionView
}
```

约束：

- `BackendConnection` 拥有共享 transport/process factory，可以复用连接但不能共享 session state；
- `SessionBackend` 是 per-session actor，可并发 dispatch，但内部必须串行化 wire write 和 generation 分配；
- `Dispatch` 快速返回 receipt，只表示 adapter 已接受或拒绝命令，不等待 turn 完成；
- `Events()` 是唯一向上时序通道，不能另开 `Done`/`Errors` channel；
- reconnect/resume 创建新 handle 和新 capability snapshot，但绑定相同 logical session ID；
- `BackendConnection.CloseSession` 释放 per-session transport binding，不伪造 completed。

### Command contract

首期直接覆盖固定 reference 的完整命令词汇，不先做阉割子集：

| Command            | 语义与关键约束                                                      |
| ------------------ | ------------------------------------------------------------------- |
| `Send`             | multimodal content + metadata；接受时分配唯一、单调 turn generation |
| `Cancel`           | target 为 tool/turn/session；cancel 与 crash 是不同事件             |
| `Steer`            | 注入当前 in-flight turn，仅 capability 支持时可用                   |
| `AnswerPermission` | request ID、decision、selected option、完整 question answers        |
| `AnswerAuth`       | 回答匹配的 re-auth challenge；credential 不进入事件或日志           |
| `Acknowledge`      | conversation/read layer 行为，不改变 FSM                            |
| `SetMode`          | 由后续 `ConfigChanged` 确认，不做乐观更新                           |
| `SetModel`         | 由后续 `ConfigChanged` 确认，不做乐观更新                           |
| `SetConfigOption`  | provider 宣告的 typed option，例如 reasoning effort                 |
| `Rewind`           | idle-only，Provider 映射到原生 history/rollback 单位                |
| `ListCheckpoints`  | 异步产生 checkpoint list event                                      |
| `QuerySessionInfo` | context usage/session cost，只读且不改变 FSM                        |

Command 用 Go discriminated union：固定 `Kind` 加对应 typed payload，validator 要求且只允许一个 payload。不得加入 `Invoke(method string, payload any)`、raw JSON escape hatch 或自动把 unknown 命令传给 Provider。

`CommandReceipt` 保留 reference 的 `accepted + admission + turn generation` 语义。Orrery 在外层 dispatch ledger 关联 command ID、fingerprint、backend correlation 和 sanitized rejection，用于分布式幂等，但不能改变 receipt 的 admission 含义。相同 command ID + 相同 fingerprint 返回原 receipt；相同 ID + 不同 payload 是 conflict。Receipt 不是 completion signal。

### Event 与 envelope contract

`SessionEnvelope` 至少包含 logical session ID、turn generation、source/projection、单调 sequence、timestamp 和 typed `SessionEvent`。事件全集按固定 `event.rs` 移植，至少覆盖：

- turn lower：`TurnStarted`、`Cancel`；
- content：message/thought delta、image/file、item completion；
- tool：call、output delta、result、subagent attribution；
- control：permission/request resolved、auth、prompt accepted、config/catalog changed；
- progress：plan、usage、heartbeat、notice、MCP provisioning、subagent/workflow；
- discovery：session info、checkpoint list、rewound、backend bound/suspended；
- lifecycle：turn result、detached、adapter-specific opaque diagnostic。

每个 reference event 都必须在 Go 端有：typed payload、validator、reducer classification、persistence/visibility classification 和 provider fixture。`AdapterSpecific` 只允许进入受限 diagnostics；业务和 reducer 不得根据其 tag/payload 推断状态。

### 状态与唯一 reducer

Go FSM 保留 reference 的四个核心 state：

```text
Starting
Running {
  since_epoch
  saw_substantive_output
  terminal_result_seen
  requires_action { waiting_on_approval, waiting_on_auth }
  subagents
}
Error { reason }
Idle
```

`RequiresAction` 不是独立 state，而是 `Running` 的引用计数子条件。解决一个 permission 不能在还有其他 pending request 时解锁。`ThoughtDelta` 不算 substantive output；非空 message 或 tool result 才计入。Provider status/system frame 不能触发 Running，只有 lowered `TurnStarted` 可以。

唯一纯函数 `step(previous, event) -> transition` 负责状态合成：

- Provider adapter 不写 state；
- Conversation/UI projection 不补写 state；
- cancel lower 为 Cancel，不伪装 Detached；
- process EOF 前先 drain/parse stdout，再处理 Detached；
- terminal/idle/error 对同一 generation 的 late event 具有吸收语义；
- `TurnResult.epoch < Running.since_epoch` 的迟到结果丢弃，不能结束新 turn；
- Provider EOF 没有 authoritative result 时显式归一为 clean-no-result/crash，不凭已有 text 推断成功；
- AionCore reducer不自行给 permission/turn 加超时。Orrery workload deadline 只能由上层 policy 发出显式 `Cancel`，不能引入另一条隐式 Timeout 状态路径。

### Orchestrator 与有序 fold lane

所有来源先进入一个 serialization point：

1. command lower event；
2. Provider `SessionEnvelope`；
3. reconnect/rehydrate event；
4. lifecycle normalization event。

Orchestrator 负责验证 session/generation、分配 Orrery sequence、调用 reducer、更新 snapshot、广播 envelope。不能让 command handler 和 provider reader 各自更新 state。广播采用 bounded ring/buffer；lagging subscriber 必须收到明确 lag signal，再从 cached snapshot/cursor 恢复，不能无声缺帧。

### ConversationSession 与消息 admission

`ConversationSession` 拥有 pending message FIFO，而不是 Renderer、HTTP handler 或 Provider adapter：

- Send 先以 client message ID 进入 pending queue；
- receipt accepted 只更新 admission，不删除消息；
- 只有匹配该 ID 的 `PromptAccepted` 才 drain 对应 pending item；
- rejected/failed delivery 保留可解释状态，不制造 assistant turn；
- active turn 下是否 queue/proactive input 由 capability 和 provider mapping 决定；
- reload/reconnect 从 snapshot 重建 pending、已接受和等待 permission 的展示状态；
- 同一 prompt 不因 HTTP retry、subscriber reconnect 或 process resume 重复发送。

### Capabilities

Capabilities 是不可变 session snapshot，连接重建后才刷新。必须覆盖：

- 完整 supported commands；
- prompt block kinds；
- model/mode/config option catalog 与 current values；
- auth methods；
- prompt-accepted assurance；
- proactive input、queue、multi-turn、resume、rewind/checkpoint；
- slash commands；
- signal support：tool lifecycle、terminal result、usage、plan、subagent 等；
- pending limits、payload limits、idle suspend 和 transport evidence/version。

unknown 默认 unsupported。不得继续使用当前 `CapableRuntimeBackend` 缺失时自动宣告 cancel/stream/resume 的乐观默认。

## AionCore 到 Go 的一比一映射

| AionCore 固定源文件               | Go 目标                                                                                    | 必须保留的行为                                                                                          |
| --------------------------------- | ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| `backend/mod.rs`                  | `internal/agent/core/runtime_backend.go`                                                   | `BackendConnection`/`SessionBackend` 两级 seam、fast dispatch、events、capabilities、pending permission |
| `backend/types.rs`                | `internal/agent/core/runtime_command.go`、`runtime_envelope.go`、`runtime_session_spec.go` | Command、Receipt、Admission、ContentBlock、SessionSpec、Snapshot 的 typed shape                         |
| `event.rs`                        | `internal/agent/core/runtime_event*.go`                                                    | 全事件 union、terminal outcome、permission、tool、usage、catalog、subagent、projection/visibility       |
| `state.rs`                        | `internal/agent/runtime/session/state.go`                                                  | 四态 FSM、requires-action counters、epoch 和 per-turn carry                                             |
| `reducer.rs`                      | `internal/agent/runtime/session/reducer.go`                                                | 单一纯 reducer、late event/terminal invariants、crash/cancel 区分                                       |
| `backend/orchestrator.rs`         | `internal/agent/runtime/session/orchestrator.go`                                           | command lowering、单 lane ordered fold、snapshot、broadcast/lag recovery                                |
| `backend/conversation_session.rs` | `internal/agent/runtime/session/conversation.go`                                           | pending FIFO、admission、PromptAccepted correlation、send availability                                  |
| `capability.rs`                   | `internal/agent/core/runtime_capability.go`                                                | command/block/catalog/auth/signal/slash/proactive capability snapshot                                   |
| `backend/rehydrate.rs`            | `internal/agent/runtime/session/rehydrate.go`                                              | cached snapshot/reconnect restoration，不重放副作用                                                     |
| `backend/suspend.rs`              | `internal/agent/runtime/session/suspend.go`                                                | opt-in idle suspend、in-flight guard、provider-native resume                                            |
| `backend/codex_conn.rs`           | `internal/agent/runtime/providers/codex/session_*.go`                                      | app-server handshake/thread/turn/approval/discovery/terminal 的完整映射                                 |
| `backend/claude_conn.rs`          | `internal/agent/runtime/providers/claude/session_*.go`                                     | persistent CLI/control、fresh/resume、permission/question/config/session info/terminal 的完整映射       |
| `testing.rs` 及源文件内 tests     | `internal/agent/runtime/conformance/` + provider fixtures                                  | port-first tests、differential event traces、故障与 race 覆盖                                           |

Go 文件按职责拆分，单文件不得用“忠实移植”为理由超过仓库 1000 行限制。大 Provider 文件拆成 connection、session、wire、reader、mapper、permissions、discovery、terminal 和 tests，但每个拆分文件保留 upstream source anchor 注释，例如 `Reference: aionui-session/backend/codex_conn.rs@v0.1.52`。

### Phase 0 reference audit 记录

固定 checkout 已在 2026-07-25 核验：tag `v0.1.52` 精确指向 commit `76f5554286ba0b6d33fb74d5c2bb2b3b0b83100d`，`crates/aionui-session` 包含 32 个 tracked entries、11 个 captured fixtures；完整 `git ls-tree` 规范化后的 SHA-256 为 `249f18b4d12e0bc037fe3397f5e0542bee27508995f49342bc4a5c27d8957db4`。开发环境用 `scripts/verify-runtime-bridge-reference.ps1` 重复核验，构建和 CI 不读取 `.cache`。

当前实现盘点如下，所有项均是迁移输入而非 parity 证据：

| 项目           | 当前 transport/行为                                                                            | 已有证据                                                                                   | 缺口与迁移结论                                                                                                                                                                                           |
| -------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Codex          | 每次 `Execute` 启动 `codex app-server --listen stdio://`；自有 JSON-RPC client；remote WS 可选 | thread/turn 参数、通知 parser、approval broker、tool dispatch、usage、部分 session ID 测试 | 仍投影为 `RuntimeSession.Messages/Result`；无 Connection 多 Session、canonical reducer、command receipt、pending FIFO、catalog/config/checkpoint/suspend 完整语义；重构为原生 Provider，旧 parser 不双写 |
| Claude default | Node Agent SDK sidecar，单次 `run` 请求后关闭输入                                              | approval continuation、并发 approval、tool/text/thinking parser 测试                       | 不是固定 reference 的 persistent Claude CLI SessionBackend；SDK shape 必须封装在 Provider 内或由原生 persistent CLI 替代                                                                                 |
| Claude legacy  | 每 turn 启动 `claude -p --output-format stream-json --input-format stream-json`                | stream-json text/tool/plan/usage/session/approval parser 测试                              | 一次性进程、EOF/result 推断和旧多 channel contract 不满足 parity；只可作为迁移输入                                                                                                                       |
| 旧核心         | `RuntimeAdapter.Run/Cancel`、`RuntimeBackend.Execute`、`RuntimeSession.Messages/Result`        | adapter/registry/stream tests                                                              | `Progress/Done/Errors` 跨 channel、乐观默认 capability、无 generation fence；仅允许从 canonical events 单向 projection                                                                                   |
| 版本固定       | Codex/Claude executable 由部署环境解析，未形成原生 Session Gate 的版本证据                     | process version helpers 和 provider config 校验                                            | Provider Gate 增加受支持版本检查及真实 CLI smoke；无凭据环境不可用 fake-success 代替                                                                                                                     |

Parity row 到固定上游测试/fixture 的追踪锚点如下。后续 Go 测试采用同名语义前缀并在测试文件写 source anchor；表中 `+` 表示同一 row 的共同证据。

| Codex parity row    | 固定 upstream source/test anchor                                                                                                                                             | Go 预期证据                                                     |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| 启动/握手           | `codex_conn.rs::b1_initialize_frame_opts_into_experimental_api_nested` + `bound_thread_timeout_is_handshake_timeout_not_transport`                                           | captured initialize writer/response + handshake budget tests    |
| Fresh/Resume        | `thread_started_lowers_backend_bound_with_thread_id` + `thread_resume_resends_full_start_surface_with_thread_id` + `thread_resume_error_clears_binding_and_poisons_dispatch` | fresh/resume binding fixtures，错误不创建第二 thread            |
| 配置注入            | `thread_start_frame_threads_cwd_and_never_model` + sandbox/approval/MCP tests                                                                                                | thread start allowlist byte-shape fixtures                      |
| Send                | `dispatch_send_writes_turn_start_with_thread_id` + `gap_a_turn_start_response_emits_prompt_accepted_for_client_msg_id`                                                       | active guard、receipt/generation、PromptAccepted correlation    |
| Cancel              | `dispatch_cancel_writes_turn_interrupt_with_active_turn_id` + `dispatch_cancel_with_no_active_turn_writes_no_frame`                                                          | active/no-active/cancel-crash tests                             |
| Steer               | `dispatch_steer_writes_turn_steer_with_expected_turn_id` + `dispatch_steer_without_active_turn_is_rejected`                                                                  | correct turn binding and stable rejection                       |
| Model/Mode/Effort   | `dispatch_set_model_writes_thread_settings_update` + config option/mode/reconcile tests                                                                                      | catalog validation、confirmed ConfigChanged、notice on error    |
| Approval            | `approval_reverse_rpc_surfaces_as_permission` + pending/answer/elicitation/server-resolved tests                                                                             | recoverable registry、unknown ID fail closed、decision fixture  |
| Auth                | `dispatch_answer_auth_writes_keyed_refresh_response` + `r5_answer_auth_after_resolved_clears_pending_drops_credentials`                                                      | correlation、race、credential redaction                         |
| Content/Tool        | `full_codex_turn_maps_to_canonical_events` + bracket/error/MCP/dynamic/file/image tests                                                                                      | canonical trace fixtures preserve content and attribution       |
| Plan/Diff/Usage     | `turn_plan_updated_maps_to_plan_event` + `turn_diff_updated_maps_to_turn_diff_updated` + full turn usage                                                                     | ordered plan/diff/usage trace                                   |
| Subagent            | `collab_agent_keys_on_child_thread_with_real_status_and_parent` + status mapping tests                                                                                       | reconstructable parent/ref/status                               |
| Slash commands      | `route_slash_command_maps_bridge_table` + advertised/dispatch slash tests                                                                                                    | advertised catalog equals actual dispatch                       |
| Rewind/Checkpoint   | rewind reject/rollback tests + `list_checkpoints_response_maps_to_checkpoint_list_event`                                                                                     | idle/zero/active and unit correctness                           |
| Terminal            | `r8_completed_then_idle_yields_one_turn_result` + idle-first/idle-only/two-turn/error tests                                                                                  | exactly one authoritative result per generation                 |
| EOF/Unknown         | `a1_unknown_item_type_does_not_panic_falls_to_adapter_specific` + EOF/fatal tests                                                                                            | drain-before-exit、opaque unknown、synth terminal               |
| Discovery/reconcile | model/permission discovery, catalog broadcast and reconcile tests                                                                                                            | immutable capability snapshot and persisted selection reconcile |
| Suspend/wake        | `f4_off_by_default_no_suspension` + `f4_suspend_then_wake_respawns_through_spawner`                                                                                          | opt-in/in-flight/wake/leak tests                                |

| Claude parity row | 固定 upstream source/test/fixture anchor                                                  | Go 预期证据                                               |
| ----------------- | ----------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| 启动模型          | `claude_conn.rs::dispatch_send_drives_turn_and_emits_envelopes` + process lifecycle tests | persistent process multi-turn trace                       |
| Fresh/Resume      | `claude_session_id_minted_for_non_uuid_passthrough_for_uuid` + resume tests               | logical ID/UUID mapping，首 prompt 不重复                 |
| 初始化            | `build_claude_init_args_*` + `prepend_args_orders_init_before_caller`                     | args/strict MCP/extra config fixtures                     |
| Send/admission    | `adapter/claude.rs::deliver_prompt_stamps_client_msg_id_as_uuid` + dispatch tests         | generation and exact PromptAccepted correlation           |
| Proactive queue   | `claude_conn.rs` proactive send/config queue tests                                        | FIFO、in-flight policy、latest-wins config                |
| Cancel            | cancel control + `claude_2.1.185_cancel_before_output_result.ndjson`                      | interrupt and late epoch preservation                     |
| Permission        | permission open/answer/reload tests + control request parser tests                        | allow/deny/always and registry recovery                   |
| AskUserQuestion   | `ask_user_question_permission_carries_tool_name_and_questions` + answer tests             | multi-question/select/updatedInput fidelity               |
| Model/Mode/Effort | model/mode/effort dispatch/reconcile tests                                                | queued control、catalog validation、ConfigChanged         |
| Discovery         | initialize models/config/slash tests                                                      | capability snapshot/catalog events                        |
| Session info      | session cost/context query tests                                                          | read-only info events                                     |
| Content/Tool      | `claude_*_turn.ndjson` fixtures + `adapter/claude.rs` parser/property tests               | fixture zero-diff for text/thought/tool/result/image/file |
| Subagent/Workflow | `claude_2.1.176_workflow_multiagent_3parallel_1fail.ndjson` + roster tests                | rich lifecycle and parent attribution                     |
| Terminal          | result outcome/property tests + `claude_error_result.ndjson`                              | `is_error` authoritative、truncated flush                 |
| Crash/EOF         | startup zero-frame/transport/partial-frame tests                                          | redacted stderr、Detached、no false timeout               |
| Epoch fence       | cancel-immediate-next-turn and late result tests                                          | stale generation discarded only by reducer                |
| Suspend/wake      | suspend module tests + Claude wake/resume tests                                           | default off、in-flight guard、no reader leak              |

Canonical command names are frozen to `send`, `cancel`, `steer`, `answer_permission`, `answer_auth`, `acknowledge`, `set_mode`, `set_model`, `set_config_option`, `rewind`, `list_checkpoints`, and `query_session_info`. Stable rejection/error/reason codes are frozen to `invalid_command`, `invalid_payload`, `unsupported_command`, `command_conflict`, `session_mismatch`, `generation_mismatch`, `queue_full`, `handshake_timeout`, `workspace_unavailable`, `transport_closed`, `protocol_error`, `backend_rejected`, `unknown_permission_request`, `unknown_auth_request`, `cancelled`, `detached_clean_no_result`, and `detached_crash`. Additions or semantic changes需要先登记到本文差异表并提供 executable test evidence。

## Provider 实现规则

### 共享规则

- 先复制/移植上游测试、captured wire fixture 和不变量，再写 adapter；优先采用成熟行为，不自行简化。
- Native wire 只存在于 Provider package；canonical event 不携带未审查 raw payload。
- 一个 reader 可以解析多种 native notification，但所有 canonical event 仍进入 Orchestrator 单 lane。
- process writer 有窄锁；不能用 session-wide mutex 阻塞多 session 并发。
- startup handshake 使用有界、可配置预算；cold start 超时返回 retryable handshake error，不用 magic 2s busy poll。
- Drop/Close 必须终止 reader、释放 process binding，并经过 leak/race 测试。
- 所有 stderr 只经过 allowlist redaction 和长度上限后才可进入 diagnostic event。

### Codex parity matrix

Codex 第一优先，目标是 app-server 原生 SessionBackend，不以当前一次性 `codex exec` 或旧 parser 代替。

| 能力                | AionCore reference 行为                                                         | Orrery 验收                                                        |
| ------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| 启动/握手           | `codex app-server` initialize，experimental API 位于正确 capability 层级        | captured request/response fixture byte-shape 等价                  |
| Fresh/Resume        | `thread/start`、`thread/resume`，logical session 与 backend thread 绑定         | restart/resume 不创建错误第二 thread                               |
| 配置注入            | cwd、sandbox、approval policy、MCP/preset；不在 thread start 错放 model         | 参数 allowlist 与 reference fixture 一致                           |
| Send                | `turn/start`、active turn guard、client message correlation                     | 只有成功 response 发 `PromptAccepted`；失败不 drain pending        |
| Cancel              | active `turn/interrupt`；无 active turn 不伪造 wire                             | cancel 与 crash/failed terminal 可区分                             |
| Steer               | `turn/steer` 且绑定正确 turn ID                                                 | 无 active turn 稳定拒绝                                            |
| Model/Mode/Effort   | discovery + `thread/settings/update`，结果经 ConfigChanged/CatalogUpdated       | 不做乐观状态；错误产生 notice                                      |
| Approval            | reverse RPC、command/file approval、elicitation、decision response              | pending registry 可恢复，未知 request ID fail closed               |
| Auth                | token refresh request/resolved 与 AnswerAuth correlation                        | credentials 不进入 event/log，race 不丢响应                        |
| Content/Tool        | message/reasoning、command、MCP/dynamic tool、output delta/result、file/image   | start/result bracket、is_error、content 和 parent attribution 不丢 |
| Plan/Diff/Usage     | plan full snapshot、diff、usage notification                                    | canonical event 顺序与 fixture 一致                                |
| Subagent            | collab-agent lifecycle 和 parent/child thread mapping                           | status/parent/ref 可重建，不靠 display text 推断                   |
| Slash commands      | reference bridge table及无 turn 命令                                            | advertised catalog 与实际 dispatch 一致                            |
| Rewind/Checkpoint   | rollback、thread list/checkpoint projection                                     | idle-only、zero/active invalid、结果单位正确                       |
| Terminal            | completed/interrupted/failed/idle reconciliation                                | 每 turn 恰一结果；idle/completed 任意先后均不重复                  |
| EOF/Unknown         | drain-before-exit、fatal synth terminal、unknown item opaque                    | 不 panic、不以最后文本判成功                                       |
| Discovery/reconcile | model、effort、permission profile catalog 和 persisted selection reconciliation | 无效旧配置清除，合法配置应用                                       |
| Suspend/wake        | opt-in idle suspension、resume binding                                          | in-flight 不 suspend，wake 后 logical session 不变                 |

Codex Gate：矩阵每一行都有 fixture/test；Go provider 通过共享 conformance、race、真实 CLI smoke；现有业务消费者只从 canonical events 得到结果。

### Claude Code parity matrix

Claude Code 第二优先，按 reference 的 persistent CLI/stdin control 设计实现。若继续复用现有 Node Agent SDK 代码，也必须藏在 Provider 内并证明产生完全相同的 canonical 行为；不能以 sidecar 已能返回文本为理由降低 parity。

| 能力              | AionCore reference 行为                                             | Orrery 验收                                           |
| ----------------- | ------------------------------------------------------------------- | ----------------------------------------------------- |
| 启动模型          | persistent Claude Code process，stdin 控制，不是每 turn 重启        | 多 turn 共用 session，进程生命周期可观测              |
| Fresh/Resume      | logical ID 到 Claude session UUID，fresh/resume 参数正确            | 非 UUID 安全映射，resume 不重复首个 prompt            |
| 初始化            | model/mode/preset、MCP strict config、extra args/env                | 参数顺序、JSON shape 与 captured fixture 等价         |
| Send/admission    | turn generation、prompt write、replay prompt ack                    | 有 client message ID 才匹配 `PromptAccepted`          |
| Proactive queue   | in-flight 控制或下一 turn queue 按能力执行                          | FIFO、不重复、同类配置 latest-wins 规则可测           |
| Cancel            | interrupt control request                                           | cancel 后 trailing result 保留旧 epoch，不结束新 turn |
| Permission        | `can_use_tool`、pending map、allow/deny/always                      | open/resolved/reload recovery 完整                    |
| AskUserQuestion   | 单/多问题、single/multi-select、updatedInput answers                | 所有 question/labels 不丢，未知 request 拒绝          |
| Model/Mode/Effort | in-band control、mid-turn queue、catalog validation、ultracode flag | ConfigChanged 确认；invalid option 有 notice          |
| Discovery         | initialize response 的 models/config/slash commands                 | capability snapshot 与 catalog update 一致            |
| Session info      | context usage、session cost query                                   | 只读 event，不改变 FSM                                |
| Content/Tool      | stream-json text/thought/tool/result/image/file                     | 与 legacy parser zero-diff fixture 对照通过           |
| Subagent/Workflow | Task/Workflow lifecycle、rich detail、parent attribution            | foreground/background roster 行为与 reference 一致    |
| Terminal          | result `is_error` 路由、rich outcome、truncated flush               | subtype 不覆盖 is_error；EOF 不丢最后半帧             |
| Crash/EOF         | startup guard、process-exit、redacted stderr、Detached              | 无 frame startup 超时；长 silent turn 不被误杀        |
| Epoch fence       | late cancelled result 带原 generation                               | 跨 turn stale result 被 reducer 丢弃                  |
| Suspend/wake      | idle TTL 默认关闭，opt-in suspend 后 `--resume`                     | in-flight guard、environment 复用、reader 无泄漏      |

Claude Gate：reference real fixtures 的 parse zero-diff、共享 conformance、race、真实 CLI multi-turn smoke 全部通过；permission、cancel、late-result、EOF 四类负向测试不可跳过。

### PI native RPC matrix

PI 不在固定 AionCore `v0.1.52` Provider 集内，因此以官方 `@earendil-works/pi-coding-agent@0.79.9` `docs/rpc.md` 和实际 CLI wire 作为 Provider-native 权威来源，canonical contract、reducer 和 application 边界仍完全复用本方案。

| 能力                      | PI `0.79.9` native 行为                                                                                  | Orrery 验收                                                                                       |
| ------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| 启动/握手                 | 单个持久 `--mode rpc` JSONL stdio process；`get_state`、`get_available_models`、`get_commands` discovery | 版本精确固定；真实 CLI handshake、backend bind、catalog 和 transport evidence 通过                |
| Fresh/Resume              | fresh session 或 `--session` 恢复既有 session                                                            | backend session ID 精确绑定；resume mismatch fail closed                                          |
| Send/admission            | `prompt` response 明确接受或拒绝；后续 event 可紧随 response                                             | 成功 response 的 `PromptAccepted` 必须先于任何 terminal event；拒绝不消费 generation              |
| Multi-turn/control        | 同一 process 连续 prompt；`steer`、`abort`、model、thinking level、session stats                         | multi-turn、steer、authoritative cancel、model/effort 和 session info fixture 通过                |
| Content/tool/plan/usage   | RPC event stream、extension tool bridge、plan tools 和 token/cost stats                                  | canonical message/tool/plan/usage 只映射一次；真实 Discussion/Worker tool path 可产生业务副作用   |
| Permission/UI             | server-owned approval bridge 与 extension UI request/response                                            | pending registry 可恢复；delivery 失败保留待重试；approval/UI ID 分域；未知或无效答案 fail closed |
| EOF/protocol/backpressure | JSONL record、process exit 和 extension bridge 都是 Provider-private transport                           | record size 上限、redaction、drain/process-tree cleanup、明确 failed terminal 和 bounded queue    |
| 安全/配置                 | provider config、LLM profile、workspace、system prompt 和本地 extension 由服务端解析                     | canonical contract 不暴露 executable、argv/env、credential、绝对路径或 raw RPC payload            |
| Conformance               | PI 没有 AionCore adapter 可直接移植                                                                      | 使用同一 shared conformance suite；capability 只宣告实际实现的命令和 signal                       |

PI Gate：固定版本 native fixtures、shared conformance、race、真实 `0.79.9` RPC handshake、production Registry、Discussion/Worker 和真实 `/tool-call` 业务副作用路径全部通过；没有一次性 `Run` 包装、旧 parser 或 fake-success 路径。

### Generic ACP matrix

通用 ACP Provider 以 ACP JSON-RPC/NDJSON stdio wire 为权威边界，不绑定某个供应商 CLI。可执行文件、参数、环境、工作区和 MCP 只从服务端授权后的 Runtime binding 注入，均不进入 canonical event 或对外 API。

| 能力              | ACP native 行为                                                         | Orrery 验收                                                                                                                                     |
| ----------------- | ----------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| 启动/握手         | 持久 stdio process；`initialize` 协商版本与能力                         | payload 有界；stderr 脱敏；握手超时和进程树清理 fail closed                                                                                     |
| Fresh/Resume      | `session/new` 创建，`session/load` 恢复 backend session                 | backend ID 绑定；resume mismatch 拒绝；不重复首 prompt                                                                                          |
| Send/cancel       | `session/prompt`、`session/cancel`                                      | prompt admission、generation、单 terminal 和 cancel trailing event 进入同一 reducer                                                             |
| Content/tool/plan | `session/update` text、thought、tool call/result、plan、command、config | 只映射 typed canonical event；未知或无效 update 不进入业务状态                                                                                  |
| Permission        | `session/request_permission` 及 Agent 提供的 option                     | pending request 可恢复；只接受已提供 option；无兼容选项时 fail closed                                                                           |
| Controls          | mode、model、config option                                              | capability catalog 驱动命令支持；成功响应后才发 `ConfigChanged`                                                                                 |
| MCP               | `session/new/load` 注入 MCP servers                                     | 只使用服务端已解析 binding；credential、argv/env 和本地路径不外泄                                                                               |
| Conformance       | shared canonical suite + 标准 ACP AgentSide peer                        | 服务端 shared suite/真实子进程，加桌面 SDK peer 覆盖 initialize -> new -> prompt -> permission -> stream -> terminal -> controls -> load/resume |

ACP Gate：真实 SDK peer、production Registry selection、workspace/MCP binding、permission fail-closed、resume、process cleanup 和 canonical projection 测试必须全部通过；不能按 executable 名称硬编码供应商语义。

## Orrery 集成边界

### Application 与旧 Runtime

Canonical Session 体系放在应用与 Provider 之间。迁移期曾提供两个单向 adapter：

- `LegacyConnection`：把未迁移 Provider 的旧 `RuntimeAdapter` 投影为保守的 SessionBackend，只宣告真实子集；
- `ExecutionProjection`：把 canonical event 投影为尚未迁移消费者需要的 `RuntimeExecution/RuntimeResponse`。

Phase 7 已在 telemetry 零调用后删除 `LegacyConnection`、旧 `RuntimeAdapter/RuntimeBackend/RuntimeSession` contract 和兼容 registry。当前 `RuntimeResponse` 仅是 application 对同一 canonical ordered stream 的有界投影，不是第二执行源。同一 run 只有一个 canonical source，不存在旧 parser/新 Provider 双写、旧 `Done` 反向修改 reducer 或 feature-flag fallback。

### Device Runtime

Desktop 仍按独立 Device Protocol 接入。Codex、Claude parity 完成后实现 `DeviceConnection/DeviceSessionBackend`，通过 typed `DeviceWorkOrderBroker` 映射到 canonical contract：

- 云端 `OpenSession + Send` 映射为 `agent_turn` WorkOrder；
- local permission 可产生 canonical Permission event，但远程 `AnswerPermission/AnswerAuth` 必须 unsupported；
- disconnect 是 detached/recoverable，重连按 durable cursor replay；
- local path、argv、env、AionCore route 永远不进入 SessionSpec、Command 或 Event；
- WorkOrder terminal 是 Device turn terminal 的唯一来源。

Device adapter 复用相同 reducer、orchestrator 和 conformance，不在 Gateway 内复制 Session FSM。

### 业务 deadline

AionCore session 没有自动 turn timeout。Orrery 的任务 deadline、租户预算和 operator cancel 属于 application policy：触发时向 Session dispatch 明确 `Cancel` 并记录 causation。Provider process 若随后退出，late Detached 不能把 cancel 改写成 crash。这样既保留 reference reducer 语义，也保留 SaaS 控制面治理。

## 安全与可靠性

- Runtime identity、tenant、run、agent、workspace policy 和 credential ref 来自服务端授权上下文，不从 prompt/native payload 获取。
- Command 和 Event 都是 typed allowlist；Provider raw JSON 是不可信输入，先做 size/schema/redaction 再映射。
- AnswerAuth credential 只在 command delivery 短生命周期内存在，不持久化、不广播、不记录。
- permission request ID、backend RPC ID 和 tool call ID 分域，不能互相替代。
- bounded queue 对 delta 可按同 content ID 合并；permission、tool result、PromptAccepted、config、terminal 不可静默丢弃。
- lagging subscriber 使用 snapshot/cursor 恢复；不能让 Provider stdout reader因慢消费者永久阻塞。
- cancel、close、deadline、EOF、crash、protocol error、backpressure 各有稳定原因，并且每 turn 只有一个 authoritative result。
- Provider 不能直接写 Mission/Task/Discussion store；业务 commit 只消费 reducer 验证后的 canonical sequence。
- 日志禁止 prompt、tool body、raw stderr、credentials、argv/env、本地 path 和未脱敏 provider payload。

类型化 Bridge 不是沙箱。Workspace containment、Provider 网络外发、恶意 CLI/MCP 和被攻陷 worker 仍由独立安全层处理。

## 差异登记规则

默认要求零语义差异。任何无法或不应一比一移植的行为，必须先在下表登记，随后才允许合并代码：

| Reference 行为                                                                 | Orrery 映射                                                                                                                                                | 原因                                                                                                      | 兼容影响                                                                             | 测试证据                                                                                                                                                                                |
| ------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| AionCore 不自动 timeout                                                        | Application 在 deadline 时显式 dispatch Cancel                                                                                                             | SaaS 任务治理要求                                                                                         | reducer/cancel 语义不变                                                              | deadline -> Cancel -> late Detached conformance                                                                                                                                         |
| AionCore 内部 broadcast snapshot                                               | 服务端可增加 durable event cursor，但 live fold 仍保持同序                                                                                                 | 多实例恢复要求                                                                                            | additive，不改变 Provider observable order                                           | restart/replay differential test                                                                                                                                                        |
| Reference receipt 不承担分布式幂等                                             | Orrery 在 SessionBackend 外增加 command ledger，receipt 仍保持 accepted/admission/turn generation                                                          | HTTP/queue retry 需要稳定去重                                                                             | additive，不改变 Provider dispatch                                                   | duplicate ID/fingerprint/conflict tests                                                                                                                                                 |
| Desktop/local auth context                                                     | 服务端从 tenant-scoped credential broker 注入 ref                                                                                                          | 服务端不能使用桌面本地凭据模型                                                                            | 不允许 credential 进入 canonical event                                               | auth redaction/tenant isolation tests                                                                                                                                                   |
| Canonical `Detached` 会终止 turn；Device `detached` frame 表示可恢复的本地断连 | Device adapter 将 `detached` 映射为 non-terminal warning `Notice`，保持 WorkOrder subscription 和 generation；只有 `work_order.terminal` 映射 `TurnResult` | Device Protocol 要求断线后按 durable cursor 恢复，WorkOrder terminal 是 Device turn terminal 的唯一事实源 | 仅 Device transport 的断连不再触发 canonical terminal；Codex/Claude reducer 语义不变 | `TestDeviceRuntimeUsesCanonicalOrchestratorAndKeepsPrivateExecutionMaterialLocal`、`TestDurableBrokerRestartReplaysOfferAndWorkOrderCursor`、`TestDeviceSessionPassesSharedConformance` |
| AionCore `v0.1.52` 没有 PI SessionBackend                                      | 以 PI 官方 `0.79.9` RPC 为 Provider-native reference，映射到同一 `BackendConnection/SessionBackend` 和 canonical event contract                            | Codex/Claude/Device Gate 完成后，PI 是仍有生产价值的下一 Provider                                         | additive Provider；不修改 reducer、orchestrator 或其他 Provider observable semantics | PI native fixture matrix、`TestNativePiPassesSharedConformance`、`TestNativePiRealRPCHandshakeSmoke`、production Discussion/Worker tests                                                |
| AionCore `v0.1.52` 的 ACP client 不是 Go 服务端可复用的 Provider               | 服务端实现协议级 JSON-RPC stdio Provider，并以 shared conformance、真实 Go 子进程和桌面标准 SDK AgentSide process 作为证据                                 | 允许接入任意兼容 ACP Agent，同时保持 Go 服务端无 Rust/AionCore 生产依赖                                   | additive Provider；不修改 canonical reducer，不暴露 provider-private launch material | `TestACPProviderPassesSharedConformance`、`TestACPRealProcessPromptPermissionAndControls`、production Registry selection/capability test                                                |

每条新差异必须同时填写：原因、准确 Orrery mapping、compatibility impact 和可执行 test evidence。以“Go 写法不同”“现有代码已经这样”或“先简化”不能作为语义偏差理由。

## 实施计划

### Phase 0：建立可重复的 reference audit

- [x] 验证本地 AionCore tag/commit，记录 `aionui-session` 文件清单与摘要；CI 不依赖 `.cache`，但开发脚本可检查 reference 是否匹配。
- [x] 逐项盘点当前 Codex/Claude transport、CLI version、fixture、session/resume、cancel、permission、tool、usage、terminal 和 cleanup 行为。
- [x] 将 upstream test 名称和 fixture 建立到本文件 parity row 的可追踪映射；优先 port 上游测试，不新造不同语义。
- [x] 冻结 canonical names 和 reason codes；后续不创建另一个 v2 ADR/计划/矩阵。

Gate：reference commit 可复现，Codex/Claude 每个 parity row 已关联上游源位置和预期测试。

### Phase 1：Canonical contract、FSM 与 reducer parity

目标文件：

```text
internal/agent/core/runtime_backend.go
internal/agent/core/runtime_command.go
internal/agent/core/runtime_event.go
internal/agent/core/runtime_event_payloads.go
internal/agent/core/runtime_capability.go
internal/agent/core/runtime_session_spec.go
internal/agent/runtime/session/state.go
internal/agent/runtime/session/reducer.go
internal/agent/runtime/session/validate.go
```

- [x] 先实现 compile-time fake 和严格 union validator。
- [x] 移植 reducer table/property tests：四态、requires-action ref count、substantive output、epoch、cancel/crash、terminal absorption、unknown opaque event。
- [x] Snapshot defensive copy，禁止 secret/raw provider/path 字段。
- [x] unknown command/event/capability fail closed；所有 stable error/reason code 有负向测试。

Phase 1 review（2026-07-25）：逐项对照 `backend/types.rs`、`event.rs`、`capability.rs`、`state.rs`、`reducer.rs` 完成自审；整改了 credential fingerprint 冲突、permission counter overflow、生产代码中的 compile-only backend、capability/catalog 未校验、permission raw JSON、MCP header secret 和跨平台绝对路径识别问题。`go test`、`go test -race`、`go vet` 及 `internal/agent/...` 回归通过；contract/reducer 未发现未登记语义偏离。

### Phase 2：Orchestrator、ConversationSession 与 conformance

目标文件：

```text
internal/agent/runtime/session/orchestrator.go
internal/agent/runtime/session/conversation.go
internal/agent/runtime/session/command_ledger.go
internal/agent/runtime/session/event_bus.go
internal/agent/runtime/session/rehydrate.go
internal/agent/runtime/session/suspend.go
internal/agent/runtime/conformance/
```

- [x] 单 serialization lane 合并 lowered/provider/recovery event；并发/race 下 sequence 与 generation 不重复。
- [x] 实现 pending FIFO、PromptAccepted 精确 correlation、duplicate command receipt、queue/admission。
- [x] bounded broadcast、lag signal、snapshot recovery、subscriber disconnect 和 backpressure。
- [x] 将 reference reducer、conversation、orchestrator、EOF、permission、suspend tests 移植为共享 conformance。
- [x] 建 differential harness：相同 canonical trace 输入 reference fixture 和 Go reducer，比较 state/event projection。

Gate：fake backend 完整通过 conformance；故意制造 double terminal、late result、lost PromptAccepted、permission underflow 时测试必须失败。

Phase 2 review（2026-07-25）：逐项对照 `orchestrator.rs`、`conversation_session.rs`、`rehydrate.rs`、`suspend.rs` 和 `testing.rs` 完成自审；整改了 `Events()` 重复取流、关闭后 subscription 二次 close、后台 activity snapshot 校验、dispatch 入 lane 后取消导致副作用与 ledger 结果分叉、provider 伪造 `TurnStarted`、ahead/mismatched generation、terminal generation 归属、并发 HTTP retry 提前返回不同 receipt、teardown 后迟到 `PromptAccepted` 复活消息等问题。shared conformance 覆盖 double terminal、stale result、lost `PromptAccepted`、permission underflow、drain-before-EOF、lag recovery 和 race；pinned differential fixture 对每步 state 做比较。`go test`、`go test -race`、`go vet` 全部通过，未发现未登记语义偏离，生产代码未加入 fake/mock backend。

### Phase 3：Codex 原生 Provider

- [x] 复用现有 Codex process/client 代码中成熟部分，但以 `codex_conn.rs` 行为重构为 Connection/SessionBackend；不保留双 parser。
- [x] 按 parity matrix 分批移植 handshake、thread、turn、approval/auth、content/tool、discovery/config、rewind/checkpoint、terminal 和 suspend tests。
- [x] 使用固定 app-server captured fixtures 做 parser/writer contract；unknown notification 不 panic。
- [x] 用真实受支持 Codex CLI 做 fresh、multi-turn、cancel、steer、approval、resume 和 crash smoke。
- [x] 通过 `go test -race`，Drop/Close 后无 process、goroutine、pending RPC 泄漏。

Gate：Codex matrix 全绿，shared conformance 全绿，production Registry 可在 feature flag 下选择新实现。

Phase 3 review（2026-07-25）：逐行对照固定基线 `codex_conn.rs` 与 Codex parity matrix 完成自审；整改了 Windows npm shim 子进程树清理、Close 的 process-tree 停止顺序、bounded event queue 的 delta 合并/显式 backpressure terminal、真实 CLI 快速完成时的 cancel/steer smoke 竞态，以及 shared conformance 对不支持 queue Provider 的并发 admission 假设。固定 app-server fixtures、shared conformance、production Registry feature selection、`go test -race`、`go vet` 和 `internal/agent/...` 回归通过；受支持的真实 `codex-cli 0.145.0` fresh、multi-turn、cancel、steer、approval、resume、crash 全套 smoke 通过。矩阵逐项核对未发现未登记语义偏离。

### Phase 4：Claude Code 原生 Provider

- [x] 以 `claude_conn.rs` 为参考建立 persistent process、stdin writer、reader、pending controls 和 resume；评估并复用现有 Claude 代码，不让 Agent SDK shape 泄露出 Provider package。
- [x] 移植 captured stream-json/control fixtures并保持 parser zero-diff；覆盖 partial EOF frame。
- [x] 完成 permission/AskUserQuestion、model/mode/effort、proactive queue、session info、subagent/workflow、epoch 和 terminal tests。
- [x] 用真实 Claude Code CLI 做 multi-turn、mid-turn config、cancel + immediate next turn、permission reload、resume 和 crash smoke。
- [x] 通过 race/leak/redaction tests。

Gate：Claude matrix 全绿。Codex 与 Claude Gate 同时满足后，canonical contract 才可标记 stable。

Phase 4 review（2026-07-26）：逐行对照固定基线 `claude_conn.rs`、`adapter/claude.rs` 与 Claude parity matrix 完成自审；整改了仅凭 provider replay UUID 误发 `PromptAccepted`、多问题漏答、effort 使用跨模型并集校验、错误 setting 的脱敏诊断、early terminal 后 late result 的 generation 归属、partial/consolidated 文本去重、result `is_error`/outcome 解耦、usage cache token 漏计、final non-newline frame、bounded event backpressure，以及 Windows npm/native launcher 退出后子进程无法可靠回收的问题。固定 upstream write fixture 与 legacy parser differential、persistent process helper、shared conformance、production Registry feature selection、`go test -race`、`go vet` 和 `internal/agent/...` 回归通过；受支持的真实 Claude Code `2.1.220` multi-turn、mid-turn config、cancel 后立即新 turn、permission recovery、resume、crash 全套 smoke 通过。矩阵逐项核对未发现未登记语义偏离，生产路径未加入 mock/fake-success。

### Phase 5：业务消费者与 Registry 迁移

- [x] 实现 LegacyConnection 和 ExecutionProjection，仅用于未迁 Provider/消费者，capability 采用保守映射。
- [x] Runtime Registry 以 `BackendConnection` 为主入口；BaseAgent、Discussion streaming/recording、Worker 逐个改为消费 canonical events。
- [x] Application 层拥有 deadline -> Cancel、tenant authz、usage/artifact commit；Provider 不写业务表。
- [x] production wiring tests 证明真实 Codex/Claude 走新 SessionBackend，不能被旧 RuntimeAdapter 静默接管。
- [x] 记录旧 projection 调用 telemetry，达到零调用后才删旧 `Progress/Done/Errors` 主路径。

Phase 5 review（2026-07-26）：逐项对照业务边界、Registry 入口、单向 compatibility 和安全约束完成自审；整改了 runtime record 长期保留 executable、credential ref、argv/env、MCP config 和本地绝对路径，LegacyConnection 无 call ID 的 text delta/final 使用不同 item ID 并在最终投影中重复，以及生产 Worker 未从 canonical runtime record 提交 usage 的问题。新增 runtime 级 auth dispatch/fail-closed、record redaction、legacy item identity/consolidation、application usage ownership和真实生产 wiring 证据；Provider/session package 无 `internal/app`、业务 store 或数据库依赖。shared conformance、Codex/Claude fixture、业务 Discussion/Worker、production wiring、focused race 和 focused vet 均通过；未发现未经登记的语义偏离，生产路径未加入 mock、fake-success 或第二事实源。

### Phase 6：Device Runtime 接入

- [x] 按客户端方案实现 typed `DeviceWorkOrderBroker` 与 `DeviceConnection/DeviceSessionBackend`。
- [x] Device events 经过 schema/tenant/device/sequence/size 校验后进入同一 Orchestrator。
- [x] 覆盖 offer、accepted、running、local permission、detached/replay、cancel、terminal 和服务端重启恢复。
- [x] 明确远程 permission/auth unsupported，本地路径/argv/env/provider config 不进入 canonical contract。

Phase 6 review（2026-07-26）：按 intended-vs-implemented 方法逐项对照本节、Device Protocol v1 schema、桌面共享 fixtures、canonical safety boundary 和 production Registry 完成自审；整改了 migration 未注册到 production modules、跨 connection epoch message ID 去重、per-device cursor/ordinal 原子分配、duplicate offer 非持久消息与 subscription 泄漏、nested timestamp 非 UTC schema 偏差、cancel-before-accepted 丢失 admission、kill switch 下 pending offer replay、lease stale timer、early artifact、不同 session 复用 WorkOrder、失败 Send 消耗唯一 turn、Session close 不结束 event stream、服务端 identity 被 provider config 覆盖，以及本地路径/credential 文本脱敏问题。真实 SQLite migration/restart、共享 desktop fixtures、durable broker/replay、shared conformance、application wiring、production Registry/migration、focused `go test -race` 和 `go vet` 全部通过；Postgres dialect statement rendering 与类型已锁定，未发现未登记语义偏离。Device Gateway 的 HTTP/WSS 配对和 credential service 属于桌面客户端方案的独立 server delivery，不作为本 Runtime Bridge phase 的替代事实源；本 phase 没有加入 gateway fake、mock transport 或 fake-success 路径。

### Phase 7：其他 Provider 和 legacy removal

- [x] 只有 Codex、Claude、业务消费者和 Device 全部通过 Gate 后，才按价值顺序评估 PI/ACP/其他 Provider 原生迁移。
- [x] 每个新增 Provider 必须完成同一 capability/parity/conformance 流程，不能仅包装一次性 `Run` 就宣称原生 Session。
- [x] 删除旧 `RuntimeSession.Messages/Result`、重复 parser、乐观 capability 默认和无调用 compatibility adapter。
- [x] 全量 Go、race、production wiring、Discussion/Worker 和 device E2E 通过后再移除 feature flag。

Phase 7 review（2026-07-26）：按 intended-vs-implemented 方法逐项对照本方案、PI 官方 `0.79.9` RPC、production Registry 和全部 12 条完成标准完成自审。Codex、Claude、业务消费者和 Device Gate 完成后才迁移 PI；PI 使用持久 native JSONL RPC、真实 capability、shared conformance 和真实 CLI handshake，其他 Provider（含 `managed`）显式 unavailable。整改了 PI approval delivery 失败后错误消费 pending request、approval/UI request ID 冲突、PI 大文件职责拆分、Mission Control retry 回退到不可用 `managed`、queued AgentRun/mention workspace 与 runtime config 选择、空 `json.RawMessage` queue round trip、production Discussion tool fixture、Codex/PI prompt acknowledgement 可能晚于 terminal、Conversation flush engine 抢先 dispatch 导致 receipt 错报 queued、Claude fixture capture/handshake race、reference verifier 在 Windows PowerShell 下无法运行，以及 Bridge 消费者 `base_agent.go` 超过 1000 行限制的问题；结果归一化与校验已无行为变更地拆入 `base_agent_result.go`，Bridge 相关 Go 文件现均不超过 1000 行。旧 `RuntimeSession.Messages/Result`、`RuntimeAdapter/RuntimeBackend`、重复 parser、compatibility registry、feature flag 和 legacy fallback 已删除；production `RuntimeResponse` 只由 canonical stream 投影，不存在双写或第二事实源。PI matrix/native fixtures、真实 `0.79.9` handshake、真实 Codex fresh/multi-turn/resume、shared conformance、全量 Go、分片全量 `cmd/server` race、runtime focused race、`go vet ./...`、production wiring、Discussion/Worker、Device durable E2E 和 reference verifier 均通过；未发现未登记的边界偏离，也未加入 mock 或 fake-success 生产路径。

Phase 7 后 ACP delivery（2026-07-30）：通用 ACP Provider 通过持久 JSON-RPC stdio process 实现 initialize、fresh/load、prompt/cancel、permission、mode/model/config option、MCP 和 typed session update 映射；生产 Registry、Discussion external CLI history 和 settings/OpenAPI runtime kind 已同步。真实标准 SDK AgentSide process 覆盖完整生命周期，未知 permission option、session mismatch、超限 payload、敏感诊断和退出清理均 fail closed；没有供应商 executable 分支、一次性 Run 包装或 fake-success 路径。

### 整体验收（2026-07-26）

最终验收再次以本文件的 12 条完成标准为 intent，逐项检查 production code、负向测试、真实 CLI 证据和发布边界；结论如下：

| 完成标准 | 实现与测试证据                                                                                                                                                                                               | 结论 |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---- |
| 1        | `docs/runtime/` 仅保留本方案；`verify-runtime-bridge-reference.ps1` 在 PowerShell 7 和 Windows PowerShell 均核验 tag `v0.1.52`、commit `76f5554...`、32 entries 和固定 tree SHA-256；`.cache` 被 gitignore   | 通过 |
| 2        | `runtime_backend.go`、`runtime_command.go`、`runtime_event*.go`、`runtime_capability.go`、`session/{conversation,orchestrator,reducer,state,rehydrate,suspend}.go` 均保留固定 upstream source anchor         | 通过 |
| 3        | Provider 只输出 typed `SessionEnvelope`；`Orchestrator.foldEvent` 是唯一调用 `Step` 并替换 FSM state 的 production lane，且拒绝 Provider 伪造 orchestrator-owned event                                       | 通过 |
| 4        | shared conformance、`phase2_test.go`、`reducer_test.go` 和 `snapshot_test.go` 覆盖 FIFO、PromptAccepted、generation、cancel/crash、drain-before-EOF、permission、snapshot/lag recovery                       | 通过 |
| 5        | Codex matrix 对应 native fixtures、shared conformance 与 `TestNativeRealCLISmoke`；受控环境的 `codex-cli 0.145.0` fresh/multi-turn/cancel/steer/approval/resume/crash 证据已通过                             | 通过 |
| 6        | Claude matrix 对应 persistent helper/fixtures、shared conformance 与 `TestNativeClaudeRealCLISmoke`；受控环境的 Claude Code `2.1.220` multi-turn/config/cancel/permission/resume/crash 证据已通过            | 通过 |
| 7        | Phase review 顺序记录证明 Codex、Claude、业务消费者与 Device Gate 完成后才加入 PI native RPC；其他 Provider 未伪装成 native Session                                                                          | 通过 |
| 8        | 全部有意差异只出现在“差异登记规则”表，均具备 mapping、原因、兼容影响和可执行测试；最终代码审查未发现额外边界偏离                                                                                             | 通过 |
| 9        | Bridge 源码与依赖扫描无 Rust、Cargo、FFI 或 AionCore production dependency；`.cache` 仅供 verifier 使用                                                                                                      | 通过 |
| 10       | command/event/session validators、runtime record projection 和 Device protocol 负向测试拒绝 arbitrary dispatch、raw Provider payload、credential 持久化、argv/env 与绝对本地路径                             | 通过 |
| 11       | production symbol/search 与 wiring test 确认旧 contract、parser、compatibility registry、feature flag 和 fallback 已删除；`ResponseProjection` 只消费 canonical ordered stream且不回写 reducer               | 通过 |
| 12       | shared conformance、Provider fixtures、真实 CLI smoke、业务消费者、production wiring、Device durable E2E、runtime race、全量分片 `cmd/server` race、最终 `go test ./... -count=1` 与 `go vet ./...` 全部通过 | 通过 |

最终 prohibited-symbol、production mock/fake-success、Provider 业务存储依赖、未登记 fallback 和 Bridge Go 文件行数扫描均无发现；整体验收未发现 attacker/victim 边界错配或需要继续整改的偏离。

## 验证命令

实施后至少执行：

```powershell
cd orrery_v2
go test ./internal/agent/core ./internal/agent/runtime/session ./internal/agent/runtime/conformance -count=1
go test ./internal/agent/runtime/providers/codex ./internal/agent/runtime/providers/claude ./internal/agent/runtime/providers/acp -count=1
go test -race ./internal/agent/runtime/session ./internal/agent/runtime/conformance ./internal/agent/runtime/providers/codex ./internal/agent/runtime/providers/claude
go test ./internal/agent/... ./internal/app/discussionsession/... ./cmd/server/... -count=1
go test ./... -count=1
```

真实 CLI smoke 需要固定受支持版本和凭据，仅在受控 CI/本地环境运行；无凭据 CI 仍必须运行全部 captured fixture、differential 和 process fake tests。

## 完成标准

1. 本文件是唯一 Bridge 方案；AionCore commit 和本地忽略 reference checkout 可核验。
2. Go 代码明确对应 `aionui-session` 的 Connection、SessionBackend、Command/Event、ConversationSession、Orchestrator、Reducer 和 Capabilities。
3. Provider adapter 无权修改 FSM；所有状态只由单一 reducer 在 ordered fold lane 中产生。
4. pending FIFO、PromptAccepted、generation fence、cancel/crash、drain-before-EOF、pending permission、snapshot/lag recovery 与 reference 等价。
5. Codex parity matrix 全绿，真实 app-server fresh/multi-turn/cancel/steer/approval/resume smoke 通过。
6. Claude Code parity matrix 全绿，persistent CLI multi-turn/control/permission/resume/late-result/EOF smoke 通过。
7. Codex 和 Claude 完成前没有启动 PI、Device 或其他 Provider 的原生迁移。
8. 所有差异都在本文登记原因、mapping、compat impact 和 test evidence；不存在未经登记的“简化实现”。
9. 服务端没有新增 Rust、Cargo、FFI 或 AionCore 生产依赖；reference checkout 不进入发布产物。
10. 新 contract 不暴露 arbitrary invoke、raw Provider payload、credential、argv/env 或本地绝对路径。
11. legacy compatibility 迁移期保持单向和 telemetry，并在零调用后删除；同一 run 不双写，不存在两个事实源。
12. shared conformance、Provider fixtures、race、真实 CLI smoke、业务消费者和 production wiring 全部达到对应 Gate。
