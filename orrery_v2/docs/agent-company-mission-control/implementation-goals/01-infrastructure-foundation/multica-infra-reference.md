# Multica 基础设施借鉴清单

本文记录从 `multica/server` 源码中可借鉴的基础设施设计，以及在 Agent 公司 Mission Control 中应如何吸收。

原则：

```text
抄机制，不抄业务形态。
借鉴可靠运行、热路径优化、可降级设计、健康检查和测试支撑。
不继承 issue / comment / agent_task_queue 作为新系统核心模型。
```

## 1. 启动装配

Multica 参考点：

```text
multica/server/cmd/server/main.go
```

可借鉴：

- 进程启动时集中装配 DB、Redis、event bus、realtime hub、storage、analytics、service。
- Redis 是可选增强；无 Redis 时系统降级为单节点内存/DB 路径。
- 使用独立 Redis client 分担不同职责，避免 blocking consumer 影响请求路径。
- shutdown 时显式 stop relay、cancel context、close clients。

我们的采用方式：

```text
internal/platform/bootstrap
  LoadConfig
  OpenDB
  OpenRedis(optional)
  BuildInfraServices
  StartBackgroundWorkers
  Shutdown
```

## 2. 数据库与迁移

Multica 参考点：

```text
multica/server/cmd/migrate/main.go
multica/server/internal/migrations/migrations.go
multica/server/sqlc.yaml
multica/server/pkg/db/queries/*.sql
```

可借鉴：

- `schema_migrations(version, applied_at)`。
- `migrate up/down` 独立命令。
- migration 文件按后缀排序。
- readiness 检查最新 migration 是否已应用。
- sqlc 把 SQL 查询边界显式化。

我们的采用方式：

```text
internal/platform/migration
internal/platform/db
internal/platform/testkit
```

第一阶段可以用 SQLite，但仍保留 migration tracking、query 边界和测试 DB fixture。

## 3. Health / Readiness

Multica 参考点：

```text
multica/server/cmd/server/health.go
multica/server/cmd/server/health_realtime.go
```

可借鉴：

- `/health/live` 和 `/health/ready` 分离。
- ready 检查 DB ping 和最新 migration。
- readiness 有短 TTL 缓存，避免健康检查打爆 DB。
- metrics / realtime health endpoint 有访问控制。

我们的采用方式：

```text
internal/platform/health
  LiveHandler
  ReadyHandler
  ReadinessChecker
  CachedReadiness
```

## 4. Storage Adapter

Multica 参考点：

```text
multica/server/internal/storage/storage.go
multica/server/internal/storage/local.go
multica/server/internal/storage/s3.go
```

可借鉴：

- `Storage` interface。
- Local 和 S3 双实现。
- local storage 防路径穿越。
- sidecar metadata 保存原文件名和 content type。
- S3 支持 CDN domain、custom endpoint、path style。

我们的采用方式：

```text
internal/platform/filestore
  FileStore interface
  LocalFileStore
  S3FileStore
```

业务只保存 `ArtifactRef`，不直接保存本地路径。

## 5. Runtime Liveness

Multica 参考点：

```text
multica/server/internal/handler/runtime_liveness_store.go
multica/server/cmd/server/runtime_sweeper.go
```

可借鉴：

- Redis TTL key 表示 runtime heartbeat。
- DB 仍是权威状态。
- Redis 不可用时退回 DB stale window。
- sweeper 判断 stale runtime 前先查 Redis liveness。

我们的采用方式：

```text
internal/platform/execution
  LivenessStore
  NoopLivenessStore
  RedisLivenessStore
```

## 6. Negative Claim Cache

Multica 参考点：

```text
multica/server/internal/service/empty_claim_cache.go
```

可借鉴：

- 只缓存“没有任务可 claim”的负结果。
- 每个 runtime 有 version key。
- enqueue 时 bump version。
- claim 前读 version，DB 查空后 mark empty(observed version)。
- stale empty verdict 因 version 不匹配而失效。
- Redis 错误不阻塞业务。

我们的采用方式：

```text
internal/platform/execution
  NegativeClaimCache
  CurrentVersion
  MarkEmpty
  Bump
  IsEmpty
```

用于 AgentRun claim 热路径，减少 idle runtime / agent 对数据库的空轮询。

## 7. Claim / Lease / Sweeper

Multica 参考点：

```text
multica/server/pkg/db/queries/agent.sql
multica/server/cmd/server/runtime_sweeper.go
```

可借鉴：

- claim 使用原子 `UPDATE ... WHERE id = (SELECT ... FOR UPDATE SKIP LOCKED)`。
- 状态转移明确：queued / dispatched / running / completed / failed / cancelled。
- stale dispatched / running 有超时清理。
- queued 有 TTL 过期清理。
- sweeper 有 interval、batch size、timeout，不长事务扫全表。

我们的采用方式：

```text
internal/platform/execution
internal/platform/execution
internal/platform/execution
internal/platform/execution
internal/platform/execution
```

Stage 0 采用 Multica 的推进经验，但只迁移公共机制：`queued / dispatched / running / completed / failed / cancelled` 状态覆盖、lease、timeout、retry、cancel、queued TTL、stale recovery、batch sweeper。新系统不继承 `agent_task_queue` 的 `agent_id / issue_id / runtime_id` 等业务字段，后续业务对象应叫 `AgentRunLease` 或类似概念，并在业务层映射到平台 queue / claim 能力。

## 8. 日志与观测

Multica 参考点：

```text
multica/server/internal/logger/logger.go
multica/server/internal/analytics/client.go
multica/server/internal/analytics/posthog.go
```

可借鉴：

- slog 结构化日志。
- LOG_LEVEL 控制。
- request_id、user_id、client metadata 进入日志上下文。
- analytics Capture 非阻塞，bounded queue，满了 drop + counter。

我们的采用方式：

```text
internal/platform/logging
internal/platform/metrics
internal/platform/tracing
internal/platform/audit
```

关键原则：

```text
非关键观测不能阻塞业务。
关键审计必须进入 EventLedger 或 AuditLog。
```

## 9. 后台任务

Multica 参考点：

```text
runtime_sweeper.go
backfill_task_usage_daily
backfill_task_usage_dashboard_daily
```

可借鉴：

- 后台 worker 和 HTTP server 解耦。
- sweeper / backfill 有独立入口或独立 runner。
- 批处理有 batch size。
- 错误记录后下一轮继续。

我们的采用方式：

```text
internal/platform/execution
internal/platform/execution
cmd/backfill_*
```

## 10. 不采用的部分

不采用：

```text
issue-centered schema
comment as collaboration source
agent_task_queue as Agent lifecycle
runtime row as Agent identity
frontend timeline as progress source
```

原因：

```text
我们的目标是 Agent 公司运行时。
Mission、AgentInstance、TaskContract、AgentResult、MissionEvent 才是核心。
Multica 的基础设施机制有价值，但业务骨架不应继承。
```
