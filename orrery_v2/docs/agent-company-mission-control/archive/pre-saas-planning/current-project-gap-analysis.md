# 当前项目与 Agent 公司目标模型差异核实

本文核实现有 Orrery 项目与 [Agent 公司与 Mission Control 协作模型](./agent-company-mission-control-model.md) 的差异。结论基于当前仓库中的前端、后端、数据库 schema、Mission、Market、Squad、PM timeline、worker config、agent runtime wrapper、comment/mention/QA/evidence 相关实现。

## 总体判断

现有项目已经具备 Agent 公司模型的一部分底座：

- Mission-first 应用外壳和 Mission dashboard。
- PM conversation、PM timeline、Mission brief/run/task/evidence/review packet。
- Agent、runtime、skills、market catalog、worker config。
- 多 CLI coding agent backend，包括 Codex、Claude、Copilot、Cursor、Gemini 等。
- Squad、squad member、comment、reply、mention、inbox/notification。
- QA contract、QA plan、QA check、QA review。
- Mission staffing assignment、worker request、parallel wave。

但现有实现仍更接近：

```text
Mission + task queue + agent runtime + market template + QA/evidence
```

目标模型则是：

```text
Agent 公司 Mission Control
+ Agent 组织与人事体系
+ BaseAgent 统一责任协议
+ RealHumanAgent
+ Agent 包装 Tool/CLI/API/RPA
+ 强制 summary / deliverable / evidence
+ 轻上下文讨论组
+ 事件流投影前端
```

因此当前项目不能再按“到处补字段、补桥接、补兼容”的方式演进。更合理的判断是：现有 Mission、Agent、Market、QA、Timeline 能力可以作为认知资产和局部代码资产，但不应成为新架构的兼容约束。这个系统仍处于初创阶段，应接受围绕 Agent 公司运行时内核进行推翻重写。

## 已具备能力

### Mission 与运行上下文

现有后端已有 Mission 相关概念：

- `mission_brief`
- `mission_run`
- `mission_task`
- `mission_evidence`
- `mission_validation_result`
- `mission_review_packet`
- `mission_staffing_assignment`
- `mission_timeline_event`

前端已有 `missions` 主页面和 Mission dashboard 类型，能展示 brief、run、evidence、validation、timeline、review packet、staffing、usage 等。

这和目标模型中的 Mission Control 很接近，但当前 UI 更偏 dashboard / timeline，还不是“团队、任务、成果、证据、验收”的项目作战室视角。

### Agent 与 runtime

现有 `agent` 表已有：

- `name`
- `person_name`
- `role_title`
- `description`
- `instructions`
- `runtime_id`
- `runtime_mode`
- `worker_config`
- `custom_env`
- `custom_args`
- `mcp_config`
- `model`

后端 `pkg/agent` 已有统一 CLI backend 接口，支持 Codex、Claude、Copilot、Cursor、Gemini 等。

这对目标模型有价值：现有 runtime backend 可以成为 `Agent.runtimeAdapter` 的实现基础。但当前实现仍更像“执行 prompt 的 backend”，还不是“BaseAgent 责任主体协议”。

### 组织目录雏形

现有 `market_agent_catalog` 和 `/api/market/agents` 已经提供 market catalog、template 创建、instantiate agent 等能力。

当前 market catalog 字段包括：

- `kind`
- `slug`
- `name`
- `role`
- `source`
- `status`
- `description`
- `prompt`
- `tools`
- `model`
- `builtin`

前端 `MarketAgent` 也有 `agent_type`、`role_kind`、`runtime_adapter`、`tools`、`members`。

这已经是 Agent 组织目录的早期雏形，但当前 schema 仍允许 `kind IN ('template', 'tool')`，且有 `externalMarketTools` 中的 `tool:codex`。这与目标原则冲突：Tool/CLI/API/RPA 不能直接成为组织成员或任务团队成员，必须由 Agent Profile 包装。

目标组织目录不能是扁平列表，至少需要表达：

- Organization / Department / Position / Role。
- ReportingLine / manager_agent_id。
- AgentDirectoryEntry / OrgMembership。
- MissionAssignment，即某个 Mission 中的临时任务身份。

### Worker config 与 QA

`workerconfig.Config` 已经支持：

- `AgentType`
- `RoleKind`
- `RuntimeAdapter`
- `Kind`
- `Role`
- `Responsibilities`
- `InputFields`
- `Deliverables`
- `QAConfig`

QA 能力也比较完整：

- `qa_contract`
- `qa_plan`
- `qa_check`
- `qa_review`
- required evidence
- acceptance criteria
- pass threshold
- revision attempts

这和目标中的独立责任单元、QA Plan、QA Check 基本方向一致。

### 讨论、reply、mention

现有 comment 支持：

- `author_type`
- `author_id`
- `content`
- `type`
- `parent_id`
- thread resolve/unresolve

mention 工具支持 `member`、`agent`、`squad`、`issue`、`all`，并有 comment-trigger/inbox/notification 相关实现。

这可以承载 Agent-and-Human-in-the-loop 的基础，但当前 comment 仍是 legacy issue API 兼容形态，Mission-native discussion group 语义还不完整。

## 主要差异

### 1. 缺少 BaseAgent 统一责任协议

当前项目有 agent/runtime/task/comment/worker_config，但还没有明确的 `BaseAgent` 层。

目标模型要求所有团队成员都必须具备统一行为：

- readContext
- sendMessage
- mention
- reply
- callTool
- requestClarification
- proposeChange
- commitStructuredEvent
- reportProgress
- submitEvidence
- generateSummaryFromTrace
- submitSummary
- submitDeliverable

当前实现中，这些能力分散在 handler、service、runtime backend、comment、task queue、PM tools 中，没有统一的 Agent envelope。

影响：

- 不容易强制所有 Agent 提交 summary / deliverable。
- 不容易抹平真人和 AI Agent。
- 不容易限制工具直接入组。
- 不容易统一 context policy。

### 2. 组织目录仍混合 Agent 与 Tool，且缺少组织层级

当前 `market_agent_catalog.kind` 允许 `template` 和 `tool`。`externalMarketTools` 中存在 `tool:codex`，描述为第三方工具。

目标模型要求：

```text
进入团队的是 Agent Profile，不是 Tool。
Codex CLI 不是成员；CodeWorkerAgent 才是成员。
Codex CLI 只能作为 CodeWorkerAgent 的 runtime adapter 或 tool。
```

当前差异：

- market catalog 没有强制 Agent Profile 语义。
- `tools` 字段承载了工具和 metadata 的混合信息。
- 没有 `wrapped_tools`、`summary_contract`、`deliverable_contract`、`context_policy`。
- 没有 department、position、role、manager、reporting line 等组织身份字段。
- 没有区分稳定组织身份和 Mission 内的临时 MissionAssignment。
- 前端市场也会展示 runtime/tool 信息，但还没有明确“Agent profile 包装工具”的产品语义。

### 3. 缺少 RealHumanAgent 模型

当前系统已有 `member`、workspace user、comment author_type `member`，但这不是 `RealHumanAgent`。

目标模型要求真人也以 Agent profile 参与团队：

- 领取任务。
- 提交 summary。
- 提交 deliverable。
- 提交 evidence。
- 响应 mention。
- 接受 QA。

当前差异：

- 真人可以评论、审批、决策，但没有被建模为可招募的 `RealHumanAgent`。
- 没有人类成员的 task contract / deliverable contract / evidence contract。
- 前端无法把真人和 Agent 作为同一种 Mission member 展示。

### 4. summary / deliverable 不是 BaseAgent 强制出口

当前项目有多处 summary：

- PM conversation summary。
- mission timeline event summary。
- mission evidence summary。
- review packet summary。
- trigger_summary。
- worker_request summary。

但这些不是统一的 BaseAgent 强制提交结果。

目标模型要求：

```text
所有 Agent 完成任务或阶段时都必须提交 summary 和 deliverable。
summary 由 BaseAgent 根据 internalTrace 自动生成。
deliverable 由任务契约决定。
discussion group 默认只接收压缩摘要、成果、证据和阻塞。
```

当前差异：

- `agent_task_queue.result` 是最终结果，但没有拆成 summary / deliverable / evidence。
- `task_message` 记录工具和消息，但没有明确 internalTrace 到 summary 的自动生成流程。
- comment `content` 仍是主消息载体，没有独立 summary/content/evidence visibility。
- 没有 summary contract / deliverable contract。

### 5. 讨论组仍偏 legacy issue/comment 兼容

当前 comment 结构以 `issue_id` 为主，Mission 通过 compatibility payload 关联：

- `legacy_issue_id`
- `mission_brief_id`
- `mission_run_id`
- `mission_task_id`

目标模型中讨论组是 Mission 天然上下文，应该是项目作战室。

当前差异：

- `comment` 表仍必须有 `issue_id`。
- comment author_type 只有 `member` / `agent`，没有 Mission member / RealHumanAgent / role-level envelope。
- 消息没有 `summary`、`visibility`、`related_evidence_id` 等目标字段。
- 组内轻上下文策略还未实现。

### 6. 事件模型还不够统一

当前有：

- protocol events。
- PM timeline event。
- mission timeline event。
- task messages。
- comments。
- inbox notifications。
- QA records。

但目标模型需要统一事件流投影：

```text
Mission events / Discussion messages / Agent events
-> missionProgressProjection
-> MissionControlView
```

当前差异：

- 前端 timeline projection 主要是 PM timeline 分类。
- Mission 页面展示 dashboard/timeline/evidence，但还没有统一 Mission Control projection。
- `@mention`、reply、tool call、summary、deliverable、evidence、QA、staffing 还没有形成一个统一的事件投影模型。

### 7. 前端还不是 Mission Control 产品形态

当前前端已有：

- `missions`
- `chat`
- `skills`
- `squads`
- `autopilot`
- `market`
- `legacy-history`

但目标前端需要：

```text
Mission Overview | Team | Tasks | Evidence | Timeline
```

当前差异：

- Market 是独立页面，不是 Mission 组队视角的一部分。
- Squad 是独立模块，和 Mission 项目组语义没有完全统一。
- MissionsPage 更像 dashboard，不是“Agent 公司项目作战室”。
- 缺少 team/member profile、context policy、summary-only view、deliverable/evidence grouped by task。

### 8. 软件开发倾向仍较强

当前 runtime backend 明确偏 coding agents，agent templates 也大量偏软件开发，例如 frontend builder、bug fixer、code reviewer、qa-plan/check 等。

这不是坏事，但目标模型不应局限软件开发。

当前差异：

- Market 和 runtime adapter 需要支持更通用的 domain agent。
- Mission type 虽已有 research/writing/travel/procurement/ops/analysis/mixed，但组织目录、模板、前端表达仍偏开发执行。
- Tool wrapper / API-backed / RPA-backed / RealHumanAgent 的产品语义还不完整。

## 建议重写路径

上一版建议过渐进迁移，但在“不考虑兼容性、追求合理解耦、纯粹、优雅、稳定”的前提下，迁移路线应调整为重写路线。

核心原则：

```text
不在旧 schema 上补字段。
不在旧 comment 上补 Mission 讨论组。
不在旧 task queue 上补 Agent 生命周期。
不在旧 market tool 语义上补 Agent Profile。
```

应该先建立新的领域内核，再决定哪些旧代码值得作为 adapter 复用。

### Phase 1：文档与命名收敛

目标：不改大架构，先统一语言。

1. 将 `market_agent_catalog.kind = tool` 语义废弃或标记为 legacy，并建立新的组织目录模型。
2. 在 UI 和 API 文案中把 market 改为组织目录 / Agent Directory。
3. 明确 `runtime_adapter` 是 Agent 内部字段，不是团队成员来源。
4. 将 `externalMarketTools` 改为 `CodeWorkerAgent` 这类 Agent Profile。
5. 增加设计文档中的术语索引：Agent、Tool、RuntimeAdapter、RealHumanAgent、Organization、Department、Position、Role、ReportingLine、MissionMember、Evidence。

### Phase 2：BaseAgent runtime kernel

目标：建立新的 Agent 运行时内核，而不是在现有 `agent_task_queue` 外层继续包兼容壳。

建议新增或演进字段：

```text
agent_profile_id
summary_contract
deliverable_contract
context_policy
wrapped_tools
runtime_adapter
```

并在任务完成时强制生成：

```text
summary
deliverable_refs
evidence_refs
internal_trace_ref
```

旧 `agent_task_queue.result` 不应作为新内核的设计约束。必要时只作为历史实现参考。

### Phase 3：Mission-native discussion group

目标：建立 Mission 原生讨论组，而不是从 legacy issue comment 兼容过渡。

需要支持：

- mission_id / discussion_group_id。
- parent_id reply。
- content。
- summary。
- visibility。
- related_task_id。
- related_evidence_id。
- author_member_id / author_agent_id / author_role。

如果仍需要读取历史数据，可以做一次性导入或只读归档，但不要让 legacy comment 语义进入新内核。

### Phase 4：RealHumanAgent

目标：真人和 AI Agent 在任务系统中表现一致。

需要：

- Human profile。
- Human task assignment。
- Human submit summary/deliverable/evidence UI。
- Human mention notification。
- Human QA acceptance。

注意：真人入口要极简，不要要求真人填写复杂 Agent 表单。

### Phase 5：Mission Control projection

目标：前端不再直接拼 timeline，而是从统一事件流投影。

建议输出：

```text
MissionControlProgress {
  missionId
  businessDirection
  members[]
  tasks[]
  insertions[]
  summary
}
```

前端展示：

- Mission Overview。
- Team。
- Tasks。
- Evidence。
- Timeline。

## 风险与注意事项

1. **不要补丁式演进**  
   当前阶段应优先建立干净领域模型。现有多 CLI backend 是资产，但只能作为 `RuntimeAdapter` 复用，不能让旧任务队列和旧结果结构决定新系统骨架。

2. **不要让 market 继续暴露 tool 成员语义**  
   这是最容易把产品带偏的地方。工具可以被展示，但必须作为 Agent Profile 的内部能力展示。

3. **不要把 summary 当作真相**  
   summary 必须可追溯 evidence / artifact / internal trace。

4. **不要把讨论组做成日志黑洞**  
   组内默认只显示摘要、成果、证据和阻塞；内部 trace 应可展开但不默认进入主上下文。

5. **不要过早做通用行业模板**  
   先选择 1-2 个有交付价值的场景验证，如市场调研交付、销售线索研究、合同初审、AI agency 交付管理。

6. **不要忽略权限凭证**  
   Agent 包装工具后需要 scoped credentials、RBAC、审计、撤销和 kill switch。

7. **不要把 RealHumanAgent 做重**  
   真人只需要提交 summary、deliverable、evidence、blocker，越像简单任务回执越好。

## 生产级闭环差异目标

第一条闭环也必须按生产级系统切片实现，而不是 MVP 或演示版。建议目标是：

```text
Mission 创建
-> HR 从 Agent Profile 市场选择 CodeWorkerAgent / ResearchAgent / RealHumanAgent
-> Planner 创建 Worker Task Contract
-> Worker 完成任务后 BaseAgent 强制生成 summary 和 deliverable
-> Worker 提交 evidence
-> QA Check 按 evidence 验收
-> Mission Control UI 展示 Team / Tasks / Evidence / Progress / QA / Discussion / Inbox
```

这个闭环应同时验证目标模型的核心价值和生产级工程边界：不是更强的单点 Agent，而是可控、可追踪、可观测、可恢复、可验收的 Agent 公司协作。
