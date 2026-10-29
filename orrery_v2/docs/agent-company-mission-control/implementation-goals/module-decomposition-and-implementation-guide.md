# Agent 公司 Mission Control 模块拆分与实现指导

本文把 [Agent 公司与 Mission Control 协作模型](../agent-company-mission-control-model.md) 拆解到 `implementation-goals` 下的具体功能模块。它不是 BDD 验收清单，而是每个模块的生产级设计入口：说明模块为什么存在、归属哪里、与其他模块如何协作、底层应如何实现、哪些边界不能越过。

所有 goal 都以生产级系统为目标。分阶段只表示交付顺序，不表示可以把模块做成 MVP、demo、临时脚手架或凑合闭环。每个模块都必须明确可替换接口、错误码、日志、测试、权限、幂等、恢复策略和完成证据。

所有模块都必须遵守优雅解耦原则：解耦不是为了拆分而拆分，而是为了让业务更灵活、实现更健壮、代码更易读易懂。模块边界、包边界、接口和 adapter 只有在减少真实耦合、提高替换性、改善测试或降低认知负担时才成立；如果拆分只是增加跳转，就应合并或推迟。

后续每个 goal 的 README 都应至少包含四层内容：

```text
顶层设计：该模块在 Agent 公司模型中的职责、边界和价值。
中层规划：包结构、领域对象、服务接口、状态流、依赖关系和跨 goal 协议。
底层实现指导：文件拆分、接口形态、存储/事务/日志/错误/测试/并发策略。
验收与证据：BDD 场景、测试入口、覆盖关系、禁止事项和完成证明。
```

所有实现必须遵守 [项目结构设计](../project-structure-guidelines.md)。如果某个 goal 为了方便把其他 goal 的职责搬进来，即使测试通过也视为设计失败。

## 0. 复用原则：不是从 0 开始

本轮拆分不是要求从空白工程重写所有能力。正确策略是：

```text
目标模型优先。
已有实现能干净落入目标模块边界，就优先直接复制或移动到目标目录，再按新边界改名、切分、去耦和补测试。
已有实现带有旧业务耦合，但底层能力可用，就先迁入可复用部分，再移除旧业务概念；不要因为需要改返回错误、接口命名或配置接入，就放弃迁移重写。
已有实现如果会反向改变 Agent 公司模型，就不作为架构约束。
从头写只能作为最后选项；必须在 README 或实现计划中说明为什么 Multica / 原 Orrery 没有可迁移资产，或为什么直接迁移会破坏目标边界。
没有可迁移资产时，必须优先评估成熟标准库或开源包；自研只允许用于目标边界、依赖、许可、运行约束或领域语义确实无法满足的部分。
```

可直接评估复用的资产包括：

```text
Orrery 已有 Mission、QA、Evidence、PM timeline、comment/reply/mention、inbox、agent runtime wrapper、worker config、market catalog。
Multica 已有 CLI backend、runtime session、execenv、daemon、health、storage、liveness、negative claim cache、sweeper、migration、日志和后台任务机制。
```

复用时必须写清楚四件事：

```text
来源：来自 Orrery 还是 Multica，原始路径是什么。
采用方式：直接复制、移动迁入、改名切分、adapter 包装、只借鉴机制，还是废弃；默认优先级是直接复制或移动，其次才是 adapter 或重新实现。
去耦要求：必须移除哪些旧概念，例如 issue_id、comment_id、agent_task_queue、squad_id、timeline-as-source。
验收证据：迁入后如何证明它符合新的包边界、契约、日志、测试和权限要求。
```

因此，“不做旧模型补丁”不等于“从 0 写”。它的意思是：不让旧表、旧 API、旧 timeline 或旧 queue 决定新系统骨架；但可用实现应尽量迁移复用，尤其是已经稳定的基础设施、runtime、QA、证据、讨论和通知能力。对底层通用包，例如 storage / filestore、S3、本地文件 sidecar、migration、health、liveness、negative claim cache、sweeper、日志和后台任务机制，默认判断应是“先搬过来再按目标接口修”，而不是“自己重新想一个简化版”。

每个模块开工前的固定检查顺序是：先找 Multica / 原 Orrery；能迁移就直接复制或移动后适配；不能迁移再找成熟包；成熟包仍不满足时才自研。README 或实现计划必须记录这个判断，否则验收时视为复用评估不完整。

## 1. 主模型到功能模块的映射

| 主模型概念 | 归属 goal | 主要代码归集 | 说明 |
| --- | --- | --- | --- |
| 公共基础设施 | `01-infrastructure-foundation` | `internal/platform/*` | DB、migration、UOW、eventstore 物理存储、outbox、queue、filestore、lock、claim、liveness、sweeper、logging、config、secret、metrics、tracing、health、testkit |
| 核心契约语言 | `02-core-contracts` | `internal/agent/core`、`internal/mission/core`、`internal/mission/task` | Mission、AgentProfile、AgentIdentity、TaskContract、AgentResult、Deliverable、Evidence、ContextPack 等共享语言 |
| Agent 生命周期 hook | `03-lifecycle-signals` | `internal/agent/lifecycle` | Signal、Hook、Middleware、SignalBus、AgentRunState 生命周期扩展点 |
| 业务事实与投影 | `04-event-ledger-projection` | `internal/mission/event`、`internal/mission/projection`、`internal/app/missioncontrol` | MissionEvent、EventLedger 语义、Projector、MissionControlViewDTO、QueryService、StreamService |
| Tool / Skill / Memory | `05-resource-registry-memory` | `internal/resource/*`、`internal/memory/*` | Tool/MCP/Skill 注册、解析、权限；个人记忆、集体记忆、MemoryDigest、ContextPack 记忆片段 |
| 交付与验收闭环 | `06-task-delivery-qa` | `internal/mission/task`、`internal/agent/component/{delivery,evidence,qa}` | TaskLifecycle、DeliverableSubmission、EvidenceSubmission、QAResult、ReworkRequest |
| BaseAgent 与 Runtime | `07-base-agent-runtime` | `internal/agent/core`、`internal/agent/assembly`、`internal/agent/component/*`、`internal/agent/runtime/*` | AgentAssembler、BaseAgent pipeline、RuntimeAdapter、ResultNormalizer、TraceRecorder、Multica runtime 迁移 |
| 组织身份与调配 | `08-organization-staffing` | `internal/agent/core`、`internal/mission/team` | AgentOrgIdentity、组织目录查询、MissionTeam、MissionAssignment、HR staffing |
| 讨论组与收件箱 | `09-discussion-inbox` | `internal/mission/discussion`、`internal/app/missioncontrol` 讨论视图 | DiscussionGroup、Message、Mention、ReplyThread、InboxItem、DiscussionDigest |
| API / Stream 出口 | `10-api-transport` | `internal/transport/http`、`internal/transport/websocket`、`pkg/publicapi` | HTTP route、WebSocket hub、DTO envelope、错误映射、鉴权上下文 |
| 前端 Mission Control | `99-deferred-frontend-mission-control` | frontend | Mission 作战室视图，消费后端 DTO / patch，不成为事实源 |

## 2. 依赖顺序与完成证据

模块顺序不是任意编号，而是为了防止业务代码反向创造基础设施或重复定义事实源：

```text
01 platform
-> 02 contracts
-> 03 lifecycle + 04 event/projection
-> 05 resource/memory + 06 delivery/QA
-> 07 BaseAgent/runtime
-> 08 organization/team + 09 discussion/inbox
-> 10 API transport
-> 99 frontend
```

每个 goal 的完成证据必须同时包含：

```text
1. README 中列出的公开接口和目录已经存在。
2. 关键不变量有单元测试或集成测试。
3. 错误路径、权限边界、重复提交、并发冲突至少有一条验证。
4. CROSS_GOAL 假设没有被私自实现成对方职责。
5. 日志、错误码、correlation_id、可审计来源在关键路径可追踪。
6. 文档中的独立验收场景能映射到测试、静态检查或人工审计证据。
```

## 2.1 Stage 0 基础设施使用约定

后续所有 goal 默认运行在 `01-infrastructure-foundation` 已提供的平台层之上。业务模块只定义自己的领域契约、配置 section、migration / initializer、repository 接口和 app service 语义；数据库连接、分库路由、事务、文件存储、日志、配置热加载、密钥、健康检查、metrics、tracing、queue、lock、claim、liveness、sweeper 和测试夹具都由 `internal/platform/*` 托管。

### 配置与热加载

`ConfigManager` 是内存中的 last-known-good 配置中心。业务读取配置时优先使用 `Current()`、dotted lookup 或 `DecodeSection()` 主动 pull 当前快照；订阅发布只用于通知需要重建副作用资源的组件，例如日志 sink、FileStore、metrics buffer 或 runtime adapter pool。业务不得把订阅事件当成唯一配置来源，也不得把收到的配置复制成长期事实源。

业务模块可以注册自己的配置 section 和 validator，但不得让平台 config 提前定义业务字段。平台自身的 `database / logging / filestore / queue / lock / liveness / metrics / sweeper / health / paths` 是首批 section，用来验证 section 注册、解码、校验、last-known-good、reload error 和 readiness 诊断链路。

YAML 中必须明确 `paths.base_dir`，或者接受“配置文件所在目录作为 base dir”的默认规则。所有本地相对路径，包括 SQLite URL、FileStore root、日志文件路径，都必须由 config 层基于 base dir 解析成稳定路径，避免打包、工作目录变化或服务管理器修改 cwd 后写错位置。业务代码不得自己解释相对路径。

配置与密钥必须分离。普通配置来自 YAML / 受控 env override；secret 只通过 `secret.Provider` 获取，不得进入 config dump、日志字段、事件 payload、artifact metadata 或 discussion message。

### 数据库、迁移与事务

SQLite 存在明确单库写锁竞争，后续模块不得假设所有表都在一个文件里。业务层使用 `DBRouter` 和 `RoutedUOW` 看到的是逻辑表名，例如 `events.mission_event`、`jobs.task_queue`、`runtime.agent_claim`；平台层负责把 role prefix 路由到对应 named database，并在物理表名中去掉 role 前缀。没有 role prefix 的表默认进入 `core`。

业务模块只注册自己的 migration / initializer 以及目标 database role。平台 migration coordinator 负责排序、幂等、重复版本拒绝、事务回滚、schema_migrations 记录、跨 role readiness 检查和初始化错误暴露。业务不得自行创建 DB registry、migration runner 或一组 store map。

单个 `RoutedUOW` 只保证单 role 事务。跨库操作必须用 outbox、eventstore、幂等 command、补偿任务或显式 saga 设计，不允许伪装成跨 SQLite 文件原子事务。查询缺失 role、格式错误的逻辑表名、关闭后的数据库访问必须返回显式错误，不允许 panic-only lookup、`Must` API 或 not-found 混淆。

### 日志、观测与健康检查

所有模块使用 platform logger。控制台日志用于本地开发、容器 stdout 和实时运维观察；JSON lines 文件日志用于长期审计和问题追踪。两路输出必须使用同一组结构化字段、correlation_id、operation、error_code、duration_ms 和敏感字段 redaction。

业务代码不得使用 `fmt.Print*`、`log.Print*` 或直接 stdout/stderr 作为业务日志。关键路径至少记录 component、operation、correlation_id、领域 ID、错误码、耗时和降级原因；日志不得输出 secret、token、完整 prompt、完整 memory、完整 discussion payload 或未裁剪 runtime trace。

Readiness 必须覆盖 named DB、registered migrations、FileStore、config reload error、restart-required drift 和必要后台组件。Live 只证明进程还活着，不能替代 ready。配置热加载失败、subscriber 失败、可恢复组件重建失败和静态配置漂移都必须进入健康诊断，而不是只写日志。

### FileStore、artifact 与本地优先

FileStore 返回稳定 artifact reference，不把本地绝对路径作为业务持久契约。本地 FileStore 是默认生产路径；S3 adapter 是可替换 backend。两者都必须保持相同契约：checksum、size、content_type、created_at、streaming reader、delete / batch delete 错误返回和 context cancellation。

业务模块不得直接写本地 upload root、不得依赖 sidecar 文件格式、不得拼接对象路径。路径穿越、sidecar 读取、空 key、backend 配置错误、checksum mismatch、删除失败都必须被平台层显式拒绝或返回错误。

### 后台基础设施与生命周期

Execution、Outbox 和 Lock 都必须使用平台提供的 durable SQLite-backed 实现和测试夹具。后续阶段可以在领域层定义 task、assignment、runtime run 或 inbox 语义，但不得各自发明内存队列、临时 lease、私有 heartbeat 或无持久化 claim cache 作为生产实现。

业务模块需要 queue + claim + liveness + sweeper 时，默认通过 `internal/platform/execution` 装配。`execution` 是真实合并后的运行基础设施包，不再套在旧 `queue / claim / liveness / sweeper` 包外；包内按文件拆分职责，业务侧不应把这些能力重新组合成一套私有 runtime 基础设施。

通用 durable queue 的生命周期必须接近 Multica 的任务推进经验，但不能继承 `agent_task_queue` 的业务字段。平台状态机至少覆盖 `queued / dispatched / running / completed / failed / cancelled`：claim 只表示派发为 `dispatched`，start 才进入 `running`，complete 只允许 owner 完成 running job，fail 支持 retry 与最终 failed，cancel 覆盖 queued / dispatched / running，recovery 区分 stale dispatched 与 stale running，queued TTL 通过 sweeper 批量过期。Agent、Mission、Task 的业务状态只能在后续模块定义。

所有 watcher、logger、DB、FileStore、metrics sink 和 sweeper runner 都必须有明确 `Close` / stop 语义。关闭后的 runtime / config manager 只允许读取 last-known-good 快照，必须拒绝 reload、section 注册、subscriber 注册和组件重建，防止后台 goroutine 在资源释放后继续修改平台状态。

### Testkit 与验收证据

后续 goal 的测试默认使用 `internal/platform/testkit` 提供的生产相似夹具：configured logger、YAML config manager、split SQLite databases、DB router、RoutedUOW、EventStore、Outbox、Execution、Lock、FileStore、fake clock 和 deterministic ID。测试不应为了方便绕过平台层自建 JSON store、内存 fake DB 或简化 logger；只有明确测试纯领域对象时才允许使用无基础设施 fake。

每个后续 README 的 BDD 或验收段都必须能指向 Stage 0 的基础设施约定，并补充本模块自己的测试证据。完成声明前至少执行对应模块测试、platform 回归测试，以及静态检查：业务目录不直接读取环境变量、不直接 print 日志、不反向 import `internal/agent` / `internal/mission` / `internal/app` / `internal/transport` 到 `internal/platform`。

## 3. 统一模块 README 结构

每个 goal README 应使用下面结构，避免只剩一份 BDD 要求：

```text
# Goal XX：名称

## 顶层设计
说明该模块在 Agent 公司模型中的位置、要解决的系统问题、为什么不能归到其他模块。

## 生产级职责边界
列出本模块负责什么、不负责什么、禁止做什么，以及错误的归集示例。

## 中层模块规划
列出包结构、文件职责、领域对象、服务接口、状态机和跨模块交互。

## 底层实现指导
说明事务边界、并发控制、幂等、日志、错误码、测试夹具、fake 实现、复用来源和迁移策略。

## 数据与事件设计
说明核心类型、事件、状态、DTO、存储模型或读模型。

## 现有实现复用计划
列出 Orrery / Multica 可复用资产、原始路径、采用方式、去耦要求和验收证据。

## 运行流程
用文本或 Mermaid 描述从输入到输出的主流程和失败路径。

## 测试与验收
把 BDD 场景映射到测试类型、测试文件建议和完成证明。

## CROSS_GOAL
说明依赖谁、提供什么、当前假设和后续契约。
```

## 4. 跨模块设计规则

### 4.1 Platform 不承载业务语义

`internal/platform` 可以提供通用 event envelope、queue job、artifact、lease、lock，但不能定义 Mission、Agent、Task、Discussion、QA 或 RuntimeAdapter。业务语义必须从 `02` 之后的模块开始。

### 4.2 Contract 先于 Runtime

`TaskContract`、`AgentResult`、`Deliverable`、`Evidence`、`ContextPack` 必须先稳定，BaseAgent 和 runtime 只能消费这些契约，不能在执行阶段发明新的结果结构。

### 4.3 Signal 不是 Event

Signal 是运行时 hook；Event 是业务事实。Signal 可以触发 Event，但不能默认全部持久化。Event 才是投影和前端视图的事实来源。

### 4.4 记忆通过摘要进入上下文

个人记忆、集体记忆、Mission 记忆、讨论组记忆都必须经过检索、权限过滤、压缩和来源绑定，以 `MemoryDigest` 或 `DiscussionDigest` 形式进入 `ContextPack`。任何模块都不能把全量记忆、全量消息或内部 trace 直接注入 runtime。

### 4.5 Tool / Runtime 不能入组

Tool、MCP、CLI、API、RPA、RuntimeAdapter 都只是 Agent 的能力或运行适配器。进入组织目录、Mission Team、DiscussionGroup 的只能是 AgentProfile / AgentInstance / RealHumanAgent。

### 4.6 QA 是 Agent 能力，不是顶层业务中心

QAContract / QAPolicy 属于 AgentProfile 可装配能力，QACheckAgent 是一种 AgentInstance。Mission task 可以记录 QA 结果，但不能直接拥有 QA 引擎。

### 4.7 前端只消费投影

前端不能消费 MissionEvent 原始事件、projection row 或 domain struct，也不能根据固定流程模板推断状态。前端只展示 `internal/app/missioncontrol` 暴露的 DTO / patch。

## 5. 模块补全优先级

当前文档体系的短板不是“没有验收要求”，而是缺少中间设计层。因此补文档按这个顺序推进：

1. 修复损坏或不可读文档：`01-infrastructure-foundation/README.md`、`stage-0-bdd-coverage.md`。
2. 扩写 Stage 0：把基础设施拆到 logging/config/db/eventstore/outbox/execution/filestore 等子模块，明确每个子模块的接口、失败策略和测试。
3. 扩写 Stage 1：`02`、`03`、`04` 形成共享语言、生命周期和事实源，避免后面 runtime 与 discussion 自行造模型。
4. 扩写 Stage 2：`05`、`06`、`07` 打通资源、记忆、交付、QA、BaseAgent 和 runtime。
5. 扩写 Stage 3：`08`、`09` 补团队调配和讨论组上下文，将个人目标与讨论组目标注入规则落到模块。
6. 扩写 API / 前端：`10`、`99` 只作为稳定投影出口和显示层，不能提前倒逼领域模型。

## 6. 完成后的文档形态

最终每个 goal 应能回答以下问题：

```text
这个模块为什么存在？
它拥有哪些领域对象和服务接口？
它的代码应该落在哪些目录和文件？
它依赖哪些前置模块？
它向后续模块提供哪些接口？
它能复用哪些 Orrery / Multica 现有实现，复用边界是什么？
它的事务、幂等、并发、错误、日志、权限和测试策略是什么？
哪些行为属于本模块，哪些必须留给其他 goal？
怎么证明它满足主文档里的 Agent 公司模型？
```

只有能回答这些问题的 README，才算从“BDD 要求”升级成生产级设计文档和实现指导。
