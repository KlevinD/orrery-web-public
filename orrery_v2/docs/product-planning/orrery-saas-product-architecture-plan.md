# Orrery SaaS 商业价值与产品架构规划

本文是 Orrery v2 的 SaaS-first 决策文档。上一版偏架构整理，这一版按 PM 方法重写，重点回答四个问题：

1. Orrery 作为 SaaS 是否有商业价值。
2. 竞品已经占住了哪些位置。
3. Orrery 真正可能形成的核心竞争力是什么。
4. 为了形成核心竞争力，现在最薄弱的点在哪里。

使用的方法包括：Product Strategy、Value Proposition、ICP、Business Model、Competitor Analysis、Competitive Battlecard、SWOT、PRD、Outcome Roadmap 和 Pre-mortem。

## 0. 结论

Orrery 不应该把自己定位成“又一个 Agent 平台”。这个市场已经被大厂、自动化平台、开发者框架和 coding agent 挤满。

更现实的商业定位是：

```text
Orrery 是面向 AI agency / 咨询交付团队 / 自动化工作室的
AI-assisted delivery control layer。

它不卖“更多 Agent”，而卖“可交付、可验收、可审计的 AI 协作过程”。
```

一句话：

```text
We do not sell agents.
We sell accountable AI-assisted delivery.
```

### 商业判断

市场机会存在，但不是无脑乐观。

AI Agent 的采用在变快，但企业买单越来越看重清晰 ROI、风险控制和可落地工作流。通用 Agent、通用编排、通用聊天助手会快速商品化。Orrery 如果只是做“Agent 组队、聊天、执行任务”，商业价值不够硬，容易被 Microsoft、Salesforce、ServiceNow、UiPath、Workato、Zapier、Relevance AI、LangGraph、Codex、GitHub Copilot、Devin 等产品从不同方向挤压。

Orrery 的机会在一个更窄、更具体的付费场景：

```text
帮助交付团队把客户目标转成 Mission，
让 human + agents + tools 的执行过程留下责任、证据、QA 和验收包，
从而减少 PM 协调成本、返工成本、客户解释成本和交付风险。
```

这不是纯技术卖点，而是交付管理卖点。

### 必须接受的现实

- Orrery 现在没有大厂生态，没有 CRM/ITSM/M365/Slack/GitHub 原生分发优势。
- Orrery 现在没有自动化平台级别的连接器数量。
- Orrery 现在也没有 Codex / Devin / Factory 这类垂直 coding agent 的执行能力品牌。
- Orrery 当前更像“已有 mission/discussion/agent runtime 能力的产品胚胎”，还不是一个商业闭环 SaaS。
- 要形成核心竞争力，必须把产品重心从“运行 Agent”转到“交付可被客户验收”。

## 1. 市场边界

Orrery 不进入完整的“企业 Agent 平台”大市场正面战场。那个战场的购买者通常是 CIO、AI CoE、IT、合规、安全和业务系统 owner，采购周期长，要求 SSO、DLP、审计、数据治理、集成生态、私有化、供应商信誉和 SLA。

Orrery 首选市场应更窄：

```text
10-100 人的 AI agency、咨询交付团队、自动化工作室、外包型技术服务团队。
```

这些团队已经在使用 ChatGPT、Claude、Codex、Cursor、Zapier、n8n、Notion、Slack、GitHub、Google Drive 等工具。痛点不是“不会用 AI”，而是：

- 项目目标散在聊天和文档里。
- Agent 输出很难变成客户可验收的交付物。
- PM 需要手动追踪进度、解释过程、拼接证据。
- 客户问“为什么这样做、谁做的、依据是什么”时难以回答。
- 同一个任务经常在人工、Agent、代码仓库、自动化工具之间跳转，责任链断裂。
- 项目失败后难复盘是需求不清、执行错误、工具失败、权限问题还是 QA 标准不完整。

Orrery 要买单的对象不是“AI 爱好者”，而是对客户交付结果负责的人。

## 2. 竞品格局

### 2.1 企业套件型 Agent 平台

代表：

- Microsoft Copilot Studio / Agent 365
- Salesforce Agentforce
- ServiceNow AI Control Tower / AI Agent Fabric

它们的优势：

- 自带企业客户、采购通道和信任基础。
- 能连接企业核心数据和身份体系。
- 能把 Agent 嵌入 CRM、M365、ITSM、Slack 等现有工作流。
- 更容易卖治理、合规、安全、监控和企业级支持。

它们对 Orrery 的威胁：

- 如果 Orrery 做“企业通用 Agent 管理平台”，基本会被这些产品压住。
- 如果 Orrery 做“企业级 Agent 治理总控台”，ServiceNow 这类产品会更可信。
- 如果 Orrery 做“办公自动化 Agent”，Microsoft 和 Salesforce 的入口更强。

Orrery 应避开：

```text
不要先做企业全域 AI governance。
不要把第一卖点放在“帮大企业统一管理所有 Agent”。
```

Orrery 可切入：

```text
把治理落到一个可验收 Mission，而不是全公司 AI 资产治理。
```

### 2.2 自动化 / iPaaS / RPA 平台

代表：

- UiPath Maestro
- Workato Agent Studio / Enterprise MCP
- Zapier Agents

它们的优势：

- 海量连接器。
- 已有自动化客户基础。
- 强流程编排、触发器、集成和执行能力。
- 客户容易把 Agent 理解为自动化能力升级。

它们对 Orrery 的威胁：

- 如果 Orrery 做“workflow builder + tool call”，会陷入连接器数量劣势。
- 如果 Orrery 做“把 Agent 接到很多 SaaS”，Zapier / Workato 更容易赢。
- 如果 Orrery 做企业长流程编排，UiPath Maestro 更有 RPA 和流程客户基础。

Orrery 应避开：

```text
不要用连接器数量当第一竞争点。
不要做通用自动化平台。
```

Orrery 可切入：

```text
把自动化结果变成客户交付证据，记录 why / who / evidence / QA / acceptance。
```

### 2.3 AI workforce / no-code agent 平台

代表：

- Relevance AI
- Lindy
- CrewAI Enterprise

它们的优势：

- “AI teammate / AI workforce”心智清楚。
- 上手快，适合业务团队创建 Agent。
- 更像新员工市场，模板化和角色化强。

它们对 Orrery 的威胁：

- 如果 Orrery 讲“组建 Agent 团队”，容易被认为是同类。
- 如果 Orrery 做 no-code agent builder，会和 Relevance AI、CrewAI 正面碰撞。
- 如果 Orrery 缺少交付闭环，它的 mission room 只会像另一个 agent workspace。

Orrery 应避开：

```text
不要把 Agent 市场 / Agent 招募 / Agent 角色扮演当主产品。
```

Orrery 可切入：

```text
Agent 只是责任主体之一。
Orrery 的核心对象是 Mission delivery contract，而不是 Agent 本身。
```

### 2.4 Coding agent / 软件工程代理

代表：

- OpenAI Codex
- GitHub Copilot coding agent
- Cognition Devin
- Factory Droid

它们的优势：

- 专注代码执行，品牌和效果更强。
- 直接进入仓库、分支、PR、CI、review。
- 能独立完成工程任务并产出 diff。

它们对 Orrery 的威胁：

- 如果 Orrery 把“软件开发 Agent”当唯一方向，会被这些产品直接压住。
- 如果 Orrery 自己造 coding agent，成本高且差异不明显。
- 用户更愿意让专门 coding agent 写代码，而不是让 Orrery 重新实现。

Orrery 应避开：

```text
不要和 Codex / Copilot / Devin / Factory 比谁更会写代码。
```

Orrery 可切入：

```text
把这些 coding agents 当 execution worker，
Orrery 负责上游任务契约、下游证据、QA、客户验收和跨 Agent 协同记录。
```

### 2.5 Agent 开发基础设施 / observability

代表：

- LangGraph / LangSmith
- AgentOps
- Langfuse / Braintrust 等相邻工具

它们的优势：

- 开发者生态。
- tracing、eval、debug、deployment 能力强。
- 适合工程团队构建自己的 Agent 应用。

它们对 Orrery 的威胁：

- 如果 Orrery 做开发者平台，会被它们覆盖。
- 如果 Orrery 只做 run trace，看起来像低配 observability。

Orrery 应避开：

```text
不要先做开发者 Agent PaaS。
不要把 tracing/debug 当客户价值终点。
```

Orrery 可切入：

```text
使用 traces / events / artifacts 作为 evidence 来源，
但最终产物是 PM 和客户能理解的 Review Packet。
```

## 3. Orrery 的可付费价值

### 高价值场景

最可能付费的价值不是“多 Agent 很酷”，而是：

```text
减少交付负责人在 AI 协作项目中的解释成本、返工成本和验收风险。
```

对第一 ICP 来说，Orrery 应该证明这些结果：

- PM 花更少时间追进度和整理汇报。
- 客户更快理解交付依据。
- 任务返工原因能被分类。
- Agent 输出不再只是聊天记录，而是有证据和 QA 的交付单元。
- 团队能复用 Mission 模板、TaskContract、QA checklist 和 Review Packet。
- 交付团队能更放心地把部分执行交给 Agent，同时保留人类审批。

### 低价值或危险场景

以下卖点不够硬：

- “我们是 Agent 公司操作系统。”
- “我们可以创建很多 Agent。”
- “我们支持多 Agent 讨论。”
- “我们有实时聊天。”
- “我们有任务面板。”
- “我们未来可以接很多工具。”

这些都容易被理解为功能，而不是愿意付费的商业结果。

### 第一可售卖产品包装

第一可售卖包装应是：

```text
Client-ready Mission Review Packet
```

它包含：

- Mission brief：目标、范围、非目标、验收标准。
- Responsibility map：human / agent / tool 的责任分配。
- Task contracts：每个任务的 owner、输入、输出、required evidence、QA criteria。
- Evidence ledger：文件、链接、工具调用、代码 diff、数据源、人工确认。
- QA result：通过、失败、风险、需复核项。
- Decision log：关键判断和批准记录。
- Client summary：面向客户的结果摘要和开放风险。

这才是 Orrery 可以收费和差异化的核心商品。

## 4. 核心竞争力

Orrery 的核心竞争力不应该定义成“有 Agent runtime”。Agent runtime 会商品化，模型能力会外部化，执行工具会被专业产品拿走。

核心竞争力应该定义为：

```text
把模糊客户目标转成可执行、可追踪、可验收、可复盘的 AI-assisted delivery system。
```

### 4.1 Mission Delivery Contract 数据模型

这是第一护城河。

Orrery 必须围绕 Mission 建立强数据模型：

- Mission brief。
- Scope / non-scope。
- Acceptance criteria。
- TaskContract。
- Required evidence。
- Actor：human、agent、tool、external system。
- Artifact。
- QA check。
- Decision / approval。
- Review Packet。
- Audit event。

如果这些对象只是 UI 文案，产品没有护城河。它们必须成为数据库、API、事件、权限、实时同步、导出和 QA 的中心对象。

### 4.2 Evidence-first workflow

第二护城河是 evidence-first。

Orrery 不应允许 Mission 只靠聊天摘要闭环。每个关键结论都应能关联证据：

- 人工上传文件。
- Agent 生成 artifact。
- 工具调用结果。
- 代码提交 / PR / diff。
- 外部系统记录。
- 人类审批。
- QA 检查结果。

Review Packet 不是漂亮总结，而是证据索引。

### 4.3 Human + Agent responsibility mapping

第三护城河是责任映射。

真实交付不会完全由 Agent 完成。Orrery 应把 human、agent、tool 都当成可追踪 actor，并记录：

- 谁负责。
- 谁执行。
- 谁审核。
- 谁批准。
- 哪个工具产生了关键结果。
- 哪个 Agent 使用了哪个上下文和权限。

这能把 Orrery 和普通聊天 / workflow / agent builder 区分开。

### 4.4 Vertical delivery playbooks

第四护城河是场景化模板。

通用模板价值有限。Orrery 应优先沉淀少数能收费的 playbook：

- 客户需求澄清与方案交付。
- 竞品/市场调研交付。
- 自动化流程设计与验收。
- 小型软件功能交付与代码审查。
- 数据整理 / 报告生成 / 客户汇报。

每个 playbook 都应包含 Mission brief 模板、TaskContract 模板、required evidence、QA checklist、Review Packet 结构。

### 4.5 Runtime adapter strategy

第五护城河不是自己写所有 Agent，而是成为多执行器的控制层。

Orrery 应保留接口，让这些执行器成为 worker：

- 内置 Agent runtime。
- Codex / GitHub Copilot / Devin / Factory 这类 coding agent。
- Zapier / Workato / n8n 这类自动化执行器。
- 人类任务。
- 自定义 MCP / tool runner。

Orrery 的价值是统一 mission contract、evidence、audit、QA 和 acceptance，而不是替代所有执行器。

## 5. 当前最薄弱点

这一节是后续实施的主要约束。凡是不能补强这些薄弱点的工作，都不应进入近期核心路线。

### 5.1 产品价值还停留在“能运行”，不是“能验收”

当前项目已有 mission、discussion、agent、runtime、permissions、audit 等基础，但商业上最关键的 Review Packet / evidence / QA / acceptance 还没有被放到产品中心。

风险：

```text
用户会觉得 Orrery 是另一个 agent workspace，而不是交付控制台。
```

必须补强：

- Mission 完成必须以 Review Packet 为闭环。
- Task 必须要求 evidence。
- QA result 必须成为一等对象。
- 前端主路径必须围绕“交付验收”组织，而不是围绕聊天组织。

### 5.2 差异化叙事过于抽象

“Agent Mission Control”“人机共生组织控制平台”方向感有，但商业语义太大。销售时容易被问：

- 和 Copilot Studio 有什么区别？
- 和 Relevance AI 有什么区别？
- 和 Zapier Agents 有什么区别？
- 和 LangGraph / AgentOps 有什么区别？
- 和 Jira / Linear / Notion AI 有什么区别？

必须补强：

```text
把定位收窄到 AI-assisted delivery control。
核心输出物是 client-ready Review Packet。
```

### 5.3 第一 ICP 还没有证据

现在假设第一 ICP 是 AI agency / 咨询交付团队，但还没有足够客户访谈、真实项目导入、付费意愿和竞品替代证据。

必须补强：

- 找 5-10 个设计伙伴。
- 用真实客户项目跑完整 Mission。
- 记录他们现在用什么替代方案。
- 量化 PM 整理时间、返工率、客户验收时间、证据完整度。
- 验证是否愿意按 workspace + mission usage 付费。

### 5.4 数据模型还需要围绕交付重塑

如果当前数据模型主要围绕 conversation、message、agent run，后续很容易继续被聊天产品牵引。

必须补强：

- MissionContract。
- TaskContract。
- Evidence。
- Artifact。
- QAResult。
- ReviewPacket。
- Approval。
- Actor / responsibility map。

这些对象要进入 repository、service、event、audit、realtime、permission、export，而不是只做页面展示。

### 5.5 SaaS 商业底座不足

作为 SaaS，必须补齐：

- Tenant / workspace / organization 边界。
- RBAC / policy。
- Billing account。
- Usage ledger。
- Cost attribution。
- Audit log。
- Support / ops view。
- Data retention / export。
- Invite / member lifecycle。

这些不是锦上添花。没有这些，产品无法从个人工具变成团队 SaaS。

### 5.6 前台、管理端、运营端边界混在一起

用户工作台、租户管理端、平台运营端不能长期硬集成在一个产品壳里。

必须补强：

- User app：Mission 日常交付。
- Tenant admin：成员、Agent、工具、知识、权限、用量。
- Platform ops：租户支持、队列、worker、成本、故障、迁移。

物理上可以先是 monorepo 和模块化单体，但信息架构和授权边界必须先分开。

### 5.7 执行器能力不应成为唯一赌注

如果 Orrery 想靠自研 Agent runtime 赢，风险很高。模型、coding agent、自动化执行器都在快速进化。

必须补强：

```text
把 Agent runtime 抽象成 adapter。
Orrery 自己的 runtime 是默认执行器之一，不是唯一核心资产。
```

### 5.8 竞品 battlecard 缺失

现在还没有面向销售和产品决策的 battlecard。

必须补强：

- Orrery vs Copilot Studio：不比企业办公自动化，比客户交付验收。
- Orrery vs Salesforce Agentforce：不比 CRM agent，比跨工具交付证据。
- Orrery vs ServiceNow AI Control Tower：不比企业全域治理，比 mission-level accountability。
- Orrery vs Zapier / Workato：不比连接器数量，比证据、QA、客户验收。
- Orrery vs Relevance AI / Lindy：不比 AI employee 创建，比交付责任链。
- Orrery vs Codex / Devin / Factory：不比代码执行，比从需求到验收的交付控制。
- Orrery vs LangGraph / AgentOps：不比开发者 tracing，比客户可读 Review Packet。

## 6. 产品策略

### Positioning

```text
For AI agencies and consulting delivery teams
who use humans, AI agents, and tools to deliver client work,
Orrery is an AI-assisted delivery control SaaS
that turns goals into auditable Missions with responsibility, evidence, QA, and client-ready review packets,
unlike agent builders, workflow automation tools, or coding agents,
Orrery manages the delivery accountability layer across people, agents, tools, and approvals.
```

中文表达：

```text
Orrery 帮交付团队把分散的 AI 协作变成可审计 Mission：
每个任务都有负责人、证据、QA 结果和客户可验收的 Review Packet。
```

### ICP

第一 ICP：

```text
10-100 人的 AI agency、咨询交付团队、自动化工作室或外包技术服务团队。
```

关键角色：

- 创始人 / 交付负责人。
- PM / 项目经理。
- 自动化顾问。
- 解决方案顾问。
- QA / 交付质量负责人。
- 客户成功 / 客户交付经理。

排除对象：

- 只想要个人 AI 助手的用户。
- 只要 API / SDK 的开发者平台客户。
- 还没有真实 AI 交付任务的大企业 AI CoE。
- 只想买企业全域 AI 治理的客户。
- 只想要 coding agent 的研发团队。

### Jobs To Be Done

```text
当我承接一个客户目标时，
我想把它拆成可执行、可追踪、可验收的 Mission，
让人、Agent 和工具协同完成，
并最终交付一个有证据、有责任、有 QA 结论的客户验收包。
```

### North Star

```text
Accepted Missions with evidence per active workspace
```

不是“创建了多少 Agent”，也不是“发了多少消息”，而是每个活跃 workspace 完成了多少个带证据并被验收的 Mission。

### One Metric That Matters

近期最重要指标：

```text
Review Packet acceptance rate
```

辅助指标：

- Evidence coverage per completed Mission。
- QA pass rate。
- Rework reason classification rate。
- Time from Mission brief to Review Packet。
- PM manual summary time saved。
- Customer review turnaround time。

## 7. 商业模式

推荐混合定价：

```text
Workspace subscription
+ seat tier
+ accepted Mission / active Mission allowance
+ usage-based runtime cost pass-through
+ business/enterprise audit and admin features
```

### 为什么不是纯 seat

Agent 执行成本和任务量强相关。纯 seat 可能覆盖不了模型、工具、worker、存储和导出成本。

### 为什么不是纯 usage

交付团队需要预算确定性。纯 usage 会让用户不敢让 Agent 多跑，也不利于团队协作。

### 推荐包装

- Team：面向小型交付团队，核心 Mission、TaskContract、Evidence、Review Packet。
- Business：多客户空间、模板库、权限、用量、导出、审计。
- Enterprise / Private：SSO、私有 runtime、数据隔离、高级审计、专属支持。

### 付费验证

近期必须验证三件事：

- 用户是否愿意为 Review Packet 付费，而不是只为 Agent 执行付费。
- 用户更接受按 workspace、seat、mission、还是 runtime usage 组合收费。
- Orrery 是否能减少 PM 汇总时间、返工率或客户验收时间。

## 8. PRD：最终落地目标

这里不按临时阶段规划，而按最终 SaaS 目标定义产品边界。实现时可以拆小任务，但不能偏离最终目标。

### User App

用户工作台只服务日常交付：

- Mission intake。
- PM clarification。
- Mission brief。
- Mission room。
- Discussion group。
- Task board。
- TaskContract。
- Evidence submission。
- QA review。
- Approval。
- Review Packet。
- Inbox / mentions。

### Tenant Admin

租户管理端服务客户自己的管理：

- Workspace / organization。
- Members / roles。
- Agent profiles。
- Tool / MCP / skills。
- Knowledge / memory。
- Usage limits。
- Audit log。
- Billing settings。
- Export / retention policy。

### Platform Ops

平台运营端服务 Orrery 自己：

- Tenant support view。
- Runtime / worker health。
- Queue / outbox / realtime health。
- Failed run retry。
- Dead-letter review。
- Migration status。
- Cost and usage observability。
- Incident audit。

### Execution Plane

执行平面负责运行，不负责产品叙事：

- Agent runtime worker。
- Runtime adapter。
- Tool call guardrails。
- Artifact capture。
- Evidence binding。
- QA checks。
- Retry / failure classification。
- Outbox / realtime publication。

## 9. 架构边界

Repo 级客户端、服务端和协议边界，以 [SaaS 客户端、服务端与协议边界规划](../agent-company-mission-control/saas-monorepo-boundaries.md) 为准。本节只保留产品架构方向摘要。

2026-07-25 的客户端决策将 Desktop 调整为仓库根平级的 `orrery-desktop/` AionUI 衍生项目；`orrery_v2` 继续承载 SaaS Web、Go Server 和共享协议。详细边界以 [Orrery Desktop 客户端方案](../../../orrery-desktop/docs/orrery-desktop-client.md) 为准。

目标 repo 边界：

```text
repositories/
  orrery_v2/
    clients/
      web/          browser SaaS client

    server/
      api/          Go API control plane
      worker/       Agent runtime/background execution
      tools/
        resourcectl/
      internal/     Go server private domain/platform code
      config/
      migrations/

    protocol/
      openapi/      HTTP API source contracts
      events/       realtime event source contracts
      schemas/      shared JSON schemas

    docs/
    scripts/

  orrery-desktop/  AionUI derivative: Electron + React + AionCore + Device Connector
```

### Control Plane

- Auth/session。
- Tenant/workspace/org。
- User/member/role。
- Mission / task / discussion / evidence / review。
- Resource admin。
- Billing / usage。
- Audit。
- Realtime token and publication policy。

### Execution Plane

- Agent run。
- Runtime adapter。
- Tool call。
- Sandbox / workspace execution。
- Artifact / evidence capture。
- QA run。
- Retry and failure classification。

### Realtime/Event Plane

- DB outbox。
- Centrifuge publication。
- Redis/NATS adapter interface reserved。
- Channel authorization by tenant/user/mission scope。

先不使用 Redis，但事件发布、队列和 realtime 层必须保留 adapter 接口，避免未来迁移时重写业务逻辑。

## 10. 近期不做

为了避免重复工作，以下不应进入近期核心实施：

- 通用 Agent marketplace。
- 通用 no-code agent builder。
- 企业全域 AI governance。
- 大而全连接器平台。
- 独立开发者 PaaS。
- 把 desktop 当主产品形态。
- 以聊天为中心继续堆功能。
- 自研 coding agent 去正面打 Codex / Devin / Factory。
- 没有 evidence / QA / acceptance 的漂亮 dashboard。

## 11. Pre-mortem

### 大风险

1. Orrery 看起来像另一个 agent workspace。

缓解：产品主路径必须以 Review Packet 闭环，Mission 完成必须有 evidence 和 QA。

2. 被连接器平台夹击。

缓解：不拼连接器数量；只把外部执行结果纳入 evidence 和 audit。

3. 被 coding agent 夹击。

缓解：把 coding agent 当 worker；Orrery 负责需求、责任、证据、QA、验收。

4. SaaS 底座不足导致无法卖团队。

缓解：tenant、RBAC、usage、audit、billing、ops view 必须是核心架构对象。

5. 第一 ICP 不买账。

缓解：先跑真实 design partner mission，以付费意愿和 Review Packet 使用率判断方向。

6. Review Packet 变成无依据摘要。

缓解：Review Packet 的每个关键结论必须绑定 evidence、QA result、artifact、tool call 或 human approval。

### 纸老虎

- “必须立刻微服务化。” 现在不需要。模块化单体 + 独立 worker 入口足够。
- “必须第一天上 Redis。” 现在可用 DB outbox + Centrifuge，但要保留 Redis/NATS adapter。
- “必须拥有自己的所有 Agent 能力。” 不需要，真正价值在交付控制层。

### 暗雷

- 定价未验证。
- 客户可能更想买 done-for-you 服务，而不是纯 SaaS。
- 交付团队可能愿意用 Notion/Jira/Slack 手工拼接，除非 Review Packet 明显省时间。
- Agent 输出质量不稳定会反噬客户信任。
- 如果没有垂直 playbook，产品会显得泛化。

## 12. 文档和实施约束

后续所有实施 goal 都要对照本节 code review，确认没有偏离。

每个小阶段 code review 必须检查：

- 是否强化 Mission Delivery Contract。
- 是否把 Evidence / QA / Review Packet 前置。
- 是否保持 User App / Tenant Admin / Platform Ops 边界。
- 是否保留 runtime adapter，而不是把某个执行器写死为核心。
- 是否维持 DB outbox + Centrifuge 当前方案，并保留 Redis/NATS 可启用接口。
- 是否避免回到 desktop-first。
- 是否避免做通用 Agent builder / 通用 workflow builder。

## 13. 公开资料索引

以下资料用于本轮竞品和市场判断，后续应按季度更新：

- [Microsoft Copilot Studio](https://www.microsoft.com/en-us/microsoft-365-copilot/microsoft-copilot-studio)
- [Microsoft Copilot Studio autonomous agents training](https://learn.microsoft.com/en-us/training/modules/autonomous-agent/)
- [Salesforce Agentforce](https://www.salesforce.com/agentforce/)
- [Salesforce Agentforce 2.0 announcement](https://investor.salesforce.com/news/news-details/2024/Introducing-Agentforce-2.0-The-Digital-Labor-Platform-for-Building-a-Limitless-Workforce/default.aspx)
- [ServiceNow AI Control Tower](https://www.servicenow.com/products/ai-control-tower.html)
- [UiPath Maestro](https://www.uipath.com/platform/agentic-automation/agentic-orchestration)
- [UiPath Maestro documentation](https://docs.uipath.com/maestro/automation-cloud/latest/user-guide/overview)
- [Workato Agent Studio](https://www.workato.com/agentstudio)
- [Zapier Agents](https://zapier.com/agents)
- [Relevance AI](https://relevanceai.com/)
- [Relevance AI Workforce](https://relevanceai.com/workforce)
- [LangGraph](https://www.langchain.com/langgraph)
- [LangSmith Observability](https://www.langchain.com/langsmith/observability)
- [OpenAI Codex cloud](https://developers.openai.com/codex/cloud)
- [GitHub Copilot cloud agent](https://docs.github.com/en/copilot/concepts/agents/cloud-agent/about-cloud-agent)
- [Devin](https://devin.ai/)
- [Factory](https://factory.ai/)
- [McKinsey State of AI 2025](https://www.mckinsey.com/capabilities/quantumblack/our-insights/the-state-of-ai)
- [Deloitte State of AI in the Enterprise](https://www.deloitte.com/us/en/what-we-do/capabilities/applied-artificial-intelligence/content/state-of-ai-in-the-enterprise.html)
- [Gartner agentic AI cancellation prediction](https://www.gartner.com/en/newsroom/press-releases/2025-06-25-gartner-predicts-over-40-percent-of-agentic-ai-projects-will-be-canceled-by-end-of-2027)
- [Gartner enterprise apps AI agents prediction](https://www.gartner.com/en/newsroom/press-releases/2025-08-26-gartner-predicts-40-percent-of-enterprise-apps-will-feature-task-specific-ai-agents-by-2026-up-from-less-than-5-percent-in-2025)
