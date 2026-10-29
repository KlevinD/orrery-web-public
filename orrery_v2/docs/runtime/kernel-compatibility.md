# Orrery 自研内核兼容层

`kernelcompat` 兼容层把自研循环接入现有 `SessionRuntime` / `SessionBackend` 契约。`agentkernel` 已提供首版 Go 模型与工具循环，接入方式和边界见 [AgentCore 设计](agent-kernel.md)。生产启动注入 Engine，并提供可显式选择的 Orrery AgentCore 配置；不自动切换已有 Agent。

## 选择规则

- `runtime.kind = pi` 继续直接解析到原来的 `pi.NativeConnection`，没有增加 PI 包装、转发或自动切换逻辑。
- 复用已有 `managed` 类型标识自管运行时，设置及 Agent API 的枚举同步允许该类型；没有改变任何 Agent 默认值。
- 只有宿主程序显式设置 `SessionRegistryConfig.ManagedKernel` 并提供有效 `Engine` 时，`managed` 才可解析。业务还需显式选择 `managed`。
- 未提供 Engine 时，`managed` 保持不可用。现有 `runtime.managed.enabled` 配置本身仍不足以启用该路径。
- 新内核打开或运行失败会返回失败，不自动改用 PI，避免两条执行路径重复产生业务副作用。
- 生产装配使用现有 runtime 数据库的持久化工具账本；缺少依赖时不可用，当前仅放行已授权的只读工具。

## 接入接口

```go
registry, err := providers.NewSessionRegistry(providers.SessionRegistryConfig{
    ManagedKernel: &kernelcompat.Config{
        Engine: yourKernelHost,
    },
    Authorizer: yourRuntimeAuthorizer,
})
```

`Engine.OpenSession` 接收标准会话配置和不透明工作区引用，返回会话级 `Runner`。`Runner.Run` 负责实际模型与工具循环，返回显式的终止状态。`Runner.Close` 释放模型连接、工具和其他会话资源。兼容层负责生命周期、序号、轮次、超时、取消和标准事件输出。

Engine 必须通过受信任的宿主解析工作区引用，使用现有授权工具分发器；会话配置和模型参数不能授权目录、知识库、凭据或工具。`SessionRuntime` 的应用授权发生在创建工作区与打开 Engine 之前。直接调用 BackendConnection 的内部宿主也须承担同样的授权责任。

首版未声明能落实显式任务工具范围，要求 `RequireExplicitToolScopes` 的请求仍由现有宿主拒绝，且不会打开 Engine。完成真实工具分发器、公共与内部知识库的隔离及其测试之前，不应将接待 Agent 切换到此内核。

## 首版能力与限制

- 支持文本输入、消息增量、工具调用与结果事件、用量事件、明确结束状态、轮次取消和同会话顺序执行。
- 兼容层为每轮分配 epoch 和有序事件；开始与取消生命周期事件由现有 Orchestrator 生成，Runner 不能伪造开始、批准或结束事件。
- 同一轮工具调用 ID 不得重复，结果必须对应尚未完成的调用，成功终止前不得遗留未完成调用。
- 能力声明不包含恢复、交互审批、转向输入、修改模型、多媒体或远程桌面执行。请求这些能力会明确返回不支持。
- 超时、无效事件和执行失败使会话不能再次开始新轮次，宿主必须替换会话。超时和取消之后，迟到结果不能改写终止状态。
- 即使 Runner 不及时响应取消，也不会在它实际退出之前启动另一轮。Go 不能强制终止任意 goroutine；真实内核与工具必须响应取消，必要时在独立进程中提供硬终止边界。
- 事件消费阻塞达到上限会关闭流，宿主可观察到传输失败；不会无限等待或静默丢失结束事件。
- 默认事件容量 128、背压等待 1 秒、单轮预算 2 分钟、关闭等待 2 秒；宿主可显式配置。请求与单个事件负载上限为 1 MiB。
- 调度确认请求的短期取消不会取消已接纳的执行。执行由轮次预算、明确取消命令和会话关闭控制。
- 复用现有会话命令账本，提供有限保留窗口内的进程内重放保护。这不是数据库去重、持久化恢复或严格一次业务执行保证。

## PI 回归与后续工作

注册测试覆盖默认 managed 不可用、显式启用后 PI 类型及能力不变、授权发生在 Engine 打开之前、新内核失败不切换到 PI。兼容层测试覆盖消息和工具事件、重复命令、取消、超时、迟到结果、无效事件、panic、背压及关闭。

开发验证还需运行原 PI provider 测试和生产运行时注册测试。测试中的 Engine/Runner 仅是契约替身，没有随生产启动注册的示例或假成功实现。

`agentkernel` 已按同一接口接入现有模型工厂、工具分发接口和持久化工具执行账本。工具会话缺少账本时拒绝打开；账本记录不等于会话检查点，兼容层仍不声明恢复能力。生产知识库授权策略、完整恢复及审批能力继续分阶段实现，每项能力通过行为测试后再扩展声明。桌面端需另行增加打包后的本地进程接口和 LocalAgentFacade 适配，不能因为 Go 接口可编译就视为客户端已经兼容。
