# Orrery 完整形态架构草图

> 2026-10-18：本文保留历史完整形态设计和提示词，不能作为当前 WEB/API 首发范围或线上能力验收依据。当前候选的代码装配与可编辑图见 [WEB/API 源码架构](web-api-architecture.md)。6 张缺少完整来源分发证据的历史 PNG 已移出源码，原图与未解决记录保留于私有审计及根目录 `compliance/web-asset-rights.json` 的 `retiredAssets`。

本文描述 Orrery 的完整产品形态，并为技术架构图和功能架构图提供可直接用于生图模型的提示词。

整体架构不是四层等权堆叠，而是“一个平台核心、两个共享能力面、一个按需仿真服务”：

```text
Platform    企业业务运行核心，持有事实、资源、任务和真实执行
Ontology    Platform 之上的共享语义、数字孪生与行动治理面
Discussion  面向用户、真人和 Agent 的统一交互协作面
IRIS        按需调用的企业仿真与决策验证服务
```

Platform、Ontology 和 Discussion 构成平台主运行链路；IRIS 只在需要预测和方案验证时接入：

```text
主运行链：Discussion -> Platform 业务操作 -> DomainEvent -> Ontology -> 企业上下文 -> Discussion
治理链路：Discussion 已确认决策 -> Ontology Action Contract -> Platform 受控执行
仿真支路：Discussion 仿真请求 -> IRIS <- Ontology 数字孪生快照
仿真返回：IRIS 预测与方案对比 -> Discussion
校准支路：Platform 真实结果 -> Ontology 状态刷新 -> IRIS 模型校准
```

## 1. 总体定位

Orrery 不是单纯的聊天工具、Agent 平台或工作流系统，而是一套面向真人与数字员工的企业智能协作操作系统。

四个架构域分别回答四个问题：

| 架构域 | 定位 | 核心产物 |
| --- | --- | --- |
| Discussion | 人、Agent 和组织如何围绕目标进行协作？ | 对话、决策、任务、审批、交付和反馈 |
| IRIS | 在需要决策验证时，不同选择可能产生什么结果？ | 仿真场景、预测结果、方案对比、风险、敏感性和置信度 |
| Ontology | 企业里有哪些对象、关系、事实、证据和允许动作？ | Schema、Context Graph、Lineage、Proposal、Action Contract |
| Platform | 企业如何持续运行，事实写在哪里，真实结果如何产生？ | 组织、人员、Agent、Mission、资源、知识、执行记录和审计 |

## 2. 技术架构

### 2.1 技术架构原则

1. Platform 是系统核心，是业务事实、数字劳动力、应用能力和真实副作用的所有者。
2. Ontology 是 Platform 之上的共享语义、数字孪生与行动治理面，为全部上层能力提供统一企业上下文。
3. Discussion 是用户、真人成员和 Agent 的统一交互协作面，也是日常工作进入 Platform 的主要入口。
4. IRIS 是按需调用的专业仿真服务，读取受治理的数字孪生快照，只负责预测和决策验证；它不处于平台主链路，也不负责任务或行动编排。
5. 所有行动通过 Action Contract、权限、风险策略、HIL 和审计后进入 Platform。
6. Platform 写入产生 DomainEvent，并持续更新 Context Graph、Lineage、数字孪生状态和 IRIS 模型校准数据。
7. 即使 IRIS 不可用，Discussion、Ontology 和 Platform 的正常业务运行仍然成立。
8. 租户隔离、身份授权、数据策略、审计和可观测性贯穿全部架构域。

### 2.2 技术架构图生图提示词

[当前 WEB/API 技术架构与源码对应](web-api-architecture.md#技术装配)

下面的提示词面向支持复杂信息图的生图模型。图中文字必须使用提示词中给出的中文和英文术语，不要自行改写。

```text
Use case: infographic-diagram
Asset type: Orrery 中文企业技术架构图，用于高层汇报和技术评审

生成 16:9 横版中文技术架构图，标题“Orrery 技术架构”。白色和浅雾灰背景，高级、明亮、通透、有科技感。

非对称布局：中央最大区域是“Platform｜企业业务运行核心”，含“工作与交付 / 组织与人员 / 数字员工 / 工具与运行时 / 知识与记忆 / 自动化与运营”；上方是横向“Ontology｜企业语义与数字孪生”，含“本体注册表 / 上下文图谱 / 事实与证据 / 规则与约束 / 动作契约”；顶部是“Discussion｜统一交互与协作”，含“多端会话 / 人机协作 / 任务与交付 / 决策与反馈”；右侧放置较小的独立模块“IRIS｜按需仿真与决策验证”，含“场景构建 / 多方案推演 / 风险压测 / 结果预测 / 模型校准”，明确标注“按需调用 / 非主链路”。

粗实线主链：“Discussion 业务操作 -> Platform 业务运行 -> 领域事件 -> Ontology 状态刷新 -> 企业上下文 -> Discussion”。细虚线仿真支路：“Discussion 仿真请求 -> IRIS；Ontology 数字孪生快照 -> IRIS；IRIS 预测与方案对比 -> Discussion”。IRIS 不连接 Platform 执行入口。

左侧写“平台治理｜租户隔离 / 身份授权 / 数据策略 / HIL / 审计 / 可观测性”。使用深炭文字、钴蓝 Platform、暖金 Ontology、青色 Discussion、翡翠绿 IRIS，极淡网格和克制数据轨迹，中文水平清晰。

禁止黑色背景、把 IRIS 放在中央或画成系统核心、IRIS 编排或执行、四条等高横条、卡片墙、Excel 感、廉价霓虹、3D 插画、交叉连线、小字、乱码、水印。
```

### 2.3 技术域说明

| 架构域 | 技术职责 | 主要模块边界 |
| --- | --- | --- |
| Platform | 持有业务状态、数字劳动力、资源、知识、权限和真实执行能力 | Mission、Organization、Agent、Resource、Knowledge、Memory、Operations |
| Ontology | 定义企业语义、投影业务事实、构建数字孪生上下文并治理行动 | Registry、Context Graph、Reasoning、Governance、Action Contract |
| Discussion | 管理会话、消息、成员、turn/run、Agent 交互、工具结果、审批和实时状态 | `discussionsession`、HTTP、Realtime、Discussion UI |
| IRIS | 作为独立专业服务，按需消费数字孪生快照并执行预测、反事实、压力和敏感性仿真 | Simulation Engine、Scenario、Twin Snapshot、Forecast、Comparison、Calibration |
| Infrastructure | 提供持久化、事务、事件、队列、文件、身份、实时和可观测性 | PostgreSQL、EventStore、Outbox、Centrifuge、FileStore、Kratos、OpenFGA |

### 2.4 技术主链路

| 链路 | 完整流程 |
| --- | --- |
| 业务主链 | Discussion command -> Platform application service -> business state + execution result -> Realtime -> Discussion |
| 事实语义链 | Platform write -> DomainEvent -> MappingRule -> Conformance Gate -> Context Graph + Lineage -> enterprise context |
| 决策链路 | human decision -> Ontology Action Contract -> permission/risk/HIL -> Platform writeback |
| 按需仿真链 | Discussion simulation request + Ontology Twin Snapshot -> IRIS simulation -> forecast + comparison + risk + confidence -> Discussion |
| 校准支路 | Platform actual result -> Context Graph refresh -> actual vs forecast evaluation -> IRIS model calibration |

## 3. 功能架构

### 3.1 功能架构视角

功能架构不按菜单或四个等权层级堆叠，而按“平台核心 + 共享能力 + 按需服务”组织：

```text
Platform：核心业务运行、数字劳动力和真实执行
Ontology：共享企业语义、数字孪生和行动治理
Discussion：统一交互、协作、决策与交付入口
IRIS：按需提供仿真、预测和决策验证
```

### 3.2 功能架构图生图提示词

[当前 WEB/API 功能入口与源码对应](web-api-architecture.md#功能入口)

```text
Use case: infographic-diagram
Asset type: Orrery 中文企业功能架构图

生成 16:9 横版中文企业能力地图，标题“Orrery 功能架构”。不要画成四个等权层级，Platform 必须是最大能力核心，IRIS 必须是右侧较小的按需专业服务。

构图：
- 中央和下半部最大区域：“Platform｜企业业务运行核心”，含“工作与交付”“组织与人员”“数字员工”“能力资源”“知识系统”“记忆系统”“自动化执行”“平台治理”。
- Platform 上方横向共享能力面：“Ontology｜企业语义与数字孪生”，含“范围管理”“本体注册表”“上下文图谱”“事实证据与血缘”“规则与约束”“动作契约”。
- 顶部横向入口：“Discussion｜统一协作与交付入口”，含“多端会话”“团队协作”“任务沉淀”“决策确认”“交付验收”“实时反馈”。
- 右侧独立小区域：“IRIS｜按需仿真与决策验证”，含“场景构建”“多方案推演”“风险压测”“敏感性分析”“结果预测”“反馈校准”，明确标注“按需调用”。
- 左侧展示“业务用户 / 管理者 / 租户管理员 / 真人与 Agent 团队”；右下展示“可控执行 / 可验证交付 / 组织知识沉淀”。

主流程用粗实线：Discussion -> Platform -> Ontology -> Discussion。治理流程：Discussion 已确认决策 -> Ontology 动作契约 -> Platform 受控执行。仿真流程只用细虚线：Discussion 仿真请求 -> IRIS；Ontology 数字孪生快照 -> IRIS；IRIS 预测与方案对比 -> Discussion。IRIS 不直接执行，也不连接 Platform 执行入口。

视觉使用白色和浅雾灰背景、深炭文字、钴蓝 Platform、暖金 Ontology、青色 Discussion、翡翠绿 IRIS。高级、明亮、通透、有适度科技感；严格网格、大留白、细线连接，所有中文水平清晰。

禁止：把 IRIS 放在中心、把 IRIS 画成系统核心、四条等高横条、黑色背景、卡片墙、平均分栏、Excel 感、放射思维导图、3D 插画、廉价霓虹、交叉连线、小字、乱码、水印、宣传口号。
```

## 4. 四大架构域功能明细

### 4.1 Platform 核心

| 功能域 | 完整能力 | 核心产物 |
| --- | --- | --- |
| 工作中心 | Todo、Idea、Project、Mission、Task、进度、依赖和执行波次 | Mission Brief、TaskContract、Schedule |
| 交付中心 | AgentResult、Deliverable、Evidence、QA、Approval、Evaluation | Deliverable、Evidence、QAResult、Review Packet |
| 组织管理 | 组织、部门、岗位、汇报关系、角色和任职 | Organization Graph、Reporting Line |
| 数字员工 | Agent Profile、Agent Instance、真人成员、混编 Squad 和 Staffing | Workforce Directory、Mission Team |
| 能力资源 | Tool、MCP Server、Skill、Runtime Profile 和 Agent Binding | Resource Catalog、Capability Grant |
| 知识系统 | 知识库、文档、页面、分块、锚点、PageIndex 和检索 | Knowledge Context、Citation |
| 记忆系统 | 个人记忆、组织记忆、候选记忆、日记和提炼 | Memory Record、Digest、Lineage |
| 身份治理 | Identity、Role、Permission、ReBAC、Approval 和 Audit | Principal、Policy Decision、Audit Trail |
| 自动化执行 | Scheduler、Worker、Queue、Runtime、Gateway 和 Realtime | Run、Job、Tool Result、Realtime Event |
| 平台运营 | Usage、Health、Metrics、Tracing、Migration 和 Settings | Usage Ledger、Operational Signal |

### 4.2 Ontology 共享能力面

| 功能域 | 完整能力 | 核心产物 |
| --- | --- | --- |
| Scope | 按组织、项目、Mission、资源和系统范围隔离企业上下文 | ScopeBinding、Access Context |
| Schema Registry | 定义对象、关系、属性、映射和动作类型 | ObjectType、LinkType、PropertyType、MappingRule、ActionType |
| Event Projection | 把 Platform DomainEvent 转换为语义图事实 | ProjectionResult、ProjectionAudit |
| Context Graph | 保存企业对象、关系、事实、证据和当前状态 | Object、Link、Fact、Evidence |
| Lineage | 跟踪事实来源、断言历史、引用和投影过程 | Provenance、Assertion、Projection Trace |
| Reasoning | 生成实体、关系、事实、元数据和动作候选 | ReasoningRun、Candidate、ValidationResult |
| Governance | 审查和发布 Schema、Mapping、Metadata 变更 | Proposal、Plan、Receipt |
| Action Contract | 统一动作目标、权限、风险、HIL、执行和回写 | ActionRun、WritebackReceipt |
| Workbench | 提供 Scope、Schema、Context、Lineage、Governance 和 Operations 视图 | Ontology Workspace |

### 4.3 IRIS 按需仿真服务

| 功能域 | 完整能力 | 核心产物 |
| --- | --- | --- |
| 场景构建 | 把业务目标转换为基准情景、候选情景、假设、变量和时间范围 | Simulation Scenario、Assumption Set |
| 状态镜像 | 从 Ontology 获取受权限约束的企业数字孪生快照和证据 | Twin Snapshot、Evidence Bundle |
| 模型管理 | 选择仿真模型，管理参数、版本、适用范围和置信边界 | Model Profile、Parameter Set |
| 多方案推演 | 并行运行基准、候选和反事实情景，保持输入条件可比较 | Simulation Run、Scenario Result |
| 风险压测 | 注入极端条件，识别边界、脆弱点、连锁影响和恢复能力 | Stress Test、Risk Surface |
| 敏感性分析 | 识别决定结果的关键变量、阈值和影响弹性 | Sensitivity Map、Critical Driver |
| 结果预测 | 输出未来状态、指标区间、方案差异和置信度 | Forecast、Comparison、Confidence |
| 可解释仿真 | 展示因果路径、证据、假设、局限和不确定性来源 | Simulation Explanation、Decision Brief |
| 反馈校准 | 对比真实结果与预测，评估模型并更新参数 | Forecast Error、Calibration Record |
| 决策支持 | 向 Discussion 提供可追问、可复现、可审计的仿真结论 | Decision Package、Simulation Evidence |

### 4.4 Discussion 交互协作面

| 功能域 | 完整能力 | 核心产物 |
| --- | --- | --- |
| 会话入口 | 私聊、群聊、Mission 会话、历史会话和多渠道接入 | Conversation |
| 团队协作 | 成员、mention、Agent 回复、Inbox、未读和状态同步 | Message、Mention、Inbox Item |
| 工作上下文 | 附件、Workspace、Knowledge、Memory 和引用 | Discussion Context |
| Agent 协作 | turn/run、direct/group/mention target、流式响应和 Tool Call | Turn、Run、Tool Call |
| 任务沉淀 | 从对话创建 Mission、Task、Assignment、Dependency 和 Schedule | Mission、TaskContract |
| 人机控制 | 澄清、修改、Approval、HIL、继续、重试和终止 | Decision、Approval Event |
| 交付验收 | 展示成果、证据、QA、风险、Review Packet 和执行时间线 | Delivery View、Review Packet |
| 实时反馈 | 同步消息、run、tool、approval 和执行结果 | Realtime Event |
| 全渠道协作 | Web、Desktop 和企业消息渠道统一进入 Discussion | Channel Binding、Delivery Receipt |

## 5. 平台主链与按需仿真支路

| 阶段 | Platform 核心 | Ontology 共享能力面 | Discussion 交互面 | IRIS 按需服务 |
| --- | --- | --- | --- | --- |
| 日常业务进入 | 接收任务、资源和业务操作并维护真实状态 | 提供统一 Schema、Scope 和企业上下文 | 接收用户目标、附件、协作和审批 | 不参与 |
| 日常业务执行 | 调度数字员工、工具和运行时，产生交付和执行记录 | 通过动作契约治理受控写入 | 展示进度、阻塞、证据和实时结果 | 不参与 |
| 事实沉淀 | 持久化业务结果并发布 DomainEvent | 投影 Context Graph、Fact、Evidence 和 Lineage | 展示更新后的企业上下文 | 不参与 |
| 发起仿真 | 提供当前业务事实和历史结果 | 生成受权限约束的 Twin Snapshot、规则和证据 | 提交目标、基线、假设、变量和时间范围 | 接收按需仿真请求 |
| 情景推演 | 不参与仿真计算 | 保证语义、事实、规则和指标口径一致 | 展示仿真进度并允许调整假设 | 运行基准、候选、反事实和压力情景 |
| 方案比较 | 提供现实能力边界 | 提供可追溯仿真血缘 | 展示预测、差异、风险、敏感性和置信度 | 返回可解释、可复现的仿真结论 |
| 人工决策 | 准备真实执行资源 | 将已确认决策转换为受治理的动作契约 | 用户选择、拒绝或修改方案 | 保存仿真报告，不直接发起执行 |
| 结果校准 | 产生真实结果 | 更新数字孪生状态和校准证据 | 展示真实结果与预测偏差 | 对比预测与真实结果并校准模型 |

## 6. 出图规范

两张正式图保持统一视觉语言：

- 比例：`16:9` 横版。
- 输出目标：适合 `3840 x 2160` 的演示文稿或架构文档。
- 背景：两张图均采用白色、浅雾灰或极浅冷灰背景，不使用黑色大背景。
- 排版：中文无衬线字体，所有标签水平排列。
- 视觉权重：Platform 最大且居中；Ontology 为共享语义平面；Discussion 为顶部交互面；IRIS 位于侧边并明确标注按需调用。
- 色彩：每层一个稳定颜色，颜色只用于分层，不给每个模块随机上色。
- 连线：主链路使用实线箭头，返回链路使用单独的闭环箭头。
- 质感：允许克制的数据轨迹、浅色玻璃质感和轻微空间纵深，但禁止黑色赛博风、廉价霓虹、巨大光晕、3D 城市和等距插画。
- 禁止：卡片墙、平均分栏、交叉连线和小字堆叠。
- 文字：只使用提示词指定的标题和标签，不增加宣传口号。

## 7. 架构依据

- [生产服务装配](../cmd/server/main.go)
- [Ontology 默认 Schema](../internal/app/ontology/defaults.go)
- [Context Graph](../internal/app/contextgraph/service.go)
- [Ontology Reasoning](../internal/app/ontologyreasoning/service.go)
- [Action Contract](../internal/app/actioncontract/service.go)
- [IRIS Adapter](../internal/app/irisadapter/adapter.go)
- [IRIS Context Consumer](../internal/app/irisadapter/mcp_context_consumer.go)
- [Discussion Service](../internal/app/discussionsession/service.go)
- [Discussion Turn Executor](../internal/app/discussionsession/service_turn_executor.go)
- [Discussion Mission Tools](../internal/app/discussionsession/mission_tools.go)
- [前端功能入口](../web/src/lib/routes/navigation.ts)
- [Ontology Workbench](../web/src/routes/resources/ontology/+layout.svelte)
