# Orrery AgentCore 循环与工具执行账本

`internal/agent/runtime/agentkernel` 提供可接入 `kernelcompat.Engine` 的 Go 实现。已实现模型流、工具调用与结果回送、多轮会话、参数和权限校验、预算、取消与持久化工具执行账本。生产启动通过共享 WorkspaceBroker、现有模型配置和 runtime 数据库注入它。新增 `runtime-orrery-agentcore` 配置，显示为 Orrery AgentCore；需在 Agent 编辑页显式选择，已有 PI、Codex、Claude 配置和默认执行路径不变。

首版生产策略只允许已有授权的知识、记忆、网页和组织检索，以及指定的公开知识 MCP 只读接口。官网接待 Agent 额外禁止通用内部知识、记忆、组织工具。实际调用继续通过生产工具分发器和 MCP 网关，逐次检查真实知识库权限；不能仅凭工具名称放行。写入、未知 MCP 服务、交互审批、图片、完整恢复与显式任务工具范围暂不支持，也不自动回退至 PI。

Claude 原生运行时使用 Orrery 独立安装的 `runtime/claude-native-2.1.220`；存在该安装且没有已有 Claude 配置时提供 `runtime-claude-orrery-native`。已有 Claude 配置（包括禁用状态）优先保留。PI 与 Codex 使用各自已有配置。内存数据库不启用 AgentCore，避免将非持久化存储误当作执行账本。

## 开源参考与实际复用

| 项目 | 参考内容 | 本实现采用方式 |
| --- | --- | --- |
| [PI Agent Core](https://github.com/earendil-works/pi/blob/main/packages/agent/src/agent-loop.ts) | 模型输出、工具执行、工具结果回送和继续推理的循环；工具调用配对与事件顺序 | 用 Go 编写适合 Orrery 契约的循环；没有引入 PI 的完整执行器或新增 Node 服务。 |
| [Codex](https://github.com/openai/codex/blob/main/codex-rs/core/src/tasks/mod.rs) | 会话与轮次、取消传播、任务终止和资源清理边界 | 用于设计单会话串行执行、关闭、失败后禁用会话、迟到结果失效的行为。审批和沙箱能力仍待后续接入。 |
| [Eino v0.9.4](https://github.com/cloudwego/eino/blob/v0.9.4/adk/runner.go) | Go 模型接口、消息、事件迭代及检查点/恢复设计 | 直接复用已有 Eino `BaseChatModel`、消息和工具 schema、流片段合并，以及 Orrery 的 `ProfileModelFactory`。循环由 Orrery 管理；首版未实现 ADK 检查点与恢复。 |

PI 根许可证为 [MIT](https://github.com/earendil-works/pi/blob/main/LICENSE)，Codex 和 Eino 根许可证为 [Apache-2.0](https://github.com/openai/codex/blob/main/LICENSE)、[Apache-2.0](https://github.com/cloudwego/eino/blob/main/LICENSE)。以上参考代码用于研究公开设计；本次没有复制或翻译其循环源文件。后续若直接移植代码，须记录具体来源版本、版权、许可证和修改说明。继续遵守实际使用与分发组件的许可条件。

## 循环与可信边界

1. 宿主 `SessionRuntime` 完成身份授权，再通过 `WorkspaceBroker` 创建工作区引用。
2. Engine 解析该引用，从可信运行请求获取身份、模型、历史、工具绑定与授权上下文。`SessionConfig.Model` 等字段不能扩大已授权请求。
3. 首次发送必须与已绑定请求的标准输入一致，模型收到原始结构化消息。后续文本轮次追加到本会话历史。
4. 请求模型，消费消息流，合并完整工具调用并检查结束原因。缺失结束标识、空回答、流错误及截断均不能成为成功；明确拒答保留对应结束类型。
5. 整批工具调用先校验名称、ID、JSON 与 JSON Schema，再按顺序执行。重复 ID、未知工具、无效参数或不足的执行预算均在分发前拒绝。
6. 每次工具执行前调用宿主 `ToolPolicy`，先原子持久化调用意图，再通过现有 `RuntimeToolDispatcher` 执行。结果状态落盘后，工具结果按 ID 配对，加入下一次模型输入，直到模型返回最终回答。
7. `kernelcompat` 管理标准事件序号、轮次和最终结果；现有 Orchestrator 生成开始与取消生命周期事件。

工具存在时，必须同时提供真实工具分发器、`AuthorizeTool` 和持久化 `ToolJournal`，否则在创建模型前拒绝打开会话。纯文本会话不要求工具账本。策略必须检查参数中的知识库、文档、目录等范围，并使用可信租户、Agent、用户或设备授权。工具名字在白名单中，不代表任意参数都得到授权。需要交互审批的操作在首版策略中应拒绝。

权限回调与分发器收到独立副本，不能修改后续执行的参数或会话授权。工具凭据不加入模型工具说明。检索结果和初始上下文的资料授权仍由宿主负责；测试中的策略不代替生产知识库的权限实现。

显式任务工具范围能力尚未声明：要求 `RequireExplicitToolScopes` 的请求在宿主和 Engine 层均拒绝。在生产授权策略和隔离验证完成前，不将官网接待或内部 Agent 切换到此内核。

## 首版预算和失败语义

- 默认每轮最多 8 次模型请求、16 次工具调用。可信请求中的 `MaxTurns` 只能缩小配置上限。
- 默认上下文序列化上限 512 KiB；单次模型流累计片段及单个工具结果上限 256 KiB。超限失败，不静默截断历史或拆散工具配对。
- 模型初始化与单次模型请求各自预算 45 秒；权限检查和工具执行共用单次工具预算 30 秒。整轮仍受兼容层和宿主预算控制。首版没有金额或精确 Token 的硬预算。
- 模型请求和工具传输错误不自动重试。工具执行结果不明确时，终止本轮，交由宿主确认，避免自动重复写入。
- 校验失败、执行失败或取消后，本 Runner 禁止复用；上层兼容层保留其既有生命周期规则。成功轮次才提交会话历史。
- 模型初始化、流建立/读取、权限检查和工具执行通过可取消的回调等待返回。超时或关闭后不再等待不合作回调；迟到的流会被关闭，迟到的权限批准或工具结果不会继续执行或改写状态。回调 panic 转为脱敏失败。
- 每个共享 Engine 默认最多 32 个同时执行的模型/权限/工具回调，可用 `MaxConcurrentCallbacks` 配置。已超时但实际尚未退出的回调继续占用名额；容量耗尽时拒绝新回调，避免无限累积等待 goroutine。该上限不跨 Engine 或进程共享，宿主应复用 Engine，不能通过新建 Engine 绕过限制。
- 返回超时不代表外部操作已经终止：不合作回调仍可能继续占用资源或产生副作用，直到其自行退出。真实模型、工具和数据库实现必须遵守取消契约；Go 无法强制结束任意不合作代码，硬终止需独立进程隔离。
- 会话历史仍在内存中，没有完整会话检查点或自动断点续跑。工具账本持久化调用身份和结果状态；不同调用 ID 或新运行 ID 的相同业务操作，仍需要宿主做业务去重。

核心可以消费真实流式 Provider。现有 `ProfileModelFactory.Stream` 在完整 HTTP 响应后输出单块；复用该工厂时，不把它描述为已实现上游 SSE 或已改善首包延迟。

## 工具执行账本与恢复边界

`SQLToolJournal` 复用宿主运行数据库的 `platform_records`，使用独立逻辑表名 `runtime_agent_kernel_tool_executions`。构造器接受文件或 PostgreSQL 数据库，拒绝内存驱动；不创建新数据库、启动迁移或新增服务。宿主必须提供持久化数据库并在重启后复用相同存储。当前实库测试使用文件 SQLite，未进行 PostgreSQL 实库验证。

唯一身份为 `TenantID + RunID + CallID`，调用指纹覆盖可信 Agent、执行主体、工具 ID 和已验证参数。数据库唯一插入负责抢占，任意已有状态都阻止再次分发；同一身份换参数、工具或 Agent 返回冲突。结束操作检查领取凭据，已落盘终态不能被其他终态改写。

同一租户与运行只允许一个尚未结束的工具调用。只要该范围存在 `started` 或 `unknown` 记录，即使模型换了调用 ID 或工具名，也拒绝新的工具领取。检查与领取在同一事务中完成，PostgreSQL 使用运行范围的事务锁，SQLite 先取得写锁再检查；被拒绝的领取会回滚。此检查直接读取已有账本，重启和旧版记录同样生效，不依赖新的内存标记。已完成、已报告失败或明确未分发的记录允许后续不同调用 ID；原调用 ID 始终禁止重放。

| 状态 | 含义与处理 |
| --- | --- |
| `started` | 已持久化调用意图。若进程退出或结束状态写入失败，无法仅凭此状态判断外部操作是否发生，必须核对；不自动重放。 |
| `completed` | 分发器正常返回可接受结果，状态先落盘再发结果事件；事件消费者随后断开也保留记录。 |
| `failed` | 分发器正常返回 `status=failed/error`；不代表外部系统一定没有产生副作用，同样阻止重放。 |
| `unknown` | 执行途中超时、取消、传输错误、panic 或无法接受返回结果；记录固定原因码，交给宿主核对。 |
| `not_dispatched` | 领取后、调用分发器前已取消或回调容量耗尽；保留证据并阻止原调用 ID 自动重放。 |

调用前登记失败时不会执行工具。调用后状态写入失败时停止循环，保留 `started`，不能发送成功工具结果或继续模型请求。取消后的状态写入使用独立、有时限的清理上下文，默认 2 秒；数据库实现仍须遵守上下文。授权拒绝或调用事件消费者拒绝时不领取调用记录。

账本只保存身份、摘要、状态、固定原因码和时间，不保存工具参数、工具结果、凭据或原始上游错误。宿主可用 `Get` 和 `ListRun` 查询指定租户与运行的记录；分页每页最多 100 条，跨范围游标拒绝。查询接口属于内部存储接口，宿主必须先授权；本阶段没有新增面向用户的查询 API 或管理界面。

此机制阻止相同调用身份重复分发，并阻止有未确认结果的运行继续执行工具；不缓存结果供模型重用，也不提供完整恢复或业务上的严格一次执行。已完成操作换调用 ID、宿主新建运行 ID、清理历史记录或使用另一数据库，仍不在业务去重保证内。当前没有自动清除未确认记录或人工核对后解锁的 API；宿主必须核对外部系统，不能简单新建运行绕过记录。对于会写入业务数据的工具，后续接入还需稳定业务幂等键和核对流程；不能因账本存在就开启自动重试或将 `SupportsResume` 改为支持。

## 宿主接入

先创建共享的 `WorkspaceBroker`，传给 `agentkernel.NewEngine` 和 `providers.NewSessionRegistry`。Engine 还需宿主提供模型工厂、工具分发器、权限策略和工具账本；通过 `SessionRegistryConfig.ManagedKernel` 注入，再显式选择 `managed`。未注入时保持不可用。桌面本地进程打包与 LocalAgentFacade 适配单独实现。

```go
journal, err := agentkernel.NewSQLToolJournal(runtimeStore)
if err != nil {
    return err
}
engine, err := agentkernel.NewEngine(agentkernel.Config{
    Workspaces: workspaces,
    Models: modelFactory,
    Tools: toolDispatcher,
    AuthorizeTool: authorizeTool,
    ToolJournal: journal,
})
```

宿主必须按其已授权的租户/模型配置创建工厂；首版从绑定请求读取模型名。不能向会话配置开放任意端点、凭据、工作区路径或模型配置引用。

## 验证

测试覆盖分片工具参数合并、模型—工具—模型的完整闭环、真实 HTTP 模型适配器与标准 Runtime 的集成、工具结果配对、多轮历史、参数校验、内部知识库拒绝、授权副本、工具重复 ID、预算、超时、取消、关闭、无效结束状态和事件消费失败。集成测试使用受控 HTTP 服务，不调用生产模型或真实知识库。

文件 SQLite 测试覆盖数据库关闭与重开、未结束调用记录、多个数据库连接并发抢占、终态不被重写、调用身份冲突、租户/运行隔离和分页。循环测试覆盖登记先于工具调用、结束写入先于结果事件、登记/结束写入失败、执行前后取消、超时迟到成功、panic、异常返回与结果超限；新 Engine 使用同一账本时不能再次执行相同调用身份。

补充测试覆盖不响应取消的模型工厂、流建立、权限回调和工具，迟到流关闭、迟到工具成功/panic 不改写结果、共享回调容量在实际退出前保持占用，以及单个名额完成顺序循环。文件 SQLite 测试还覆盖不同调用 ID 的跨连接并发领取、未确认运行在数据库重开后的阻断，以及拒绝领取后的事务回滚。

运行 `go test -p 1 ./internal/agent/runtime/agentkernel ./internal/agent/runtime/providers/... ./internal/agent/runtime/session ./internal/agent/core -timeout 180s`，并运行生产 Registry、Authorizer、Credential、PI 和默认运行时相关测试。Windows 测试辅助程序的嵌套构建可使用进程级 `GOFLAGS=-buildvcs=false`。本地临时目录和 Go 构建缓存放在 `D:/Orrery/.tmp` 与 `D:/Orrery/.cache`。
