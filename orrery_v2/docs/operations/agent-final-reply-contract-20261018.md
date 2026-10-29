# AgentCore 最终回复与定时报告契约

关联设计发现 F04，契约 `ORR-RUNTIME-01`、`ORR-REPLY-01`、`ORR-DELIVERY-01`、`ORR-DELIVERY-02`。核对日期：2026-10-18。部署及知识发布另留回执，不用代码提交代替线上送达证据。

## 当前故障与证据

最近五次真实定时运行的 trace 均有完整 `orrery-public-summary` 与正式结果围栏，但所有文本消息的 final 状态均缺失。正式提交的 `UserReport` 因而为空。样本包括公众号、论文核验、知识统一管理、工作日记及记忆健康任务；它们的业务验收结果不同。

AgentKernel 流式生成 `message_delta`，最终由 runner 返回 `TurnResult.ResultText`。managed 适配器原先只发送终态，没有发送 `message_finalized`。响应投影发现已经有文本时不再补终稿；报告提取严格要求 final，因此跳过实际存在的 Agent 公开报告。

飞书直接投递的 `delivery_status` 与个人助理微信结果队列是不同链路。标记飞书已投递，不证明微信已收到，也不证明原业务完成。

## 适配层负责终稿边界

managed 适配器在 runner 返回后，先验证结果、调用错误、取消、超时、权限请求和未完成工具；仅对非空且验证通过的完成结果发送一次 `message_finalized`，随后发送终态。

终稿内容直接取自该次 runner 的 `ResultText`，保留原文。它不从中间进度拼接报告、不用故障字符串替代 Agent 正文，也不另调用模型重写。

存在终稿流式 item 时，终稿使用同一 item ID，让响应投影合并相应碎片。工具调用前的文本属于进度；如果之后只有终态正文，就分配当前轮的独立终稿 ID。空拒答不制造正文，失败或取消不把部分输出升级为终稿。事件继续受原有大小、顺序、背压及轮次限制。

定时报告提取仍要求 final，并继续检查公开内容边界。机器结果、产物、证据、QA、审批、正式任务状态和渠道投递各自验收。运行内核完成而正式业务 blocked 时，只要已有合法公开报告，仍保留 Agent 对阻塞的说明。

## 回归与排查

- 适配器覆盖流式/仅终态正文、工具前进度、多轮顺序，以及失败、取消和未完成工具。
- 真实 HTTP 模型适配器与 AgentKernel 组合检查 final 状态和唯一终稿。
- server 组合覆盖流式模型 → 生产 registry → 正式结果 → JSON 持久化恢复 → 定时 AgentReport → 私助微信待发队列；重启扫描两次只有一条原文，模型只调用一次，禁止调用外部 sender。
- 原有无报告不造模板、正文保持、租户/权限和投递幂等回归继续适用。

线上核查按同一个 runtime run 检查最终事件、trace 消息状态、正式提交 `UserReport`、scheduler `AgentReport`、私助队列及真实 delivery receipt。隔离队列验证不等于真实微信 delivered。历史运行不因此重放或被改写。

本次修复集中于 `internal/agent/runtime/providers/kernelcompat/session.go`。部署前应保留全部当前生产增量，并在共享发布锁内核对当前 release、源码及二进制 hash；健康、任务配置、Web 与维护 worker 分别核验。知识基线使用稳定 document ID 修订，记录代码、测试和激活状态。
