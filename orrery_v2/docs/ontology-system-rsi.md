# Ontology 系统级 RSI：统一底座

RSI 的改进单位是完整任务与流程。一项改进允许同时涉及 workflow、skill、tool、business_logic、agent_loop、knowledge、memory、ontology 和 optimizer。技能版本管理只是其中一个领域。

## 本次实现

`internal/app/improvement` 提供任务级改进案例及持久化生命周期：

```text
observed → diagnosed → proposed → tested → evaluated → monitoring → verified
                                                        ↓
                                                 rollback_required → rolled_back
```

尚未采用的案例可以拒绝或取消；已经进入监控的案例需要结果验证及回滚记录，不能用“取消”隐藏已发生的发布。结束后的新任务观察可通过 `parent_case_id` 接续下一轮，同一任务的各轮保持独立证据。

- Observation 绑定任务、执行、来源访问范围、证据、指标和已知依赖。没有可信版本或摘要时保留缺失信息，不能把它当成已冻结的实验基线。
- Diagnosis 记录有证据的判断和涉及的领域，可引用已有 Ontology DiagnosisCase。
- ChangeSet 固定业务目标、最小收益、成本/时长上限、实验预算、基线依赖、多个目标的候选版本、补丁和回滚产物，以及数据集、策略、生成器与裁判版本。变更目标必须对应基线，依赖必须无环。
- Experiment 绑定变更集摘要、不同的基线/候选执行和产物。
- Evaluation 保存结果，包括不合格的结果；仅有不退步分数不足以进入采用状态。
- Release 记录实际完成的领域动作及全部目标的回执，不能作为发布命令使用。
- Outcome 绑定发布后的执行与结果。只有满足冻结业务目标的结果才能确认收益；回归进入 rollback_required。
- Rollback 要求全部目标恢复到基线版本的实际动作回执，部分恢复不能被记录为完成。

每次领域状态转换在同一平台数据库事务中保存当前聚合、不可变修订、命令幂等回执和 Ontology 投影 outbox。工作租约持久化，续约/接管会轮换 fencing token。旧 worker 不能凭过期令牌完成写入；进程重启后可读取当前轮次并接续。调用方修改输入对象不能修改已存证据。

从实验开始，执行证据必须经受信任 `Verifier` 验证。该端口需要从可信存储解析实际产物、核验摘要和执行来源，确认裁判独立、领域动作授权及回执，并验证后续执行实际使用发布版本。没有配置 verifier 时，这些转换拒绝执行。字段格式校验不能替代来源验证。

`DomainAdapter` 定义资源读取、候选校验、实际采用与回滚边界。各领域保留自己的租户授权、治理动作、并发版本检查和幂等语义。注册表报告实际已登记能力；声明 TargetKind 不等于已实现该领域的自动修改。

## Ontology 集成

默认定义安装中增加 improvement_case、improvement_change_set、improvement_resource_version、improvement_experiment、improvement_evaluation、improvement_release、improvement_outcome，以及任务、执行、诊断、前轮和各阶段之间的关联。

投影通过正常 MappingRule、ConformanceGate 和生产 Ontology 状态投影处理，沿用现有事务水位检查，拒绝旧修订覆盖新状态。所有节点沿用来源任务的 SourceAccessRef；不将工作租约令牌、原始任务输出或模型推理写入展示字段。完整候选和执行产物留在所属系统，通过引用与内容摘要关联。

生产构造器将统一存储接到已有 Agent 执行 Ontology sink。任务 completed、failed、timed_out、blocked、cancelled 的终态事件形成观察，成功完成也保留为后续业务质量与效率分析的输入。开始事件不会产生改进案例。已存在的私人助手 Ontology 隔离规则继续适用，当前这条入口不覆盖私人助手执行。

`ontology.improvement_projection` 底层 sweeper 将事务 outbox 投递到 Ontology，失败保留供重试。它负责投影，不承担候选生成或资源修改；历史 cron 不构成这条机制。

生产请求与终态捕获、终态本地日志及恢复器补充了实际任务反馈的持久性。`ontology.runtime_feedback_recovery` 在数据库恢复或重启后补交已持久化日志，不重复执行业务任务，详见 [生产终态反馈恢复](ontology-system-rsi-feedback-recovery.md)。没有终态证据的请求继续保留未知状态。

任务终态还会生成[运行证据审查](ontology-system-rsi-runtime-review.md)，自动区分运行事实、九个领域的基线缺口与尚未完成的业务评价。审查沿用 Ontology 投影链路，实验和采用记录须额外通过受信基线核验。

## 运维检查

```sh
resourcectl improvements --tenant '<ontology-tenant>' --limit 20
resourcectl improvements --tenant '<ontology-tenant>' --id '<case-id>'
```

数据库地址可由 `ORRERY_RSI_DB_URL` 提供，避免出现在命令行中。该命令需要主机数据库权限，只提供明确租户范围内的只读检查，不提供状态转换、工作领取或发布参数；输出隐藏工作租约。

## 尚待完成的生产能力

本次是统一底座及任务事件入口，不能验收为完整系统级 RSI。生产尚未登记真实候选生成器、实验 runner、独立产物/回执 verifier 和各领域 apply adapter，也没有跨存储发布协调器。当前底座不自动修改工具、业务代码、知识或 Agent 运行机制。

后续接入顺序：

1. 任务指标与反馈归因器，以及从可信运行记录解析完整依赖、版本和内容摘要的基线解析器。
2. 工具、知识、Agent/流程等领域的真实候选生成与隔离实验；冻结业务评估与独立裁判。
3. 可信来源 verifier、领域治理动作执行器和持久化发布协调器。跨资源部分失败需要发布日志和补偿，当前单库证据事务不代表跨系统原子发布。
4. 发布后实际任务反馈控制器、回滚执行与下一轮调度，完成至少两轮跨领域真实案例。
5. 逐项覆盖九个领域，验证优化器 O0/O1 公平比较及采用后由新优化器执行下一轮。

测试中构造的候选、评估结果、回执和 verifier 用于验证生命周期、存储、投影及约束，不能算作生产模型自我优化的证据。过期覆盖豁免属于已有仓库门禁问题，不能通过延长日期或忽略门禁来宣称全仓通过。
