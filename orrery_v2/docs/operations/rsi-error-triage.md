# RSI 错误分流与维修反馈

适用契约：ORR-RSI-01、ORR-TRACE-01、ORR-REPAIR-01、ORR-RECOVERY-01、ORR-DEPLOY-01。本增量修复错误留在普通观察、维修反馈没有诊断接线及重复计数问题；实验、独立验收、发布和效果证明仍分别执行。

## 状态与证据

- 新的 `managed_repair_needs_attention` 进入 `pending_diagnosis`，与既有 RuntimeDiagnosis 队列、每小时预算和持久化调用回执共用后台。诊断输入保留原维修 Agent 的已脱敏摘要、来源 ACL、故障、实际 run 和证据引用；不伪造另一场调查，也不把摘要当独立验证。
- 诊断完成后，原维修尚需处理的事项进入 `blocked`，保留诊断及原摘要；不能仅靠模型文字清除供应商条件、原业务验收或审批。原运行出错、诊断完成、业务完成、报告正文和渠道送达各自保留真实状态。
- 诊断关闭、证据不全、历史禁入、模型不可用或输出校验失败，明确记录阻塞原因。模型失败不反复重发不确定的付费调用。
- 新的 captured runtime failed/timed_out/blocked 进入待诊断；缺少完整执行/依赖证据则受阻。只有生命周期的同类事件标为受阻并要求关联 captured evidence，不拿缺失的原始执行让模型猜测。
- `observed` 保留给普通改进观察，不代表修复已经成功。`diagnosed` 也不自动意味着确定了根因。

## 去重和历史

每份接受回执继续不可变保存；同一租户、故障、维修 job、batch、实际 run 和维修状态使用一个主案例。计数、last_seen 和回执时钟刷新不会增加诊断任务；新的实际 run 或结果状态会创建自己的案例。

后台按持久化游标有界扫描旧案例。只有正常的当前回执接入已建立主案例时，才把同身份、同 ACL、未被占用的旧 `observed` 维修副本标为 `superseded`，记录主案例 ID 并保留原始 observation 和历史修订。不会因此重新派发维修、调用业务工具、重发消息或重放旧业务；也不会批量将旧失败标为已诊断或已修复。

图谱仍使用原来源访问边界，展示排队、受阻原因或主案例引用；不把原 Agent 的诊断正文复制进通用图谱摘要。公开回复仍由 Agent 决定。本增量不修改微信凭据、原业务目标、定时配置、QA、审批或独立 repair worker。

## 验证

回归入口为 improvement、einomodel 和 server 的 AutomaticRepair / RuntimeDiagnosis / Improvement / RuntimeEvidence / SystemUpdate 测试，另有生产平台原生测试。新增覆盖接受回执到诊断队列、原结论复用、同次反馈去重、失败受阻、租户隔离、事务回滚、历史不重放及旧记录归并。实际部署与自然增量结果需看对应发布回执，测试通过不等于微信故障已解决或已送达。
