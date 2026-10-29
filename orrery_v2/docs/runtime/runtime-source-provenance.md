# Session Runtime source provenance

审阅日期：2026-10-15。范围为 Web / Go 会话层，客户端不属于本次发布范围。

2026-10-19 整合补充：上述日期对应历史来源审阅。本轮保留 16 处来源锚点、许可证和署名，同时纳入主线与生产的会话能力更新，重新计算所变更源码的摘要。具体输入及验证见根目录 `docs/opensource-pr269-current-integration-20261019.md`；不能把历史阶段“仅增加出处注释”的说明当成本轮整合没有修改实现。

## 依赖结论

Web / Go 构建、启动和会话执行不需要克隆、编译、下载或运行 AionCore。
Go module 依赖中没有 `aionui-session`；会话由仓库中的 Go AgentCore 和原生
provider 执行。自研 agent 循环并不能单独证明历史会话层从未改编第三方源码。

历史方案曾明确要求对齐 AionCore 会话状态、命令、事件和 provider 行为，并注明
测试移植来源。当前生产源码保留 16 处既有来源锚点；本次仅增加出处及改动声明，
没有重写这些生产文件的逻辑。共享 conformance suite 同样补充了来源声明。
因此，源码分发继续按包含历史改编部分处理，保留 Apache-2.0 许可证和署名。
该声明记录来源，不要求用户额外安装供应商 runtime。

## 固定来源和许可证据

- 仓库：[iOfficeAI/AionCore](https://github.com/iOfficeAI/AionCore)。
- 固定提交：`76f5554286ba0b6d33fb74d5c2bb2b3b0b83100d`，历史标签 `v0.1.52`。
- 参考范围：`crates/aionui-session`，固定树包含 32 个文件，其中 11 个 fixture。
- 根目录 [LICENSE](https://github.com/iOfficeAI/AionCore/blob/76f5554286ba0b6d33fb74d5c2bb2b3b0b83100d/LICENSE)
  为 Apache-2.0，载有 `Copyright 2025 AionUi (aionui.com)`。
  完整原文保存在 [许可证副本](../../../compliance/licenses/aionui-session-76f5554-Apache-2.0.txt)。
- 该提交的根 `Cargo.toml` 写有 workspace package license `MIT`，但
  `aionui-session/Cargo.toml` 没有声明继承该字段。这里依据实际根 LICENSE
  保留 Apache-2.0 原文，并记录元数据不一致，未将 crate 推定为 MIT。
- 固定树中没有根 `NOTICE`，不虚构上游 NOTICE；Orrery 在自身 NOTICE 中说明改编。

[来源清单](../../../compliance/source-adaptations.json)记录固定来源文件的 Git blob
与 SHA-256、保留文件的路径和摘要、许可证摘要及改动边界。它是源码来源记录，
不是一份 AionCore 二进制依赖清单，也不是对整个仓库的发布许可批准。

## 本次处理范围

| 范围 | 处理 |
| --- | --- |
| 命令测试 | 替换原 `command_names_are_stable` 移植用例，改为 Orrery JSON 协议往返、12 种命令的缺失/混合/错配 payload 校验和认证凭据隔离。保留其余现有回归测试。 |
| 状态测试 | 替换原标注移植的 reducer 用例，按 Orrery 契约覆盖 64 种输出/拒绝/错误组合、取消与下一代隔离、双权限计数边界、子任务终态、非法输入和输入不可变性。 |
| 参考 trace | 移除固定上游提交的 differential fixture，使用 `orrery-session-v1` 自有混合审批/认证、工具结果、下一代、取消与迟到事件验收 trace。 |
| 开发规范 | 当前 [运行时契约](runtime-bridge.md)替代上游优先的行为规范；删除只验证外部 checkout 的旧脚本。普通开发和测试不需要上游 checkout。 |
| 生产会话层 | 保留现有来源注释和实现，按历史改编部分保留许可证与署名；没有以改名或删除注释证明独立原创。 |
| Claude 测试样例 | `internal/agent/runtime/providers/claude/testdata/claude_write_tool_turn.ndjson` 的五条记录与固定上游样例在换行和破折号归一化后相同。保留，并附 `.license` 文件标明出处、版权和改动。 |
| 协议兼容 | Device Protocol v1 的 `aioncore_version` 是既有 wire 字段，值为本地 Core 版本；本次不破坏客户端协议。 |

旧方案可从提交 `307b03aee56898d073ae1ab542040b1b34808d60` 下的
`orrery_v2/docs/runtime/runtime-bridge.md` 追溯。旧源码、移植测试及审阅证据也保留
在私有审阅材料中。本次替换测试不追溯改变既有实现的来源。

## 比对结果与边界

审阅取得固定树下全部 32 个文件以及根 LICENSE、Cargo.toml，并按 Git blob SHA
核对下载内容。对 11 份上游样例逐份比对，确认上述 Claude fixture 的复用；
整文件字节或 JSON 相等检查会漏掉这处仅替换破折号的改动。

13 个上游 Rust 文件与 17 个本地映射文件的连续文本比对没有出现三行及以上的
有效文本相同块。Rust 和 Go 的语法差异使该结果无法排除翻译或改编，也不能
作为独立原创证明；来源注释、历史设计及 fixture 证据仍有效。

保留 Apache-2.0 来源声明允许在遵守原许可条件的前提下继续按 Apache-2.0 分发
改编部分；这与 Multica 曾发现的用途限制风险不同。若后续目标是连历史源码改编
也全部排除，仍需逐项替换并重新审阅生产会话层和保留样例。本次不宣称该目标完成。
