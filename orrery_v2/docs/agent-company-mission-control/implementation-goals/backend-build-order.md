# 后端搭建顺序

本文是后续开发时的执行顺序入口。

结论：

```text
当前先完成后端。
基础设施是第一阶段，不是业务模块的附属品。
基础设施内部最先实现 logging / config。
前端 Mission Control 延后。
Goals 是阶段式依赖关系，不是完全并发关系。
不是从 0 重写所有实现；Multica 和 Orrery 现有能力能干净落入目标模块边界时，应优先复用或迁入。
优雅解耦优先；解耦不是为了拆分而拆分，而是为了让业务更灵活、实现更健壮、代码更易读易懂。
每个阶段开工前先比较 Multica / 原 Orrery 是否已有成熟实现；能复制或移动后适配就先迁移，没有可迁移资产再优先引入成熟包，最后才自研并说明原因。
```

执行每个阶段前，先阅读 [模块拆分与实现指导](./module-decomposition-and-implementation-guide.md)，确认该阶段的复用来源、代码归集、CROSS_GOAL 边界和完成证据。

## 0. 第一阶段：基础设施公共服务

先完成：

```text
01-infrastructure-foundation
```

必须提供：

```text
Logging / Config
Database / Migration
UnitOfWork / Transaction
Repository 基础约定
EventStore / EventLedger 存储
Outbox
Queue / JobRunner
FileStore / ArtifactStore
Lock
Config / Secret
Logger / Metrics / Tracing
Health Check
Test Harness
```

原因：

- 日志系统必须最先存在，后续所有模块都要用 debug / info 日志排错。
- 后续所有业务 goal 都需要数据库、事务和测试夹具。
- 事件账本需要可靠存储。
- BaseAgent 需要队列、锁、文件、日志、配置和密钥。
- 如果不先做公共服务，各模块会各自实现一套基础设施，后面很难收回来。

## 1. 第二阶段：后端基础协议

完成：

```text
02-core-contracts
03-lifecycle-signals
04-event-ledger-projection
```

原因：

- `02-core-contracts` 定义所有模块共享的 ID、TaskContract、AgentResult、Deliverable、Evidence。
- `03-lifecycle-signals` 定义 BaseAgent 后续必须调用的生命周期 hook。
- `04-event-ledger-projection` 定义业务事实如何持久化、回放、投影，并通过 app query service 暴露前端 DTO。

这三个可以局部并行，但最终以 `02-core-contracts` 为共同语言，并使用 `01-infrastructure-foundation` 提供的公共服务。

## 2. 第三阶段：资源、记忆与后端任务闭环

完成：

```text
05-resource-registry-memory
06-task-delivery-qa
07-base-agent-runtime
```

目标：

```text
Tool / MCP / Skill registry
-> MemoryProfileRef / MemoryDigest
-> AgentProfile refs
-> AgentAssembler
TaskContract
-> BaseAgent
-> lifecycle signals
-> fake RuntimeAdapter
-> AgentResult
-> Deliverable / Evidence
-> QA approve / reject
-> MissionEvent
-> EventStore / Outbox
```

这一阶段完成后，后端应该能证明一个 Agent 可以从接任务到交付验收完整跑通。

## 3. 第四阶段：团队与协作

完成：

```text
08-organization-staffing
09-discussion-inbox
```

目标：

```text
AgentProfile.org_identity
-> AgentProfile query
-> AgentInstance
-> MissionTeam / MissionAssignment
-> DiscussionGroup member
-> Message / mention / reply
-> InboxItem response
```

这一阶段把单 Agent 执行闭环扩展成团队协作闭环。

## 4. 第五阶段：API 出口

完成：

```text
10-api-transport
```

目标：

```text
internal/app/missioncontrol QueryService / StreamService
-> HTTP handler
-> WebSocket stream
-> MissionControlViewDTO / MissionControlPatchDTO
```

这一阶段只暴露后端服务出口，不开发前端页面。

## 5. 第六阶段：前端延后

最后再考虑：

```text
99-deferred-frontend-mission-control
```

前端必须等后端投影 DTO 稳定后再进入主开发阶段。

## 6. 暂不做

当前后端搭建阶段不做：

```text
前端动态进度图
旧 comment 兼容
旧 task queue 兼容
完整行业模板
复杂权限系统
通用 workflow builder
扁平成员池
```
