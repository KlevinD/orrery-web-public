# Stage 0 Goal：基础设施公共服务

## Goal

在严格遵守 [项目结构设计](../../project-structure-guidelines.md) 的前提下，实现 Agent 公司 Mission Control 的 Stage 0：基础设施公共服务。

本阶段只建设后端公共基础设施，不实现 Agent、Mission、Task、Discussion、QA、RuntimeAdapter 等业务语义。实现完成后，后续业务阶段应能通过 `internal/platform/*` 暴露的接口和 testkit 获得统一的日志、配置、SQLite-backed 数据库、分库存储 registry、迁移、事务、事件存储、outbox、队列、文件存储、锁、claim、liveness、sweeper、metrics、tracing、health check 和测试夹具。

BDD 验收标准见 [stage-0-bdd.md](./stage-0-bdd.md)。

## 公共结构约束

代码结构必须以 [项目结构设计](../../project-structure-guidelines.md) 为最高依据：

```text
internal/platform/*
```

Stage 0 代码不得散落到 `internal/agent`、`internal/mission`、`internal/app`、`internal/transport` 等业务目录，也不得让 `internal/platform` import 这些业务目录。

必须遵守：

1. 基础设施统一归集到 `internal/platform/*`。
2. `platform` 只提供公共能力，不承载 Mission / Agent 业务语义。
3. 业务模块不得私自创建数据库连接、事务、队列、文件存储或日志体系。
4. 单文件代码必须小于 1000 行，超过 800 行应主动拆分。
5. 类型定义、接口、实现、错误、测试夹具必须分文件组织。
6. `logging / config` 必须最先实现，后续平台模块必须使用统一 logger。
7. 禁止 MVP 化或凑合式实现；Stage 0 必须交付生产级基础设施切片，覆盖错误处理、日志、测试、扩展点、替换实现和失败恢复。
8. Config 与 Secret 必须分离，业务代码不得直接读取环境变量。
9. Config 只定义平台基础设施的公共字段；平台自身的 database、logging、filestore、queue、lock、liveness、metrics、sweeper、health 等 section 必须作为第一批注册者，用来验证配置注册、解码、校验和热加载发布机制。
10. Mission、Agent、Worker、RuntimeAdapter 等业务模块必须定义自己的配置结构，并通过 YAML section / dotted lookup / DecodeSection 一类扩展入口注册、校验和读取，不能把业务字段塞进平台 Config。
11. EventStore 只提供可承载事件账本的存储基础，不定义 MissionEvent 业务语义。
12. Queue / Claim / Liveness / Sweeper 只提供公共运行基础能力，不定义 Agent runtime 业务模型。

## 实现范围

按 [项目结构设计](../../project-structure-guidelines.md) 中的平台层要求建设：

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

## 实现顺序

Stage 0 内部按下面顺序推进：

1. Logger
2. Config
3. Secret
4. Clock / ID Generator
5. DB / Migration
6. UnitOfWork / Transaction
7. Health Check
8. EventStore storage foundation
9. Outbox
10. Queue / JobRunner
11. FileStore / ArtifactStore
12. Lock
13. Claim / Runtime lease foundation
14. Liveness Store
15. Sweeper / Reaper
16. Metrics / Tracing
17. Test Harness

## 验收要求

Stage 0 完成时必须满足：

1. Logger 支持 `debug / info / warn / error`、结构化字段、`component`、`correlation_id`、`operation`、`duration_ms`、`error_code`，并支持控制台可读日志与 JSON lines 文件日志双路输出。
2. Config 与 Secret 分离，Config 支持 YAML 加载、dotted 速查接口、section 注册、注册 section 解码/校验、热加载和业务扩展 section 解码；平台 Config 只承载基础设施字段，平台自身配置是首批注册 section，后续业务模块不直接读取环境变量，也不要求平台提前定义自己的业务配置字段。
3. 可以创建 SQLite-backed 测试数据库并执行 migration。
4. 数据库层支持 named registry 分库存储，至少能隔离 `core / events / jobs / runtime / observability` 等逻辑库，避免所有高写入模块挤在同一个 SQLite 文件写锁上。
5. Migration 层提供平台内置迁移注册表，把 EventStore、Outbox / Queue、Lock / Claim / Liveness 等基础设施表按 role 注册到 `events / jobs / runtime` 等分库；后续业务模块只注册自己的 migration / initializer，执行顺序、幂等、重复版本拒绝、错误回滚和 readiness 检查由基础设施负责。
6. readiness 能检查 DB 与最新 migration 状态，live 与 ready 分离。
7. UnitOfWork 成功时提交事务，失败时回滚事务。
8. EventStore 可以 append / load 通用 event envelope，并支持 stream version、idempotency key、correlation id。
9. Outbox 可以 enqueue、claim、complete、fail、retry、dead letter。
10. Execution 包具备稳定接口和 fake / test 实现，在一个包内按文件承载 durable queue、job runner、claim lease、negative claim cache、liveness heartbeat 和 sweeper runner；durable queue 必须覆盖 `queued / dispatched / running / completed / failed / cancelled`，并提供 claim、start、complete、fail、retry、cancel、timeout recovery、queued TTL 过期等通用状态机能力。
11. FileStore 可以保存 artifact 并返回引用，业务层不依赖本地绝对路径；本地 FileStore 复用 Multica 的 sidecar、GetReader、DeleteKeys、路径穿越防护能力，并提供可选 S3 adapter。
12. Lock 独立表达通用互斥资源；Execution 表达原子 claim、heartbeat、stale lease 回收和 sweeper。业务模块优先通过 `internal/platform/execution` 取得 Queue、Claim、Liveness、Sweeper 能力，不再分别 import 或重建 `queue / claim / liveness / sweeper` 包。
13. Negative claim cache 能通过 version bump 避免 stale empty verdict。
14. Sweeper 能按 batch 清理 stale lease / stale job，失败不导致进程崩溃。
15. Metrics / tracing 的非关键队列不能阻塞业务请求。
16. Test Harness 提供 `NewTestLogger`、`NewTestConsoleAndFileLogger`、`NewTestConfig`、`NewTestDB`、`NewTestDatabases`、`NewTestUnitOfWork`、`NewTestEventStore`、`NewTestFileStore`、`NewFakeClock`、`NewDeterministicIDGenerator`。
17. 提供单元测试、关键不变量测试、错误路径测试。
18. 不引入 Agent / Mission / Task / Discussion / QA / RuntimeAdapter 的业务实现。

## BDD 验收入口

实现与测试必须覆盖 [stage-0-bdd.md](./stage-0-bdd.md) 中的场景。每个场景可以对应一个或多个 Go 测试，但测试名称应能追溯到 BDD 场景含义。

## 完成证明

完成后应能证明：

```text
后续业务阶段可以只依赖 internal/platform 暴露的接口和 testkit，
完成 SQLite-backed 数据库、分库存储 registry、迁移、事务、事件存储、outbox、队列、文件、锁、
日志、配置、健康检查和测试环境搭建，
而不需要在业务模块中重复实现基础设施。
```

## 非目标

Stage 0 不做：

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
