# 定时 Agent 报告生命周期

本增量遵守 `ORR-REPLY-01`、`ORR-DELIVERY-01/02`、`ORR-RECOVERY-01`、`ORR-TRACE-01`、`ORR-WORK-01`、`ORR-SCHEDULE-01`、`ORR-BUSINESS-01`。完整系统基线的编辑源和 PageIndex 发布库另有稳定版本，本文说明这次运行增量，不把它冒充全系统重新审计。

正常报告是执行 Agent 在原运行中写出的最终公开回复。正式业务结果、报告和投递具有不同状态：内核结束不等于业务通过；报告准备好也不等于微信送达。

报告后台与投递后台各自每 30 秒唤醒，使用现有内部 heartbeat，不新增用户定时任务。投递后台只读取已保存的正文，既不调用模型，也不重跑业务。

## 报告状态与恢复

jobs 库逻辑表 `scheduled_agent_reports` 保存原 scheduled run、原 runtime run、Agent、来源指纹、状态、尝试次数、租约、退避、期限、实际作者 runtime run、正文 SHA-256、trace 引用与失败原因。

| 状态 | 含义 |
| --- | --- |
| pending | 缺少报告，等待首次或退避后的恢复 |
| generating | 已原子领取一次恢复，租约有效 |
| ready | 已保存、通过公开内容校验的 Agent 正文 |
| withheld | 内容不安全，或恢复期间来源/收件权限改变 |
| failed | 达到次数或期限等边界，保留内部原因 |

正常路径保存原 AgentReport，额外模型调用为零。缺少报告时先读取同一执行的持久化 queue payload 与终态 trace，核对 tenant、Agent、run、任务与 mission。如果终态 trace 已有完整 final 正文，恢复该份原文；不把未完成流式草稿当报告。

确实没有正文时，仅支持具有空工具面约束的 managed AgentCore，由原 Agent 身份在独立 `scheduled-report-*` 运行中根据已保存证据补写。沿用任务显式模型/profile；未指定时按原 Agent 当前配置继承，不作额外 fallback。使用独立空目录、单轮、18 秒运行预算；不继承原业务 prompt、会话、工具、技能、MCP、环境、权限信封或 mission/task 执行绑定。通过共享 production registry 和既有 durable trace / Ontology / RSI evidence recorder 留证；不提交或改写原业务结果。

不支持的 native runtime、缺失/不匹配证据、过大材料和不安全正文保留具体内部失败码；不静默改用其他模型或运行时。模型不可用时不能承诺一定产出报告，也不发送系统模板顶替 Agent。

每个来源版本最多尝试 3 次，退避 5 分钟，原任务完成后 30 分钟截止；单次租约 1 分钟，每轮最多开始一次恢复。尝试在调用前落盘，完整记录 CAS 防止多进程重复领取与过期作者覆盖。重启恢复沿用次数、期限与首次激活时间。

`recovery_activation_v1` 只在首次启用时保存，不随重启后移。仅激活后结束的新运行参与缺报告补写，显式旧 replay ID 也不会越过这一边界；不重放已过期历史。既有正常报告的通知激活/幂等规则保持。

## 正文与投递

原执行号与报告作者执行号分别保存。原始 final / trace 恢复的作者仍是原 runtime run；补写的作者是新的 report runtime run，不伪装成历史作者。投递前后校验来源指纹、作者、正文 hash、收件人与实时权限，正文不安全时暂缓，不改写为另一份回复。

微信仍复用已有主人个人助理出口及幂等键。入队前把确切正文持久化；网关重试同一正文。报告不会把 failed 改成 completed，不降低产物、QA、审批或股票/公众号任务要求，也不因本次变更启用旧小时知识同步。

## 验证范围

回归覆盖正常报告零改写、报告恢复与投递独立、重启/退避/次数/期限、原子租约与过期作者、来源变化、跨租户/Agent、权限撤销、历史禁入及终态原文恢复。真实 production registry + AgentKernel + 流式模型适配器的隔离测试同时验证正常补写和恶意工具请求；业务 dispatcher 必须为零次。微信 sender 在测试中禁止外发，队列证明不能冒充真实 delivered。

生产发布与当前激活/知识修订证据记录在同次发布回执及系统设计基线修订中。日常维护可通过原 scheduled run / runtime run 查询报告逻辑记录和 trace，再关联 `gateway_message_deliveries`；当前不新增公开报告管理 API 或页面。
