# Stage 0 BDD：基础设施公共服务验收

本文定义 Stage 0 的行为驱动验收标准。实现时以 [项目结构设计](../../project-structure-guidelines.md) 为最高结构约束，以本文件作为验收用例来源。

## Feature: 统一日志与配置

### Scenario: 日志携带结构化上下文

```gherkin
Given 一个使用 platform logger 的组件
And context 中存在 correlation_id
When 组件记录一次 info 日志
Then 日志必须包含 level、component、operation、correlation_id
And 可以附加 duration_ms、error_code、mission_id、agent_id、task_id、run_id 字段
And 日志不得输出 token、secret 或完整隐私 payload
```

### Scenario: 日志同时输出控制台和文件日志

```gherkin
Given 一个生产装配的 platform logger
And 配置了控制台输出与文件日志输出
When 组件记录一次 warn 或 error 日志
Then 控制台日志必须是开发者可读的文本格式
And 文件日志必须是可机器解析的 JSON lines 格式
And 两份日志都必须包含 component、operation、correlation_id 和 error_code
And 文件日志路径的父目录不存在时应自动创建
And 敏感字段在控制台和文件日志中都必须被 redacted
```

### Scenario: 业务代码不直接读取环境变量

```gherkin
Given 一个需要数据库路径、文件存储路径或运行模式的模块
When 模块初始化
Then 它必须通过 platform config 获取配置
And 不得直接调用 os.Getenv 读取业务配置
And 测试可以覆盖配置值
```

### Scenario: 配置与密钥分离

```gherkin
Given 系统同时需要普通配置和 API key
When 组件读取这些值
Then 端口、数据库路径、队列参数等普通配置来自 Config
And API key、CLI token、OAuth credential、webhook secret 等敏感值来自 Secret Provider
And Secret 值不得出现在普通配置 dump 或日志中
```

### Scenario: YAML 配置支持速查与热加载

```gherkin
Given 一个包含 database、logging、filestore、queue、lock、liveness、metrics 的 YAML 配置文件
When platform config 加载该文件
Then Config 必须填充强类型字段
And 调用 dotted lookup 可以读取 database.urls.events、queue.claim_ttl、logging.file
And database、logging、filestore、queue、lock、liveness、metrics、sweeper、health 必须作为平台内置 section 注册并完成解码校验
When YAML 文件被修改为有效配置
Then Config Manager 必须发布新版本并通知订阅者
When YAML 文件被修改为无效配置
Then Config Manager 必须保留 last known-good 配置
And readiness 可以读取最近一次 reload 错误
```

### Scenario: 业务配置通过 section 注册扩展

```gherkin
Given 后续业务模块需要自己的配置字段
When 业务模块注册自己的 YAML section 和校验函数
Then platform config 必须允许业务模块解码自己的 section
And platform Config 结构体不需要新增业务字段
When 热加载后的业务 section 未通过校验
Then Config Manager 必须拒绝发布新版本
And 订阅者不能收到被拒绝的配置
And last known-good 配置继续可用
```

## Feature: 数据库、迁移与事务

### Scenario: 测试数据库可以隔离初始化

```gherkin
Given 测试需要一个干净数据库
When 调用 testkit 创建测试数据库
Then 数据库应使用隔离 SQLite 路径或隔离 SQLite 实例
And 应能执行当前全部 migration
And 测试结束后可以清理资源
```

### Scenario: SQLite 分库存储隔离高写入模块

```gherkin
Given Stage 0 使用 SQLite 作为本地数据库基础
And SQLite 单库存在写锁竞争风险
When 调用 DB registry 创建 core、events、jobs、runtime、observability 等命名数据库
Then 每个命名数据库应使用独立 SQLite 文件或隔离实例
And 写入 events 数据库的记录不能出现在 jobs 或 core 数据库
And Config 应能通过 YAML 的 database.urls 提供命名数据库 URL
And testkit 应提供 NewTestDatabases 供后续 goal 复用分库夹具
```

### Scenario: migration readiness 检查最新版本

```gherkin
Given 数据库存在 schema_migrations 记录
When readiness check 执行
Then 如果最新 migration 已应用则返回 ready
And 如果最新 migration 未应用则返回 not ready
And live check 不应因为 migration 未应用而失败
```

### Scenario: 平台和业务初始化通过 migration registry 注册

```gherkin
Given Stage 0 基础设施需要初始化 EventStore、Outbox、Queue、Lock、Claim、Liveness 等表
And 后续业务模块需要初始化自己的业务表或索引
When 平台装配数据库
Then 平台必须先注册内置基础设施 migration
And EventStore migration 必须落到 events 数据库
And Outbox 与 Queue migration 必须落到 jobs 数据库
And Lock、Claim、Liveness migration 必须落到 runtime 数据库
And 业务模块只注册自己的 migration / initializer 及目标 database role
And migration infrastructure 负责排序、幂等、重复版本拒绝、事务回滚和 schema_migrations 记录
```

### Scenario: UnitOfWork 成功时提交事务

```gherkin
Given 一个 UnitOfWork.Do 调用
And 回调中写入业务状态、eventstore 记录和 outbox job
When 回调返回 nil
Then 所有写入必须在同一事务中提交
And 后续读取可以看到这些写入
```

### Scenario: UnitOfWork 失败时回滚事务

```gherkin
Given 一个 UnitOfWork.Do 调用
And 回调中已写入部分数据
When 回调返回错误
Then 事务必须回滚
And 业务状态、eventstore 记录和 outbox job 都不能留下部分写入
And logger 记录 operation、correlation_id 和 error_code
```

## Feature: EventStore 存储基础

### Scenario: append 并读取事件 envelope

```gherkin
Given 一个 mission_id
And 一个事件 envelope 包含 event_id、stream_version、event_type、event_version、payload_json、metadata_json、correlation_id
When 调用 EventStore append
Then 事件可以按 mission_id 和 stream_version 顺序读取
And payload_json 与 metadata_json 保持原样
```

### Scenario: stream version 冲突被拒绝

```gherkin
Given mission_id 的当前 stream_version 是 3
When 两个写入同时尝试追加 stream_version 4
Then 只有一个写入成功
And 另一个写入返回 conflict 类错误
And 不会产生重复 stream_version
```

### Scenario: idempotency key 防止重复写入

```gherkin
Given 一个 idempotency_key 已经成功写入
When 使用相同 idempotency_key 再次 append
Then EventStore 不应创建重复事件
And 返回值必须能让调用方识别该请求已处理
```

## Feature: Outbox 与 Queue

### Scenario: outbox job 完整生命周期

```gherkin
Given 一个待投递 outbox job
When job 被 enqueue
Then worker 可以 claim 该 job
When worker 处理成功
Then job 状态变为 done
When worker 处理失败且仍可重试
Then job 状态变为 pending 或 retry_scheduled
And next_attempt_at 按 backoff 更新
When retry 次数耗尽
Then job 状态变为 dead_letter
```

### Scenario: queue claim 是原子操作

```gherkin
Given 队列中存在同一个 pending job
When 两个 worker 同时 claim
Then 只有一个 worker 能拿到该 job
And 另一个 worker 拿不到相同 job
And logger 记录 claim 结果和 worker_id
```

### Scenario: job runner 不阻塞业务请求

```gherkin
Given 异步投影或通知任务进入队列
When 队列积压或 worker 暂不可用
Then enqueue 可以可靠落库
And 业务请求不等待任务执行完成
And 后续 worker 可以继续 claim 并处理任务
```

### Scenario: queue lifecycle 区分派发、运行和完成

```gherkin
Given 一个 durable queue job 处于 queued
When worker 原子 claim 该 job
Then job 状态必须变为 dispatched
And 记录 worker_id、lease_until、dispatched_at
When 非 owner worker 尝试 start 或 complete
Then 操作必须失败
When owner worker start 该 job
Then job 状态必须变为 running
And 记录 started_at
When owner worker 完成该 job
Then job 状态必须变为 completed
And 记录 completed_at
```

### Scenario: queue 支持取消、超时恢复和 queued TTL

```gherkin
Given durable queue 中存在 queued、dispatched、running 三类 job
When 控制平面取消这些 job
Then 它们都必须进入 cancelled
And 记录 failure_reason 与 completed_at
When dispatched job 的 lease 超时但尚未 start
Then sweeper recovery 必须将它恢复为 queued
And 清空 worker_id 与 lease_until
When running job 的 lease 超时
Then sweeper recovery 必须将它标记为 failed
And failure_reason 必须为 timeout
When queued job 超过配置 TTL
Then sweeper 必须按 batch 将它标记为 failed
And failure_reason 必须为 queued_expired
```

### Scenario: 运行基础设施通过聚合入口装配

```gherkin
Given 后续业务模块需要 queue、claim、liveness 和 sweeper
When 它装配 Stage 0 基础设施
Then 应优先通过 internal/platform/execution 获取统一运行基础设施
And 不应在业务目录分别手写 queue、claim、heartbeat、sweeper 装配
And execution 包不得引入 Agent / Mission / Task 业务语义
```

## Feature: FileStore 与 ArtifactStore

### Scenario: 保存 artifact 并返回引用

```gherkin
Given 一个成果或证据文件
When 调用 FileStore Put
Then 返回 ArtifactRef 类引用
And 引用包含 artifact_id、content_type、size、checksum、created_at
And 业务层不得依赖本地绝对路径作为持久契约
```

### Scenario: checksum 校验文件内容

```gherkin
Given 一个已经保存的 artifact
When 调用 FileStore Get
Then 返回内容的 checksum 必须与保存时记录一致
And 内容不存在时返回 not_found 类错误
```

### Scenario: 本地 FileStore 复用 Multica 成熟行为

```gherkin
Given 本地优先生产部署使用 platform FileStore
When 保存 artifact
Then 文件内容和 sidecar metadata 必须持久化到本地文件系统
And ArtifactRef 不得暴露本地绝对路径
And GetReader 可以流式读回内容
And DeleteKeys 可以批量删除对象和 sidecar
And 路径穿越 key 与 .meta.json sidecar key 必须被拒绝读取
```

### Scenario: S3 FileStore 复用 Multica 成熟行为

```gherkin
Given platform FileStore 配置为 S3 backend
When 解析已有 CDN、virtual-hosted-style、path-style、custom endpoint URL
Then KeyFromURL 必须返回稳定 object key
And Uploaded URL 必须优先使用 CDN，其次 custom endpoint，其次 AWS S3 URL
And bucket 字段误填 S3 hostname 时必须能被配置校验或警告识别
And 上传对象必须设置 content_type、content_disposition、cache_control 和合适 storage class
```

## Feature: Execution 与 Lock

### Scenario: lock 防止重复执行

```gherkin
Given 两个执行者尝试处理同一个互斥资源
When 第一个执行者获得 lock
Then 第二个执行者在 lock 释放或过期前不能获得同一把 lock
And lock 过期后可以被重新获取
```

### Scenario: runtime liveness 支持降级

```gherkin
Given liveness store 配置了 Redis 或等价热路径
When 热路径不可用
Then liveness 检查退回 DB-only 策略
And 不能把热路径作为唯一事实源
And logger 记录 warn 级降级日志
```

### Scenario: negative claim cache 使用 version 避免陈旧空结果

```gherkin
Given 某个 runtime 刚刚 claim 不到任务并标记 empty
When 新任务 enqueue 给该 runtime
Then enqueue 必须 bump runtime version
And 后续 claim 不能因为旧 empty verdict 跳过数据库查询
```

### Scenario: sweeper 批量清理 stale lease

```gherkin
Given 存在过期的 lease 或 stale running job
When sweeper 执行
Then 它按 batch size 清理或标记失败
And 不进行无上限全表长事务扫描
And 单个 sweep 失败只记录错误，不让进程崩溃
```

## Feature: Health、Metrics、Tracing 与 Test Harness

### Scenario: live 与 ready 分离

```gherkin
Given 进程已经启动
When 调用 live check
Then 只验证进程存活
When 调用 ready check
Then 检查 DB ping、migration 状态和必要基础设施状态
And ready 结果可以短 TTL 缓存
```

### Scenario: 非关键 metrics 不阻塞业务

```gherkin
Given metrics 或 audit shipping 使用有界队列
When 队列已满
Then 新指标可以被丢弃并计数
And 业务请求不被阻塞
And 系统周期性输出 warn 摘要
```

### Scenario: testkit 提供后续 goal 可复用夹具

```gherkin
Given 后续业务 goal 需要测试基础设施
When 它导入 platform testkit
Then 可以创建 NewTestLogger、NewTestConsoleAndFileLogger、NewTestConfig、NewTestDB、NewTestDatabases、NewTestUnitOfWork、NewTestEventStore、NewTestFileStore、NewFakeClock、NewDeterministicIDGenerator
And 每个夹具都有确定性行为
And 每个夹具不会要求业务模块自建基础设施
```

## Feature: Stage 0 边界

### Scenario: platform 不引入业务语义

```gherkin
Given Stage 0 只实现基础设施
When 检查 internal/platform 依赖
Then platform 不得 import internal/agent、internal/mission、internal/app、internal/transport
And 不定义 Mission、Agent、TaskContract、Discussion、QA、RuntimeAdapter 的业务模型
And EventStore 只保存通用 envelope，不解释 MissionEvent 语义
```

### Scenario: 后续业务阶段只依赖 platform 接口

```gherkin
Given 后续 goal 需要数据库、事务、事件存储、outbox、队列、文件、锁、日志、配置或测试夹具
When 它实现业务逻辑
Then 它可以依赖 platform 暴露的接口和 testkit
And 不需要在业务目录重复实现基础设施
```
