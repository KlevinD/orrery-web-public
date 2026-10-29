# Orrery 人机共生组织控制平台商业企划书

生成日期：2026-05-31

## 1. 执行摘要

回头核对原始商业方案后，需要把定位从“Agent 公司 Mission Control”重新拉回更大的母体：**人、组织、智能体共生的组织管理与执行控制平台**。

Mission Control 仍然重要，但它不是全部商业方案，而是最容易被客户理解、最适合早期销售的第一产品前台。真正长期壁垒不是一个任务看板，而是把人员主数据、组织关系、Agent 成员、Squad、任务、权限、审批、HIL、MCP 工具调用、证据和评价放进同一个控制基座。

Agent 公司 Mission Control 的商业机会，不在于再做一个更会写代码、更会聊天或更会自动化的单点 Agent，而在于做 **人类与多个 Agent 共同工作的任务交付控制层**，并把这个交付层接入组织、权限、工具和评价体系。

当前市场已经出现大量单点 Agent、业务 Agent、RPA 编排、no-code workflow、AI employee 和软件开发 Agent。它们证明了需求存在，也说明正面竞争非常拥挤。我们的机会是站在这些能力之上，把 Codex、Claude Code、Cursor、RPA、CRM Agent、财务 Agent、法务 Agent、客服 Agent、真人协作者等包装成可承担责任的团队成员，让它们在一个 Mission 中协作、留痕、交付、验收和审计。

修正后的一句话定位：

```text
人、组织、智能体共生的组织管理与执行控制平台：
用组织事实源、Agent 目录、Mission Control、权限/HIL、MCP Gateway、证据和评价，
管理跨真人、跨 Agent、跨工具的业务任务交付与治理。
```

这个产品不卖“万能自动化”，而卖一个更具体、更容易验证的结果：

```text
用 Agent 公司帮客户交付一个可审计、可追踪、可验收的业务任务。
```

但长期真正要卖的是：

```text
让企业安全、可控、可追责地雇佣、调度、授权和评价一支由真人与 Agent 混编的数字组织。
```

## 2. 原始商业方案回溯

早期原始方案的核心不是“给 Agent 多一个看板”，也不是“再造一个 HRIS”，而是一个新类目：

```text
Human-Agent Organization Operating System
人机共生组织操作系统
```

它要回答的不是单一任务怎么跑，而是企业在引入 Agent 后必然遇到的一组基础问题：

- 谁是组织里的真实人员？
- 哪些 Agent 是组织成员？
- 人和 Agent 如何组成 Squad？
- 谁能把任务交给哪个 Agent？
- Agent 可以代表谁调用哪些 MCP 工具？
- 哪些动作需要人审批或 HIL？
- 谁授权了哪个 Agent 做了什么？
- 人、Agent、Squad 的表现如何被评价？
- 下游 MCP 生态如何安全、低成本、可审计地接入？

因此，原始商业方案的完整闭环应是：

```text
People Directory / Organization Graph
  -> Agent Directory / Mixed Squad
  -> Mission Brief / Mission Staffing / Mission Task
  -> Agent Runtime / MCP Tool Request
  -> Permission Decision
  -> HIL / Approval
  -> Evidence / Review Packet
  -> Evaluation / Ops Dashboard
```

原始方案里最重要的七个模块：

| 模块 | 商业含义 |
| --- | --- |
| People Master Data | 记录组织里真实人员，但不做重型 HRIS |
| Organization Graph | 让人、Agent、Squad 都进入组织关系和汇报关系 |
| Agent & Squad Management | 把 Agent 从工具升级为可调度、可授权、可评价的组织成员 |
| Work / Mission Control | 把用户目标转成可交付、可验收、可追责的任务过程 |
| Permission & Policy Center | 决定某个主体在某个上下文里能不能做某个动作 |
| Approval / HIL Center | 把人类介入产品化，而不是临时弹窗 |
| MCP Gateway & Audit Console | 成为下游 MCP 工具生态的统一安全入口和审计层 |

这意味着：Mission Control 是商业切入口，组织控制面才是长期平台。早期可以卖“可审计任务交付”，中后期要升级为“企业人机共生组织的控制平面”。

## 3. 自身产品介绍

Agent 公司 Mission Control 是一个“任务型 Agent 公司”的操作系统。用户不需要先学习某个 Agent 工具、workflow 平台或自动化脚本，而是像向一个项目团队下达任务一样，说清楚自己想完成什么。系统负责理解目标、组建团队、拆分责任、推进执行、收集证据、组织验收，并交付最终成果。

产品可以被理解为三层：

```text
第一层：Mission Control
客户看到的任务控制台，展示目标、团队、任务、进度、证据、风险和最终交付。

第二层：Agent 公司
由 PM、HR、Planner、QA、Worker、RealHumanAgent 组成的动态项目团队。

第三层：组织与治理基座
People Directory、Organization Graph、Agent Directory、Permission、HIL、Evaluation。

第四层：工具与运行适配器
Codex、Claude Code、Cursor、RPA、CRM API、搜索工具、财务系统、法务工具等。
```

关键点是：客户购买的不是工具集合，而是“可被管理的交付过程”。工具只负责执行局部动作，Agent 才是责任主体。

### 3.1 产品给客户解决什么

客户交给系统的不是“调用某个工具”，而是一个结果导向任务，例如：

- “帮我做一份某行业竞品监控周报。”
- “帮我筛一批销售线索，更新 CRM，并输出跟进建议。”
- “帮我完成一个小型自动化脚本，并用浏览器验证结果。”
- “帮我初审这批合同，标出风险，整理给业务负责人确认。”
- “帮我处理一批复杂客服工单，哪些需要人工主管介入要标出来。”

系统输出的也不是单纯聊天答案，而是一个交付包：

- 最终成果。
- 执行摘要。
- 任务拆分和责任人。
- 每个子任务的交付物。
- 关键证据和附件。
- QA 验收记录。
- 风险、阻塞和人工决策记录。
- 后续建议。

### 3.2 产品里的核心角色

Agent 公司不是一堆工具节点，而是一组可承担责任的成员。

| 成员 | 对客户的可理解职责 |
| --- | --- |
| PM Agent | 理解客户目标，澄清范围，组织交付，最终汇总 |
| HR / Staffing Agent | 根据任务需要选择合适 Agent 或真人进入团队 |
| Planner Agent | 把目标拆成可验收的责任单元 |
| Worker Agent | 执行具体任务，可以包装 Codex、RPA、API、搜索、领域工具 |
| QA Agent | 检查成果和证据是否满足验收标准 |
| RealHumanAgent | 真人专家、审批人、操作员或客户侧协作者 |
| Customer / User | 提出目标、补充信息、做关键决策、确认最终结果 |

这些角色不一定每次全部出现。简单任务可以只有 PM、一个 Worker 和 QA；复杂任务可以临时追加新成员。系统的价值在于团队可变，但责任协议统一。

### 3.3 客户实际看到的产品

客户打开产品后，看到的是一个 Mission 工作台，而不是技术编排器。

工作台应包含：

- **任务目标**：这次 Mission 要达成什么，交付标准是什么。
- **团队成员**：谁在参与，是真人还是 Agent，分别负责什么。
- **任务看板**：每个责任单元的状态、负责人、截止时间、阻塞。
- **讨论组**：客户、真人、Agent 都能在同一上下文中沟通，支持 `@mention` 和 reply。
- **证据区**：报告、截图、文件、工具日志、链接、测试结果等证据。
- **验收区**：每个任务是否通过，失败原因是什么，返工给谁。
- **最终交付包**：面向客户的一份干净总结，隐藏内部噪音，保留证据链接。

客户不需要看到 Agent 的全部思考链、内部尝试和工具噪音。默认展示的是摘要、成果、证据、决策和阻塞。

### 3.4 第一个版本应该长什么样

第一版产品不需要像大型企业平台，也不需要完整开放市场。它应该像一个可以真实交付任务的“项目作战室”。

最小可卖版本：

1. 创建 Mission：输入目标、背景、交付要求。
2. 组建团队：从少量 curated Agent Profile 和真人中选择成员。
3. 拆任务：生成多个可验收的责任单元。
4. 执行与讨论：Agent 和真人在任务上下文中协作。
5. 提交成果：每个成员必须提交 summary、deliverable、evidence。
6. QA 验收：按任务检查证据和结果。
7. 交付包：生成客户可读的最终报告和审计记录。

第一版最应该做好的不是“自动化程度”，而是“交付可信度”。

### 3.5 产品线拆分

从商业包装上，产品可以拆成五条可逐步销售的产品线：

| 产品线 | 第一阶段卖点 | 中后期价值 |
| --- | --- | --- |
| Mission Control | 可审计任务交付工作台 | 多团队、多行业任务运行中心 |
| Workforce & Org Control | 人和 Agent 的组织目录 | 组织权限、汇报关系、岗位和资源调度 |
| Permission / HIL Center | 高风险动作审批与人工介入 | 企业级 Agent 权限和审批控制面 |
| MCP Gateway | 工具调用审计和风险拦截 | 下游 MCP 生态入口、凭证托管和调用治理 |
| Evaluation Center | 任务证据和 QA 记录 | 人、Agent、Squad 的表现评价和运营看板 |

这比单纯 Mission Control 更完整：客户前台买的是交付可信度，企业后台买的是组织治理、权限安全、工具接入和责任可追溯。

## 4. 背景与机会

AI Agent 正从“回答问题”走向“执行工作”。市场上已经出现三类明显趋势：

1. 软件开发 Agent 正在快速成熟，例如 Codex、Claude Code、Cursor、Devin、Copilot 等。
2. 企业平台开始把 Agent 接入真实业务系统，例如 Salesforce Agentforce、Microsoft Copilot Studio、ServiceNow、UiPath、Workato、Zapier 等。
3. 企业和团队真正关心的问题不只是“Agent 能不能做事”，而是“谁能控制它、谁负责结果、如何验收、如何审计、失败后找谁”。

因此，商业机会不是在每个垂直场景里重做一个 Agent，而是建立一个上层控制台：

- 让不同来源的 Agent 可以被纳入同一个任务团队。
- 让真人和 Agent 以相同的交付协议协作。
- 让任务从开始到交付都有责任、证据、验收和审计记录。
- 让用户可以像引导 Codex 一样，随时通过讨论组影响执行方向。

## 5. 用户痛点

当前团队在使用 AI Agent 和自动化工具时，常见痛点不是“没有工具”，而是工具太散、责任不清、结果难验收。

典型痛点：

- 多个 AI 工具、自动化脚本、外包人力和真人协作者各自工作，过程分散。
- PM 不知道哪个 Agent 正在做什么，做到哪一步，缺什么信息。
- 任务失败后无法定位责任，是需求不清、工具失败、Agent 理解错误，还是 QA 标准不合理。
- Agent 输出只有总结，没有证据；客户或管理者无法判断是否可信。
- 真人协作者和 Agent 的交付格式不一致，项目管理成本很高。
- 企业担心权限、凭证、数据泄露、工具误调用、审计缺失。
- 现有 workflow 工具适合固定流程，但真实业务经常需要临时补人、返工、调整方向和人工判断。

这些痛点共同指向一个需求：团队需要的不是更多孤立 Agent，而是一个能管理 Agent 团队交付过程的 Mission Control。

## 6. 产品定位

Orrery 是人机共生组织控制平台，Agent 公司 Mission Control 是它的第一产品形态。它根据用户需求临时组建项目团队，让 PM Agent、HR Agent、Planner Agent、QA Agent、Worker Agent、RealHumanAgent 在同一个 Mission 中协作；同时用组织、权限、审批、MCP Gateway 和评价体系约束这些成员能做什么、代表谁做、结果如何追责。

从客户角度看，它是“把任务交给一个可审计 Agent 团队”的入口；从团队角度看，它是“让多个 Agent 和真人按同一交付协议工作”的控制台；从企业角度看，它是“把 AI 执行纳入权限、证据、验收和责任体系”的治理层。

产品边界：

| 我们做 | 我们不做 |
| --- | --- |
| 跨 Agent、跨真人、跨工具的任务交付控制层 | 不做单点 AI 程序员 |
| Agent 责任、证据、验收、审计 | 不做裸 workflow builder |
| 动态组队和任务责任划分 | 不做固定流程自动化模板站 |
| 可插拔 Worker，包括 Codex、Claude Code、RPA、API Agent、真人 | 不和 Codex、Cursor、Devin 正面比写代码能力 |
| 讨论组协作、@mention、reply、结构化事件 | 不把聊天记录当作完整项目管理 |
| 人、Agent、Squad 的组织目录和权限控制 | 不做薪资、考勤、社保等重型 HRIS |
| MCP Gateway、HIL、审批和审计 | 不让下游工具各自重复实现企业权限 |

核心原则：

- Agent 代替的是人的生态位，不是工具生态位。
- Tool、CLI、API、RPA 只能被 Agent 包装后使用，不能直接成为团队成员。
- 真人也应被包装为 RealHumanAgent，以同样方式提交总结、成果、证据和阻塞。
- 所有 Agent 都必须交付 summary 和 deliverable。
- summary 不是信任源，evidence 才是信任源。
- 全局流程不能写死，固定流程只存在于局部小闭环中。

## 7. 目标客户

早期最适合的客户，不是大型企业，也不是纯软件研发团队，而是已经有跨工具、跨人、跨 Agent 交付痛点的团队。

| 客户类型 | 主要痛点 | 为什么适合 |
| --- | --- | --- |
| AI 自动化工作室 / AI agency | 用很多工具和外包人力交付，过程散、证据散、客户难验收 | 可以把每次交付包装成 Mission，统一团队、成果、证据和 QA |
| 咨询公司 / 外包交付团队 | PM、顾问、执行、QA 混合协作，责任容易模糊 | Agent 公司模型天然贴近项目制交付 |
| 中小企业老板 / 运营负责人 | 不想学一堆工具，只想把事情办完 | Mission Control 可以包装成托管式任务交付 |
| 跨职能运营团队 | 销售、客服、财务、HR、法务经常跨系统协作 | 需要讨论组、责任、证据、审批和审计 |
| 企业 AI CoE / 自动化中心 | 关心治理、权限、ROI、审计 | 适合中后期，但销售周期较长，不建议作为最早切入点 |
| MCP 工具服务商 / 内部平台团队 | 自己不想重复做权限、审批、审计和凭证托管 | 可以通过 Gateway 接入人机组织控制面 |

第一阶段建议优先服务 AI agency、咨询/外包交付团队和中小企业运营负责人。它们更容易理解“帮我把任务交付出来并留下证据”的价值，采购链路也更短。

## 8. 早期切入场景

第一阶段不应承诺“全自动公司”，而应选择能快速证明价值的任务。

适合的早期任务：

- 市场调研报告交付。
- 竞品监控与周报。
- 销售线索研究与 CRM 更新。
- 客服复杂工单处理。
- 发票、对账、异常处理。
- 合同初审与风险摘要。
- 小型软件或自动化任务交付。

这些任务有共同特征：

- 需要多个角色协作。
- 需要明确责任和交付物。
- 需要证据支持。
- 允许中途补信息、返工和人工判断。
- 不要求一开始做到完全无人值守。

最推荐的第一个商业验证场景：

```text
AI agency / 咨询团队的“可审计任务交付工作台”。
```

原因是这类客户本来就在用多种 AI 工具、人力和自动化脚本交付项目，最容易感知“过程可追踪、责任可定位、客户可验收”的价值。

## 9. 产品形态

早期产品可以包装成一个 Mission Control 工作台，而不是复杂平台。

核心体验：

1. 用户提交一个业务目标。
2. PM Agent 澄清目标、边界、交付物和风险。
3. HR / Staffing 从 Agent 组织目录选择合适成员，包括 AI Agent 和真人。
4. Planner 将任务拆成独立责任单元。
5. Worker 执行任务并提交成果、总结、证据。
6. QA 根据每个任务的验收标准独立检查。
7. Mission Control 展示团队、任务、进度、证据、风险、返工和最终交付。

客户看到的不是复杂技术架构，而是：

- 这个任务谁在做。
- 做到了哪里。
- 每个人负责什么。
- 交付了什么成果。
- 有什么证据。
- 哪些地方失败过，如何返工。
- 最终是否可以验收。

### 9.1 交付包形态

每个 Mission 最终都应产出一份客户可读的交付包。交付包是商业上最容易被客户感知价值的部分。

交付包包含：

| 内容 | 用途 |
| --- | --- |
| 最终成果 | 客户真正购买的结果 |
| 执行摘要 | 让客户快速理解做了什么 |
| 责任清单 | 说明每个子任务由谁完成 |
| 证据目录 | 支撑结论可信度 |
| QA 结果 | 告诉客户哪些通过、哪些返工过 |
| 风险和假设 | 防止交付结果被误用 |
| 后续建议 | 帮客户进入下一步行动 |

这使产品不只是过程管理工具，而是能稳定产出可交付成果的服务系统。

### 9.2 产品护城河

早期护城河不是模型能力，也不是工具数量，而是交付过程数据和责任协议。

可积累资产包括：

- 不同类型任务的拆分模板。
- 不同行业的验收标准。
- Agent Profile 的能力记录和历史表现。
- 证据格式和 QA 规则。
- 人机协作中的常见阻塞和处理方式。
- 可复用的 Mission 模板和交付包模板。
- 人员、Agent、Squad、组织、权限和工具调用之间的审计图谱。
- MCP 工具风险画像、授权策略和 HIL 处理记录。
- 人、Agent、Squad 的评价证据与表现历史。

这些资产越多，系统越知道“什么任务应该找什么成员、用什么证据、按什么标准验收”。

## 10. 商业模式

早期可以采用“产品 + 服务”混合模式，不建议一开始只做纯自助 SaaS。

可选收费方式：

| 模式 | 适用阶段 | 说明 |
| --- | --- | --- |
| 按 Mission 收费 | 最早期验证 | 每个任务包一个价格，适合 AI agency 和中小企业 |
| 月度工作台订阅 | 早期产品化 | 按团队席位、Agent 数量、任务量收费 |
| Agent / Tool 运行额度 | 使用规模扩大后 | 按 Agent run、工具调用、上下文处理、证据存储收费 |
| MCP Gateway 调用与治理 | 企业接入私有工具和 MCP 生态后 | 按 MCP server、tool call、审计存储、凭证托管和风险策略收费 |
| HIL / 审批中心 | 进入高风险业务流程后 | 按审批席位、审批请求量、SLA、流程数量收费 |
| 组织与权限控制面 | 企业版本 | 按人员、Agent、Squad、组织单元、策略数量收费 |
| Evaluation / Ops Dashboard | 客户形成持续使用后 | 按报告、评价周期、运营看板、历史证据存储收费 |
| 企业私有化 / 混合部署 | 中后期 | 面向企业 AI CoE、合规敏感客户 |
| 模板与行业包 | 产品成熟后 | 针对客服、销售、法务、财务、IT 工单等场景售卖 |

早期不建议把“Agent 市场”或“工具市场”作为主要收入。市场应该是供给增长后的结果，不是第一天的商业核心。更重要的是先证明：客户愿意为“可控的人机组织”和“可审计的工具调用”付费。

## 11. 竞品格局

| 方向 | 代表产品 | 它们强在哪里 | 我们避开的正面战场 | 我们的机会 |
| --- | --- | --- | --- | --- |
| 软件开发 Agent | Codex、Claude Code、Cursor、Devin、Copilot | 写代码、改 PR、IDE/CLI 入口 | 不比谁更会写代码 | 把它们接入为 CodeWorkerAgent |
| 企业 Agent 平台 | Salesforce Agentforce、Microsoft Copilot Studio | 企业生态入口、CRM/M365 内置 Agent | 不抢平台生态入口 | 做跨平台、跨真人、跨 Agent 的任务交付控制层 |
| RPA / Agent 编排 | UiPath、ServiceNow AI Control Tower | 企业流程治理、机器人编排、ITSM 入口 | 不做重型流程治理平台 | 强调项目制 Mission、责任和证据 |
| No-code 自动化 | Zapier、Workato | app 连接、workflow builder、MCP 集成 | 不做裸工具流 | 做责任主体、讨论组和验收闭环 |
| AI assistant / AI employee | Lindy、Relevance AI | 轻量助理、GTM Agent、模板化自动化 | 不只卖单个 AI 员工 | 做多成员任务团队和交付控制 |
| Agent 框架 | LangGraph、AutoGen、CrewAI 类 | 底层编排能力 | 不做开发者框架 | 做业务用户可理解的协作工作台 |
| HR / HCM | Workday、Rippling、BambooHR、HiBob | 人员、组织、岗位、权限基础 | 不做薪资、假勤和复杂人事 | 吸收“组织事实源”的严肃性 |
| 权限 / HIL / MCP 网关 | Permit、HumanLayer、Pomerium、Arcade、OpenFGA | 工具调用授权、审批、凭证和审计 | 不只做底层安全 SDK | 把它们接入任务、组织和评价闭环 |

红海区域：

- 通用 Agent Builder。
- No-code workflow automation。
- 企业 Agent governance。
- CRM / Office 内置 Agent。
- RPA orchestration。
- 软件开发 Agent。

更有机会的区域：

```text
跨 Agent / 跨真人 / 跨工具的任务交付控制层。
```

## 12. 差异化优势

### 12.1 责任主体，而不是工具节点

Zapier、n8n、RPA 和很多 Agent workflow 产品容易把工具调用当作节点。我们的核心观点相反：工具不是成员，Agent 才是责任主体。

这意味着每个成员都必须能：

- 接收任务。
- 理解上下文。
- 沟通和回复。
- 调用工具。
- 提交总结。
- 提交成果。
- 提交证据。
- 接受 QA。
- 对失败负责。

### 12.2 人与 Agent 抹平协作差异

真人不是旁路审批人，而是 RealHumanAgent。真人也按同样方式提交成果、总结、证据和阻塞。

这样系统管理的是“可承担责任的成员”，而不是“AI 一套流程、人类一套流程”。

### 12.3 交付可审计

每个 Worker Task 都有独立责任、交付物、证据和验收记录。客户不只看到最终答案，还能看到为什么可信、哪里失败过、谁负责修复。

### 12.4 动态协作，而不是固定流程

真实工作经常变化：信息不足、任务返工、临时加人、用户改变方向、Worker 之间互相提醒。系统不应把全局流程写死，而应通过讨论组、事件、mention、reply 和结构化状态支持动态协作。

### 12.5 客户交付包，而不是内部日志

很多 Agent 产品把过程日志、工具调用和聊天记录直接暴露给用户，客户仍然需要自己判断哪些信息有用。Agent 公司 Mission Control 应该把内部执行噪音压缩成客户可读的交付包，只把必要证据和风险留给客户核查。

这会让产品更像一个可靠的交付团队，而不是一堆自动化日志。

### 12.6 组织控制面，而不是普通任务看板

普通任务看板只回答“任务做到哪里了”。Orrery 要回答的是：

- 谁在组织里有资格执行这个任务？
- 这个 Agent 是哪个部门、岗位或 Squad 的成员？
- 它能不能代表某个人或某个组织调用这个工具？
- 这个工具调用为什么被允许、拒绝或要求 HIL？
- 任务结果、工具调用、审批动作和 QA 结论如何进入评价证据？

这才是原始方案里最商业化的部分：Orrery 不只是让 Agent 干活，而是让组织可以管理 Agent 干活。

## 13. Go-to-market 路径

### 阶段一：用服务化交付验证价值

目标客户：

- AI agency。
- 小型咨询团队。
- 外包交付团队。
- 中小企业运营负责人。

打法：

- 不卖平台，先卖“一个可审计任务交付包”。
- 手动和半自动结合，验证客户是否愿意为责任、证据、验收付费。
- 每个任务结束后沉淀模板、角色、证据格式和 QA 标准。

可销售话术：

```text
你不用管理一堆 AI 工具和外包人员。
把任务交给 Agent 公司，我们负责组队、推进、留证据、做验收，并交付一份可审计结果包。
```

### 阶段二：产品化 Mission Control

目标：

- 固化 Mission、Team、Task、Evidence、QA、Timeline。
- 让客户能看到每个任务的执行状态和证据。
- 让人类和 Agent 都通过统一入口提交总结与成果。

收费：

- 按月订阅。
- 按 Mission 数量。
- 按 Agent run / 工具调用额度。

### 阶段三：进入企业治理场景

目标客户：

- 企业 AI CoE。
- 自动化中心。
- IT / 安全 / 运营平台团队。

卖点：

- Agent 权限治理。
- 工具调用审计。
- MCP Gateway 和凭证托管。
- HIL Inbox 与审批中心。
- 人、Agent、Squad 的组织目录和评价证据。
- 私有系统接入。
- 企业流程证据链。
- 跨 Agent 责任归属。

这个阶段销售周期更长，但客单价更高。

## 14. 里程碑

| 阶段 | 目标 | 成功标准 |
| --- | --- | --- |
| 0：人工验证 | 用人工 + Agent 组合交付 3-5 个真实任务 | 客户愿意为可审计结果付费 |
| 1：任务工作台 | 建立 Mission、Team、Task、Evidence、QA、Timeline | 每个任务能追踪责任、证据和验收 |
| 2：组织事实源 | 建立 People Directory、Organization Graph、Agent Directory | 人、Agent、Squad 都能被组织化管理 |
| 3：可插拔 Worker | 接入 Codex、Claude Code、RPA、API Agent、真人 | Worker 可替换，交付协议不变 |
| 4：权限与 HIL | 建立 Permission Decision、Approval、HIL Inbox | 高风险动作可拦截、可审批、可恢复 |
| 5：MCP Gateway | 接入 MCP Registry、Tool Audit、Credential Vault | 下游工具调用可授权、可审计、可治理 |
| 6：行业模板 | 沉淀调研、销售、客服、法务、财务等模板 | 新任务启动成本下降 |
| 7：企业版本 | 私有部署、治理看板、评价中心 | 能进入企业 AI CoE 场景 |

## 15. 核心风险

| 风险 | 表现 | 应对 |
| --- | --- | --- |
| 定位太大 | 客户听成“万能 Agent 公司” | 早期只卖可审计任务交付 |
| 技术先行过重 | 文档和产品都像架构平台 | 商业叙事聚焦责任、证据、验收、结果 |
| 和巨头正面竞争 | 被看成 Copilot Studio / Agentforce / Devin 替代品 | 明确我们是上层控制层，接入它们而不是替代它们 |
| 交付成本失控 | 多 Agent 上下文、工具调用和返工成本过高 | 默认 summary-only，上下文压缩，证据链接，限制重试 |
| 客户不信任结果 | Agent 总结看起来像幻觉 | 所有 summary 必须绑定 evidence |
| 真人使用门槛高 | 真人不愿意按复杂流程提交 | 真人入口只保留成果、摘要、证据、阻塞 |
| 组织目录冷启动 | 没有足够可用 Agent | 先做 curated directory，不做开放市场 |
| ROI 难证明 | 客户觉得只是管理界面 | 用返工减少、证据完整、交付周期、客户验收成本衡量 |
| 做成普通 Agent 看板 | 只有任务日志，没有组织、权限、HIL、MCP 壁垒 | 把组织事实源、权限决策、HIL 和 MCP Gateway 放入核心路径 |
| 做成重型 HRIS | 陷入薪资、假勤、社保、复杂人事流程 | 只做协作、授权、任务和评价所需的人员主数据 |
| HIL 变成橡皮图章 | 审批人只看到“同意/拒绝”，不知道风险 | HIL 必须展示调用者、代表关系、工具、参数摘要、风险、历史和恢复动作 |

## 16. 原始调研过程回顾

本商业企划来自当前会话中的几轮方向修正。

更早的原始方案实际来自 `OrreryApp/docs/human-agent-organization-platform/` 下的人机共生组织平台文档。当时的核心判断是：Orrery 不应该只做 Agent Mission Control，也不应该做成传统 HRIS，而应该做“人、组织、智能体共生的组织管理与执行控制平台”。

第一轮判断偏向软件开发 Agent 协作层。当时结论是：不要做另一个 Devin/Codex，而是做 Agent-and-Human-in-the-loop 的协作操作系统，让 PM、HR、Planner、QA、Worker 和用户在同一个讨论组中协作、留痕、验收和交付。

第二轮方向被修正为不限制在软件研发。原因是软件开发 Agent 已经有 Codex、Claude Code、Cursor、Devin、Copilot 等强竞争者，正面竞争没有优势。更合理的是把这些软件开发 Agent 当作 Worker，做它们上面的 Agent Control Plane / Mission Control。

第三轮调研扩展到非研发应用：Salesforce Agentforce、Microsoft Copilot Studio、ServiceNow、UiPath、Workato、Zapier 等都在证明企业 Agent 正在进入真实业务流程。它们也说明控制、治理、观测、权限、审计和跨工具协作正在成为市场需求。

第四轮回溯到原始商业方案后，定位进一步修正：Mission Control 是前台任务入口，背后的商业母体是人机组织控制面。它包括 People Directory、Organization Graph、Agent Directory、Mixed Squad、Permission Center、HIL / Approval Center、MCP Gateway 和 Evaluation Center。

最终形成的商业判断：

```text
市场不缺单点 Agent，也不缺单纯任务看板。
市场缺的是能让人、组织、Agent、工具和审批处在同一个责任体系里的控制层。
```

## 17. 参考竞品与资料

- [Workday HCM](https://www.workday.com/en-us/products/human-capital-management/overview.html)
- [Rippling](https://www.rippling.com/)
- [Asana AI Teammates](https://asana.com/product/ai)
- [Salesforce Agentforce](https://www.salesforce.com/platform/agentforce-platform/)
- [Microsoft Copilot Studio May 2026](https://www.microsoft.com/en-us/microsoft-copilot/blog/copilot-studio/new-and-improved-computer-using-agents-a-new-workflows-experience-and-real-time-voice-experiences/)
- [ServiceNow Autonomous Workforce](https://newsroom.servicenow.com/press-releases/details/2026/ServiceNow-turns-enterprise-AI-chaos-into-control-with-the-platform-for-governed-autonomous-work/default.aspx)
- [UiPath for Coding Agents](https://www.uipath.com/newsroom/uipath-for-coding-agents-launch)
- [Workato Agentic](https://docs.workato.com/agentic/agentic.html)
- [Zapier MCP](https://zapier.com/mcp)
- [OpenAI Codex](https://openai.com/index/codex-now-generally-available/)
- [GitHub Copilot coding agent](https://docs.github.com/en/copilot/using-github-copilot/coding-agent/about-assigning-tasks-to-copilot)
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code/overview)
- [Cursor Background Agents](https://docs.cursor.com/id/background-agent)
- [Windsurf Cascade](https://docs.windsurf.com/windsurf/cascade)
- [Factory Droids](https://docs.factory.ai/enterprise)
- [Permit](https://www.permit.io/)
- [HumanLayer](https://www.humanlayer.dev/)
- [OpenFGA](https://openfga.dev/)

## 18. 最终商业判断

Orrery 值得做，但要避免把自己缩窄成“Agent 公司 Mission Control”。更准确的判断是：

```text
Mission Control 是销售入口。
人机共生组织控制面是商业母体。
MCP Gateway + HIL + 权限 + 评价证据是长期壁垒。
```

不要卖：

```text
万能 Agent 公司
```

也不要卖：

```text
更强的软件开发 Agent
```

应该卖：

```text
一个可审计的 Agent 任务交付控制台，
以及一套能管理人、Agent、组织、工具调用和审批责任的人机组织控制平台。
```

第一阶段只需要证明一件事：

```text
客户愿意为“多 Agent + 真人协作的可追踪、可验收、可审计交付”付费。
```

只要这个成立，再向组织事实源、Agent 组织目录、权限中心、HIL、MCP Gateway、行业模板、企业治理、私有工具接入和大规模任务运行扩展，才有商业意义。
