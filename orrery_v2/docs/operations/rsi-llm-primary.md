# RSI 使用大模型主诊断

生产配置 `ORRERY_RSI_MODE=llm` 后，RSI 失败样本跳过 Laya 初判，由现有后台诊断 worker 读取已保存的完整证据，调用平台默认 LLM。其他决策场景仍使用各自的 decision profile。省略此配置或设置为 `laya` 保持原先的 Laya 初判与按需 LLM fallback。

此模式要求 `ORRERY_DECISION_MODE=shadow`（兼容 `ORRERY_LAYA_MODE=shadow`）。无效模式或关闭 observer 时启用 LLM 主诊断会阻止启动，避免配置被静默忽略。

`ORRERY_RSI_LLM_FALLBACK_PROFILE` 继续用于指定诊断 LLM profile；空值使用 `platform-default`。`ORRERY_RSI_LLM_FALLBACK_MAX_CALLS_PER_HOUR` 同时约束主诊断，默认每小时 20 次，允许 1–100。主诊断不受 `ORRERY_RSI_LLM_FALLBACK=off` 影响，该开关只控制 Laya 模式的 fallback。

样本、证据和模型调用预留仍持久化。相同观察只预留一次调用，重启不重复付费调用；超出每小时预算或完整样本 JSON 超过 128 KiB 时保留未能诊断的状态，不截断证据后强行作答。队列有界，模型推理不阻塞工具执行。

诊断可返回技能缺陷、工具使用问题、环境问题或证据不足。学习建议通过 `assessment_source=llm_primary` 标识来源，模型信息取自实际诊断结果。模型输出只提供改进建议；候选生成、复现、测试、发布与权限控制继续使用现有 RSI 流程。此配置不会自动放开技能发布或失败重试。

启动日志 `decision.rsi.enabled` 包含生效模式与每小时调用上限。回退时移除 `ORRERY_RSI_MODE=llm` 或改为 `laya`，重启 API；保留已有样本和诊断记录。
