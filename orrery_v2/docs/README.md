# Orrery V2 文档

本目录是 v2 当前产品与架构文档的统一入口。

当前方向：**SaaS-first Agent Mission Control**。SaaS Web 与 Go control plane 位于 `orrery_v2/`；AionUI 衍生桌面客户端、Electron/React UI、本地 AionCore 与 Device Connector 已在 `orrery-desktop/` 实现。源码、客户端 CI、生产联通与签名发布分别验收；稀疏检出时先核对 Git tree，不能按本地目录缺失判断实现不存在。

当前设计、生产接线、用户已确定行为与未完成能力，先读 [Orrery 当前设计与行为基线](./design-baseline/README.md)（2026-10-17，生产源码 `a321b308`）。修复前按 [契约与回归方法](./design-baseline/11-change-contract.md) 核对相关 `ORR-*` 行为，避免把历史计划、当前缺陷和目标设计混为一谈。旧方案仍在其适用范围内提供细节，基线不替代原业务合同或最新用户指令。

## 文档语言约定

- 新增或更新文档默认使用中文。
- 官方术语、API 名称、代码标识、目录名、命令和配置项可以保留英文。
- 如需引用英文原文，应在中文说明中明确其含义和落地影响。

## 当前决策文档

- [手动验证与部署](./operations/manual-deployment.md)：2026-10-19 起停用 GitHub Actions CI/CD，使用手动发布并保留实际验证证据。
- [SaaS 产品与架构规划](./product-planning/orrery-saas-product-architecture-plan.md)
- [Orrery Desktop（AionUI 衍生）方案](../../orrery-desktop/docs/orrery-desktop-client.md)
- [Server Runtime Bridge 方案](./runtime/runtime-bridge.md)
- [开发项目管理与 Todo Agent 执行闭环方案](./product-planning/development-project-management-implementation-plan.md)
- [后续规划](./future-planning.md)
- [SaaS 客户端、服务端与协议边界规划](./agent-company-mission-control/saas-monorepo-boundaries.md)
- [项目结构设计与公共约束](./agent-company-mission-control/project-structure-guidelines.md)

## 当前架构文档

- [全景架构：用户、功能、应用与数据（2026-10-15）](./architecture/panorama-2026-10-15/README.md)：包含 Ontology、RSI、定时任务等四个视图，提供图片、PDF、交互 HTML 与可编辑源文件。
- [完整形态技术与功能架构草图：Platform / Ontology / IRIS / Discussion](./architecture-diagrams-draft.md)
- [Agent 公司与 Mission Control 协作模型](./agent-company-mission-control/agent-company-mission-control-model.md)
- [Go 技术架构重写方案](./agent-company-mission-control/technical-architecture-rewrite-plan.md)
- [认证与授权三阶段落地方案](./agent-company-mission-control/identity-access-three-phase-roadmap.md)
- [实现目标拆分](./agent-company-mission-control/implementation-goals/README.md)
- [后端建设顺序](./agent-company-mission-control/implementation-goals/backend-build-order.md)
- [Mission Control OpenAPI 契约](./api/mission-control.openapi.yaml)
- [运行时发现机制](./runtime-discovery.md)
- [People v2 本地接入](./people-v2-ingestion.md)

## 补充产品笔记

- [后续产品能力](./product-planning/idea-后续产品能力TODO.md)
- [讨论组电话会议与语音 Agent](./product-planning/idea-讨论组电话会议与语音Agent.md)
- [MCP 共享记忆与知识库产品](./product-planning/idea-MCP共享记忆与知识库产品.md)

## 实施记录

- [生产源码与 PR 补齐（2026-10-18）](./verification/production-pr-reconciliation-20261018.md)

- [Superpowers 规格](./superpowers/specs/)
- [Superpowers 计划](./superpowers/plans/)
- 客户端与 Runtime Bridge 的实施顺序分别合并在上述两份权威方案中。
- [代码评审记录](./superpowers/reviews/)

这些记录用于描述实现意图和评审检查点，可以作为过程证据；当前产品方向仍以上方的当前决策文档和当前架构文档为准。

## 归档地图

- [根目录文档归档](./archive/root-docs/)：保存从仓库根目录迁移来的历史文档。
- [参考资料归档](./archive/reference/)：保存历史参考笔记。
- [Agent Company 归档](./agent-company-mission-control/archive/)：保存 SaaS 之前的规划、生成的 PDF/HTML，以及已明确废弃的商业计划。

归档文档只作为参考材料。除非新的当前文档明确引用其历史上下文，否则不要把归档文档作为当前实现或产品决策依据。
