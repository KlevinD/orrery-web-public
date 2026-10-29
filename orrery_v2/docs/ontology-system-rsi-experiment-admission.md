# 原生 RSI：诊断后的实验准入检查

本阶段完成“新任务证据 → 模型诊断 → 宿主实验准入检查 → Ontology 证据引用”小闭环。它没有接通原生任务的源码候选生成、隔离回放或生产采用；完整自主 RSI 尚未完成。

`orrery-runtime-experiment-admission-v1` 在已有诊断落库流程中执行，不增加定时业务任务或独立 worker，不额外调用模型。它重新核对冻结的 request、execution、terminal、review、observation、诊断输入摘要和模型收据；模型文本和手填的 baseline_ready 都不能补齐基线证据。

准入结果包含 workflow、skill、tool、business_logic、agent_loop、knowledge、memory、ontology、optimizer 九个领域的捕获范围及缺口，并独立记录候选修改范围、隔离任务数据集、sandbox 策略和受信原生基线核验器尚未准入。实际派发依赖被截断、包含非法观察或运行器未返回时也记录对应阻断原因。当前集成缺口决定 v1 结果为 blocked；它不是可执行授权，也不证明模型诊断的因果判断正确。

结果绑定原租户、case、attempt、输入 SHA256 和诊断收据，随新诊断持久化。case 保持 diagnosed；现有 Ontology case 的 evidence_ids 带上准入收据 ID，沿用原任务 ACL，不投影诊断正文、原始任务/工具输出或租约 token。保存诊断与推进 case 之间发生中断时复用已保存答案，不再次付费。

该准入协议适用于捕获了原生运行证据的诊断。维修反馈复用同一诊断队列，但不具有 request/execution/terminal 基线，保留其诊断收据和 blocked 状态，不生成原生实验准入结果；查询返回 unavailable。两条路径组合升级时，已提交的维修诊断继续确认，不改历史、不重复调用模型。

升级前已完成的诊断只在显式读取时生成派生准入结果，并标记 persisted=false；不补写历史 case、修订或图谱。旧二进制已提交 case、尚未确认诊断队列时，新二进制可继续确认，保留原 case 修订。历史请求排除规则和现有小时调用上限继续生效。

主机只读检查：

```sh
resourcectl improvements --db "$ORRERY_RSI_DB_URL" --tenant TENANT --attempt ATTEMPT
```

输出新增 experiment_admission 和 experiment_admission_persisted。尚无已完成诊断时不输出准入对象。命令没有候选执行、资源采用或发布入口。

后续必须接入受信运行基线、明确变更范围、隔离数据集与领域适配器，再对真实候选进行独立评估。现有 rsictl 源码实验器仍拒绝直接承接生产任务基线，不以离线文件实验结果冒充生产任务修复。
