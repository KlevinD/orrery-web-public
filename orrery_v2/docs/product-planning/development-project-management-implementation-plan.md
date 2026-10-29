# 开发项目管理与 Todo Agent 执行闭环：详细开发方案

> 状态：待实施
> 日期：2026-07-24
> 范围：`orrery_v2/`

## 1. 结论

Orrery 需要新增一个一级页面“开发项目”，但不能把所有 Todo 都强制放进项目，也不能直接把现有通用 `Project` 扩成代码仓库管理器。

最终模型如下：

```text
通用项目（可选的业务归类） ──默认关联──> 开发项目
独立开发 Todo              ──直接关联──> 开发项目
日常/综合 Todo              ──无需关联──> 任何项目或代码仓库

开发项目 ──绑定──> 一个或多个代码仓库
开发 Todo ──选择──> 一个开发项目中的仓库范围
开发 Todo ──唯一复用──> 一个群聊
Todo ──审批并开始执行──> Mission（一次执行尝试）
Mission ──拆分──> MissionTask（可监控的实际工作）
Mission ──复用 Todo 群聊──> 讨论、实施与审批上下文
开发 Todo 的 Mission ──使用──> 独立 Git 分支与 worktree
Todo / 开发项目 ──查看摘要──> Mission Control
```

核心决策：

1. UI 名称使用“开发项目”，后端实体使用 `DevelopmentProject`，与现有通用 `Project` 分开。
2. Todo 默认不自动处理。只有明确选择自动化模式的 Todo 才会被系统定时任务扫描。
3. 保留现有 Scheduler 定时任务 `todo-agent-automation-periodic-scan`，仍按 `*/5 * * * *`、`Asia/Shanghai` 执行；它不是 Sweeper。
4. 同一个 Todo 从方案讨论、审批、实际执行到结果复核始终复用同一个群聊。
5. 代码执行时不复制整套仓库，也不使用 Orrery 服务自身的活动工作目录；每个开发 Todo 使用独立 Git 分支和 worktree。
6. 页面权限采用“粗粒度权限 Scope + 对象关系权限 OpenFGA + 运行时约束”的交集，Scheduler 和 Agent 均不继承 superadmin 权限。
7. 代码仓库连接和凭据单独管理。开发项目 Owner 可以选择被授权的连接，但不能读取或管理连接中的密钥。
8. Agent 可在批准范围内创建候选 commit，但 push/PR 和 merge 分级授权；merge 必须绑定确切 SHA、最新基线和独立人工审批。
9. 不新建第二套执行进度系统。Todo 获批执行后创建或复用一个 Mission，所有可执行工作落成 MissionTask；现有 Mission Control 是单个 Todo 执行的详细控制台，开发项目页只做跨 Todo 聚合监控。

## 2. 当前基线与必须先修的问题

已有能力可以复用：

- `cmd/server/default_todo_automation_schedule.go` 已注册系统定时任务。
- `internal/app/scheduler/dispatcher.go` 已通过 `RunAutomationScan` 调用 Todo 自动化服务。
- `internal/app/todo/automation_service.go` 已有讨论、方案、审批、执行状态和群聊复用逻辑。
- Todo 页面和详情页已经能展示自动化状态并进入关联群聊。
- 群聊已有成员、消息、运行时审批和工作目录能力。
- `/missions/[missionId]` 已有 Overview、Team、Tasks、Evidence 和 Timeline；Mission 投影已有 TaskBoard、ProgressGraph、QA、Blockers、交付率和待审批统计，并通过 Mission stream 实时刷新。

但不能直接在这些能力上接一个页面，以下问题必须纳入首批开发：

| 问题 | 当前情况 | 必须采取的措施 |
|---|---|---|
| 通用 Project 租户边界 | `projectadmin` 仓储按 `ProjectID` 取记录，服务没有完整对象级过滤 | 所有读写改为 `TenantID + ProjectID`；列表先做对象授权过滤 |
| Project 权限 | 当前 `project.view/edit` 复用了 `mission.read/write` | 开发项目使用独立 Scope 和 OpenFGA 对象类型 |
| Todo 扫描范围 | 所有处于活动状态且存在人工审批人的 Todo 都可能进入扫描 | 新增 `automation_mode`，默认 `manual`，候选查询只返回明确开启的 Todo |
| 空扫描记录 | 每五分钟会产生 Scheduler run | 空扫描不得创建群聊；为 Scheduler run 增加保留和聚合策略 |
| 群聊绑定 | `ConversationID` 主要依附当前自动化状态 | 增加稳定的 `TodoDiscussionBinding`，保证状态重建后仍复用原群聊 |
| Mission 创建 | 现有 `CreateMission` 会新建 DiscussionGroup，并让 Mission Viewer 继承群聊读取权 | 增加 Todo 专用创建入口，绑定既有 Todo 群聊；Mission 读取权和群聊读取权分开判定 |
| Mission 租户边界 | Mission ReadModel 主要按 `MissionID` 读取，现有创建/关系投影仍有空 TenantID 路径 | Todo-origin Mission 必须持久化 TenantID；仓储键、列表、详情、stream 和 OpenFGA object 都按租户校验 |
| 两套讨论标识 | Mission 有内部 `DiscussionGroupID`，生产接线又用 `ensureMissionDiscussionSession` 创建/关联 `ConversationID` | Todo-origin Mission 以已有 `ConversationID` 为唯一可见会话，只保存 Mission link，禁止 ensure 流程再创建 Conversation 或并行 DiscussionGroup |
| 执行进度 | Todo 自动化状态只能描述粗粒度阶段，Agent 子任务可能只留在聊天或 runtime trace | Todo 开始执行时绑定 Mission；每项实际工作必须创建 MissionTask，MissionEvent/Projection 成为执行进度事实源 |
| 工作区 | 群聊使用低层级绝对路径 `workspace_root_dir` | 开发流程只传 `workspace_id`，由服务端解析真实路径 |
| 仓库管理 | `resourceadmin` 只管理 Tool、Skill、MCP、Memory 等资源 | 新建 SCM Connection 与开发项目模块，不把仓库塞入 `resourceadmin` |
| 方案审批 | 当前方案主要是自由文本 | 生成结构化、带摘要哈希的执行计划；审批绑定到确切版本和 commit |

## 3. 产品边界与术语

### 3.1 通用项目

通用 `Project` 继续表达业务目标、客户项目或工作归类。它可以没有代码，也可以给 Todo 提供默认开发项目，但不能自动授予代码访问权。

### 3.2 开发项目

`DevelopmentProject` 是代码执行边界，负责：

- 可用代码仓库及默认分支。
- 源码子目录、构建命令、测试命令。
- 默认 Todo 自动化模式。
- 可参与人员、Agent 和权限。
- 并发限制、网络策略、命令策略、审批策略。
- Todo 分支、worktree、执行记录、diff、测试结果和清理状态。

### 3.3 开发 Todo

Todo 新增 `work_kind`：

- `general`：默认值，日常或综合工作，不需要开发项目。
- `development`：开发工作；讨论方案可以暂时不绑定开发项目，但任何代码执行前必须完成绑定。

Todo 再新增 `automation_mode`：

- `manual`：默认值，不参与定时扫描，不自动创建群聊。
- `suggest`：Agent 定时判断是否可推进、讨论并附加方案；用户明确点击“开始执行”后才进入执行审批。
- `managed`：Agent 定时讨论方案并直接请求执行审批；审批通过后在原群聊中实施。

现有 Todo 状态与自动化状态必须保持分离。例如 Todo 可以是 `in_progress`，自动化同时处于 `pending_approval`。

`work_kind` 与 `automation_mode` 正交。综合或日常 Todo 也可以选择 `suggest/managed` 并在执行时进入 Mission Control，只是不会创建 Git workspace；只有 `development` Todo 才要求开发项目和仓库绑定。

### 3.4 Todo、Mission 与 MissionTask

职责必须固定为：

```text
DevelopmentProject = 长期代码资源、仓库、成员权限和并发策略
Todo               = 用户需求、方案讨论、审批与最终业务状态
Mission            = Todo 某一次获批执行的容器
MissionTask        = 该次执行中可分配、可依赖、可验收的工作单元
Mission Control    = 单次执行的详细进度、证据、QA 和运行控制台
```

Todo 不是 MissionTask 的别名，开发项目也不是 Mission。方案获批前的评估和讨论仍属于 Todo；真正开始执行时才创建 Mission。这样普通手工 Todo 不会凭空出现在 Mission 列表中，也不会为了显示进度复制一套任务引擎。

## 4. 用户流程

### 4.1 当前 Orrery 仓库的管理员接入

1. 具备连接管理权限的管理员注册一个“受管本地 Git 仓库”连接。
2. 创建开发项目“Orrery”，设置源码子目录为 `orrery_v2`，配置默认分支、构建和测试命令。
3. 系统检查仓库是否为有效 Git 仓库、默认分支是否存在、基线 commit 是否可读取。
4. 管理员添加 Viewer、Contributor、Maintainer 和可执行 Agent。
5. 创建开发 Todo，选择 Orrery 开发项目和 `suggest` 或 `managed`。
6. 定时任务发现 Todo 后，在唯一群聊中讨论并生成执行计划。
7. Reviewer 或有审批权的 Maintainer 审批计划。
8. 系统创建 Todo 的执行 Mission，把计划步骤物化为 MissionTask，并继续复用原 Todo 群聊。
9. 系统从已提交的基线 commit 创建 Todo 分支和 worktree，Agent 在其中开发和测试。
10. 用户可从 Todo 打开 Mission Control，查看任务、负责人、依赖、Evidence、QA、阻塞和时间线。
11. diff、测试结果、commit 和风险回写原群聊、Mission 投影与 Todo 进度摘要。
12. 有提交权限的人把候选 branch 推送并创建 Pull Request。
13. Reviewer 请求修改或生成 merge preview；有合并权限的人批准确切 Submission 后执行合并。
14. 合并结果核验成功后更新 Todo，并在保留期结束时清理 worktree。

活动工作树中的未提交修改不属于自动化基线。以当前仓库为例，Agent 只能看到 `HEAD` 或远端已提交 commit，不能悄悄读取 `D:\Python\projects\IdeaDo` 中尚未提交的修改。页面必须明确显示基线 commit，并在必要时提示“当前活动工作树有未提交修改，自动化不会包含这些内容”。

### 4.2 普通用户自助接入仓库

自助接入优先使用 GitHub App、GitLab OAuth/Application 或部署密钥，不建议把 PAT 直接存入业务表。

1. 用户在单独的“代码仓库连接”设置页授权 Git Provider。
2. Token 或私钥进入 Secret Provider；数据库只保存 `secret_ref` 和非敏感元数据。
3. 用户创建开发项目时，只能选择自己有 `connection.use` 权限的连接和仓库。
4. 创建后用户成为开发项目 Owner，但不会自动成为 Credential Manager。
5. 连接被撤销、过期或越权时，开发项目仍可读历史记录，但新的拉取和执行被阻止。

### 4.3 普通日常 Todo

默认的普通 Todo 保持 `work_kind=general`、`automation_mode=manual`。无论系统定时任务运行多少次，都不创建群聊、不生成方案、不创建 Mission、不分配代码工作区。用户显式把综合任务改为 `suggest/managed` 后，可以使用 Mission/MissionTask 监控执行，但仍不需要开发项目或代码仓库。

## 5. 权限设计

### 5.1 总原则

任何操作必须同时满足：

```text
租户一致
AND 用户具备对应 Scope
AND 用户对具体 Todo 具备所需对象关系
AND (Todo 为 general
     OR (用户对具体开发项目具备对象关系
         AND 开发项目对仓库连接具备 use 关系))
AND Todo/方案审批仍然有效
AND (Todo 为 general 时 Agent 被明确分配到 MissionTask
     OR 开发 Todo 的 Agent 被绑定为该项目的 executor)
AND Runtime sandbox 允许本次文件、命令、网络和 Secret 操作
AND 系统策略未禁止该操作
```

前端隐藏按钮只用于体验，不能作为授权依据。列表、详情、变更、执行和 Scheduler 路径都必须在服务端检查。

### 5.2 粗粒度 Scope

新增以下 Scope：

| Scope | 用途 |
|---|---|
| `development.read` | 进入开发项目页面，查看获授权的项目和脱敏执行摘要；不自动授予详细 Evidence、diff 或群聊读取权 |
| `development.monitor` | 打开项目内 Todo 的详细 Mission Control；不包含 Todo 群聊读取权或执行控制权 |
| `development.create` | 在有管理权的组织内创建开发项目 |
| `development.write` | 创建或绑定开发 Todo、更新非敏感项目内容 |
| `development.execute` | 请求 Agent 执行已批准范围内的工作 |
| `development.submit` | 把候选 commit 推送到 Todo 分支并创建或更新 Pull Request |
| `development.approve` | 审批开发执行计划 |
| `development.manage` | 配置项目、仓库绑定、成员、Agent 和执行策略 |
| `development.merge` | 在独立人工审批后合并确切 Submission 到目标分支 |
| `connection.read` | 查看自己可使用的代码仓库连接元数据 |
| `connection.use` | 把一个已有连接绑定到开发项目并用于 fetch/push |
| `connection.manage` | 创建、更新、轮换、撤销连接；不提供密钥明文读取能力 |

不要继续用 `mission.read/write` 代表 Project 或开发项目权限。

### 5.3 开发项目角色

| 能力 | Viewer | Contributor | Maintainer | Owner |
|---|---:|---:|---:|---:|
| 查看项目和脱敏执行摘要 | 是 | 是 | 是 | 是 |
| 查看项目内 Todo 摘要 | 是 | 是 | 是 | 是 |
| 打开非本人 Todo 的 Mission Control | 否 | 否 | 是 | 是 |
| 创建/绑定开发 Todo | 否 | 是 | 是 | 是 |
| 请求方案讨论或执行 | 否 | 是 | 是 | 是 |
| 提交候选 commit、推送 Todo 分支、创建 PR | 否 | 是 | 是 | 是 |
| 配置构建、测试、运行策略 | 否 | 否 | 是 | 是 |
| 审批执行方案 | 否 | 否 | 是 | 是 |
| 管理成员和归档项目 | 否 | 否 | 否 | 是 |
| 合并代码 | 否 | 否 | 需额外 `merger` 授权 | 是 |
| 管理/读取仓库凭据 | 否 | 否 | 否 | 否 |

Credential Manager 是组织或平台层面的独立角色。开发项目 Owner 只能选择已经授权给项目使用的连接，不能看到 token、私钥或 Secret Provider 的值。

SaaS 默认禁止执行请求人审批自己的写操作。单用户本地部署可以由平台管理员显式开启 `allow_owner_self_approval`，该设置必须进入审计日志。

### 5.4 OpenFGA 对象模型

在现有授权模型中新增 `development_project`、`scm_connection` 和 `todo`。建议关系如下：

```text
type development_project
  org:         organization
  owner:       user
  maintainer:  user
  contributor: user | agent
  viewer:      user | agent
  executor:    agent
  merger:      user

  reader  = viewer | contributor | maintainer | owner | org.admin
  monitor = maintainer | owner | org.admin
  writer  = contributor | maintainer | owner | org.admin
  runner  = contributor | maintainer | owner | executor | org.admin
  submitter = contributor | maintainer | owner | executor | org.admin
  approver = maintainer | owner | org.admin
  manager = owner | org.admin
  merge_authorizer = merger | owner | org.admin

type scm_connection
  org:     organization
  owner:   user
  user:    user | agent | development_project
  manager: user

type todo
  proposer:            user | agent
  assignee:            user | agent
  reviewer:            user
  collaborator:        user | agent
  development_project: development_project

  reader: proposer | assignee | reviewer | collaborator

type mission
  origin_todo:                todo
  origin_development_project: development_project
  owner:                      user | agent  # 仅 standalone Mission 直接写入
  participant:                user | agent

  viewer = owner | participant | reader from origin_todo
  development_monitor = monitor from origin_development_project
  editor = owner | assignee from origin_todo
  qa_approver = owner | reviewer from origin_todo

type discussion
  mission: mission       # 仅 standalone Mission discussion 写入
  todo:    todo
  owner:   user | agent
  member:  user | agent

  viewer = owner | member | viewer from mission
  poster = owner | member | editor from mission
```

Todo-origin Discussion 绝不写 `discussion.mission` tuple，因此它的 `viewer/poster` 实际只根据显式 `owner/member` 计算，不从 Mission 或开发项目继承；现有 standalone Mission 仍可保留该 relation。Todo 提出人、负责人、Reviewer 和实际参与 Agent 由关系同步器写成显式成员。因此开发项目 Viewer 能看到项目和允许公开的 Todo/运行摘要，Maintainer/Owner 能打开详细 Mission Control，但二者都不会自动看到 Todo 群聊。

对象 ID 必须包含租户边界，例如 `development_project:<tenant_id>/<development_project_id>`，不能只把业务 ID 发给 OpenFGA。

### 5.5 API 与页面权限映射

| 操作 | Scope | 对象关系 |
|---|---|---|
| 列出/查看开发项目 | `development.read` | `reader` |
| 查看项目级执行摘要 | `development.read` | `reader` |
| Todo 参与者打开其 Mission Control | `mission.read` | Mission `viewer` 通过 `origin_todo.reader` |
| 打开项目内非本人 Todo 的 Mission Control | `development.monitor` | `monitor`，Discussion 子视图仍单独校验 `discussion.read` |
| 创建开发项目 | `development.create` | 目标 organization 的 `viewer/admin` |
| 更新普通字段 | `development.write` | `writer` |
| 管理成员、Agent、策略、归档 | `development.manage` | `manager` |
| 绑定仓库 | `development.manage` + `connection.use` | 项目 `manager` + 连接 `user` |
| 请求执行 | `development.execute` | 项目 `runner`，同时可访问 Todo |
| 推送候选分支、创建/更新 PR | `development.submit` | 项目 `submitter`，同时受已批准计划约束 |
| 审批执行 | `development.approve` | 项目 `approver`，并满足 Todo Reviewer 策略 |
| 合并代码 | `development.merge` | 项目 `merge_authorizer` |
| 创建/轮换连接 | `connection.manage` | 连接 `manager` 或组织 `admin` |

未授权用户猜测对象 ID 时返回 `404`，避免泄露对象是否存在。服务依赖或 OpenFGA 不可用时 fail closed。

Todo-origin Mission 的 read authorizer 接受上表两条路径中的任意一条，但只能用于该 Mission 的只读控制台。`development.monitor` 不能被折算成全局 `mission.read`，也不能通过 Mission 列表看到其他项目；所有 mutation 仍使用原有细粒度 action。后端在 DTO 中返回 `can_open_control`、`can_open_discussion`、`can_create_task`、`can_assign_task`、`can_cancel_run`、`can_retry_run` 和 `can_decide_qa`，前端不根据角色名称推算。

## 6. 数据模型

所有仓储方法都必须接收 `tenantID`，存储键统一为 `<tenant_id>/<object_id>`，并保留 `Version` 做乐观并发控制。

### 6.1 `DevelopmentProject`

```text
TenantID
DevelopmentProjectID
OrganizationID
Name
Description
Status                    active | archived
DefaultAutomationMode     manual | suggest | managed
MaxConcurrentRuns
ExecutionPolicy           approval/network/command/secret/file policies
CreatedBy
CreatedAt
UpdatedAt
Version
```

### 6.2 `SCMConnection`

```text
TenantID
ConnectionID
OrganizationID
OwnerID
Scope                     personal | organization | platform
Provider                  github | gitlab | gitea | managed_local | generic_git
BaseURL
InstallationOrAccountID
SecretRef
AllowedRepositoryPatterns
Status                    active | expired | revoked | error
LastVerifiedAt
CreatedAt
UpdatedAt
Version
```

任何 Query DTO 都不得返回 `SecretRef` 的可解析值，更不能返回明文。普通项目成员最多看到 Provider、账号名称、仓库范围和健康状态。

### 6.3 `DevelopmentRepositoryBinding`

```text
TenantID
BindingID
DevelopmentProjectID
ConnectionID
RepositoryLocator         Provider 内稳定 ID；不要使用带 token 的 URL
DisplayName
Role                      primary | dependency
AccessMode                read_only | read_write
DefaultBranch
SourceSubdirectory
BuildCommands
TestCommands
BranchPrefix
Status
LastFetchedCommit
LastVerifiedAt
Version
```

模型允许一个开发项目绑定多个仓库。第一版执行器只需要支持“一个可写 primary 仓库 + 若干只读 dependency 仓库”，不要一开始实现跨多个可写仓库的原子提交。

### 6.4 Project 默认关联

新增 `ProjectDevelopmentBinding`：

```text
TenantID
ProjectID
DefaultDevelopmentProjectID
UpdatedBy
UpdatedAt
Version
```

它只负责给 Todo 表单提供默认值，不授予开发项目、仓库或 Secret 权限。

### 6.5 Todo 扩展

在 `TodoItem`、创建/更新请求、查询和持久化记录中增加：

```text
WorkKind                  general | development
DevelopmentProjectID     optional
AutomationMode           manual | suggest | managed
```

另建 `TodoRepositoryScope`，保存本 Todo 实际允许操作的 binding、访问模式和可选文件范围。不要把开发项目绑定的所有仓库自动挂载给 Todo。

### 6.6 群聊绑定

新增 `TodoDiscussionBinding`：

```text
TenantID
TodoID
ConversationID
CreatedAt
UpdatedAt
```

建立 `(TenantID, TodoID)` 唯一约束。创建群聊采用 get-or-create/CAS；并发触发只能成功创建一个，其余调用读取既有绑定。

`ConversationID` 指向 `internal/app/discussionsession` 的 canonical Conversation。Todo-origin Mission 不再创建另一个可见会话：Mission ReadModel 直接引用该 Conversation，并在现有 `LinkedMissionRecord` 中保存 `ConversationID -> MissionID`。内部 `mission/discussion` 若暂时不能移除，必须通过 adapter 读写 canonical Conversation；不得保存第二份消息正文或向用户暴露另一个 `DiscussionGroupID`。

自动成员规则：

- 提出人永久可见。
- 当前负责人和 Reviewer 自动可见。
- 实际参与该 Todo 的 Agent 可见。
- 负责人或 Reviewer 更换后，旧成员的自动授权应撤销；如果需要保留，必须转为显式成员并记录授权人。
- 开发项目 Owner、Maintainer、Viewer 不因项目角色自动进入群聊。

### 6.7 结构化执行计划

新增 `TodoExecutionPlan`：

```text
TenantID
PlanID
TodoID
TodoVersion
ProposalRevision
ConversationID
DevelopmentProjectID
RepositoryScopes
BaseCommits
AllowedFileScopes
PlannedCommands
NetworkPolicy
RequiredSecretRefs
RiskSummary
AcceptanceChecks
PlanSteps                 id/title/goal/dependencies/assignee capability/deliverables/evidence
PlanDigest
Status
CreatedAt
UpdatedAt
```

Agent 可以在群聊中讨论自然语言，但最终必须生成结构化计划。审批记录绑定 `PlanID + PlanDigest + TodoVersion + BaseCommits`。以下任何变化都使审批变为 `stale`：Todo 版本变化、计划变化、仓库绑定变化、文件/命令/Secret 范围扩大。

远端默认分支向前移动不应悄悄改变已经批准的基线。执行仍从批准的 commit 开始，合并前再检测冲突并请求 rebase/merge 决策。

### 6.8 Todo 与 Mission 绑定

新增 `TodoMissionBinding`，它记录执行尝试，不把 MissionID 直接覆盖在 Todo 的单一字段中：

```text
TenantID
BindingID
TodoID
MissionID
DevelopmentProjectID      optional，综合任务为空
PlanID
PlanRevision
PlanDigest
ExecutionAttempt          从 1 递增
ConversationID            必须等于 TodoDiscussionBinding.ConversationID
Status                    creating | active | terminal | superseded
CreatedBy
CreatedAt
UpdatedAt
Version
```

约束：

- `(TenantID, MissionID)` 唯一，一个 Mission 不能被两个 Todo 认领。
- `(TenantID, TodoID, ExecutionAttempt)` 唯一。
- 同一 Todo 同时最多一个 `creating/active` binding；用数据库唯一约束或串行化事务保证，不能只靠进程内锁。
- 创建命令的幂等键固定为 `todo:<todo_id>:plan:<plan_revision>:attempt:<n>`。
- 当前 Mission 执行失败后的 retry 继续使用同一 Mission，并创建新的 AgentRun/任务执行次数。
- Mission 已进入终态后 Todo 被重新打开，创建新的 `ExecutionAttempt` 和 Mission，但仍复用原 `ConversationID`；Todo 详情可查看历史 attempts。

新增只读的 `TodoExecutionProgressProjection`，供 Todo 摘要和开发项目聚合页查询：

```text
TenantID
TodoID
MissionID
ExecutionAttempt
MissionStatus
CurrentPhase
DeliveryRate
TotalTaskCount
ActiveTaskCount
DoneTaskCount
BlockedTaskCount
PendingApprovalCount
CurrentAgentIDs
LastActivityAt
CodeSubmissionStatus       optional
PullRequestStatus          optional
MergeStatus                optional
MissionProjectionVersion
SubmissionRevision
ProjectedAt
```

该记录不能由 Todo API 直接修改。MissionEvent/Projection 是任务、负责人、依赖、Evidence、QA、阻塞和交付率的事实源；`TodoCodeSubmission` 是 commit/PR/merge 状态的事实源。投影器只把两者汇总成摘要，Todo 自身不重新计算进度，也不通过双向同步修改 Mission 状态。

Mission 和 MissionTask 增加不可变来源引用：

```text
Mission.OriginKind         todo
Mission.OriginID           <todo_id>
Mission.ExecutionAttempt   <n>
Mission.TenantID           <todo tenant_id>
Mission.DiscussionMode     existing_conversation
MissionTask.SourceKind     todo_plan_step
MissionTask.SourceID       <plan_step_id>
```

任何 Agent 真正执行的计划步骤都必须先成为 MissionTask。聊天消息、runtime trace 或 Git commit 可以作为证据和时间线事件，但不能代替 MissionTask，否则 Todo 页面无法可靠回答“谁在做什么、完成多少、卡在哪里”。

### 6.9 Workspace 与运行记录

`DevelopmentWorkspace`：

```text
TenantID
WorkspaceID
DevelopmentProjectID
TodoID
RepositoryBindingID
BranchName
BaseCommit
HeadCommit
Status                    provisioning | ready | leased | review | merged | failed | archived
LeaseOwnerRunID
LeaseExpiresAt
CreatedAt
LastActiveAt
CleanupAfter
FailureReason
```

`TodoExecutionRun`：

```text
TenantID
ExecutionRunID
TodoID
PlanID
ApprovalID
MissionID
MissionTaskID
AgentRunID
WorkspaceID
AgentID
Status
StartedAt
FinishedAt
CommitBefore
CommitAfter
DiffSummary
TestResults
ArtifactRefs
FailureReason
```

### 6.10 Commit 与代码提交记录

Git commit 不能只作为群聊中的一段文字保存。新增 `TodoCommitRecord`，记录每个由 Orrery 生成或接管的 commit：

```text
TenantID
CommitRecordID
TodoID
ExecutionRunID
PlanID
RepositoryBindingID
CommitSHA
ParentSHAs
TreeSHA
AuthorKind                 agent | user
AuthorID
CommitterID                固定的 Orrery Git service identity
Message
SignatureStatus
ChangedFiles
Additions
Deletions
DiffDigest
CreatedAt
```

再新增 `TodoCodeSubmission`，表示一组准备评审和合并的不可变代码结果：

```text
TenantID
SubmissionID
Revision
TodoID
PlanID
PlanDigest
WorkspaceID
RepositoryBindingID
BranchName
BaseCommit
HeadCommit
TreeSHA
CommitSHAs
DiffDigest
Status                      local_candidate | push_pending | pushed | pr_open |
                            changes_requested | checks_running | merge_ready |
                            merge_approval_pending | merging | merged |
                            conflicted | merge_verification_failed | closed | failed
RemoteBranchRef
PullRequestProvider
PullRequestID
PullRequestURL
RequiredChecksSnapshot
MergeStrategy               squash | merge_commit | rebase
ExpectedBaseHead
MergePreviewTreeSHA
MergeApprovalID
MergeCommitSHA
MergedBy
MergedAt
CreatedAt
UpdatedAt
```

`Revision` 每次新增 commit、修改 PR head 或重新生成 merge preview 时递增。执行审批和合并审批分别绑定确切的 `PlanDigest` 与 `SubmissionID + Revision + BaseCommit + HeadCommit + DiffDigest`，不能只绑定 Todo ID 或分支名。

真实磁盘路径是平台实现细节，不能出现在公共 DTO、群聊消息或审计详情中。

## 7. Scheduler 与自动化状态机

### 7.1 保留系统定时任务

保留：

```text
Task ID:  todo-agent-automation-periodic-scan
Cron:     */5 * * * *
Timezone: Asia/Shanghai
Policy:   skip_if_running
Target:   todo_automation_scan
```

建议把 Dispatcher 接口从 `RunAutomationScan(ctx)` 扩展为带调度上下文的请求，至少传入 `ScheduledRunID`、触发时间和 batch limit，便于幂等与审计。

Scheduler 只负责唤醒和记录调度，不负责决定权限、选择工作目录或以 superadmin 身份执行。Todo 自动化服务逐条重新计算候选资格与权限。

### 7.2 候选条件

候选 Todo 必须同时满足：

1. `automation_mode` 为 `suggest` 或 `managed`。
2. Todo 状态仍处于可处理集合。
3. 存在有效人工 Reviewer/审批人。
4. `next_check_at <= now`。
5. 同一 Todo 版本没有 pending、executing 或已完成的计划。
6. 如果是代码执行阶段，必须绑定可用开发项目和仓库。
7. 成功取得 `(tenant_id, todo_id)` 的原子 claim。

扫描需要按租户公平取批次，避免一个租户占满全局 batch。

### 7.3 状态机

```text
eligible
  -> discussing
  -> not_ready -----------------> 到 next_check_at 后重新检查
  -> planning_failed -----------> 指数退避后重试
  -> pending_approval
       -> rejected
       -> stale
       -> approved
            -> creating_mission
            -> materializing_tasks
            -> provisioning_workspace
            -> executing
            -> waiting_runtime_approval
            -> review
            -> executed | execution_failed
```

`suggest` 到 `pending_approval` 时审批含义是“采纳方案”。用户还需要点击“开始执行”，随后再创建执行授权；`managed` 的审批直接授权在计划范围内执行。

`provisioning_workspace` 只适用于开发 Todo。综合 Todo 在 MissionTask 物化后直接进入 `executing`。Mission/binding 创建必须幂等；处于 `creating_mission/materializing_tasks` 的中断由 outbox reconciler 续做，Scheduler 下一轮不能另起一个 Mission。

### 7.4 防止无效记录

- 空扫描只产生一条 Scheduler run，不创建群聊、消息、审批或 Todo 评论。
- 空扫描和 `not_ready` 结果不创建 Mission 或 MissionTask。
- 相同 `TodoID + TodoVersion + ProposalRevision` 使用唯一幂等键。
- `not_ready` 保持当前至少 6 小时的冷却期；失败采用 15 分钟起步的指数退避并设置上限。
- 系统任务每次返回 `scanned/eligible/claimed/planned/skipped/failed` 计数。
- 空成功 Scheduler run 保留 7 天后按日聚合；包含候选或失败的 run 保留 90 天，长期审计保存聚合和业务事件，不无限堆积五分钟级记录。

## 8. 群聊复用与可见性

同一 Todo 的所有阶段都必须先查 `TodoDiscussionBinding`：

```text
定时评估
  -> 方案讨论
  -> 用户审批意见
  -> Mission 与 MissionTask 创建
  -> Workspace 准备
  -> Agent 分工和实际实施
  -> 测试、diff、失败与复核
  -> 完成结论
```

以上全部写入同一个 `ConversationID`。不得因重试、方案修订、审批拒绝后重开或实际执行而创建第二个群聊。

Todo 详情页显示“进入群聊”入口。用户不属于群聊成员时，服务端拒绝读取，前端只显示“仅 Todo 参与者可见”，不能因为能访问开发项目页面而绕过群聊权限。

建议给 Conversation 增加：

```text
PurposeKind = todo
PurposeID   = <todo_id>
WorkspaceID = <active workspace id, optional>
```

`PurposeKind=todo` 同时定义生命周期所有权：Conversation 由 Todo 拥有，`LinkedMissionRecord` 只是非拥有型引用。删除或归档某个 Mission attempt 只能移除 link/关闭 Mission，不能级联删除 Conversation；删除 Todo 时也应先进入保留/归档流程。现有“删除 Conversation 时级联删除 linked Missions”的逻辑对 Todo-purpose Conversation 必须禁用或直接拒绝，避免历史 attempt 和唯一群聊一起丢失。

开发执行不再由前端或 Agent 设置 `workspace_root_dir`。Conversation 只引用 `WorkspaceID`，运行时通过服务端 `WorkspaceResolver` 得到真实路径。

## 9. Todo 执行监控与 Mission Control 接入

### 9.1 复用边界

现有 Mission Control 已经具备任务看板、团队、依赖、Evidence、QA、阻塞、进度图、时间线、Agent run 操作和实时 stream。它应成为 Todo 发出任务后的唯一详细执行控制台，不再新增 `TodoSubTask`、Todo 专用看板或第二套进度百分比算法。

三个页面的职责如下：

| 页面 | 展示范围 | 事实来源 |
|---|---|---|
| Todo 详情 | 当前执行摘要、当前/历史 attempt、待审批、代码交付状态、进入群聊和“打开任务控制台” | `TodoMissionBinding` + `TodoExecutionProgressProjection` |
| 开发项目详情的“执行监控” | 项目内所有 Todo 当前 attempt 的聚合列表、筛选和风险摘要 | 项目范围的进度摘要投影 |
| `/missions/[missionId]` Mission Control | 单次执行的 Team、Tasks、Evidence、QA、Timeline、Blockers 和运行控制；作为 Todo 的 canonical 详情入口 | 现有 Mission ReadModel/Event stream |

开发项目只负责长期资源和聚合视角，Mission 只负责一次执行。综合 Todo 没有开发项目时，仍可从 Todo 进入 Mission Control。

### 9.2 从 Todo 创建 Mission

新增应用层命令 `CreateMissionFromTodo`（可以由 Todo orchestration service 调用，不作为任意客户端可伪造来源的通用接口）：

1. 确认 Todo、计划版本、执行审批、执行人权限和租户仍有效。
2. 取得 Todo 级原子 claim，检查是否已有 active binding；有则幂等返回该 Mission。
3. 读取 `TodoDiscussionBinding`，没有时按第 8 节规则创建唯一群聊。
4. 以 `OriginKind=todo`、`OriginID=TodoID`、`ExecutionAttempt=n` 创建 Mission，注入既有 `ConversationID` 并设置 `DiscussionMode=existing_conversation`；不得调用普通 `CreateMission` 的 DiscussionGroup/Conversation 新建分支。
5. 在既有 Conversation 上幂等写入 `LinkedMissionRecord`；生产接线不得再调用 `ensureMissionDiscussionSession` 创建第二个 Conversation。
6. 把获批计划的每个 `PlanStep` 物化为 MissionTask，保留依赖、预期交付物、Evidence 要求和来源 step ID。
7. 写入 `TodoMissionBinding`，发布 `todo.mission.bound`，执行器只能在 binding 为 `active` 且任务物化完成后启动。

跨 Todo 与 Mission 仓储无法放入同一事务时使用 transactional outbox/saga：先写 `creating` binding 和 outbox，再幂等创建 Mission/Tasks，最后切到 `active`。重放必须得到同一 Mission；reconciler 可以修复停留在 `creating` 的记录，但不能再创建一个群聊或第二个 active Mission。

计划没有显式步骤时，创建一个根 MissionTask“完成 Todo：<title>”，之后由有权限的协调 Agent 在 Mission 内拆分；所有后续 Agent run 仍必须归属某个 MissionTask。

### 9.3 执行 attempt 与状态规则

- 一个 Todo 同一时间最多有一个 active Mission。
- 同一 MissionTask 或 Agent run 失败后的重试复用当前 Mission，保留完整时间线。
- 方案 revision 在尚未开始执行时变化，不创建 Mission；执行开始后扩大范围需要使审批 stale，并暂停当前 Mission。
- Mission 已终态后 Todo 被重新打开时创建新的 Mission attempt；原 Mission 保持不可变历史，群聊继续复用。
- Todo 详情默认显示当前 attempt，并提供历史 attempt 列表；开发项目聚合页只显示当前 attempt。
- 删除或归档开发项目不能删除 Mission 历史、Evidence 或提交记录。

状态不是简单双向镜像：Todo 保存用户意图和最终业务状态，Mission 保存执行状态。Mission 的任务全部通过 QA 后进入 `deliver/awaiting_acceptance`；开发 Todo 还要等待必需的 Submission 合并或 patch/bundle 被人工接收。只有显式的领域命令才能把 Todo 标记完成，投影更新不能反向触发状态循环。

### 9.4 进度摘要投影

MissionEvent consumer 在每个真实状态变化后更新 `TodoExecutionProgressProjection`：

- 任务创建、分配、开始、阻塞、提交结果和 QA 决策更新任务计数、当前 Agent 和交付率。
- Approval 事件更新待审批数量。
- Mission phase/status 事件更新当前阶段。
- `TodoCodeSubmission`、PR 和 merge 事件更新代码交付状态。
- 所有更新以 `MissionProjectionVersion` 和 `SubmissionRevision` 做单调版本检查；旧事件、重复事件和乱序事件不得覆盖新状态。

Todo 不单独计算“完成百分比”。页面直接显示 Mission 的 `DeliveryRate`，同时展示 `完成/总数`、活动、阻塞和待审批数量，避免一个模糊百分比掩盖阻塞。`LastActivityAt` 只由业务事件推进，不使用心跳伪造进度。

### 9.5 权限与群聊隔离

Mission 可见性与 Discussion 可见性必须分开：

- Todo 提出人、负责人、Reviewer、协作者和实际参与 Agent 可以查看对应 Mission。
- 开发项目 Viewer/Contributor 只能读取项目聚合摘要，除非其本身是 Todo 参与者。
- 开发项目 Maintainer/Owner 可凭 `development.monitor` 查看项目内详细 Mission Control，但这不授予执行、QA、审批或群聊权限。
- 创建/分配任务、取消/重试 Agent run、QA 决策、计划审批和合并继续分别检查对应 capability，不能用“能打开控制台”替代。
- Todo 群聊只允许第 6.6 节的显式成员读取和发言；Todo-origin Mission 不写入会让 Mission Viewer 继承访问权的 `discussion.mission` 关系。
- `CreateMissionFromTodo` 的服务身份只记为审计 `CreatedBy`，不能因为它代表 Scheduler/系统执行就写成 Mission owner。Todo-origin Mission 不写直接 owner relation；读取、编辑和 QA 权限分别从 Todo 关系投影，Agent 仅以 participant/任务受让人执行被分配的工作。

现有组合接口 `GET /api/missions/{mission_id}/control` 需要在组装 DTO 时按 Mission 引用的 canonical `ConversationID` 单独执行 `discussion.read`：有权限才返回 Discussion；无权限返回空内容及 `discussion_access=restricted`，不能因为 Discussion 被拒绝而拒绝整个 Mission Control，也不能把消息正文、摘要或 mention 夹带进 Timeline、ProgressGraph、Evidence 或错误文本。

`GET /api/missions/{mission_id}/discussion` 和群聊消息 mutation 始终单独授权。Mission stream 只发送可公开的结构化执行事件或 invalidation signal；聊天正文走 Conversation 自己的授权 stream。否则项目 Maintainer 可能通过 SSE 绕过页面限制读取群聊。

canonical Conversation 的消息事件不得以正文形式复制进 Mission 的通用 ProgressGraph。需要联动刷新时，只发布不含内容的 `discussion.activity_changed`（ConversationID、Revision、OccurredAt）；已有 `MessagePosted/AgentReplied` 投影在返回给无 `discussion.read` 的 Viewer 时必须按事件可见性白名单剔除，不能依靠前端隐藏。

### 9.6 实时刷新与记录控制

Todo 详情在存在 active Mission 时复用 `/api/missions/{mission_id}/stream`，收到事件后刷新进度摘要；Mission Control 继续使用现有 stream。开发项目聚合页新增项目级轻量 stream，只发送 `TodoID + MissionID + ProjectionVersion`，客户端刷新受影响行，不能为列表中每个 Mission 建立一个 SSE 连接。

进度监控是事件驱动的，不增加每五分钟轮询 Mission 的 Agent，也不创建新的聊天消息。Scheduler 仍只负责发现可评估 Todo 和发起方案流程；空扫描只留下受保留策略控制的 Scheduler run。Mission 只在任务、运行、审批、Evidence、QA、提交或状态确实变化时产生事件。

### 9.7 开发执行关联

开发 Todo 的每个 AgentRun 同时引用 `MissionID + MissionTaskID + TodoExecutionRunID + WorkspaceID`。Workspace lease、命令日志、测试结果、diff、commit 和 artifact 作为该 MissionTask 的 Evidence 或结构化事件写入；公共 DTO 仍不得暴露真实磁盘路径。

提交阶段不另开 Mission：candidate commit、push、PR、请求修改、merge preview、合并审批和最终 merge 都更新同一 Mission 的 `deliver` 阶段和同一 Todo 群聊。代码 Submission revision 变化时，Mission 中对应审批和 QA 状态同步标记 stale，但历史 Evidence 保留。

## 10. Git 与工作区方案

### 10.1 仓库布局

每个仓库绑定维护一个服务端受管 mirror 或 reference clone；每个 Todo 创建一个 worktree：

```text
managed-workspaces/
  <tenant>/
    <development-project>/
      mirrors/<repository-binding>.git
      todos/<todo-id>/<repository-binding>/
```

真实根目录来自服务端配置，不接受 API 传入绝对路径。所有 ID 需要清洗，创建后再次检查 realpath，防止 `..`、软链接或 junction 逃逸。

### 10.2 分支规则

```text
orrery/todo/<short-todo-id>-<slug>
```

- 一个 Todo 一个长期分支和 worktree，方案重试和执行重试继续复用。
- 不同 Todo 使用不同 worktree，可并发工作。
- 同一 Todo 同时只允许一个写 lease。
- fetch 使用仓库级短锁；Agent 写文件使用 Todo workspace lease。
- 第一版禁止 force push、删除默认分支和直接写默认分支。
- Todo 完成后保留 worktree 一段可配置时间，再由独立清理任务归档；不能在执行失败时直接删除证据。

### 10.3 基线与并发

创建计划时记录 `BaseCommit`，审批后严格从该 commit 创建 worktree。多个 Todo 可从同一基线并行，各自提交。

合并前执行：

1. fetch 最新默认分支。
2. 计算 ahead/behind 和冲突风险。
3. 无冲突时创建 PR 或等待有权限用户合并。
4. 需要 rebase、解决冲突或扩大文件范围时回到原群聊请求新审批。

不要给每个 Todo 完整 clone 一份仓库；mirror + worktree 能隔离写入，同时节省磁盘和 fetch 时间。

### 10.4 凭据和命令安全

- Git 凭据只在 fetch/push 适配器内部短暂解析，不能写入 remote URL、`.git/config`、环境快照或群聊。
- Git 命令使用参数数组调用，禁止拼接 shell 字符串。
- Agent 只拿到 worktree，不拿到 mirror、其他 Todo worktree 或活动服务目录。
- Runtime policy 明确文件根、工作子目录、允许命令、网络出口、Secret allowlist 和最长运行时间。
- 已批准计划外的命令、网络、Secret 或文件范围仍需运行时 HIL 审批。

## 11. Commit、提交评审与合并方案

代码执行、提交评审和合并是三个不同风险等级，必须拆开授权：

```text
执行审批
  -> 允许在隔离 worktree 中修改、测试并创建本地候选 commit

提交权限
  -> 允许把确切候选 commit 推送到 Todo 专属远端分支并创建/更新 PR

合并审批
  -> 允许把经过最新基线预演和检查的确切 submission 合并到目标分支
```

批准执行不等于批准 merge。Agent 可以在批准范围内生成 commit，但永远不能凭 `executor` 身份自行合并默认分支。

### 11.1 Agent 创建 commit 的协议

Agent 完成修改后，由 Workspace Manager 执行受控提交，不把无限制 Git shell 权限交给 Agent：

1. 获取 Todo workspace 写 lease，确认当前 run、plan、workspace 和 branch 一致。
2. 读取 `git status --porcelain=v2` 和基于批准 `BaseCommit` 的完整 diff。
3. 验证所有新增、修改、删除、重命名文件都在 `AllowedFileScopes` 内。
4. 拒绝未批准的 submodule 变化、符号链接/junction、超大文件、二进制文件、`.env`、私钥和 Secret 扫描命中。
5. 执行计划中的格式化、静态检查、构建和测试；保存命令、退出码、日志摘要与 artifact 引用。
6. 只暂存验证过的路径，不能对未知工作区直接执行无边界 `git add -A`。
7. 使用 Orrery Git service identity 作为 Committer，实际 Agent 或用户作为 Author，并使用服务端签名密钥签名。
8. 创建 commit 后重新计算 `TreeSHA`、`DiffDigest` 和文件统计，确认工作区没有漏提交或越界残留。
9. 写入 `TodoCommitRecord`，将 commit SHA、检查结果和 diff 摘要回写同一个 Todo 群聊。

Git service identity 和 SSH/GPG signing key 由平台配置并通过 Secret Provider 解析。开发项目只能选择已允许的签名配置，Owner 和 Agent 都不能导出私钥。

推荐 commit message：

```text
todo(<short-id>): <imperative summary>

Orrery-Todo: <todo-id>
Orrery-Plan: <plan-id>@<plan-digest>
Orrery-Run: <execution-run-id>
Orrery-Agent: <agent-id>
```

Commit message 不得包含隐藏推理、凭据、服务器路径或大段聊天内容。

一次 Todo 可以产生多个小 commit。每个 commit 都必须经过相同边界检查。提交到评审时把当前 `HeadCommit` 固化为一个 `TodoCodeSubmission` revision；后续修订只能追加 commit 并生成新 revision，旧 revision 保持不可变。

### 11.2 本地 commit、远端 push 和 PR 的边界

| 动作 | 是否改变外部状态 | 所需授权 |
|---|---:|---|
| 在 Todo worktree 修改文件 | 否，隔离且可丢弃 | 有效执行审批 + `development.execute` |
| 在 Todo branch 创建本地 commit | 否，仍在受管仓库内 | 有效执行审批 + `development.execute` |
| push Todo 专属远端 branch | 是 | `development.submit` + `submitter` + connection `user` |
| 创建或更新 Pull Request | 是 | 同上，且计划允许 `create_pr` |
| 修改默认分支 | 是，影响全项目 | 独立合并审批 + `development.merge` + `merge_authorizer` |

仓库连接的有效操作要拆成 `fetch`、`push_branch`、`create_pr`、`merge_pr`。即使 Provider token 实际拥有更大权限，Orrery policy 仍只放行本次所需操作。

### 11.3 提交评审流程

候选 commit 生成后，状态进入 `local_candidate`：

1. 系统生成 `TodoCodeSubmission`，固化 base/head/tree/diff digest、commit 列表和检查结果。
2. 在原群聊发出“候选变更已准备”，展示文件、增删行、风险、测试和 commit SHA。
3. `managed` 模式按开发项目策略自动请求“提交评审”审批，或由有 `development.submit` 的用户点击提交。
4. 提交服务取得短期连接凭据，只把 Todo branch 推送到远端，禁止默认分支 refspec。
5. 有 Provider API 时创建或更新一个 PR，并写入 `PullRequestID/URL`；同一 Todo 和仓库复用同一个 PR。
6. PR 标题、正文、labels 和关联信息从结构化 Todo/plan/run 数据生成，不能直接把 Agent 的任意文本当 Provider API 参数。
7. Provider URL 只作为外部证据链接；Orrery 自身仍保存不可变 SHA 和检查快照。

远端 branch 推荐继续使用：

```text
orrery/todo/<short-todo-id>-<slug>
```

禁止普通重试 force push。需要改变历史时创建新 revision branch，例如 `.../r2`；只有 Maintainer 在明确审批并带 `--force-with-lease` 语义时才能替换远端 branch，永远禁止无 lease force push。

### 11.4 评审与请求修改

评审意见无论来自 Orrery 页面、Todo 群聊还是 Provider webhook，最终都归一到同一个 Todo 和群聊：

```text
pr_open
  -> checks_running
  -> changes_requested -> 原 worktree 继续修改 -> 新 commit -> 新 submission revision
  -> merge_ready
  -> closed
```

- Reviewer 点击“请求修改”时必须提供意见，系统把意见写回原群聊并 @执行 Agent。
- Agent 复用原 worktree 和 branch，不新建群聊。
- 新增 commit 后，旧的提交评审结果和合并审批全部变为 stale。
- Todo 在 PR 打开后进入 `review`；PR 关闭未合并时，根据人工选择进入 `blocked`、`canceled` 或重新开发，不能自动标记 `done`。

Provider 状态以签名 webhook 为主，事件按 Provider event ID 幂等处理，并校验 repository、PR、base/head SHA 都属于当前 binding。另注册一个系统 Scheduler reconciliation 任务定期核对漏掉的 webhook；该任务只修正 Submission/PR 状态，不创建新群聊或重复消息。

### 11.5 Merge preview

在请求合并审批前，服务端必须生成一次可复现的 merge preview：

1. fetch 最新目标分支，记录 `ExpectedBaseHead`。
2. 在一次性 preview worktree 中，以 `ExpectedBaseHead + HeadCommit` 按项目 `MergeStrategy` 计算结果。
3. 如果存在冲突，Submission 进入 `conflicted`，在原群聊列出冲突文件；不得自动猜测冲突解决方案并继续 merge。
4. 如果没有冲突，计算 `MergePreviewTreeSHA`。
5. 在 preview 结果上重新运行项目要求的合并前检查，而不是只相信开发分支较早的测试结果。
6. 汇总 Provider required checks、Orrery 本地检查、diff、Secret scan、Review 状态和 branch protection 状态。
7. 只有全部必需检查通过，Submission 才进入 `merge_ready`。

对于 squash，preview 的结果 tree 必须与最终 squash merge 结果一致；对于 merge commit，记录两个 parent 和预期 tree；对于 rebase，必须记录重放后的 commit 序列。第一版默认 `squash`，因为它最容易稳定绑定一个 Todo、一组变更和一个审计记录。

### 11.6 独立合并审批

合并审批页和 Todo 群聊必须展示并绑定：

```text
SubmissionID + Revision
TargetBranch
ExpectedBaseHead
HeadCommit
MergePreviewTreeSHA
DiffDigest
MergeStrategy
RequiredChecksSnapshot
PlanDigest
```

审批规则：

- 审批人必须是 human user，Agent 不能成为合并审批人。
- 必须同时有 `development.merge` Scope 和项目 `merge_authorizer` 关系。
- 默认不能由最后一次执行该 Todo 的请求人自批；本地单用户例外仍需显式策略和审计。
- 任一 SHA、diff、检查结果、PR head、目标分支或 Submission revision 变化，审批立即 stale。
- 批准消息可以附加条件，但机器可执行条件必须进入结构化 policy，不能只存在自然语言备注中。

### 11.7 原子合并与 TOCTOU 防护

实际合并前必须再次读取目标分支 head：

```text
if current_target_head != approved ExpectedBaseHead:
    不合并
    重新生成 merge preview
    旧审批标记 stale
    请求新的合并审批
```

远程仓库优先通过 Provider 的受保护 PR Merge API 执行，并要求 Provider 同时校验 PR head SHA 和 branch protection。不能先在本地 merge 再无条件 push 默认分支。

成功后必须读取 Provider 返回的最终 `MergeCommitSHA` 和 tree，再与预期结果核对并写入 receipt。若 Provider 报告成功但结果无法核对，状态进入 `merge_verification_failed`，禁止把 Todo 自动设为 done，并告警人工处理。

合并操作的幂等键使用：

```text
merge:<tenant>:<submission-id>:<revision>:<approval-id>
```

相同请求重放时只读取已有 receipt，不能触发第二次 merge。

### 11.8 本地仓库的合并处理

当前 Orrery 所在的 `D:\Python\projects\IdeaDo` 是人工活动 checkout，而且可能包含未提交修改。Orrery 绝不能自动切换它的分支、更新它的默认分支或在其中执行 merge。

本地仓库分两类：

1. `managed_local_source`：像当前 Orrery checkout，只能作为只读 clone/fetch 来源。Agent 在受管 mirror/worktree 产出 branch、commit、patch 或 Git bundle；最终由人工在活动 checkout 中合并，或者配置 remote 后走 PR。
2. `managed_bare_authoritative`：由 Orrery 独占的 bare repository，没有人工活动 checkout。只有这一类才允许服务端在 ref lock 和 compare-and-swap 下更新目标 branch，并且仍需独立合并审批。

因此当前 Orrery 项目的推荐路径是：

```text
活动 checkout 提供已提交基线
  -> Orrery 受管 mirror/worktree
  -> Todo branch + signed candidate commits
  -> 推送 GitHub/GitLab remote 并创建 PR
  -> 用户审批后由 Provider 合并
  -> 人工活动 checkout 自行 pull/rebase
```

如果没有 remote，第一版只交付 branch ref、patch 或 Git bundle 和可验证命令，不自动写回活动 checkout。这个限制必须在开发项目页面明确显示。

### 11.9 合并后与回滚

合并成功后：

1. Submission 标记 `merged`，保存最终 merge commit、Provider receipt 和审批人。
2. 在原群聊发布最终 SHA、目标分支、检查结果和可验证链接。
3. Workspace 标记 `merged` 并进入保留期；不能立即删除。
4. 是否自动删除远端 Todo branch 由项目策略决定，默认在确认 receipt 后删除。
5. Todo 默认进入 `done`，但如果验收标准包含部署或业务验证，则保持 `review`，等待人工验收。

合并后发现问题不能 reset 或改写默认分支历史。必须创建新的 Revert Todo，引用原 `SubmissionID` 和 `MergeCommitSHA`，通过新的计划、审批、PR 和 merge 流程回滚。

多仓库写入不做伪原子事务。未来需要跨仓库时，每个仓库产生独立 Submission/PR，并用依赖图规定合并顺序；任何一步失败时进入补偿流程，而不是声称已经全局回滚。

## 12. 后端模块与接口

### 12.1 新增模块

建议目录：

```text
internal/app/developmentproject/
  types.go
  repository.go
  service.go
  authorization.go
  repository_binding_service.go
  membership_service.go
  workspace_service.go
  execution_service.go
  commit_service.go
  submission_service.go
  merge_service.go

internal/app/scmconnection/
  types.go
  repository.go
  service.go
  authorization.go

internal/app/todoexecution/
  binding.go
  binding_repository.go
  mission_coordinator.go
  progress_projection.go
  progress_query_service.go
  reconciliation.go

internal/platform/gitworkspace/
  git.go
  mirror.go
  worktree.go
  lease.go
  commit.go
  merge_preview.go
  path_policy.go

internal/platform/scmprovider/
  provider.go
  github.go
  gitlab.go

internal/transport/http/
  development_project_handler.go
  scm_connection_handler.go
  todo_execution_handler.go
```

领域服务只依赖接口；Git CLI、Secret Provider、文件系统和 Provider API 放在 platform/adapter 层。`todoexecution` 是跨 Todo、Mission 和开发项目的应用编排层，不把 Todo 规则塞入 `missioncontrol`；Mission 侧只增加可复用既有 Discussion 的受控创建 port、来源字段和投影事件。

### 12.2 开发项目 API

```text
GET    /api/development-projects
POST   /api/development-projects
GET    /api/development-projects/{id}
PATCH  /api/development-projects/{id}
POST   /api/development-projects/{id}/archive

GET    /api/development-projects/{id}/members
POST   /api/development-projects/{id}/members
PATCH  /api/development-projects/{id}/members/{member_id}
DELETE /api/development-projects/{id}/members/{member_id}

GET    /api/development-projects/{id}/repositories
POST   /api/development-projects/{id}/repositories
PATCH  /api/development-projects/{id}/repositories/{binding_id}
DELETE /api/development-projects/{id}/repositories/{binding_id}
POST   /api/development-projects/{id}/repositories/{binding_id}/verify
POST   /api/development-projects/{id}/repositories/{binding_id}/fetch

GET    /api/development-projects/{id}/todos
GET    /api/development-projects/{id}/workspaces
GET    /api/development-projects/{id}/runs
GET    /api/development-projects/{id}/executions
GET    /api/development-projects/{id}/executions/stream
```

`executions` 返回当前 attempt 的分页摘要，支持 Todo 状态、Mission 状态、负责人、阻塞、待审批和最近活动过滤。项目级 stream 只发送摘要失效通知，不包含聊天正文、runtime trace、Secret、diff 内容或绝对路径。

每条 execution summary 返回服务端计算的 `can_open_control` 和 `can_open_discussion`。后者必须针对绑定的 canonical Conversation 单独授权，不能因为前者为 true 自动为 true。

详情 DTO 返回后端计算好的 `capabilities`，例如 `can_update`、`can_manage_members`、`can_bind_repository`、`can_request_execution`、`can_approve`、`can_merge`。前端不自行推断 OpenFGA 关系。

### 12.3 连接 API

```text
GET    /api/scm-connections
POST   /api/scm-connections
GET    /api/scm-connections/{id}
PATCH  /api/scm-connections/{id}
POST   /api/scm-connections/{id}/verify
POST   /api/scm-connections/{id}/rotate
POST   /api/scm-connections/{id}/revoke
GET    /api/scm-connections/{id}/repositories
```

`managed_local` 连接只允许平台管理员通过受控配置或管理 API 创建；普通 SaaS 用户不能提交任意服务器路径。

### 12.4 Todo 与审批 API 扩展

- 扩展 Todo create/update/list/detail 的 `work_kind`、`development_project_id`、`automation_mode`。
- 增加 `POST /api/todos/{id}/automation/check`，手动触发必须走与 Scheduler 相同的 claim、幂等和权限路径。
- 增加 `GET /api/todos/{id}/execution-plans` 和 `GET /api/todos/{id}/execution-runs`。
- 增加 `GET /api/todos/{id}/missions`，返回当前和历史 execution attempts；增加 `GET /api/todos/{id}/progress` 返回当前摘要。
- 增加 `POST /api/todos/{id}/execution-attempts`，供 `suggest` 模式的“开始执行”和获授权的人工执行使用；`managed` 审批通过时由应用服务内部调用同一命令。它必须携带 `plan_id + plan_digest + todo_version + Idempotency-Key`。
- 复用现有 Todo automation approval 接口，但返回 `plan_id`、`plan_digest`、仓库和基线摘要。
- 审批请求必须带当前 `plan_digest`；服务端不接受只按旧 `approval_id` 批准已变化的计划。
- 增加 `GET /api/todos/{id}/discussion`，由稳定绑定返回唯一 Conversation，并执行显式成员授权。

Todo progress DTO 同样返回 `can_open_control`、`can_open_discussion` 和当前允许的执行操作；历史 attempts 逐项做 Mission 对象授权，不能只因用户能读当前 Todo 就返回原本无权访问的历史 Evidence。

Mission API 和 DTO 同步扩展：

- `MissionDTO` 增加只读 `origin_kind`、`origin_id` 和 `execution_attempt`。
- Mission 存储增加 `tenant_id` 和 `discussion_mode`；所有 Mission query/command/stream 都校验 Viewer Tenant，公共 DTO 不需要暴露 TenantID。
- `MissionControlViewDTO` 增加 `discussion_access=authorized|restricted|unavailable`；restricted 时所有 Discussion 内容必须为空。
- Mission 列表支持 `origin_kind`、`origin_id` 过滤，但对象授权仍逐项执行。
- 现有 `/api/missions/{mission_id}/discussion`、Discussion mutation 和 Conversation stream 单独校验 `discussion.read/write`。
- 现有 `/api/missions/{mission_id}/stream` 对无群聊权限的 Viewer 过滤消息内容，只保留允许公开的结构化执行事件。

### 12.5 Commit、Submission 与 Merge API

```text
GET    /api/todos/{id}/commits
POST   /api/todos/{id}/execution-runs/{run_id}/commits

GET    /api/todos/{id}/code-submissions
POST   /api/todos/{id}/code-submissions
GET    /api/todos/{id}/code-submissions/{submission_id}
POST   /api/todos/{id}/code-submissions/{submission_id}/push
POST   /api/todos/{id}/code-submissions/{submission_id}/pull-request
POST   /api/todos/{id}/code-submissions/{submission_id}/request-changes
POST   /api/todos/{id}/code-submissions/{submission_id}/merge-preview
POST   /api/todos/{id}/code-submissions/{submission_id}/merge-approval
POST   /api/todos/{id}/code-submissions/{submission_id}/merge
POST   /api/todos/{id}/code-submissions/{submission_id}/close
```

创建 commit 的 HTTP endpoint 只供受信 runtime/service principal 使用，普通浏览器用户不能伪造 Agent run。所有 mutation 必须带 `Idempotency-Key` 和当前 `Version/Revision`；push、PR、merge 额外校验确切 SHA。

所有契约同步更新 `docs/api/mission-control.openapi.yaml`，随后执行 `npm run generate:api`，禁止手工维护一套漂移的前端类型。

## 13. 前端页面

### 13.1 导航与路由

在“工作”分组新增“开发项目”：

```text
/development-projects
/development-projects/[developmentProjectId]
```

导航显示条件：用户有 `development.read` 且至少能访问一个开发项目，或者有 `development.create`。直接访问路由仍由 API 授权，前端路由守卫不是安全边界。

### 13.2 列表页

列表页提供：

- 名称、组织、状态。
- 主仓库、默认分支、最新已同步 commit 和连接健康状态。
- 活跃开发 Todo、待审批数量、执行中数量、失败数量。
- 当前用户角色和可执行操作。
- 搜索、状态过滤、组织过滤、分页。
- 有权限时显示“新建开发项目”。

### 13.3 详情页

详情页使用六个 Tab：

1. **概览**：仓库健康、默认自动化模式、活跃 Todo、待审批、最近运行和风险。
2. **Todo**：项目内开发 Todo；可从此创建 Todo、进入 Todo 详情或唯一群聊。
3. **执行监控**：项目内 Todo 当前 Mission 的交付率、任务计数、当前 Agent、阻塞、待审批、最后活动和代码交付状态；点击进入现有 Mission Control。
4. **工作区**：分支、base/head commit、lease、Agent、运行状态、diff/test 摘要和清理状态；不显示服务器绝对路径。
5. **成员与权限**：角色、Agent executor、merger 授权；只有 Owner 可修改成员。
6. **设置**：仓库绑定、源码子目录、构建/测试命令、并发、网络、审批、归档；仅 Manager 可见可编辑控件。

执行监控表格默认按“阻塞 > 待审批 > 执行中 > 最近活动”排序，支持按负责人、Mission 状态和风险筛选。Viewer/Contributor 只看到获准的摘要；Maintainer/Owner 才显示“打开任务控制台”，但“进入群聊”仍按 Todo 成员权限单独显示。列表更新使用项目级 stream，不能为每一行建立 Mission stream。

连接凭据不放在这个页面。这里仅选择已授权连接并显示脱敏健康状态；连接创建和轮换在独立设置页完成。

### 13.4 Todo 页面扩展

Todo 创建/编辑表单增加：

- 工作类型：普通任务 / 开发任务。
- 开发项目选择器：仅列出用户有 `writer` 权限的项目。
- 自动化模式：手动 / 仅建议 / 审批后执行。
- 仓库范围和可选文件范围。

Todo 详情增加：

- 开发项目链接。
- 当前计划版本与审批状态。
- 当前 Mission attempt、阶段、交付率、完成/活动/阻塞任务数、待审批、当前 Agent 和最后活动时间。
- 基线 commit、分支、workspace 和执行状态。
- “进入群聊”固定入口。
- “打开任务控制台”入口，跳转到既有 `/missions/{missionId}`；没有 active Mission 时不显示空控制台。
- 历史执行 attempts，每条可打开对应只读 Mission Control。
- diff、测试结果、产物和失败信息。
- “重新讨论”“开始执行”“审批”“请求合并”等按钮，并完全依据后端 `capabilities` 显示。

### 13.5 代码评审与合并界面

Todo 详情中的“代码变更”区域必须围绕不可变 Submission 展示，而不是直接读取仍在变化的 worktree：

- base/head commit、Submission revision、branch 和 PR 状态。
- commit 列表、文件树、逐文件 diff、二进制/大文件提示。
- 构建、测试、Secret scan 和 Provider required checks。
- “请求修改”“提交评审”“重新预演”“批准合并”“合并”操作。
- 合并审批对话框明确显示目标分支、策略、ExpectedBaseHead 和 DiffDigest。
- Submission 变化后，旧审批显示 stale 且合并按钮立即禁用。
- 本地 source 仓库没有 remote 时显示“导出 patch/bundle”，不显示虚假的自动合并按钮。

## 14. 服务端接线位置

实施者需要至少修改以下现有位置：

| 位置 | 修改内容 |
|---|---|
| `internal/app/projectadmin/` | 补齐 TenantID、对象级授权和安全列表，为可选通用项目绑定打基础 |
| `internal/app/todo/dto.go` | 增加工作类型、开发项目、自动化模式和计划/运行引用 |
| `internal/app/todo/repository.go`、`query_repository.go` | 持久化新字段，候选查询按租户和模式过滤 |
| `internal/app/todo/automation_service.go` | 接入稳定群聊绑定、结构化计划、开发项目校验和 workspace provisioning |
| `internal/app/todoexecution/` | Todo-Mission binding、幂等创建、计划步骤物化、进度摘要投影与 creating 状态修复 |
| `internal/app/missioncontrol/lifecycle_service.go` | 支持带 Todo origin 创建 Mission，并在受控路径复用既有 Discussion，不再强制新建群聊 |
| `internal/app/missioncontrol/dto.go`、`query_service.go` | 返回 origin、Discussion 访问状态；组合视图独立鉴权并清空无权内容 |
| `internal/app/missioncontrol/db_repository.go` | Mission 记录增加 TenantID，读写键、列表和 stream lookup 改为 tenant-scoped |
| `internal/mission/projection/` | 投影 Mission origin、MissionTask 来源和可公开的 Todo 执行事件 |
| `internal/app/scheduler/dispatcher.go` | 把 ScheduledRun 上下文传给扫描，记录结构化结果 |
| `internal/app/discussionsession/` | 支持 Todo purpose binding 和 `WorkspaceID` 服务端解析 |
| `internal/app/authorization/` | 新 action mapping、对象类型、relation projector、list objects 和 backfill |
| `docs/agent-company-mission-control/openfga/authorization-model.fga` | 新增 development_project、scm_connection、todo 和 discussion.todo |
| `internal/transport/http/routes.go` | 注册新 API，所有路径接入服务端授权 |
| `cmd/server/` | 生产仓储、Git adapter、Secret resolver、服务和路由接线 |
| `cmd/server/production_api.go` | Todo-origin Mission 直接关联既有 Conversation；绕过 `ensureMissionDiscussionSession` 的新建路径并回写 canonical ConversationID |
| `web/src/lib/routes/navigation.ts` | 新路由、导航项和 route scope |
| `web/src/routes/development-projects/` | 列表和详情页 |
| `web/src/routes/missions/[missionId]/` | 支持 Todo origin 返回入口和 restricted Discussion 状态；讨论按钮使用 API 返回的 ConversationID，不假定 MissionID 就是会话 ID |
| `web/src/lib/components/mission/MissionRoom.svelte` | 与 canonical Mission 页面使用同一权限/Discussion 映射，restricted 时不从 event fallback 恢复消息正文 |
| `web/src/lib/queries/missions.ts` | 继续复用 Mission 子视图和 stream；补充 origin/权限映射 |
| `web/src/lib/queries/` | 开发项目、连接、workspace、run 查询和 mutation |
| `web/src/lib/i18n/locales/` | 中英文页面文案 |
| `docs/api/mission-control.openapi.yaml` | 完整 API 契约并重新生成前端类型 |

## 15. 实施顺序与 PR 拆分

### PR 1：授权与租户基础

范围：

- 修复 `projectadmin` 的租户键、Viewer 上下文、列表过滤和对象校验。
- 补齐 Mission ReadModel/仓储的 TenantID，并消除创建、删除和 relation projector 中的空 TenantID 路径。
- 增加开发项目、SCM 连接、Todo 的 OpenFGA 类型和 action mapping。
- 增加 Scope 定义、默认角色映射、relation projector 和 backfill dry-run。
- 添加 capability 查询模式。

完成标准：跨租户猜 Project、DevelopmentProject、Todo 或 Mission ID 都不能读写或订阅 stream；无 relation 的用户即使有 Scope 也不能看到对象；OpenFGA 不可用时 fail closed。

### PR 2：开发项目与连接后端

范围：

- 新增 `developmentproject`、`scmconnection` 模块和租户安全仓储。
- 实现项目、成员、仓库绑定、连接元数据 API。
- 接入 Secret Provider，只存引用。
- 第一版实现管理员受管的 `managed_local` 连接和只读 verify。

完成标准：Owner 可管理项目但不能读取 Secret；无 `connection.use` 不能绑定；归档项目不能启动新执行。

### PR 3：开发项目页面

范围：

- 导航、列表、创建流程和资源管理相关 Tab；“执行监控”先保留 capability 驱动的空态，数据在 PR 6 接入。
- capability 驱动的按钮和只读状态。
- 连接设置入口与脱敏状态。
- Svelte check、组件测试、桌面与移动端页面验证。

完成标准：不同角色只看到允许的项目和操作；长仓库名、错误状态和移动端布局不溢出。

### PR 4：Todo 显式自动化模式

范围：

- 增加 `work_kind`、`development_project_id`、`automation_mode`。
- 现有 Todo 默认迁移为 `general/manual`；已有 automation state 的 Todo 才保留对应自动化模式。
- Scheduler 候选查询仅扫描 opt-in Todo，并返回结构化计数。
- 空 run 保留/聚合策略和指标。

完成标准：连续运行多次五分钟任务不会给普通 Todo 创建群聊；相同版本不会重复创建方案。

### PR 5：Todo 唯一群聊与结构化计划

范围：

- 增加 `TodoDiscussionBinding` 唯一约束和并发 get-or-create。
- 同步提出人、负责人、Reviewer、参与 Agent 的成员关系和撤权。
- 生成 `TodoExecutionPlan`，计算 digest，审批绑定确切计划。
- Todo 详情固定进入同一个群聊。

完成标准：并发触发、拒绝后重议、not-ready 重试和执行阶段都只存在一个 Conversation；项目 Viewer 不能借项目权限读群聊。

### PR 6：Todo-Mission 执行监控接入

范围：

- 增加 Mission origin、`TodoMissionBinding`、execution attempt 唯一约束和 `CreateMissionFromTodo` 编排。
- 以 Todo Conversation 为 canonical 会话，写入 Mission link 并绕过 `ensureMissionDiscussionSession` 新建路径；把 PlanStep 幂等物化为 MissionTask，补充 outbox/reconciliation。
- 增加 Todo 进度摘要投影、当前/历史 Mission API、项目 execution 聚合 API 和项目级 stream。
- Todo 详情增加紧凑进度和“打开任务控制台”；开发项目详情启用“执行监控”Tab。
- 拆分 Mission 与 Discussion 授权；组合 DTO 和 Mission stream 对无群聊权限的 Viewer 做服务端过滤。

完成标准：一个 Todo 同时只有一个 active Mission；重试复用 Mission、终态重开创建新 attempt 且群聊不变；每个 Agent run 都属于 MissionTask；项目 Maintainer 能看控制台但不能旁路读取群聊；空扫描不会创建 Mission。

### PR 7：Git workspace manager

范围：

- mirror/reference clone、fetch 锁、分支、worktree、lease、path policy、清理状态机。
- Conversation `WorkspaceID` 与 runtime resolver。
- 基线 commit、diff、测试结果、artifact 记录。
- 当前 Orrery 仓库的受管本地接入验证。

完成标准：两个 Todo 可在独立 worktree 并发；同一 Todo 不会双写；Agent 无法访问活动 checkout 或另一个 Todo 工作区。

### PR 8：审批后执行闭环

范围：

- `managed` 审批后 provision workspace，在原群聊发起执行。
- 执行只能通过当前 TodoMissionBinding 启动，并把 AgentRun 绑定到 MissionTask 和 workspace。
- 把 plan 的文件、命令、网络、Secret 范围转成 runtime policy。
- 超出范围继续触发现有 HIL；执行受控 staging、签名 commit，并回写 commit、diff、test 和验收结果。
- 持久化 `TodoCommitRecord` 与本地 `TodoCodeSubmission` revision。
- 处理 stale、失败重试、取消和人工接管。

完成标准：批准旧 digest 不能执行新计划；越界文件不能进入 commit；Scheduler 身份不能绕过项目、仓库或 runtime 权限；失败保留可审计证据。

### PR 9：提交评审与合并闭环

范围：

- 受控 push Todo branch、创建/更新一个 PR。
- 请求修改、新 Submission revision 和旧审批 stale。
- merge preview、合并前检查、独立合并审批。
- Provider PR merge、ExpectedBaseHead CAS、结果 SHA/tree 核验和幂等 receipt。
- 本地 source 仓库的 patch/bundle 交付与 authoritative bare 仓库受控合并。

完成标准：执行审批不能代替合并审批；目标分支或 candidate SHA 变化会阻止 merge；Agent 无法合并；重放同一 merge 请求不会重复合并。

### PR 10：Provider 自助接入与运营完善

范围：

- GitHub App/GitLab 自助连接、授权与撤销。
- workspace 清理 Scheduler 任务、run 聚合、磁盘额度和告警。
- Provider webhook 与定时 reconciliation。
- 分阶段启用 OpenFGA enforce。

完成标准：普通用户可自助授权限定仓库；撤销连接立即阻断新网络操作；只有 `development.merge` + `merge_authorizer` 可以合并。

依赖关系：

```text
PR1 -> PR2 -> PR3
        |
        +-> PR4 -> PR5 -> PR6 -> PR7 -> PR8 -> PR9 -> PR10
```

不要把 PR 6/7/8 提前塞进 UI PR；Mission 权限隔离、工作区和执行授权是三个独立安全边界，需要分别评审。

## 16. 测试方案

### 16.1 后端单元与集成测试

- 每种 Scope 与对象关系组合的 allow/deny 表格测试。
- 相同业务 ID 不同 Tenant 的隔离测试。
- Mission detail、subview、command 和 stream 的跨租户隔离测试；任何 relation tuple 不允许空 TenantID。
- list/get/update/archive 的对象授权测试。
- Owner 无法读取或轮换连接 Secret 的测试。
- 连接授权撤销、过期、Provider 错误测试。
- `manual` Todo 永不进入候选的测试。
- 相同 Todo/version 的扫描幂等测试。
- 两次并发扫描只创建一个计划和一个群聊的测试。
- 群聊提出人、负责人、Reviewer、Agent 成员及撤权测试。
- 方案 digest、Todo version、仓库 binding 变化导致 stale 的测试。
- 同一 Todo 并发开始执行只创建一个 active Mission，且所有 PlanStep 只物化一次的测试。
- active Mission 重试不新建 attempt；终态 Mission 后重开 Todo 创建新 attempt 并继续复用原群聊的测试。
- 关闭/删除一个 Mission attempt 不删除 Todo-purpose Conversation；删除 Conversation 不级联删除 Todo 历史 Mission 的测试。
- 综合 Todo 可创建无开发项目、无 workspace 的 Mission；默认 `general/manual` Todo 不创建 Mission 的测试。
- Agent run 没有有效 `MissionID + MissionTaskID` 时不能作为 Todo 执行启动的测试。
- MissionEvent 重复、乱序和旧版本不会回退 Todo 进度摘要的测试。
- 项目聚合查询按租户、对象关系和当前 attempt 过滤，历史 attempt 不重复计数的测试。
- 项目 Viewer 只能看摘要；Maintainer 能看 Mission Control 但不能读取 Discussion 或执行控制命令的测试。
- Scheduler/todo automation service principal 不是 Todo-origin Mission owner，不能借创建身份读取或控制任意 Mission 的测试。
- `MissionControlViewDTO` restricted Discussion、Mission stream、Timeline、ProgressGraph 和错误信息都不泄露聊天正文的测试。
- `creating` binding 在 outbox 重放后收敛到同一个 Mission，部分失败不会遗留第二个 Mission/群聊的测试。
- 两个 Todo 独立 worktree、同 Todo lease 冲突测试。
- Git 参数注入、路径穿越、symlink/junction 逃逸测试。
- 未提交活动工作树内容不会进入基线的测试。
- Agent 不能读取其他 workspace、mirror 和 Secret 的测试。
- Runtime 越界操作仍进入 HIL 的测试。
- 越界文件、Secret、submodule、symlink/junction 或超大文件不能被 staging/commit 的测试。
- Commit author/committer、签名、trailers、tree SHA 和 diff digest 的测试。
- 执行审批不能授权 push/merge，Agent 不能成为 merge approver 的测试。
- 同一 Todo 复用远端 branch 和 PR、新 commit 递增 Submission revision 的测试。
- 新 revision、PR head 或检查变化使旧 merge approval stale 的测试。
- ExpectedBaseHead 变化阻断 merge 并重新 preview 的并发测试。
- 相同 merge 幂等键只产生一个 Provider merge 和一个 receipt 的测试。
- Provider 返回 merge 成功但最终 tree 不匹配时 fail closed 的测试。
- 活动本地 checkout 永远不会被自动切换分支或写入的测试。
- 失败后保留记录、到期后安全清理的测试。
- 生产 server wiring 和 OpenAPI route 契约测试。

### 16.2 前端测试

- 导航和路由 Scope 测试。
- Viewer、Contributor、Maintainer、Owner 的 capability 渲染测试。
- 项目列表空态、错误态、分页和筛选测试。
- 创建开发项目、绑定仓库、成员管理表单测试。
- Todo 工作类型、开发项目和自动化模式表单测试。
- Todo 到唯一群聊的跳转测试。
- Todo 当前进度摘要、“打开任务控制台”和历史 attempts 跳转测试。
- 开发项目“执行监控”筛选、排序、受影响行实时刷新和 capability 测试。
- restricted Discussion 时 Mission Control 其余 Tab 可用且不渲染消息摘要的测试。
- Todo-origin Mission 的讨论链接使用绑定的 ConversationID，MissionRoom restricted 状态不会回退到含消息正文的通用 events 测试。
- workspace、diff、test、stale approval 和失败状态测试。
- Submission revision、commit 列表、检查结果、请求修改和 merge approval 测试。
- base/head 变化后合并按钮立即禁用的测试。
- 本地 source 无 remote 时只显示 patch/bundle 交付的测试。
- `npm run check` 与相关 Vitest 全部通过。

### 16.3 端到端验收场景

1. Owner 创建 Orrery 开发项目，Credential Manager 提供仓库连接，Contributor 创建开发 Todo。
2. 五分钟定时任务生成方案，Reviewer 审批后只创建一个 Mission，计划步骤出现在 MissionTask 看板中；Agent 在独立 worktree 修改、测试并创建签名 commit，所有过程在一个群聊中可追踪。
3. 两个 Todo 同时执行，分支和目录完全隔离，互不覆盖。
4. 日常手工 Todo 经过至少三次定时扫描后仍没有群聊、Mission 和审批记录；另一个显式开启 managed 的综合 Todo 可在无开发项目情况下进入 Mission Control。
5. 未加入 Todo 群聊的项目 Viewer 能看项目运行摘要但不能打开控制台；Maintainer 能打开控制台，但 Discussion 为空且读取群聊 API 返回未授权。
6. 审批后修改 Todo 或扩大命令范围，原审批失效，执行被阻止。
7. 撤销 Git 连接后，历史结果可读，新 fetch/push 被阻止且产生审计事件。
8. Contributor 提交 candidate branch 和 PR，Reviewer 请求修改，Agent 追加 commit；原合并审批失效且群聊不变。
9. 合并审批后目标分支发生变化，第一次 merge 被阻止；重新 preview 和审批后只合并一次并记录最终 SHA。
10. 执行失败后 retry 仍进入原 Mission；Todo 完成后重新打开则出现第二个历史 attempt，两次执行始终使用同一 Todo 群聊。

## 17. 审计、指标与运维

至少记录以下审计事件：

```text
development_project.created
development_project.updated
development_project.member_added
development_project.member_removed
development_project.repository_bound
scm_connection.created
scm_connection.rotated
scm_connection.revoked
todo.automation.claimed
todo.execution_plan.created
todo.execution_plan.approved
todo.execution_plan.stale
todo.mission.binding_created
todo.mission.bound
todo.mission.binding_failed
todo.mission.attempt_reopened
todo.execution_progress.projected
development_workspace.provisioned
development_workspace.leased
todo.execution.started
todo.execution.completed
todo.execution.failed
todo.commit.created
todo.commit.rejected
todo.code_submission.created
todo.code_submission.pushed
todo.pull_request.created
todo.pull_request.changes_requested
todo.merge_preview.created
development_merge.requested
development_merge.approved
development_merge.approval_stale
development_merge.completed
development_merge.verification_failed
```

关键指标：

- 每次扫描的 scanned、eligible、claimed、planned、skipped、failed。
- 待审批时长、计划到执行时长、执行成功率。
- active Mission 数、Mission 创建/任务物化失败率、`creating` binding 滞留数和 attempt 重开数。
- Todo 进度投影延迟、旧事件丢弃数、项目 stream 连接数和摘要刷新失败率。
- Mission Control restricted Discussion 命中数和被拒绝的群聊越权请求数；任何消息内容泄露告警数必须为 0。
- 活跃 workspace、磁盘占用、清理失败数、过期 lease 数。
- Git fetch/push 失败率和 Provider 授权失效率。
- candidate commit、Submission revision、PR checks、merge preview 和 merge 成功/失败率。
- 目标分支变化导致的 merge approval stale 数和 merge 幂等重放数。
- 重复计划/重复群聊冲突数，正常情况下应为 0。

日志和审计必须统一脱敏 token、credential、authorization header、remote URL 中的敏感查询参数和服务器绝对路径。

## 18. 迁移与上线策略

1. 先上线数据字段和授权模型，不展示页面，不启动代码执行。
2. 所有现有 Todo 回填 `work_kind=general`、`automation_mode=manual`；只有已经存在自动化状态的 Todo 按原状态回填 `suggest` 或 `managed`。
3. 运行 OpenFGA relation backfill dry-run，确认 missing/extra 后再写入；生产 enforce 前必须通过 readiness。
4. 上线开发项目页面和只读仓库 verify。
5. 上线 Todo-Mission binding 和只读进度投影，先 shadow-build 摘要并核对 Mission ReadModel，不启动新执行。
6. 启用 Todo 和开发项目监控 UI；先验证 Mission/Discussion 分离授权及 stream 脱敏。
7. 仅给内部 Orrery 开发项目开启 workspace provisioning，验证路径和并发策略。
8. 再开启 `managed` 审批后执行和本地候选 commit；此时禁止 push 和 merge。
9. 开启受控 Todo branch push、PR、merge preview 和独立人工 merge approval，先只用于内部 Orrery 项目。
10. 验证 SHA/CAS、branch protection、幂等 receipt 和回滚流程后，开放人工批准的 Provider merge。
11. 最后开放 GitHub/GitLab 自助连接；永远不默认开启无人工审批的自动 merge。

建议使用功能开关分别控制：

```text
development_projects.enabled
development_projects.workspace_provisioning_enabled
todo_execution.mission_binding_enabled
todo_execution.progress_projection_enabled
todo_automation.managed_execution_enabled
development_submission.push_enabled
development_submission.pull_request_enabled
scm_connections.self_service_enabled
development_merge.enabled
```

任一依赖未就绪时应显示明确不可用原因，不能返回假成功。

## 19. MVP 与暂缓项

第一版必须完成：

- 开发项目列表/详情/成员/仓库/工作区页面。
- 租户安全和完整权限矩阵。
- 一个可写 primary Git 仓库。
- `manual/suggest/managed` 三种 Todo 模式。
- 一个 Todo 一个群聊。
- Todo 获批执行后一个 active Mission，计划工作全部物化为 MissionTask。
- Todo 紧凑进度、历史 attempts、开发项目聚合监控和现有 Mission Control 跳转。
- Mission 查看权与 Todo 群聊查看权分离，组合 DTO 和 stream 不泄露消息。
- 结构化计划、绑定 digest 的审批。
- 一个 Todo 一个分支/worktree，支持多 Todo 并发。
- 审批后在同一群聊中开发、测试、受控 staging 并创建签名候选 commit。
- `TodoCommitRecord`、不可变 `TodoCodeSubmission` revision 和 diff/check 快照。
- 受控 push Todo branch、创建/更新一个 PR、请求修改并追加 commit。
- merge preview、独立人工合并审批、ExpectedBaseHead CAS 和最终 SHA/tree 核验。
- Provider 仓库支持人工批准后合并；本地活动 checkout 只交付 patch/bundle，不被自动写入。

可以暂缓：

- 多个可写仓库的原子提交。
- 自动解决 rebase/merge 冲突。
- 无人工合并审批的自动合并。
- Kubernetes/远程沙箱调度。
- 非 Git 代码仓库和大型二进制制品管理。
- 把所有通用 Project 资源自动继承给 Todo。

## 20. 最终完成定义

只有同时满足以下条件才算完成：

1. 开发项目是可见、可管理、权限清晰的一级页面。
2. 普通 Todo 不需要项目，也不会被五分钟定时任务制造无效群聊。
3. Scheduler 是唯一周期触发入口，空扫描不产生业务对话。
4. Todo 的提出人、负责人、Reviewer 和参与 Agent 能访问同一个群聊；其他项目成员不能越权访问。
5. Todo 获批执行后创建一个 active Mission，实际工作全部落为 MissionTask；重试与重开规则明确且可审计。
6. Todo 和开发项目能显示实时进度摘要，并能进入现有 Mission Control 查看任务、Evidence、QA、阻塞和时间线。
7. 项目级监控权不等于 Todo 群聊权；组合接口、Timeline 和 stream 均不能泄露无权消息。
8. 开发 Todo 能定位正确仓库、commit、源码子目录和测试命令。
9. 并发 Todo 使用独立分支/worktree，同一 Todo 有写 lease。
10. 用户批准的是确切计划和权限范围，任何扩大范围都会使审批失效或再次触发 HIL。
11. Agent、Scheduler、项目 Owner 都不能借流程读取 Secret 或继承 superadmin 权限。
12. 候选 commit 的文件范围、作者、签名、SHA、diff 和测试结果可验证。
13. 执行审批、提交权限和合并审批相互独立；Agent 不能合并默认分支。
14. 合并绑定确切 Submission revision、base/head/tree/diff；目标分支变化时旧审批自动失效。
15. 合并请求幂等，最终 Provider SHA/tree 被核验；回滚只能通过新的 Revert Todo 和 PR。
16. 所有关键操作有审计、指标、失败状态和可验证测试。
