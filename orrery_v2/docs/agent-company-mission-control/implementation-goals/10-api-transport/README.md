# Goal 10：API Transport 与前端服务出口

本 goal 把后端 app service 安全、稳定地暴露给前端、CLI、自动化客户端和外部集成。它不是业务层，不定义 Mission、Task、Agent、Memory、Discussion 的业务语义；它只做协议适配、鉴权入口、请求绑定、响应编码、错误映射、流式订阅和兼容壳迁移。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

## Stage 0 基础设施使用约定

本 goal 必须遵守 [Stage 0 基础设施使用约定](../module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)。Transport 负责协议适配和 app service 出口，不直接创建 DB、FileStore、queue、logger、config manager 或 runtime 资源。服务启动使用 platform runtime assembly；配置热加载以 `ConfigManager` 内存快照为事实源，HTTP / WebSocket 组件只在明确可热更新的连接参数、日志级别或 stream policy 上响应订阅通知。

## 顶层设计

Transport 的核心原则是“薄出口、厚 app service”：

```text
HTTP / WebSocket request
-> transport auth / request binding / validation shape
-> internal/app/missioncontrol or internal/app/resourceadmin
-> ResponseDTO / PatchDTO / ErrorResponse
-> client
```

Transport 不允许直接读取 repository、SQL、eventstore、projection store、runtime adapter、agent queue 或 memory store。前端只能消费 app service 暴露的 DTO / patch，不能消费 MissionEvent 原始事件、projection row 或 domain struct。

本 goal 需要继续复用 Orrery 已有 handler、comment、inbox、QA、mission HTTP、runtime liveness 等实现经验，但必须把旧 handler 里的业务逻辑下沉到 app service 或 domain service。保留可迁移的 route、auth、request parsing、测试样例和错误映射经验，不保留 handler 直接写库、直接操作队列、直接拼业务状态的做法。

## 生产级职责边界

本 goal 负责：

```text
RouteRegistry
HTTPHandler
RequestDTO
ResponseDTO
ErrorResponse
ErrorMapper
AuthContextMapper
PaginationDTO
IdempotencyKey
ETag / version token
WebSocketHub
MissionSubscription
PatchEnvelope
PatchEncoder
StreamBackpressurePolicy
SSE fallback, if needed
Transport middleware
OpenAPI / contract snapshot, if adopted
```

本 goal 不负责：

```text
Mission domain model
Task lifecycle state machine
MissionEvent append rules
Projection build rules
Discussion business rules
Inbox business rules
QA policy
Runtime execution
Memory retrieval
Frontend component implementation
Legacy issue/comment compatibility semantics
```

禁止事项：

```text
Handler import repository / SQL generated query / eventstore / projection store.
Handler import runtime adapter or daemon package.
Handler construct MissionEvent directly.
Handler reuse domain struct as JSON response.
WebSocket handler self-subscribe eventstore and self-project patches.
Frontend-only status field appears first in transport DTO without app service owner.
Route handler directly enqueue old agent_task_queue.
Transport exposes internal trace by default.
```

## 中层模块规划

推荐目录：

```text
internal/transport/http/
  routes.go
  middleware.go
  auth_context.go
  request.go
  response.go
  error_mapper.go
  pagination.go
  idempotency.go
  mission_control_handler.go
  mission_command_handler.go
  task_handler.go
  discussion_handler.go
  inbox_handler.go
  qa_handler.go
  resource_admin_handler.go
  health_handler.go

internal/transport/websocket/
  hub.go
  client.go
  auth_context.go
  mission_subscription.go
  patch_encoder.go
  backpressure.go
  heartbeat.go
  close_reason.go

internal/app/missioncontrol/
  query_service.go
  command_service.go
  stream_service.go
  dto.go
  patch.go
  errors.go

internal/app/resourceadmin/
  query_service.go
  command_service.go
  dto.go
  errors.go
```

依赖方向：

```text
transport/http -> app/missioncontrol, app/resourceadmin, platform/auth/logging
transport/websocket -> app/missioncontrol stream service, platform/auth/logging
app/missioncontrol -> mission domain, projection query, discussion facade, inbox facade
transport never -> repository, eventstore, runtime, queue, memory store
```

HTTP Route 分组：

```text
GET    /api/missions
POST   /api/missions
GET    /api/missions/{mission_id}/control
GET    /api/missions/{mission_id}/team
GET    /api/missions/{mission_id}/tasks
GET    /api/missions/{mission_id}/progress
GET    /api/missions/{mission_id}/evidence
GET    /api/missions/{mission_id}/qa
GET    /api/missions/{mission_id}/discussion
POST   /api/missions/{mission_id}/tasks
POST   /api/missions/{mission_id}/tasks/{task_id}/assign
POST   /api/missions/{mission_id}/tasks/{task_id}/submit-result
POST   /api/missions/{mission_id}/qa/{qa_id}/decision
POST   /api/missions/{mission_id}/discussion/messages
POST   /api/missions/{mission_id}/discussion/messages/{message_id}/reply
GET    /api/inbox
POST   /api/inbox/{item_id}/read
POST   /api/inbox/{item_id}/close
GET    /api/resources/tools
GET    /api/resources/skills
GET    /api/resources/mcp-servers
GET    /api/resources/memory-profiles
```

WebSocket Route：

```text
WS /api/missions/{mission_id}/stream
```

客户端发送：

```text
subscribe { mission_id, view_names[], cursor, patch_version }
unsubscribe { subscription_id }
ack { subscription_id, patch_id }
ping
```

服务端发送：

```text
snapshot { subscription_id, MissionControlViewDTO, version }
patch { subscription_id, MissionControlPatchEnvelope, version }
heartbeat { server_time }
error { code, message, correlation_id, retry_after_ms? }
complete { reason }
```

## 底层实现指导

### RequestDTO / ResponseDTO

DTO 是 transport contract，不是 domain struct：

```text
MissionControlViewDTO {
  mission
  team
  task_board
  progress_graph
  deliverables
  evidence
  qa
  discussion
  inbox_summary
  permissions
  version
}
```

DTO 需要满足：

```text
只包含前端或客户端需要的字段。
所有 ID 使用稳定字符串格式。
时间统一 RFC3339。
枚举统一小写 snake_case。
权限裁剪后再返回。
内部 trace、raw runtime output、secret、full memory 默认不返回。
domain error 映射为稳定 error code。
```

RequestDTO 需要包含：

```text
request_id
idempotency_key, for mutating commands
expected_version, when command depends on current view
actor context from AuthContextMapper
payload
```

### ErrorResponse

统一错误格式：

```text
ErrorResponse {
  code
  message
  details
  correlation_id
  retryable
}
```

推荐错误码：

```text
invalid_request
unauthorized
forbidden
not_found
conflict
version_conflict
idempotency_conflict
rate_limited
resource_unavailable
app_rule_denied
runtime_unavailable
internal_error
```

Transport 只能把 app error 映射到 ErrorResponse，不能在 handler 内新增业务判定。

### AuthContextMapper

AuthContextMapper 负责把 HTTP / WebSocket 鉴权结果转换为 app service 的 Viewer / Actor：

```text
user_id
workspace_id
agent_id, optional
mission_id, optional
roles
scopes
request_origin
```

权限最终仍由 app service / domain policy 判断；transport middleware 只做基础认证、scope 初筛和审计字段注入。

### WebSocket PatchEnvelope

PatchEnvelope 不暴露 MissionEvent：

```text
MissionControlPatchEnvelope {
  patch_id
  mission_id
  base_version
  next_version
  view_name
  operations[]
  generated_at
}
```

operation 建议使用稳定 view patch，不使用前端组件内部路径：

```text
replace_mission_summary
upsert_team_member
remove_team_member
upsert_task_card
move_task_card
upsert_progress_node
upsert_progress_edge
upsert_evidence
upsert_qa_item
append_discussion_message
upsert_inbox_item
```

Backpressure 策略：

```text
客户端 ack 落后超过阈值时发送 compact snapshot。
单客户端队列超过阈值时丢弃中间 patch，保留 latest snapshot token。
心跳超时关闭连接。
所有 stream 错误带 correlation_id。
```

### OpenAPI / Contract Snapshot

如果采用 OpenAPI，应由 DTO 生成或手写在 transport 层维护：

```text
docs/api/mission-control.openapi.yaml
```

契约测试必须证明：

```text
DTO 字段稳定。
错误码稳定。
patch envelope 版本兼容。
frontend mock fixture 与真实 DTO schema 对齐。
```

## 现有实现复用计划

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Orrery | `backend/internal/handler/mission.go`、`mission_test.go` | 迁移 route shape、request parsing、mission 列表/详情测试经验 | handler 不再直接拼 Mission 状态 |
| Orrery | `backend/internal/handler/pm_tools_mission_task_http.go`、`pm_tools_mission_context_http.go` | 复用任务/上下文 HTTP 边界经验 | PM tool 语义下沉到 app command service |
| Orrery | `backend/internal/handler/comment.go`、`comment_*_test.go` | 复用消息、reply、mention 的 HTTP 入口和测试样例 | comment 映射为 Discussion API，不保留 issue-centered 字段 |
| Orrery | `backend/internal/handler/inbox.go`、inbox migrations / queries | 复用 inbox 列表、read、close 的出口经验 | recipient 使用 Agent/User/Mission role refs |
| Orrery | `backend/internal/handler/qa.go`、`qa_gate_test.go` | 复用 QA decision HTTP 样例 | QA 规则由 Goal 06 app service 提供 |
| Orrery | `backend/internal/handler/runtime_liveness_store.go` | 复用健康检查和 liveness 查询出口经验 | runtime 状态只作为平台 health，不作为 Mission fact |
| Orrery | `backend/internal/missioncontract/pm_tool_handler_boundary_test.go` | 作为 transport boundary 测试参考 | 扩展为 handler 禁止 repository/eventstore/runtime import |
| Orrery | `src/lib/app/missionRoutes.ts` | 作为前端 route 命名参考 | 最终 route 以 Goal 10 DTO 为准 |
| Multica | daemon health、session status、stderr tail 的 API 经验 | 复用为 runtime health / diagnostics endpoint 参考 | 不暴露 provider 内部 session 为 Mission 状态 |

迁移说明需要记录在：

```text
internal/transport/http/migration_notes/orrery.md
internal/transport/http/migration_notes/multica.md
internal/transport/websocket/migration_notes/orrery.md
```

说明每条旧 route 是否保留、重命名、删除、转发到 app service，及对应契约测试。

## 运行流程

读取 Mission Control：

```text
GET /api/missions/{mission_id}/control
-> auth middleware
-> AuthContextMapper
-> bind MissionControlRequestDTO
-> MissionControlQueryService.GetControlView
-> trim by permissions
-> encode MissionControlViewDTO
```

提交 Worker 结果：

```text
POST /api/missions/{mission_id}/tasks/{task_id}/submit-result
-> auth middleware
-> bind SubmitResultRequestDTO
-> validate request shape
-> MissionControlCommandService.SubmitAgentResult
-> app service calls TaskDelivery / QA / EventSink
-> return command receipt DTO
```

WebSocket 订阅：

```text
client connects
-> auth handshake
-> subscribe mission stream
-> stream service sends snapshot
-> projection/app patch source emits patch
-> hub encodes PatchEnvelope
-> client ack
-> compact snapshot if lagging
```

错误处理：

```text
app returns typed error
-> ErrorMapper
-> log correlation_id
-> ErrorResponse with stable code
-> never leak raw SQL/runtime/secret/internal trace
```

## 测试与验收

建议测试文件：

```text
internal/transport/http/mission_control_handler_test.go
internal/transport/http/task_handler_test.go
internal/transport/http/discussion_handler_test.go
internal/transport/http/inbox_handler_test.go
internal/transport/http/error_mapper_test.go
internal/transport/http/boundary_import_test.go
internal/transport/websocket/mission_subscription_test.go
internal/transport/websocket/backpressure_test.go
internal/transport/websocket/patch_encoder_test.go
internal/app/missioncontrol/dto_contract_test.go
```

独立验收场景：

1. HTTP handler 可以调用 fake MissionControlQueryService 返回 MissionControlViewDTO。
2. HTTP handler 可以调用 fake MissionControlCommandService 提交 task result。
3. app error 可以映射为稳定 ErrorResponse。
4. 鉴权上下文可以映射为 app service Viewer / Actor。
5. Handler 不直接依赖 repository / SQL / eventstore / projection store / runtime adapter。
6. ResponseDTO 不复用 domain struct。
7. WebSocket 可以订阅 fake MissionControlStreamService。
8. WebSocket patch 不暴露 MissionEvent 原始事件。
9. 客户端 ack 落后时触发 compact snapshot。
10. 内部 trace、raw runtime output、secret、full memory 默认不出现在 DTO。
11. 旧 Orrery route 迁移后有兼容或明确删除说明。
12. OpenAPI / fixture 与 DTO schema 一致。

完成证明：

```text
go test ./internal/transport/http ./internal/transport/websocket ./internal/app/missioncontrol
rg "internal/repository|pkg/db|eventstore|projection|runtime/.*adapter|agent_task_queue" orrery_v2/internal/transport returns only migration_notes or tests that assert absence
rg "MissionEvent" orrery_v2/internal/transport returns only comments/tests about forbidden exposure
```

## CROSS_GOAL

```text
CROSS_GOAL: 04-event-ledger-projection
需要：MissionControlQueryService、MissionControlStreamService、MissionControlViewDTO、MissionControlPatchEnvelope。
当前假设：transport 只依赖 app service interface。
后续约定：DTO 版本、分页、patch envelope、错误码、权限裁剪。
```

```text
CROSS_GOAL: 06-task-delivery-qa
需要：SubmitAgentResult、QA decision、ReworkRequest 的 command service。
当前假设：transport 不直接执行 QA。
后续约定：提交结果的幂等键、QA decision 错误码。
```

```text
CROSS_GOAL: 09-discussion-inbox
需要：DiscussionView、InboxView、Message / Reply / Mention command DTO。
当前假设：discussion 和 inbox 通过 app service facade 暴露。
后续约定：未读、已读、已响应、过期、关闭状态的 DTO 枚举。
```

```text
CROSS_GOAL: 99-deferred-frontend-mission-control
需要：前端最终消费的 HTTP / WebSocket 契约。
当前假设：本 goal 先稳定后端出口，前端可以继续 mock。
后续约定：接口命名、状态枚举、增量刷新策略、contract fixture。
```
