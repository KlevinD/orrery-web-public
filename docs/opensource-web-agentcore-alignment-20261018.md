范围更正（2026-10-18）：用户确认 People 为独立系统，原清单中其他业务接入事项属于外部 MCP，均不属于此次 Orrery Web/API 开源要求。此前把 25 项过期豁免列为开源阻塞的结论已撤回。默认覆盖门禁现按 Web/API 范围通过，外部完整集成另行验收。最新范围和验证见[首发范围说明](opensource-web-mcp-scope-20261018.md)；下文为此前阶段快照。

# WEB/API AgentCore 生产基线适配补充（2026-10-18）

本阶段比较依据为设计基线修订 5 记录的激活提交 `4ec54e6d1aee359ac5b084b16b448fcb7f110e4a`，候选提交为 `86389cdf5b27bff0b423a94c9154fcf15a120052`。本阶段未重新查询线上，未合并、部署、公开发布或运行 CI。后续修订与回归另行记录。

## 已处理的行为

- 平台默认模型通过共享 selector 读取当前 settings，一次选择仅使用一个快照。显式缺失或禁用的 profile 继续失败，保留 Orrery 自有模型接口和 HTTP 适配器。
- 生产构造采用模型/工具调用上限 128/256，单次模型/工具超时 2/20 分钟，模型 HTTP 客户端不再提前在 60 秒结束。请求 MaxTurns 继续收紧预算；无法在剩余预算内完成的工具批次不执行。
- WorkspaceBroker 绑定的宿主 deadline 优先于 timeout_seconds。过期 deadline 在模型调用前拒绝；标记 long 且存在 TaskID 的任务默认 15 分钟。适配器按宿主预算选择轮次超时，限制在 90 分钟以内；普通无预算请求仍默认 2 分钟。更短的上下文期限继续生效。
- 原生工具策略入口验证已有执行信封的摘要、有效期、tenant/run/Agent、tool/scope，以及已声明的 principal ID/kind。合法用户委托保留主人身份与执行 Agent，dispatcher/Gateway/ACL 继续检查。旧的无信封只读入口保留既有授权链。

## 实际验证

新增回归执行 18 次模型、17 次工具调用，跨越原库默认 8/16 限制；分别检查 128 次模型上限、257 个工具批次在执行前拒绝、MaxTurns 收紧、实际 turn 超时取消和过期请求零模型调用。模型选择测试使用本地 HTTP 服务，验证平台默认切换、显式选择、缺失/禁用失败以及单次 settings 读取；没有调用外部商业模型。

Windows 定向检查 39 通过、1 跳过。Linux 完整 server 与 runtime 检查共 2527 通过、17 跳过、1 失败，退出码 1；22 个 runtime 包均通过，server 为 1803 通过、9 跳过、1 失败。Go 1.25.0、CGO_ENABLED=0；Linux 容器关闭网络。输入、缓存和结果位于 D 盘，源码和测试临时运行区使用容器内存。未运行 race 或全量 ./...。

## 尚未解决的删除回归

`TestProductionDiscussionSendUsesConfiguredCodexRuntimeProcess` 在删除讨论后仍能查询到该讨论创建的 Mission。相同候选定向重复 10 次为 9 通过、1 失败；旧候选 eb5fad20 对照重复 10 次均通过。本阶段尚未确认根因，不能把它归为历史缺陷，完整检查仍为失败。

已读取的调用链包括 discussion service 从 run 工具创建回执计算删除计划、productionAPI 删除任务与 Mission projection，以及后台执行状态回写。这些仅是调查范围。没有修改删除语义、延迟断言、关闭后台工作器或忽略失败。

## 仍需整合的生产差异

本阶段开始时，对生产提交与候选的 production_agent_kernel.go 及 internal/agent/runtime 目录比较得到 122 个差异文件；本阶段只改 4 个实现文件及 3 个回归文件，未声称完整对齐。后续仍需处理：

1. 持久 checkpoint、任务恢复、已消耗调用计数与原始期限的保存/恢复。
2. 上下文压缩、prompt cache、真实流式模型与 Responses 协议能力。
3. 审批会话协议、显式责任 scope fence、有界只读并行与原子工具批次 journal。
4. F03 定时任务四项写操作与 F13 知识文档修订写权限的完整业务接线。
5. 其他生产运行证据、任务工作器和应用层差异。

恢复与审批未接齐前，不宣称已支持这些能力。持久工具 journal 的重放拒绝、模型检查、权限、QA 和发布门禁继续保留。

## 其他发布事项

40 项资源中仍有 16 项完整来源/许可对应、24 项分发依据待核验。历史 Git 对应不等于授权。Ontology 的两项门禁失败和 25 条过期豁免沿用上一阶段结果；本阶段未修改门禁或重跑全库。镜像和原生库分发审查仍未完成，PageIndex 保持独立草稿候选。

参见[上一轮测试修正](opensource-web-runtime-verification-20261018.md)、[资源核验](opensource-asset-rights.md)及[发布准备历史记录](opensource-web-release-readiness-20261017.md)。
