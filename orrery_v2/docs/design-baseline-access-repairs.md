# 设计基线的权限接缝修复

依据：`ORRERY-DESIGN-BASELINE-20261017` 的 F03、F13，以及 `ORR-AUTH-01`、`ORR-TOOLS-01`、`ORR-SCHEDULE-01/02`、`ORR-KNOWLEDGE-03`、`ORR-DEPLOY-01`。完整设计知识位于组织内部 PageIndex 库“Orrery 系统设计与行为基线”（`kb-orrery-ffe9caec81f8338ee2ac81ae506b07bf`）；编辑源见文档 PR #281。

## 修复的行为

个人助理在主人已有授权范围内可以调用定时任务的创建、更新、取消和立即执行工具。AgentCore 仍要求本次运行的工具绑定、有效执行信封、正确的用户/Agent/租户/运行身份，以及 `scheduler.write` 或 `scheduler.run`。缺少授权、过期或被改动的信封和公共 Website 入口仍被拒绝，Gateway 与调度领域服务继续执行原有检查。

可信用户委托创建的任务保留主人作为 `CreatedBy`、用户类型和 `CreatorProfileID`，执行 Agent 不变。这使后续 occurrence 按主人的档案读取创建者权限，而非把助理档案当成人员档案。既有 Agent/责任执行路径保持自己的身份。修复不重写历史任务或旧运行。

知识文档的编辑和发布现在同时校验全局知识写权限与目标库的 `AccessPolicy.CanWrite`。owner 或明确的 write/manage 绑定可以操作；只有 read 绑定或一般 public 可读性不能获得写权限。既有策略对 global owner + public 库明确允许已认证身份写入的特殊规则保持，不能把这次修复描述成改变了该规则。撤销绑定后下一次操作立即重新校验。文档仍必须属于请求中的知识库，PageIndex 的原 `expected_revision`、作者和发布者参数保持传递，版本冲突继续返回冲突。

## 版本依据

2026-10-17 的当前生产发布名为 `ontology-entity-identity-20261017-c99cc956`，其 `COMMIT` 是 Ontology 补丁提交，不能单独作为完整生产源码。发布的基础提交是 `30efc7e9`，源码快照 SHA-256 是 `2ecf838e3fb123e20bad4de2f8580ed8ac6d673d6c4bbec9c085a3b4ac7eee3f`。本次先把该快照中的实际增量恢复成独立 stage 基点，再提交以上修复；没有用 Ontology 分支覆盖 AgentCore 基础实现。

18:25 再读生产时，它已更新为 `personal-assistant-tool-linkage-20261017-c40feae5`，源码快照 SHA-256 为 `978ba3e01c09f2c91e9aeb532c23230e85104a4fff40c1ad8e9ec0d8bcb9cc92`。与初始快照比较，实际有 14 个文件增量，无删除：个人助理资源绑定权限、执行信封、工具参数校验及其回归。本次完整保留这些增量，以 stage 提交 `24b90b3abe` 为最终修复基点，并合并两组 Gateway 回归，不能拿较早的二进制覆盖它们。

18:53 等待共享发布锁期间又发布了 `rsi-task-outcome-20261017-39cd3b19`，归档 SHA-256 为 `f2d9a986e72ed3eea92c96a0e11bcfcb4967b798d2bd1e0e9407d05e962dd501`。首次候选没有激活，随后保留该版本的 24 个语义文件增量，最终 stage 基点为 `dff315c40b`。该次归档还包含 CRLF 差异；Git 审阅树规范化为 LF，发布组合仍逐文件保留所有未被本修复修改的原归档字节。

随后生产发布到 `scheduled-agent-recovery-20261017-a41c41e5`，归档 SHA-256 为 `3d12f573f70a249979301cc510078fcf849c4d054e4fbfaff297ce8d7438c073`。本次继续保留其 4 文件参数纠正/停止复核增量，最终审阅基点为 `bb0925f96a`；此前两个候选均未激活。共享发布锁持有期间固定最终生产基点，再构建、验证并激活本次修复。

20:43 的继续上线核对取得 `personal-assistant-latency-20261017-1711010a`，源码归档 SHA-256 为 `9591b3538991299184e285f68575345cc2745cfd8a6f73921bf3e30a001cd8d6`。相对 `bb0925f96a` 恢复并保留 74 个语义文件增量，包含上下文预算、Ontology 断言时间与有效性、失败反馈和对话读取；独立 stage 基点为 `4bf05b1833`。这一步不重写先前候选的回归或未激活记录，也不代表对这些独立功能作完整设计验收。新的构建、原生回归与部署回执放在 `D:/Orrery/artifacts/design-contract-access-activation-20261017`，必须核对取得共享锁后的实际版本才可切换。

为完成与前序发布的兼容性核对，另预先保留 `llm-cache-goals-20261017-9a747bde` 候选的 28 文件缓存/usage 增量，归档 SHA-256 `217cc36f60edf49ec0c2bb5e30062bb2934a4798c11eb0b512d1182c6dd61dcb`，stage 基点 `0b5478bbb2`。捕获时该候选尚未激活，因此不能据此声称它已在生产运行。本组合只在取得锁后的实际 release、commit、源码与二进制 hash 全部匹配时才允许部署；否则继续保留旧生产。此轮回执另存 `D:/Orrery/artifacts/design-contract-access-final-20261017`。

部署必须再次核对当前 release、源码与运行二进制 hash，保留当前 Web 和维修 worker。发布与验证回执保存在 `D:/Orrery/artifacts/design-contract-access-20261017`；代码提交、运行验证和实际激活是不同阶段，不能用本文替代激活证据。

## 回归与边界

- `production_agent_kernel_scheduler_test.go`：四个工具的合法授权、别名、缺失绑定/权限/信封、错误身份/租户/运行、过期、禁止动作、篡改和公共 Website 拒绝。
- `production_agent_kernel_scheduler_roundtrip_test.go`：生产 AgentCore → dispatcher → Gateway → scheduler store，实际创建、更新、立即执行和归档隔离任务，检查主人身份和四次已完成工具日志。模型和运行分派使用测试夹具，不触发真实业务或渠道投递。
- `gateway_personal_envelope_test.go`：主人成功，其他用户/租户、缺少权限/绑定在 scheduler 执行前拒绝，创建者保持主人类型和人员档案。
- `production_knowledge_workspace_test.go`：真实 HTTP router 接生产 workspace 和 AccessPolicy；owner/write/manage 成功，read-only/public/其他组织拒绝且不调用 provider 写方法，跨库文档不写入，版本冲突和绑定撤销生效。

业务任务启停、时区、目标、QA、审批、Agent 决定公开正文及微信投递规则不在本次变更范围。完整审计中的其他差距仍需分别验证；这些回归不证明所有公众号/股票历史业务已完成。
