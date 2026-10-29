# Agent 公司 Mission Control 后端搭建 Goals

本目录用于后续直接作为开发 goal 的输入。当前版本不再只描述系统设计，而是把基础设施作为独立公共服务体系放在第一位。

所有 goal 必须遵守顶层项目结构指导：[project-structure-guidelines.md](../project-structure-guidelines.md)。

执行顺序详见 [backend-build-order.md](./backend-build-order.md)。

模块拆分、README 深度要求、复用原则和跨 goal 边界详见 [模块拆分与实现指导](./module-decomposition-and-implementation-guide.md)。

后续所有 goal 必须遵守 [Stage 0 基础设施使用约定](./module-decomposition-and-implementation-guide.md#21-stage-0-基础设施使用约定)：业务模块只注册自己的配置 section、migration / initializer、领域接口和 app service 语义；平台层统一托管 config、分库 DBRouter / RoutedUOW、日志、FileStore、queue、lock、claim、liveness、health、metrics、tracing、secret 和 testkit。

本目录不是要求从 0 重写所有能力。Multica 和原始 Orrery 已有实现只要能落入目标模块边界，就应优先直接复制或移动迁入，再按新边界改名、切分、去耦、补测试；不能复用的是旧业务骨架对新模型的反向约束，而不是已有代码本身。从头写必须说明为什么没有可迁移资产，或为什么迁移会破坏目标边界。

全体 goal 必须遵守优雅解耦原则：解耦不是为了拆分而拆分，而是为了让业务更灵活、实现更健壮、代码更易读易懂。新增包、接口、adapter 或抽象层必须能解释它解决的边界、复用、替换、测试或可读性问题；如果只是在现有逻辑外再套一层补丁，就应合并、迁移或推迟。

全体 goal 的实现判断顺序是：先比较 Multica / 原 Orrery 是否已有成熟实现；能直接复制或移动迁入后修改的，优先迁移适配；没有可迁移实现时，优先引入成熟开源包或标准库能力；只有在目标边界、依赖、许可、运行约束或领域语义确实不满足时，才允许自研，并留下取舍说明和验收证据。

全体 goal 的目标是实现生产级系统切片，而不是 MVP、demo 或凑合闭环。可以分阶段交付，但每个阶段都必须具备清晰边界、错误处理、日志、测试、观测、恢复策略、替换实现和完成证据。

核心原则：

```text
先搭基础设施，再搭业务内核。
先有数据库、迁移、事务、事件存储、文件、队列、锁、配置、密钥、观测、测试夹具。
再定义核心契约、生命周期信号和事件语义。
最后让资源注册、记忆系统、BaseAgent、Agent runtime、任务交付、团队协作跑起来。
前端延后。
解耦必须服务业务灵活、实现健壮、易读易懂；不为拆包而拆包。
已有实现能干净复用就复用；复用必须服从目标模型和目录边界。
底层通用包和成熟能力优先搬迁已有实现再修改；没有可迁移资产时优先使用成熟包，不先写简化替代品。
每个阶段都是生产级系统的一部分，不允许为了短期跑通而省略关键边界、测试、日志、权限、幂等或恢复能力。
```

## 后端优先顺序

| 顺序 | Goal | 名称 | 为什么在这个位置 |
| --- | --- | --- | --- |
| 1 | `01-infrastructure-foundation` | 基础设施公共服务 | 所有后端模块共享数据库、事务、事件存储、文件、队列、锁、配置、密钥、观测和测试夹具 |
| 2 | `02-core-contracts` | 核心契约模型 | 所有业务模块共享 Mission、Agent、TaskContract、AgentResult、Evidence 等语言 |
| 3 | `03-lifecycle-signals` | Agent 生命周期信号系统 | BaseAgent 需要稳定 hook 点 |
| 4 | `04-event-ledger-projection` | 事件账本、后端投影与前端查询服务 | 业务事实需要持久化、回放、投影，并通过 app query service 暴露给前端 |
| 5 | `05-resource-registry-memory` | 资源注册与记忆系统 | Tool/MCP/Skill 是外部可注册资源，Memory 是独立大模块，AgentProfile 只保存引用 |
| 6 | `06-task-delivery-qa` | 任务交付、证据与 QA 契约 | 定义结果如何提交、校验、验收；QA 是 AgentProfile / TaskContract 引用的契约 |
| 7 | `07-base-agent-runtime` | Agent Assembly、BaseAgent 与 Runtime | 在资源引用、记忆绑定、契约、信号、事件、交付规则存在后实现 Agent 组件装配与执行管线，并借鉴 Multica runtime |
| 8 | `08-organization-staffing` | Agent 组织身份与任务调配 | 后端闭环跑通后再补全 Agent 身份属性，并加入 Mission 团队任命 |
| 9 | `09-discussion-inbox` | 讨论组、mention、reply | 团队成员明确后再接组内通讯和待响应机制 |
| 10 | `10-api-transport` | API Transport 与前端服务出口 | app service 稳定后暴露 HTTP / WebSocket 给前端 |
| 99 | `99-deferred-frontend-mission-control` | 前端 Mission Control | 后端投影稳定后再做前端 |

## 阶段式推进

### Stage 0：基础设施公共服务

必须先完成：

```text
01-infrastructure-foundation
```

它提供：

```text
DB / Migration / UnitOfWork
EventStore / Outbox
Queue / JobRunner
FileStore / ArtifactStore
Lock
Config / Secret
Logger / Metrics / Tracing
Health Check
Test Harness
```

### Stage 1：后端基础协议

在基础设施之上完成：

```text
02-core-contracts
03-lifecycle-signals
04-event-ledger-projection
```

这三个 goal 有一定局部并行度，但最终以 `02-core-contracts` 的 ID、TaskContract、AgentResult 等契约为准。

### Stage 2：资源、记忆与后端任务闭环

在 Stage 1 基础上完成：

```text
05-resource-registry-memory
06-task-delivery-qa
07-base-agent-runtime
```

目标是跑通：

```text
Tool / MCP / Skill registry
-> MemoryProfileRef / MemoryDigest
-> AgentProfile refs
-> AgentAssembler
TaskContract
-> BaseAgent fake runtime
-> lifecycle signals
-> AgentResult
-> Deliverable / Evidence
-> QA approve / reject
-> MissionEvent
-> EventStore / Outbox
```

### Stage 3：团队与协作

在 Stage 2 基础上完成：

```text
08-organization-staffing
09-discussion-inbox
```

目标是跑通：

```text
AgentProfile.org_identity
-> AgentProfile query
-> AgentInstance
-> MissionTeam / MissionAssignment
-> DiscussionGroup member
-> @mention
-> reply / inbox response
```

### Stage 4：API 出口

```text
10-api-transport
```

目标是跑通：

```text
internal/app/missioncontrol QueryService / StreamService
-> internal/transport/http
-> internal/transport/websocket
-> MissionControlViewDTO / MissionControlPatchDTO
```

### Stage 5：前端延后

```text
99-deferred-frontend-mission-control
```

前端可以先看文档做概念准备，但不应作为当前后端搭建的主目标。

## CROSS_GOAL 标记格式

跨 goal 的地方先写标记，不在当前 goal 中实现对方职责。

```text
CROSS_GOAL: <goal-id>
需要：<对方最终提供什么>
当前假设：<本 goal 暂时如何 mock 或占位>
后续约定：<后续需要补的交互契约>
```

## 全局禁止事项

1. 不把旧 comment 当 Mission 讨论组。
2. 不把旧 task queue 当 Agent 生命周期。
3. 不让 Tool / CLI / API / RPA 直接入组。
4. 不让 timeline、前端流程图、comment 成为事实源。
5. 不用 nullable 大对象兼容所有角色。
6. 不因为某个 goal 方便，就把别的 goal 的逻辑搬进来。
7. 不让业务模块私自创建数据库连接、事务、队列、文件存储或日志体系。
8. 不再把可调配成员建模为扁平成员池；必须通过 AgentProfile 的组织身份属性表达部门、岗位、角色和汇报线。
9. 不让业务模块绕过 `ConfigManager` 内存快照、`DBRouter` / `RoutedUOW`、platform logger、FileStore 或 testkit 自建基础设施。
10. 不用订阅发布替代配置读取；热加载后的事实来源是 last-known-good 内存快照，订阅只通知需要重建副作用资源的组件。
11. 不在 Multica 或原始 Orrery 已有成熟实现可迁移时，从头写一个更简单的底层替代品；若确实重写，必须留下明确取舍说明和验收证据。
12. 不为了“解耦”而机械拆包、堆接口或加 adapter；拆分必须提升业务灵活性、实现健壮性或可读性。
13. 不在成熟标准库、开源包或已迁移实现能满足生产要求时自造核心基础设施。

## 第一条生产级后端闭环

当前优先完成的不是完整产品，但也不是 MVP 或凑合闭环；它是第一条可验收的生产级后端系统切片：

```text
01-infrastructure-foundation
-> 02-core-contracts
-> 03-lifecycle-signals
-> 04-event-ledger-projection
-> 05-resource-registry-memory
-> 06-task-delivery-qa
-> 07-base-agent-runtime
```

完成后应能证明：

```text
一个 fake Agent 可以接收 TaskContract，
通过 AgentAssembler 解析 ToolRef / SkillRef / MemoryProfileRef，
经过生命周期信号系统，
调用 fake RuntimeAdapter，
产出 AgentResult，
提交 Deliverable / Evidence，
通过或未通过 QA，
写入 MissionEvent，
通过 app query service 读取 MissionControlViewDTO，
并通过基础设施完成事务、事件存储、outbox 和测试夹具验证。
```
