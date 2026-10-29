# Goal 01：基础设施公共服务

本 goal 是 Agent 公司 Mission Control 后端的公共平台层。它不是业务模块，也不是从 0 手写一套基础设施；它要在目标目录结构下复用、迁入或改造 Orrery / Multica 已有能力，形成后续业务模块统一依赖的 `internal/platform/*`。

## 全局要求

实现本子目标前必须先遵守 [implementation-goals/README.md](../README.md)、[project-structure-guidelines.md](../../project-structure-guidelines.md) 和 [module-decomposition-and-implementation-guide.md](../module-decomposition-and-implementation-guide.md) 的全局要求。本文只补充本 goal 的职责和验收，不允许覆盖全局结构、生产级质量、复用优先、Stage 0 基础设施使用约定或跨 goal 边界。

Stage 0 尤其必须遵守优雅解耦与迁移优先原则：解耦不是为了把基础设施拆成更多小包，而是为了让业务模块可以稳定、清晰、无感地使用公共能力。任何 logging、config、db、migration、uow、execution、filestore、S3、health、liveness、sweeper、metrics、tracing、testkit 能力实现前，必须先比较 Multica / 原 Orrery 是否已有成熟实现；能直接复制或移动迁入后修改的，优先迁移适配。没有可迁移实现时，优先使用成熟包或标准库；自研必须说明取舍。

## 顶层设计

Stage 0 解决的问题是：后续 Agent、Mission、Task、Discussion、QA、Runtime、API 都需要同一套数据库、分库存储、迁移、事务、事件存储、文件、队列、锁、日志、配置、密钥、观测和测试夹具。如果这些能力分散到各业务目录，后面会出现重复连接数据库、重复事务包装、重复队列语义、重复日志格式和不可追踪的失败。

因此本 goal 只建设平台公共服务：

```text
业务模块依赖 platform 接口。
platform 不依赖业务模块。
platform 可以复用现有实现，但必须去掉业务语义。
platform 的拆分必须服务稳定契约、可测试性、可替换性和可读性；不为了拆包而拆包。
```

Stage 0 完成后，后续业务 goal 应能通过 `internal/platform/*` 获得统一基础设施，而不需要在 `internal/agent`、`internal/mission`、`internal/app`、`internal/transport` 里重复实现这些能力。

## 生产级职责边界

本 goal 负责：

```text
logging / config / secret
clock / idgen
db / migration / uow
db registry / named stores
eventstore generic envelope storage
outbox
execution runtime infrastructure
filestore / artifact reference
lock / claim / liveness / sweeper
metrics / tracing
health live / ready
testkit
```

本 goal 不负责：

```text
Mission / Agent / TaskContract 业务模型
Agent 生命周期 signal
MissionEvent 业务语义
BaseAgent / AgentAssembler
RuntimeAdapter
Task delivery / QA
DiscussionGroup / mention / inbox
HTTP / WebSocket transport
Frontend Mission Control
旧 comment / task queue / timeline 兼容
```

禁止事项：

```text
platform import internal/agent、internal/mission、internal/app、internal/transport。
EventStore 在本阶段解释 MissionEvent。
Queue / Claim 在本阶段定义 AgentRun 业务状态。
FileStore 返回本地绝对路径作为业务持久契约。
业务代码直接读取 os.Getenv、直接 open DB、直接创建事务或直接写日志到 stdout。
```

## 中层模块规划

目标目录：

```text
internal/platform/logging
internal/platform/config
internal/platform/secret
internal/platform/clock
internal/platform/idgen
internal/platform/db
internal/platform/migration
internal/platform/uow
internal/platform/eventstore
internal/platform/outbox
internal/platform/execution
internal/platform/filestore
internal/platform/lock
internal/platform/metrics
internal/platform/tracing
internal/platform/health
internal/platform/testkit
```

建议每个包按职责拆分文件：

```text
types.go       接口、DTO、枚举、配置类型
errors.go      稳定错误码和错误包装
store.go       持久化实现
service.go     核心服务逻辑
testkit.go     测试夹具或 fake
*_test.go      单元测试、不变量测试、错误路径测试
```

公共错误类型至少覆盖：

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

公共日志字段至少覆盖：

```text
level
component
operation
correlation_id
duration_ms
error_code
mission_id
agent_id
task_id
run_id
attempt
status
```

## 底层实现指导

### logging / config / secret

`logging` 必须最先实现。所有后续平台模块都使用统一 logger，禁止 `fmt.Println` 作为业务日志。

`config` 负责普通配置，如端口、数据库路径、分库 URL、队列参数、文件存储路径和运行模式。`secret` 负责 token、API key、OAuth credential、webhook secret、CLI credential。Secret 不得进入 config dump，也不得出现在日志中。

实现要求：

```text
Logger 支持 debug / info / warn / error。
Logger 支持 WithFields / WithContext。
Logger 支持控制台可读文本日志与 JSON lines 文件日志双路输出。
文件日志负责长期审计与问题追踪，控制台日志负责本地开发、容器 stdout 和运维实时观察。
两份日志都必须应用相同字段、correlation_id 和敏感字段 redaction。
Config 支持严格 YAML、测试覆盖和受控 secret 注入；非敏感实例配置不接受 env 覆盖。
Config 必须支持 section 注册机制；平台自身的 database / logging / filestore / queue / lock / liveness / metrics / sweeper / health 是第一批注册 section，用来验证解码、校验和热加载发布流程。
业务模块用自己的 struct 注册并解码自己的 YAML section，不把 Mission / Agent / Worker / RuntimeAdapter 等业务配置字段塞进平台 Config。
Secret Provider 支持内存实现、红acted 值和缺失密钥错误。
```

### db / migration / uow

`db` 只负责连接、ping、关闭、连接参数和 named database registry。SQLite 明确存在单库写锁竞争，因此 Stage 0 必须支持按逻辑角色分库存储，至少覆盖 `core / events / jobs / runtime / observability`。`migration` 负责 schema 版本追踪。`uow` 负责单库内事务边界。

实现要求：

```text
文件数据库必须是 SQLite-backed，不允许用 JSON 文件伪装数据库。
Config 通过 YAML `database.urls` 提供命名数据库 URL；env 只注入 `ORRERY_DATABASE_PASSWORD`。
DB registry 能打开 core / events / jobs / runtime / observability 等命名库。
EventStore 默认使用 events 库；outbox / queue / claim / liveness 后续使用 jobs / runtime 库。
schema_migrations(version, applied_at) 记录已应用迁移。
平台内置 migration 必须通过 registry 注册到对应 database role：events 承载 EventStore，jobs 承载 Outbox / Queue，runtime 承载 Lock / Claim / Liveness。
业务模块只注册自己的 migration / initializer 和目标 database role；基础设施负责排序、幂等、重复版本拒绝、事务回滚、schema_migrations 记录和 readiness 检查。
UnitOfWork.Do 成功提交，失败回滚。
同一 UOW 内的业务状态、eventstore append、outbox enqueue 必须能共享事务。
readiness 检查 DB ping 和最新 migration；liveness 不检查 migration。
```

### eventstore / outbox / execution

`eventstore` 在 Stage 0 只保存通用 envelope，不解释业务事件。

基础 envelope：

```text
event_id
stream_id
stream_version
event_type
event_version
payload_json
metadata_json
idempotency_key
correlation_id
created_at
```

`outbox` 用于事务后异步投递，支持 pending、claimed、done、retry_scheduled、dead_letter。

`execution` 统一承载通用 durable queue、job runner、claim lease、negative claim cache、liveness heartbeat 和 sweeper runner。它是真实合并后的运行基础设施包，不再拆成 `queue / claim / liveness / sweeper` 多个 package；包内用 `queue.go`、`queue_sqlite.go`、`claim_sqlite.go`、`liveness_sqlite.go`、`sweeper.go` 等文件拆职责。

`execution` 不承载 AgentRun 语义。它必须吸收 Multica 的任务推进经验，但只保留平台通用状态机：`queued -> dispatched -> running -> completed`，以及 `failed / cancelled` 终态。claim 只能表示任务已派发给某个 worker，不能直接等价为任务已经开始执行；runner 必须显式 `Start` 后才调用 handler。这样后续 Agent、Mission、Runtime 可以在业务层定义自己的 Run / Task / Assignment，但底层 lease、timeout、retry、cancel、recovery 不会重复发明。

实现要求：

```text
append 支持 stream version 冲突检测。
append 支持 idempotency key。
outbox claim 必须原子。
retry 必须记录 attempt、next_attempt_at、last_error。
execution queue claim 必须原子，claim 后状态为 dispatched 并记录 worker_id、lease_until、dispatched_at。
execution queue start 只允许 owner 将 dispatched 转为 running，并记录 started_at。
execution queue complete 只允许 owner 完成 running job。
execution queue fail 支持 retry，重试耗尽进入 failed 并记录 failure_reason。
execution queue cancel 覆盖 queued / dispatched / running，并进入 cancelled。
execution queue recovery 必须区分 stale dispatched 和 stale running：dispatched 超时回到 queued，running 超时进入 failed(timeout)。
execution queue queued TTL 必须能按 batch 过期为 failed(queued_expired)。
job runner 不阻塞业务请求，并通过 claim -> start -> handler -> complete/fail 推进。
```

### filestore / lock / claim / liveness / sweeper

`filestore` 保存 artifact 并返回 `ArtifactRef`，业务层只保存引用和 checksum，不保存本地绝对路径。

`lock` 提供通用资源互斥执行，仍是独立包，不合入 execution。`execution` 内部提供运行执行循环使用的 claim lease、negative claim cache、liveness heartbeat 和 sweeper runner。业务侧默认通过 `execution` 取得这些能力，不能在业务目录重新拆出私有 queue / claim / heartbeat / sweeper 基础设施。

实现要求：

```text
FileStore Put 返回 artifact_id、content_type、size、checksum、created_at。
FileStore Get 校验 checksum。
Lock 支持 TTL 和过期后重新获取。
NegativeClaimCache 通过 version bump 避免 stale empty verdict。
Sweeper 有 batch size、timeout、错误日志，不做无限全表长事务扫描。
```

### metrics / tracing / health / testkit

metrics 和 tracing 是非关键观测路径，不得阻塞业务。health 必须区分 live 和 ready，并允许 ready 短 TTL 缓存。

testkit 必须给后续 goal 提供确定性夹具：

```text
NewTestLogger
NewTestConsoleAndFileLogger
NewTestConfig
NewTestDB
NewTestDatabases
NewTestUnitOfWork
NewTestEventStore
NewTestFileStore
NewFakeClock
NewDeterministicIDGenerator
```

## 现有实现复用计划

Stage 0 明确允许复用既有实现。复用判断以目标边界为准，不以旧项目目录为准。

| 来源 | 可复用资产 | 采用方式 | 去耦要求 |
| --- | --- | --- | --- |
| Multica | `cmd/migrate`、migration tracking、DB readiness | 迁入机制或重写为 `internal/platform/migration` / `db` | 不继承业务 schema |
| Multica | health live / ready、readiness TTL | 迁入机制到 `internal/platform/health` | health 不依赖旧 handler 结构 |
| Multica | storage local / S3 adapter | 改名切分为 `filestore` | 返回 ArtifactRef，不暴露绝对路径 |
| Multica | runtime liveness store | 改造成 `platform/liveness` | 不继承 runtime row 业务语义 |
| Multica | negative claim cache | 改造成 `platform/claim` | 用通用 version / empty verdict，不绑定 agent_task_queue |
| Multica | sweeper / batch worker | 改造成 `platform/sweeper` | 不继承旧状态名，业务状态由后续 goal 定义 |
| Multica | slog / analytics bounded queue | 迁入 logging / metrics 机制 | 不输出 secret，不阻塞业务 |
| Orrery | 现有 DB、任务、QA、evidence 使用经验 | 作为接口需求参考 | 不让旧表结构决定平台接口 |

若直接迁入代码，必须在对应包 README 或注释中记录来源路径、改动点和被删除的旧业务概念。

## 运行流程

推荐启动装配顺序：

```text
Load Config
-> Build Logger
-> Build Secret Provider
-> Open DB
-> Run / Check Migration
-> Build UnitOfWork
-> Build EventStore / Outbox / Queue
-> Build FileStore / Lock / Claim / Liveness
-> Build Metrics / Tracing
-> Build Health Checks
-> Build Testkit fixtures for tests
```

推荐事务流程：

```text
UnitOfWork.Do
-> domain callback 写业务状态
-> append generic event envelope
-> enqueue outbox job
-> callback nil: commit
-> callback error: rollback all writes
```

推荐后台清理流程：

```text
Sweeper tick
-> claim bounded batch
-> check liveness / expiration
-> mark stale lease or stale job
-> log summary
-> continue next tick even when one batch fails
```

## 测试与验收

BDD 来源：[stage-0-bdd.md](./stage-0-bdd.md)。

覆盖证明：[stage-0-bdd-coverage.md](./stage-0-bdd-coverage.md)。

实现计划：[stage-0-implementation-plan.md](./stage-0-implementation-plan.md)。

完成时必须证明：

```text
go test ./internal/platform/...
go test ./...
stage-0-bdd-coverage.md 中每个场景都有测试或审计证据
platform 不 import internal/agent、internal/mission、internal/app、internal/transport
README 中列出的 testkit 夹具可以被后续 goal 直接使用
```

## CROSS_GOAL

```text
CROSS_GOAL: 02-core-contracts
需要：后续契约模型使用 platform error、idgen、clock、testkit。
当前假设：Stage 0 不定义 Mission / Agent / TaskContract 类型。
后续约定：业务 ID 类型在 core contracts 定义，底层 ID generator 只提供字符串或通用 ID。
```

```text
CROSS_GOAL: 04-event-ledger-projection
需要：EventStore 通用 envelope、stream version、idempotency、outbox。
当前假设：Stage 0 只保存 envelope，不解释 MissionEvent。
后续约定：MissionEvent schema、projection 和 replay 由 Goal 04 定义。
```

```text
CROSS_GOAL: 07-base-agent-runtime
需要：queue、claim、liveness、sweeper、filestore、logger、config、secret。
当前假设：Stage 0 提供通用 lease / job / heartbeat 能力。
后续约定：AgentRunLease、RuntimeDaemon、RuntimeAdapter 语义由 Goal 07 定义。
```
