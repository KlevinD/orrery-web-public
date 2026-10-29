# Stage 0 Review：基线净化、完成定义与不变量

日期：2026-07-24

适用方案：

- `docs/superpowers/specs/2026-07-24-agent-responsibility-system-options.md`
- `docs/product-planning/development-project-management-implementation-plan.md`

## 阶段结论

Stage 0 的生产 mock/demo 接线已经移除，责任对象、双状态机、完成门、租户标识、审计事件名和指标名已形成可执行 Go 契约；前端基线和生产接线约束已恢复为全绿。当前没有未解决的 Stage 0 review finding。

本结论不代表开发项目、SCM 连接、OpenFGA 关系、Todo 自动化、唯一群聊、持久化 Activation、工作区、Submission 或 Merge 已经实现。这些能力仍必须按 Stage 1-5 的开发、review、整改和复验闸门推进，不能用本阶段的契约代码代替真实生产实现。

## Intended vs Implemented

| 方案意图 | 实际 enforcement | 影响与结论 |
|---|---|---|
| 生产页面不得使用 mock/fixture | Evaluation 只读取 workforce 和 evaluation 的真实查询投影；生产前端扫描测试阻止 mock/fixture/fake-success 导入 | 无评估数据时显示明确空态，缺失投入数据不伪装为 0 |
| Demo facade 不得进入生产 server | Demo API 文件改为 `_test.go`；生产 Go 文件扫描禁止 `demoAPI`、`newDemoHandler`、fake-success 和临时评估 mock | `go list ./cmd/server` 的生产文件不包含 Demo API；显式 demo 模式继续 fail closed |
| 测试 double 只能位于测试边界 | lifecycle helper 改为 `_test.go`，无行为的 QA testkit 删除 | 未发现生产入口对这些 helper 的引用 |
| Runtime completed 不等于责任完成 | Assignment 状态机禁止 `active -> fulfilled`，只允许 `active -> submitted -> fulfilled` | 防止一次 Runtime 正常退出直接关闭责任 |
| Blocked 必须可恢复 | 开放 blocker 强制包含类型、所需输入、等待对象和 `RecheckAt` | 后续 Coordinator 可据此复查和升级，不依赖聊天记忆 |
| 所有运行期责任对象必须受租户约束 | Assignment、Activation、Attempt、Blocker 和跨系统 Trace 均要求显式 `TenantID`；Activation dedupe key 由 tenant、责任、cause 和 assignment version 生成并校验 | Stage 3 的 repository 仍须使用 tenant-scoped key、唯一索引和服务端授权，不能只依赖字段存在 |
| 完成必须有交付物、证据和适用审批 | Completion gate 校验 summary、deliverable、required evidence、QA 和人工审批 | 聊天消息或 Runtime 状态不能充当完成捷径 |
| 讨论界面不得泄露内部 Agent ID | 缺失展示名的旧 Run 使用通用 `Agent` 标签 | 用户界面不再暴露稳定内部标识 |
| 大型测试套件应保持可 review | DiscussionPanel 原 32 个测试完整拆分为 5、13、14 个测试的三个 suite，并统一执行 800 行结构约束 | 未删除或弱化原行为覆盖 |
| 项目变更不能直接提交默认分支 | 根 `AGENTS.md` 固化专用分支 -> scoped commit -> push branch -> PR -> review/整改 -> 独立合并审批 | 本 Goal 后续阶段不得直接 commit/push/merge `main` |

## Review Findings 与整改

1. `P1` Evaluation 生产页面为首个 Agent 注入临时评估数据。已删除临时模块及测试，改为只消费真实投影。
2. `P1` Demo API 与 messenger 编译进生产 server，注释不能形成约束。已移动到测试编译边界，并增加生产文件扫描测试。
3. `P2` 缺失 investment 投影时，页面把未知值显示成数字 0，容易被理解为真实测量。已改为 `--`，只有真实数值才格式化展示。
4. `P2` 旧 Discussion Run 在缺少展示名时暴露内部 Agent ID。已改为不透明通用标签，并更新回归测试。
5. `P2` Responsibility 运行期对象没有显式 tenant 边界，且任意非空 dedupe key 无法证明绑定当前 assignment version，后续持久化容易出现跨租户误关联或旧 Activation 重放。已补齐必填 `TenantID`、assignment version 和 canonical dedupe key 校验。
6. `P2` DiscussionPanel 测试文件超过结构上限。已按行为边界拆分，保留全部 32 个测试，并对每个 suite 执行相同结构约束。

## 复验记录

- `go test ./...`：通过。
- `npm run check`：0 errors，0 warnings。
- `npm run test -- --run --reporter=dot`：163 个 test files、877 个 tests 全部通过。
- `git diff --check`：通过。
- `go list ./cmd/server` 生产文件审计：不包含 Demo API 文件。

在本 review 后新增的 Stage 0 整改必须重新运行聚焦测试、Go 全量测试、Svelte check 和 Vitest 全量测试，结果更新后才允许提交 Stage 0 PR。
