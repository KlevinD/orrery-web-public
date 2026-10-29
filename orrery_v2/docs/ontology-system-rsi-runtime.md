# RSI：受控任务运行器的采用与反馈

本阶段在真实候选与隔离实验之后增加实际运行版本、授权采用、采用后任务验收、回滚和下一轮观察。实现为 `internal/app/improvement/source_runtime_*.go`，主机入口为 `cmd/rsiruntime`。

## 范围

SourceRuntime 是一个明确准入的源码任务运行器：数据库中的活动快照决定后续任务使用哪些代码和隔离策略。它复用原实验的真实源码、驱动和 Docker 镜像，执行时重新读取已提交的活动快照，不从请求或候选提案中取代码。一个 bundle 可以包含多个不同 TargetKind 的源文件，所有目标一起采用或一起恢复。

这提供了**准入源码任务范围内的闭环**。它没有把 Orrery API 服务、生产 Agent、外部工具、业务数据库、知识索引或记忆存储切换到新实现。各领域生产 DomainAdapter 仍未注册；不能把源码的领域标签当成领域原生发布能力。采用回执属于本运行器自己的执行域，不是生产 ActionContract / HIL 的批准记录。

## 运行链路

1. `prepare` 重新验签原实验、重新计算原评估，并检查候选达到门槛。冻结 RuntimeID、Case、Evaluation、ChangeSet、当前运行器二进制摘要、观察任务及其预算。观察输入必须与原实验所有输入不同，也不能互相重复；仍要求 repair、regression、held_out、业务与安全样本。
2. `authorize` 用单独的宿主授权密钥签署完整计划和有效期。授权可以允许首次建立基线运行器，范围同时包含本次采用及必要的回滚。候选模型没有授权密钥或证据签名密钥。宿主协调器和密钥持有者属于信任边界；HMAC 不能防御协调器或宿主被控制。
3. `cycle` 核对运行器自身摘要。采用前检查当前活动源码、资源版本、任务与策略均匹配冻结基线；首次建立基线必须已获授权。原子事务同时写入新活动快照、已完成动作凭据、monitoring Case 修订和 Ontology outbox。数据库故障不会留下只切换代码、没有 Case 的半完成发布。
4. 在新观察数据上实际执行冻结基线与已采用版本；每次已采用任务都从活动快照读取代码。两组使用同一镜像、输入与限制，留出验收仍由宿主精确 JSON oracle 完成。全部执行、容器 ID 与原实验均不同，比较同一批新输入的两组分数，避免把不同数据集的分数直接相减。
5. 宿主重新计算实际 gain、逐项回归、held_out、业务和安全结果、耗时与操作单位。通过进入 verified；不满足门槛进入 rollback_required，`cycle` 随后用同一范围授权执行回滚。
6. 回滚仅在当前活动指针仍精确匹配本次采用快照时进行，恢复全部源文件与资源版本，并增加运行器 revision。后续所有任务读取恢复后的代码。检测到别人已采用新版本时拒绝覆盖。
7. 完成后创建一个 parent_case_id 指向本轮的 observed Case，记录本轮最终版本、实际任务批次和结果证据。后续实验仍需明确准入；这里没有自动调用下一轮模型，也没有实现优化器 O0/O1 自举比较。

## 预算与中断

原 ChangeSet.Budget 约束原实验。采用后的任务执行使用授权计划中额外明确给出的 observation_budget，单位同为 bounded_operations，不能超过原 Goal.MaxCost。观察执行不再收取一次模型调用单位；没有调用新模型。它仍不是供应商账单或 token 预算。

观察阶段复用持久化阶段预留：已完成阶段和最终 Case 可在重启后复用；外部执行结果未知时拒绝自动重复执行。观察期间发生基础设施故障、失去租约、基线对照完全失败或授权过期，可能停在 monitoring / rollback_required，需要运维处理；当前不提供未知执行自动补偿，也不伪造业务失败来通过状态机。运行器升级须重新冻结二进制和计划。

历史 Case 只通过 bounded evidence 与 parent 关联进入下一轮观察，不自动注入全部历史内容。持久化证据保留/容量治理、持续生产监控、生产队列和领域原生执行适配器仍是后续工作。

## 主机命令

```sh
# 两个不同的受保护宿主密钥（>=32 字节随机值，base64），不写入计划：
# ORRERY_RSI_SEAL_KEY: 原实验的证据验签密钥
# ORRERY_RSI_AUTH_KEY: 运行器的范围授权密钥
# request.executor_sha256 必须是当前 rsiruntime 的真实二进制摘要。
rsiruntime prepare --db file:///project/runtime.db \
  --work-root /project/.tmp --input /project/request.json > /project/plan.json
rsiruntime authorize --input /project/plan.json \
  --issuer host-operator --ttl 1h > /project/grant.json
rsiruntime cycle --db file:///project/runtime.db --work-root /project/.tmp \
  --input /project/plan.json --authorization /project/grant.json
```

`observe` 和 `rollback` 可分阶段恢复，`active --tenant ... --runtime ...` 读取当前快照。单独读取活动状态仍需要宿主密钥环境；没有公开 HTTP 写入接口。独立运行器数据库的 Ontology outbox 不会自动进入生产图谱。Windows 上所有工作目录、输入、数据库、临时目录、构建和报告都须按仓库约定放在 D 盘。
