# Idea: MCP 共享记忆与知识库产品

标记日期：2026-06-07

## 核心判断

知识库、记忆和经验沉淀不应该只硬集成在 Orrery 平台内部。它可以单独抽象成一个产品：通过 MCP server 形式，为任意 Agent、任意 IDE/CLI、任意自动化平台提供共享记忆、团队经验、知识检索和可审计上下文注入。

Orrery 可以成为这个 Memory MCP 的首个使用者，但不应该成为唯一宿主。

一句话定位：

```text
给所有 Agent 使用的共享记忆层：把项目经验、团队知识、个人偏好、踩坑记录和可复用决策沉淀为可权限控制、可检索、可追溯的 MCP 工具。
```

## 为什么值得单独做

现在 Agent 生态的问题不是每个 Agent 都不会干活，而是它们之间的经验不能稳定继承：

- Codex、Claude Code、Cursor、Devin、ChatGPT、浏览器 Agent、企业内部 Agent 各有上下文孤岛。
- 一个 Agent 踩过的坑，下一个 Agent 往往还会再踩一遍。
- 项目约定、用户偏好、架构决策、常见错误、验收标准散落在聊天记录、README、PR、issue、个人脑子里。
- 企业很难回答：这个建议来自哪里？谁确认过？适用于哪个项目？过期了吗？哪些 Agent 可以看？

如果记忆只在 Orrery 内部实现，它只能增强本平台。如果抽象为 MCP 产品，它能变成任何 Agent 的基础设施。

## 产品形态

### Memory MCP Server

对外暴露一组标准 MCP tools / resources：

```text
memory.search
memory.remember
memory.propose
memory.review
memory.bind_context
memory.get_digest
memory.invalidate
memory.explain_source
knowledge.ingest
knowledge.search
knowledge.link_artifact
experience.record_lesson
experience.find_similar_cases
```

Agent 不直接读原始数据库，只通过 MCP 获取经过权限过滤、压缩、带来源的 MemoryDigest / KnowledgeDigest。

### Memory Studio / Console

一个轻量管理界面：

- 查看个人记忆、团队记忆、项目记忆、组织知识库。
- 审核 Agent 提交的记忆候选。
- 合并、降权、废弃冲突或过期记忆。
- 管理 scope、权限、来源、可信度、保留期限。
- 查看哪些 Agent 在什么时候读取或写入了哪些记忆。

### SDK / Adapter

提供不同 Agent runtime 的接入方式：

- Codex / Claude Code / Cursor 通过 MCP 配置接入。
- ChatGPT Apps 或自定义 Agent 通过 MCP client 接入。
- 企业内部 Agent 通过 HTTP/gRPC adapter 或 MCP Gateway 接入。
- Orrery 内部 AgentAssembler 只保存 `memory_mcp_ref`，不内嵌具体实现。

## 记忆类型

### Personal Memory

归属于某个真人或 Agent：

- 工作偏好。
- 常用表达方式。
- 代码风格偏好。
- 已知能力边界。
- 某类任务的个人经验。

默认私有，只能被所属主体或明确授权的执行上下文使用。

### Team Memory

归属于一个团队、部门或项目组：

- 团队协作约定。
- 架构决策。
- Review 标准。
- 交付口径。
- 常见返工原因。

适合多个 Agent 共享。

### Project Knowledge

归属于仓库、项目、客户或业务域：

- README、设计文档、接口文档。
- 数据库 schema。
- 业务规则。
- 历史 PR / issue / incident。
- 决策记录和约束。

更像 RAG 知识库，但必须和记忆层打通：知识告诉 Agent “有什么”，记忆告诉 Agent “我们学到了什么、应该怎么用”。

### Experience / Lesson

跨项目可复用经验：

- 某种 bug 的排查路径。
- 某类需求的常见拆解方式。
- 某个供应商 API 的坑。
- 某类用户偏好的长期模式。

需要 confidence、适用范围、反例和过期策略。

## 数据原则

- 不把原始聊天、全量日志、完整私有记忆直接注入 Agent。
- 每条记忆必须有来源、scope、visibility、confidence、owner、created_at、expires_at 或 review_after。
- Agent 写入默认先成为 `MemoryCandidate`，经过规则或人工审核后才成为 `MemoryRecord`。
- 检索结果返回 digest，不返回裸记录，除非调用方有诊断权限。
- 用户明确指令、任务契约、验收标准、当前代码事实优先于旧记忆。
- 记忆可以被降权、撤销、替换、过期，而不是永久真理。

## MCP 工具草案

### `memory.search`

输入：

```json
{
  "query": "string",
  "scope": ["personal", "team", "project", "organization"],
  "agent_ref": "string",
  "project_ref": "string",
  "max_results": 8
}
```

输出：

```json
{
  "digest": "string",
  "items": [
    {
      "memory_id": "mem_...",
      "summary": "string",
      "confidence": 0.82,
      "source_refs": ["artifact_...", "pr_...", "discussion_..."],
      "visibility": "team",
      "review_after": "2026-09-01"
    }
  ]
}
```

### `memory.propose`

Agent 提交候选记忆：

```json
{
  "kind": "lesson|preference|decision|warning|fact",
  "summary": "string",
  "evidence_refs": ["string"],
  "proposed_scope": "personal|team|project|organization",
  "confidence": 0.7
}
```

### `memory.bind_context`

给某次 Agent 运行生成上下文包：

```json
{
  "agent_ref": "agent/code-worker-a",
  "task": "fix websocket reconnect bug",
  "project_ref": "repo/orrery_v2",
  "allowed_scopes": ["personal", "project", "team"],
  "token_budget": 1200
}
```

输出：

```json
{
  "memory_digest": "string",
  "knowledge_digest": "string",
  "warnings": ["string"],
  "source_refs": ["string"]
}
```

## 和 Orrery 的关系

Orrery 内部仍需要 MemoryBinder、KnowledgeRetriever、ContextPack，但实现上应该逐步改为可插拔：

```text
Orrery AgentAssembler
-> MemoryProvider interface
-> local memory implementation or external Memory MCP
-> MemoryDigest / KnowledgeDigest
-> ContextPack
```

短期可以保留本地实现；长期把 Memory/Knowledge 抽象为外部 MCP provider。Orrery 自己只负责：

- 在讨论组、任务、证据、QA 中产生 MemoryCandidate。
- 把 MemoryDigest 注入 Agent ContextPack。
- 在权限和审计界面展示记忆使用痕迹。
- 不负责成为所有外部 Agent 的唯一宿主。

## 商业化方向

### 开发者版

面向个人开发者和小团队：

- 本地优先存储。
- 接 Codex、Claude Code、Cursor。
- 项目记忆、代码库经验、个人偏好。
- 按 seat 或本地 license 收费。

### Team 版

面向 AI agency、研发团队、运营团队：

- 团队共享记忆。
- Review workflow。
- GitHub/Notion/Slack/Linear/Jira ingest。
- MCP audit log。
- 按成员数、项目数、存储量、MCP 调用量收费。

### Enterprise 版

面向企业 AI 平台：

- 私有部署。
- SSO/RBAC/ABAC。
- 数据驻留和加密。
- 审批、保留、删除、合规导出。
- 与企业 MCP Gateway / DLP / SIEM 集成。

## MVP 路线

### Phase 1: 本地 Memory MCP

- [ ] 实现一个可独立运行的 Memory MCP server。
- [ ] 支持 `memory.search`、`memory.propose`、`memory.bind_context`。
- [ ] 本地 SQLite + vector index。
- [ ] 支持按 repo / project / agent scope 存取。
- [ ] 支持 Codex 或 Claude Code 通过 MCP 读取项目记忆。

### Phase 2: 审核与治理

- [ ] MemoryCandidate 审核 UI。
- [ ] 记忆来源和证据链接。
- [ ] confidence、过期、冲突检测。
- [ ] 个人记忆和团队记忆权限隔离。

### Phase 3: 知识库接入

- [ ] 文档 ingest：Markdown、PDF、网页、Git repo。
- [ ] 连接 GitHub issue/PR、Notion、Slack、Linear/Jira。
- [ ] KnowledgeDigest 与 MemoryDigest 分离但可互相引用。
- [ ] 支持按任务生成上下文包。

### Phase 4: 多 Agent 共享

- [ ] 给多个 Agent/runtime 分配 identity。
- [ ] 记录每次 Agent 读取和写入。
- [ ] 支持团队经验复用和跨项目 lesson 检索。
- [ ] 提供云同步或私有部署。

## 风险

- 如果只做普通 RAG，会变成同质化知识库。
- 如果没有审核和来源，记忆会污染 Agent。
- 如果权限模型太重，个人和小团队不会用。
- 如果只绑定 Orrery，会失去 MCP 产品的生态价值。
- 如果 Agent 可随意写团队记忆，会快速变成噪音库。

## 关键差异

普通知识库回答“文档里有什么”。

Memory MCP 要回答：

```text
这个团队已经学到了什么？
哪些经验被验证过？
这次任务应该继承哪些上下文？
哪些旧经验可能已经过期？
这个 Agent 为什么会得到这条建议？
```

这不是平台附属功能，而是 Agent 生态的共享经验层。
