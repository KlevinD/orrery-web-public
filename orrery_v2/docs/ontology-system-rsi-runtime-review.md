# RSI：任务运行证据审查与实验准入

生产终态反馈同步生成 `orrery-runtime-review-v1` 审查记录，将运行事实、业务质量、故障归因和基线覆盖分开保存。它是宿主的固定证据策略，不是模型业务评分或根因诊断。

## 自动审查

`BuildRuntimeReview` 只读取原 attempt 已捕获的请求和终态，不查询结束时的最新注册表。它保留运行状态、trace 收尾、耗时和 provider 报告的各模型 token 用量；用量不换算为供应商账单。

运行完成或失败时，业务质量均保持 `unassessed`，故障因果保持 `unresolved`。成功不证明业务目标达成，失败不证明某个绑定工具或技能有错。只有请求而无终态时，状态为 `unknown`，耗时保持缺失。

审查覆盖 workflow、skill、tool、business_logic、agent_loop、knowledge、memory、ontology、optimizer 九个领域：

- 控制参数摘要为 `controls_only`，仍缺少运行循环及 provider 实现证据。
- 工具和技能为 `declared_bindings_only`，版本缺失、同一资源版本或包摘要冲突会单列。绑定摘要不充当工具实现哈希，技能声明摘要仍需核验实际包内容。绑定不证明已调用。
- 其他领域为 `uncaptured`，不自动推断不适用。

v1 请求格式不能证明完整依赖清单，所以 `baseline_ready` 为 false。模型填入 ready 或 pass 字段不能改变这一事实。完整可信基线解析器仍需接入。

## 持久性与 Ontology

审查与原终态、Observed Case、不可变修订和投影 outbox 一起提交数据库。冲突回滚整个事务，终态日志恢复沿用同一路径。原 Case ID 和 Observation 内容保持一致，旧日志重放不创建新案例或改写历史。

记录存于 `ontology_improvement_runtime_reviews`，按租户、attempt 和协议版本隔离。读取时用原请求与终态重新计算结果，拒绝内容与原证据不符的记录。哈希不能抵御受信宿主或数据库管理权限被控制。

正常 outbox 投影增加 `improvement_runtime_review` 对象和 `improvement_case_has_runtime_review` 关系，沿用原 SourceAccessRef，通过 MappingRule 与 ConformanceGate。图谱显示状态、覆盖和缺口，不展示 prompt、输出、密钥或租约令牌。审查不可变，案例后续转换不改写先前审查。

## 准入约束

生产捕获案例进入 `tested` 或 `monitoring` 时，除原执行与回执 verifier 外，还必须提供受信 `RuntimeBaselineVerifier`。该集成须解析原 attempt 的完整依赖、核验不可变实现内容和来源、匹配 ChangeSet 冻结基线，并以证据解决基线覆盖缺口。业务质量可继续等待后续独立评估。当前尚未注册生产实现，普通 verifier 不能绕过检查。

只有历史 lifecycle、没有原请求快照的案例也不能进入这些状态。现有源码回放运行器在模型或容器操作前拒绝这些生产案例，SourceRuntime 也不会首次采用。正常准入的离线源码任务按原协议执行；已采用案例的结果反馈与回滚补偿不受新增准入限制。

monitoring 记录仍是已发生动作的凭据，不是发布命令。后续生产发布协调器必须在实际动作前执行完整基线核验；该检查不能替代领域授权和发布适配器。

## 主机读取

```sh
resourcectl improvements --tenant '<ontology-tenant>' --attempt '<runtime-attempt-id>'
```

输出增加 review 和 review_persisted。旧记录或只有 prepared 请求时可只读推导，review_persisted=false；查询不回填数据库、领取工作或改变状态。跨租户请求继续拒绝。

该阶段完善诊断与准入输入。独立业务质量评价、真实故障归因、完整可信基线、各领域原生改进执行、跨资源发布协调和真实任务的多轮收益验证仍未完成。
