# RSI 第四阶段：真实运行请求与终态证据

这一阶段把生产任务执行代码连接到 RSI 观察账本。前三阶段的受控源码运行器能够采用、观察和回滚候选，但生产 Agent 的历史事件通常只有运行器名称，无法可靠区分一次任务实际采用了什么配置。本阶段补上证据入口，不宣称已经实现所有资源的自主升级。

## 实际行为

讨论、mention/个人助手和任务队列 worker 的生产依赖工厂都接入 `RuntimeEvidenceRecorder`。它装饰已有 durable trace 接口，不读取结束时的“最新”注册表。

1. 每次 `BeginTrace` 生成新的主机随机 attempt ID；相同 Run ID 的重试拥有不同 attempt。
2. BaseAgent 完成绑定解析、工具过滤、上下文预算和 diary 请求准备后，调用 `RecordRequest`。此时先把请求证据写入 core 数据库，再交给原 trace recorder；该步骤失败则不会调用运行器。
3. `Finish` 始终执行原 trace 收尾。若已有请求证据，将请求绑定的终态、Observed Case、不可变修订和 Ontology 投影 outbox 放在同一个数据库事务内提交。
4. Ontology 不可用时，反馈仍保留在账本，既有投影 sweeper 可重试。运行成功只进入 Observed，不产生“业务评估通过”、自动采用或 Verified。

请求证据明确标记 `prepared_for_dispatch`，它证明准备发送的请求，不证明 provider 接收成功，也不证明所有绑定的技能/工具实际被使用。终态另行保留 trace 收尾状态，trace 写入失败不会被伪装为完整 trace。

## 证据范围

- 控制参数摘要覆盖 model、max turns、timeout、语义闲置超时、context limit、sandbox mode、approval、network、boundary。这只是有名称和版本的控制参数投影，不是完整 Agent 源码或流程版本。
- 输入、有效 prompt、ContextPack 分别保存 SHA256，不保存原文。任务和记忆变化不会被误算成控制参数升级。
- 工具保存 ID、alias、version 和去掉 SecretEnv 的绑定摘要；绑定摘要不当作工具实现内容哈希。
- 技能保留绑定摘要和格式有效的声明 package digest。声明的 digest 仍需在后续准入时通过真实包内容核验。
- 已知且无歧义的版本进入 Observation 的依赖。没有版本、同一资源有冲突版本、没有实现哈希时，保留证据缺口，不能据此构造完整冻结基线。
- 终态保存 pipeline status、duration、model token usage、完成时间；不复制输出正文、错误正文或 provider 任意 metadata。

数据库记录以 tenant 与 attempt 隔离。同一请求/终态可幂等重试，不同内容不能覆盖同一 attempt。Case 的 `related_objects` 包含 `runtime_attempt`，便于查回证据。所有图谱事件继续沿用来源任务的访问范围。

## 运维读取

主机 CLI 继续只读，不提供修改、lease 或发布能力。数据库 URL 从已有 `ORRERY_RSI_DB_URL` 读取，避免在命令行打印凭证。

```sh
resourcectl improvements --tenant '<ontology-tenant>' --id '<case-id>'
resourcectl improvements --tenant '<ontology-tenant>' --attempt '<runtime-attempt-id>'
```

请求已经捕获但没有终态时，第二个命令返回 `terminal: null`，不会猜测任务结果。任务队列和普通讨论的 tenant 归属沿用现有投影规则；不匹配的 mission 范围被拒绝。

## 当前边界

本次代码改变生产入口的装配方式，但开发分支尚未合入当前生产版本。验收使用独立测试库和替代 provider，不把测试调用描述为真实生产流量或新的付费模型实验。

若数据库在结果提交后不可用，错误沿用 BaseAgent 的 post-commit 分类，避免错误触发业务任务重跑。第四阶段未包含恢复器；后续已增加[生产终态反馈恢复](../ontology-system-rsi-feedback-recovery.md)，可补交已经持久化在宿主日志中的终态。日志发布前崩溃仍可能留下 prepared 记录，不能声称每次尝试最终都有完整反馈。

原始 trace 路径仍可能随相同 Run ID 的重试而被覆盖；新的请求/终态记录按 attempt 独立保存。TraceRef 仅作辅助引用，不能充当唯一的不可变证据。

完整业务策略、Agent 源码/完整循环、知识库与记忆的可执行版本、远程工具实现版本、跨资源发布适配器、独立业务评估、崩溃恢复和账本归档仍需继续实现。本阶段没有把历史证据注入模型上下文，也没有新增定时优化任务。

## 验收记录

2026-10-16 验收结果：Windows 的 improvement、agent/core、rsictl、rsiruntime 共 230 项顶层测试通过、6 项跳过；相关 server 测试 42 项通过、1 项跳过。Linux 独立容器环境下 improvement 34 项通过、0 跳过，resourcectl 的两个只读检查测试通过。Linux 编译与相关包 go vet 通过。

PostgreSQL 验收使用临时容器、随机本地端口及独立 `orrery_test_` schema，验证两个连接并发捕获/完成同一 attempt、冲突终态拒绝，以及关闭连接并重新打开后的幂等与持久性。未使用生产数据库。

全量 server 初跑缺少未随仓库跟踪的 `config/orrery.development.yaml` 模板，已中止；临时补齐已有开发模板后，重跑本次相关测试。不能将这些结果称为全量 server 回归通过。临时模板已移存至 D 盘验收目录，不进入提交。

GitNexus 未索引本工作树；提交前调用 detect_changes 返回 repository not found，影响分析由源码调用关系和上述测试补充。日志、二进制及传输哈希清单保存在 `D:/Orrery/builds/system-rsi-runtime-evidence-20261016`。
