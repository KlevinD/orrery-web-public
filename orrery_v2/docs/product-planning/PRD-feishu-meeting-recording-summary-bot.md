# PRD：飞书会议自动录制与纪要机器人

状态：Draft
调研日期：2026-07-26
适用主线：`orrery_v2/` 服务端与 SaaS Web

## 1. Summary

本功能允许用户通过 Orrery 的自建飞书机器人创建“自动纪要会议”。会议由飞书开放平台预约并开启云端自动录制；会议结束并完成录制处理后，Orrery 下载云录制媒体，执行语音识别和纪要整理，再把摘要、决策、行动项和原文入口写回发起会议的飞书会话及 Orrery 工作上下文。

飞书开放平台目前没有让自建应用机器人作为音频参会人自动加入会议的公开接口。因此 V1 中“机器人入会”是产品形象，不是飞书参会人列表中的真实媒体参与者。机器人负责创建会议、开启自动录制、跟踪状态和发布纪要；自动录制不要求机器人实际入会。

## 2. Contacts

| 角色 | 负责人 | 责任 |
| --- | --- | --- |
| 产品负责人 | 待指定 | 范围、体验、上线标准 |
| 服务端负责人 | 待指定 | 飞书会议、事件、下载和任务流水线 |
| AI 负责人 | 待指定 | ASR、说话人处理、纪要质量 |
| 安全与合规负责人 | 待指定 | 录音告知、数据保留、租户隔离 |
| 飞书管理员 | 各接入租户管理员 | 应用权限、版本发布、云录制权益 |

## 3. Background

### 3.1 用户问题

团队已经使用飞书会议，并拥有云录制权限，但没有购买飞书 AI 妙记能力。用户希望：

1. 从飞书机器人发起会议。
2. 会议开始后自动云录制，不依赖任何人的本地录音。
3. 会议结束后自动获得可检索、可追溯的纪要。
4. 将纪要写回飞书，并沉淀到 Orrery 的讨论、知识或任务上下文。

### 3.2 已确认的飞书能力

- `POST /open-apis/vc/v1/reserves/apply` 可以预约多人会议。
- 预约参数 `meeting_settings.auto_record=true` 可以开启自动录制；1v1 呼叫不支持该参数。
- Open API 预约的会议会产生 `meeting_started`、`meeting_ended` 和 `recording_ready` 事件。
- `recording_ready` 发生后可以获取录制 URL。该 URL 指向 `/minutes/{minute_token}` 资源。
- `GET /open-apis/minutes/v1/minutes/{minute_token}/media` 返回有效期为 1 天的音视频下载地址。
- 下载媒体只要求录制文件导出权限，不要求飞书 AI 摘要权限。平台可以自行完成 ASR 和纪要整理。

### 3.3 已确认的边界

- 自建飞书机器人没有公开的“加入会议并接收媒体流”接口。
- “邀请参会人”接口只负责邀请用户，不会让应用机器人自动建立媒体连接。
- `recording/start` 接口当前只支持 `user_access_token`，操作者必须是会中主持人，不适合作为 V1 的无人值守主链路。
- V1 只保证处理由 Orrery 通过 Open API 创建的会议。任意已有会议的自动接管属于后续能力。
- 飞书会议结束不等于录制文件可下载。流水线必须等待 `recording_ready`，不能在 `meeting_ended` 后立即下载。

### 3.4 产品决策

V1 将“自定义机器人入会”改写为“自定义机器人托管会议”：

```text
机器人创建会议并绑定发起会话
  -> 飞书自动录制
  -> Orrery 接收会议与录制事件
  -> 下载云录制
  -> 自有 ASR 和纪要整理
  -> 机器人发布纪要
```

若业务必须让机器人真实出现在参会人列表中、实时听会或发言，需要另外验证飞书 SIP/会议互通、Rooms 或专用真人账号客户端。该路线不属于 V1，也不能用当前 Open API 承诺。

### 3.5 平台能力与机器人策略边界

该功能按“平台级能力、机器人级策略”实现，不把会议流水线硬编码到某一个机器人：

- 平台统一拥有会议状态机、事件幂等、录制下载、ASR、纪要生成、保留期和审计能力。
- 每个飞书应用账号单独配置是否启用、允许的群和发起人、纪要 Agent、发布目标、媒体保留期和用量上限。
- V1 产品入口可以只展示一个“会议纪要机器人”，但它只是首个启用该策略的账号，不是后端能力边界。
- 普通聊天机器人继续使用最小消息权限；只有显式启用会议自动化的应用才申请 VC 和录制导出权限并订阅会议事件。
- 触发语义是“该机器人代表平台创建托管会议”，不是应用机器人作为媒体参会人真正入会。

## 4. Objective

### 4.1 目标

让团队无需飞书 AI 妙记会员，也能对 Orrery 发起的飞书云会议完成自动录制、下载、识别、整理和回写。

### 4.2 业务价值

- 降低人工开录制、下载、转写和整理会议的操作成本。
- 把会议决策和行动项连接到 Orrery 的讨论、Mission、Todo 与知识库。
- 保留从纪要结论到转写时间戳和原始会议的证据链。
- 将转写模型和纪要模板掌握在平台侧，避免依赖单一付费 AI 产品。

### 4.3 Key Results

- 内测期内，符合条件的 Open API 多人会议自动录制成功率达到 95% 以上。
- 60 分钟以内的会议，90% 在 `recording_ready` 后 10 分钟内生成纪要草稿。
- 事件重复投递不产生重复会议、重复下载或重复纪要。
- 所有录制、转写和纪要查询都通过服务端租户与资源授权检查，跨租户泄露为 0。
- 原始媒体按配置删除；默认在转写成功后删除，最长保留不超过 24 小时。

### 4.4 Non-goals

- 不让机器人在 V1 中成为真实音频参会人。
- 不提供实时旁听、实时字幕或实时语音 Agent。
- 不绕过飞书的云录制权限、会议主持人权限或录音告知机制。
- 不自动接管所有企业会议或用户自行创建的历史会议。
- 不默认把行动项直接转成已生效任务；自动提取后先生成候选项。
- 不长期保存原始音视频文件。

## 5. Market Segments

### 5.1 项目负责人

当项目负责人召开周会、评审会或复盘会时，希望会后自动获得决策和待办，不再安排专人记录。

### 5.2 飞书群协作团队

当团队从群聊发起会议时，希望纪要自动回到原群，并能追溯到会议、发言时间和责任人。

### 5.3 Orrery Mission 团队

当 Mission 团队需要同步讨论时，希望会议结论进入 Discussion 和知识上下文，但不把整份逐字稿刷进普通消息流。

### 5.4 企业管理员

管理员需要控制哪些群、用户和会议可以自动录制，了解数据保存位置，并可随时停用连接或删除产物。

## 6. Value Propositions

| 用户任务 | 当前痛点 | 本功能提供的价值 |
| --- | --- | --- |
| 创建需要纪要的会议 | 需要手动建会、提醒主持人开录制 | 机器人创建会议并设置 `auto_record=true` |
| 会后获取录制 | 需要等待并人工下载 | `recording_ready` 后自动拉取临时下载地址 |
| 生成逐字稿 | 飞书 AI 妙记需要额外权益 | 使用平台自有或自托管 ASR |
| 整理结论 | 人工整理慢，格式不统一 | 固定生成摘要、决策、行动项和开放问题 |
| 回到工作上下文 | 录制与任务、项目脱节 | 自动绑定飞书会话和 Orrery Discussion/Mission |
| 管理敏感数据 | 下载链接和音视频容易扩散 | 短时处理、加密存储、审计和自动删除 |

## 7. Solution

### 7.1 UX and User Flows

#### Flow A：从飞书机器人创建会议

1. 用户在机器人私聊或允许的群聊中点击“创建自动纪要会议”。
2. 用户填写会议主题、预约有效期和会议归属人。
3. 机器人创建多人会议，设置 `auto_record=true`。
4. 机器人发送会议卡片，展示会议链接、归属人和“将自动录制”状态。
5. 第一位真人加入后，飞书按预约设置开始云录制。
6. 会议结束后，卡片状态变为“等待录制文件”。
7. 收到 `recording_ready` 后，卡片状态变为“正在识别和整理”。
8. 纪要完成后，机器人发布纪要卡片，并提供查看全文和处理行动项的入口。

#### Flow B：从 Orrery 创建会议

1. 用户在 Discussion 或 Mission 中选择“创建飞书纪要会议”。
2. Orrery 创建飞书预约，并保存源 Discussion/Mission 引用。
3. 会议链接回写到当前上下文，也可由机器人发送到指定飞书会话。
4. 后续录制、识别和纪要流程与 Flow A 相同。

#### 纪要默认结构

```markdown
## TL;DR

## 决策

## 行动项
- [ ] 内容 — 负责人：未识别/姓名 — 截止时间：未提及

## 开放问题

## 关键上下文

## 来源
- 飞书会议
- 会议时间
- 可定位的转写时间戳
```

行动项作为候选显示。用户确认后，才创建 Orrery Todo 或 Mission Task。

### 7.2 Key Features

#### F1. 飞书会议自动化配置

- 租户管理员选择已注册的飞书自建应用。
- 系统检查应用权限和事件订阅是否齐全。
- 管理员配置允许发起会议的群、用户、Orrery 角色和工作范围。
- 管理员配置媒体保留时间、ASR 提供方、默认语言和纪要发布策略。
- 配置停用后不再创建会议或消费新事件，但保留已有审计记录。

#### F2. 机器人创建会议

- 使用 `tenant_access_token` 调用预约接口。
- `owner_id` 必须是同租户真实飞书用户，不能使用机器人身份。
- 会议类型固定为多人会议。
- 强制设置 `meeting_settings.auto_record=true`。
- 保存 `tenant_id`、`source_chat_id`、`reserve_id`、`owner_open_id` 和业务上下文引用。
- 创建失败时返回飞书原始错误的安全映射，不泄露 Token 或请求细节。

#### F3. 事件驱动状态跟踪

- 消费 `vc.meeting.meeting_started_v1`。
- 消费 `vc.meeting.meeting_ended_v1`。
- 消费 `vc.meeting.recording_ready_v1`。
- 使用飞书 `event_id` 做幂等。
- 允许事件乱序：先到达 `recording_ready` 时也能补齐会议状态。
- `meeting_ended` 只将状态改为 `awaiting_recording`，不触发媒体下载。

#### F4. 云录制下载

- 从 `recording_ready` 的 URL 中使用 URL 解析器提取 24 字符 `minute_token`。
- 必要时通过会议 ID 调用录制信息接口补查。
- 调用妙记媒体接口获取有效期为 1 天的 `download_url`。
- 下载任务必须立即进入平台现有 execution/queue/liveness 体系。
- 下载器限制协议、飞书域名、重定向次数、文件大小、内容类型和超时，防止 SSRF 与资源耗尽。
- 下载 URL、Bearer Token 和媒体内容不得写入日志。

#### F5. 音频识别

- 下载文件可能是视频容器；媒体工作器提取单声道音轨后交给 ASR。
- 首选可自托管的中文 ASR，例如 FunASR/SenseVoice；保留云 ASR provider 接口。
- 识别结果至少包含文本片段、开始时间和结束时间。
- 说话人信息不是 V1 强依赖；模型无法可靠区分时标记为“未知发言人”，不得编造姓名。
- ASR 失败可重试；模型错误和不可识别音频进入人工重试状态。

#### F6. 纪要整理

- 复用现有 Summarizer 输出摘要、决策、行动项和开放问题。
- 所有结论保留对应的转写片段或时间戳引用。
- 不把讨论内容误标为已决定事项。
- 不为行动项编造负责人和期限。
- 超长会议按时间窗口切片，再生成全局汇总。

#### F7. 发布与沉淀

- 机器人向原飞书会话发送纪要卡片。
- Orrery 中保存结构化纪要和可检索转写，不把全量转写作为 Discussion 普通消息刷屏。
- 绑定了 Discussion/Mission 时，生成 `VoiceConferenceSummary` 或等价会议产物引用。
- 行动项确认后才进入 Todo/Task，并记录确认人和来源。
- 发布失败可以重试，但同一版本不得重复发送。

#### F8. 管理与审计

- 管理员可查看会议状态、失败阶段、重试次数、媒体删除时间和纪要发布记录。
- 用户可删除转写和纪要；删除操作进入审计日志。
- OAuth/App 凭证撤销后停止所有后续调用。
- 原始媒体默认在转写成功后删除；删除失败持续告警并重试。

### 7.3 Technology

#### 推荐架构

```text
Feishu Bot / Orrery Web
  -> Meeting Automation Service
  -> Feishu VC Reserve API (auto_record=true)

Feishu Event Stream/Webhook
  -> Event verifier + idempotency ledger
  -> Meeting state projection
  -> recording_ready job

recording_ready
  -> Recording Resolver
  -> Minutes Media API
  -> controlled media downloader
  -> transient object storage
  -> ASR provider
  -> transcript store
  -> Summarizer
  -> Feishu card + Orrery meeting artifact
```

#### 飞书 API 矩阵

| 阶段 | 接口或事件 | 主要用途 |
| --- | --- | --- |
| 创建 | `POST /open-apis/vc/v1/reserves/apply` | 创建多人会议，设置 `auto_record=true` |
| 查询活跃会议 | `GET /open-apis/vc/v1/reserves/{reserve_id}/get_active_meeting` | 从预约补查 `meeting_id` |
| 开始事件 | `vc.meeting.meeting_started_v1` | 记录会议开始；仅 Open API 预约会议 |
| 结束事件 | `vc.meeting.meeting_ended_v1` | 标记等待录制；仅 Open API 预约会议 |
| 录制就绪事件 | `vc.meeting.recording_ready_v1` | 获取录制 URL、会议 ID 和时长 |
| 录制补查 | `GET /open-apis/vc/v1/meetings/{meeting_id}/recording` | 事件丢失或恢复时补查录制信息 |
| 媒体下载 | `GET /open-apis/minutes/v1/minutes/{minute_token}/media` | 获取有效期 1 天的下载地址 |

#### 最小飞书权限

| 权限 | 用途 | V1 是否需要 |
| --- | --- | --- |
| `vc:reserve` | 创建会议预约 | 必需 |
| `vc:reserve:readonly` | 查询预约和活跃会议 | 必需 |
| `vc:meeting:readonly` 或 `vc:meeting.meetingevent:read` | 接收本应用会议事件 | 必需 |
| `vc:record:readonly` | 补查录制信息 | 必需 |
| `minutes:minutes.media:export` | 获取云录制媒体下载地址 | 必需 |
| `vc:record` | 主持人在会中手动调用开始录制 | V1 不需要 |
| `vc:meeting.all_meeting:readonly` | 监听企业所有会议 | V1 禁止申请 |
| `contact:user.employee_id:readonly` | 使用 `user_id` 字段 | 可通过 `open_id` 避免 |

新应用不得依赖已经停止开放的 `minutes:minute:download`，统一申请 `minutes:minutes.media:export`。

#### 状态机

```text
creating
  -> reserved
  -> in_progress
  -> awaiting_recording
  -> recording_ready
  -> downloading
  -> transcribing
  -> summarizing
  -> draft_ready
  -> published

任意处理阶段
  -> retry_wait
  -> failed_terminal
  -> deleted
```

飞书事件只能推进外部会议事实，不能覆盖已经完成的后续状态。例如，迟到的 `meeting_ended` 不得把 `summarizing` 回退为 `awaiting_recording`。

#### 核心数据

`FeishuMeetingAutomation`：

- Orrery tenant ID、创建人和来源上下文。
- 飞书 tenant key、app/account ID、source chat ID。
- `reserve_id`、`meeting_id`、`minute_token`。
- 会议归属人、主题、开始/结束时间、录制时长。
- 当前状态、失败代码、下次重试时间和版本。

`MeetingArtifact`：

- transcript artifact ref、summary artifact ref。
- ASR provider/model/version 和 Summarizer 版本。
- 决策、行动项、开放问题及时间戳证据引用。
- 原始媒体临时引用、保留期限和删除状态。

不得保存本地绝对路径、长期下载 URL、飞书 access token 或原始事件中的秘密字段。

#### Orrery 服务边界

- 会议自动化属于 `orrery_v2/` 服务端，不落入维护模式的 Tauri 客户端。
- 复用现有飞书应用注册、消息 Gateway、事件校验、平台队列、审计和租户授权能力。
- 飞书 SSO 与会议连接器继续保持独立；登录授权不等于会议与媒体授权。
- Feishu adapter 只负责协议映射，领域状态和重试规则放在应用服务中。
- API 类型变更同步更新 `docs/api/mission-control.openapi.yaml` 并重新生成 Web 类型。

#### 建议内部接口

- `POST /api/meetings/feishu/reservations`
- `GET /api/meetings/{meeting_id}`
- `POST /api/meetings/{meeting_id}/retry`
- `DELETE /api/meetings/{meeting_id}/artifacts`
- `POST /api/meetings/{meeting_id}/action-items/{item_id}/confirm`
- `GET/PATCH /api/settings/integrations/feishu/meeting-automation`

所有接口必须从认证 Principal 和服务端绑定关系推导 tenant，不接受客户端自由指定外部 tenant key。

#### 错误处理

- `121002 not support`：标记租户或会议能力不支持，停止自动重试。
- `121005 no permission`：标记权限配置错误，提示管理员检查应用版本和资源归属。
- `122001 meeting status unexpected`：若使用手动开始录制，重新检查会议状态；V1 主链路不调用该接口。
- `124002 record processing`：录制仍在生成，指数退避后补查。
- `2091003 minute not ready`：媒体尚未准备好，指数退避重试。
- `2091005 permission deny`：停止重试并要求管理员检查录制文件阅读/导出权限。
- 下载地址过期：重新调用 media API 获取新地址，不复用旧地址。

#### 安全与合规

- 会议卡片必须明确显示“本会议将自动录制并生成纪要”。
- 依赖飞书会中录制提示，但平台仍保存录制政策版本和发起人确认记录。
- App Secret、access token 和 refresh token 使用平台 Secret Store 或加密凭证库。
- 媒体下载、转写、纪要和发布日志全部带 tenant、actor、meeting 和 trace ID。
- 下载器必须阻止任意 URL、内网地址和无限重定向。
- 转写和纪要访问权不得仅由前端隐藏，必须服务端执行租户和资源授权。
- 外部参会人不会自动获得 Orrery 纪要访问权；分享需要显式授权。

### 7.4 Assumptions and Validation Risks

#### 已验证假设

- 目标租户拥有飞书云录制能力。
- 飞书预约 API 支持多人会议 `auto_record=true`。
- 云录制可通过 Minutes media API 获取临时下载地址。
- Open API 预约会议会产生录制就绪事件。

#### 必须在开发前验证

1. 目标租户在没有飞书 AI 妙记会员时，是否仍允许应用申请并调用 `minutes:minutes.media:export`。
2. `tenant_access_token` 创建的预约，其人类 `owner_id` 是否可稳定获得并导出录制资源。
3. 实际下载文件的容器、编码、最大体积和长会议处理时间。
4. 自动录制时飞书客户端向所有参会人的提示表现是否满足企业合规要求。
5. 飞书机器人卡片所在群与 Orrery Discussion/Mission 的绑定策略。

#### 主要风险

- **能力风险**：企业拥有云录制，但开放平台的媒体导出权限仍可能需要管理员审批或额外产品授权。
- **概念风险**：用户可能把“机器人入会”理解为可见、可听、可说的真实参会人。产品文案必须明确 V1 是托管会议。
- **权限风险**：会议归属人、应用身份和录制文件 ACL 不一致会导致下载失败。
- **时效风险**：下载 URL 仅有效 1 天，任务积压会导致重复取址。
- **成本风险**：长会议的媒体存储、转码和 ASR 计算可能超过预期。
- **质量风险**：多人重叠发言、方言和远场音频会降低 ASR 与行动项负责人识别准确率。

## 8. Release

### Phase 0：可行性 Spike

用一个真实企业自建应用和测试租户完成端到端实验：

1. 用 `tenant_access_token` 和人类 `owner_id` 创建多人预约。
2. 设置 `auto_record=true`。
3. 两名真人入会并正常结束会议。
4. 收到 `recording_ready`。
5. 提取 `minute_token` 并获取 media 下载 URL。
6. 下载媒体，确认格式、大小和音轨。
7. 使用目标 ASR 生成带时间戳的中文转写。

只有该 Spike 成功，V1 才进入实现。失败时记录具体飞书错误码和所缺产品权益，不用 UI 自动化或非官方录制方式绕过。

### V1：机器人托管会议

- 飞书机器人创建自动录制会议。
- 事件驱动状态机和幂等处理。
- 云录制下载、临时保存和自动删除。
- 单一 ASR provider。
- 固定纪要模板。
- 纪要回写原飞书会话。
- 管理员状态页、失败原因和人工重试。

预计为一个中等规模迭代，先在单租户、少量内部群中灰度。

### V1.1：Orrery 工作上下文集成

- 从 Discussion/Mission 创建会议。
- 结构化会议产物进入知识库。
- 行动项确认后创建 Todo/Task。
- 多 ASR provider、成本和用量统计。
- 纪要版本与人工修订。

### Future

- 对用户自行创建的会议提供显式绑定和导入。
- 在获得企业级权限后评估全企业会议事件，但不默认启用。
- 验证 SIP/会议互通，使语音 Agent 成为真实参会方。
- 实时字幕、实时 Agent 旁听和发言。
- 多语言识别、说话人声纹映射和实时成本控制。

### V1 Acceptance Criteria

- [ ] 用户可通过允许的飞书机器人创建多人自动录制会议。
- [ ] 飞书会议归属人为同租户真人用户，机器人不冒充用户或主持人。
- [ ] 会议无需机器人真实加入也能自动录制。
- [ ] 重复和乱序事件不会产生状态回退或重复产物。
- [ ] 录制就绪后平台能在下载 URL 过期前拉取媒体。
- [ ] 一场 30 分钟中文测试会议能生成带时间戳转写和固定结构纪要。
- [ ] 纪要能够回写原飞书会话，并链接到对应 Orrery 产物。
- [ ] 非源租户和未授权用户不能查询媒体、转写或纪要。
- [ ] 原始媒体按配置自动删除，删除结果可审计。
- [ ] 缺权限、录制处理中、媒体未就绪和 ASR 失败均有明确状态与恢复路径。

### Rollout and Rollback

- 首批仅允许管理员指定的测试群和用户。
- 每个租户设置并发会议数、单文件大小、单日 ASR 分钟和媒体保留上限。
- 提供租户级总开关；关闭后停止创建新会议和消费新任务，但允许完成已进入删除阶段的清理任务。
- 出现权限越界、凭证泄露或媒体无法按期删除时立即停止下载和发布，并保留审计证据。

## 9. Backend self-check (2026-07-26)

当前代码是可编译、可测试的后端基础模块，不是已经可用的端到端后端。飞书会议相关实现统一放在 `internal/gateway/channel/feishu/meeting_*.go`；`cmd/server/platform_modules.go` 只保留平台迁移注册这一处组合根接线。飞书 SSO、HTTP 路由和 Gateway 配置属于各自基础设施边界，不复制业务实现。

### 已实现并通过测试

- [x] 机器人级策略、租户/账号/群/用户授权校验。
- [x] 会议会话持久化、幂等创建、乐观并发和单调状态转换。
- [x] 调用 `reserves/apply` 创建多人会议并强制 `auto_record=true`。
- [x] `meeting_started`、`meeting_ended`、`recording_ready` 事件解析和持久队列去重；通过 `meeting_no` 在开始事件丢失或乱序时补绑会议。
- [x] 从录制 URL 安全提取 `minute_token`，调用 Minutes media API 获取临时下载地址。
- [x] 普通聊天机器人与会议机器人权限分离。

### 尚未实现，不能宣称后端完成

- [ ] 生产 Gateway 尚未构造并注入会议服务；当前默认注册仍使用普通 `NewAdapter`。
- [ ] 尚无策略管理 API、管理员授权和机器人“创建自动纪要会议”命令/卡片。
- [ ] 录制任务尚未接入平台 execution queue，也没有消费 worker。
- [ ] 尚无受控媒体下载器、媒体格式验证、音轨提取、ASR provider 和用量限制。
- [ ] 尚无纪要 Agent 调用、飞书/Orrery 发布、失败重试、媒体清理与删除审计。
- [ ] 尚未用真实飞书租户完成 Phase 0 Spike，接口权限和“无妙记会员可否导出媒体”仍需实测。

因此，当前完成度应定义为“Feishu meeting backend foundation”，不能定义为“backend complete”。下一步必须先完成真实租户 Spike，再接生产 wiring 和 execution worker；在此之前不开放 UI 入口。

## References

- 飞书预约会议：https://open.feishu.cn/document/server-docs/vc-v1/reserve/apply
- 飞书会议开始事件：https://open.feishu.cn/document/server-docs/vc-v1/meeting/events/meeting_started
- 飞书会议结束事件：https://open.feishu.cn/document/server-docs/vc-v1/meeting/events/meeting_ended
- 飞书录制完成事件：https://open.feishu.cn/document/server-docs/vc-v1/meeting/events/recording_ready
- 飞书获取录制文件：https://open.feishu.cn/document/server-docs/vc-v1/meeting-recording/get
- 飞书下载妙记音视频：https://open.feishu.cn/document/minutes-v1/minute-media/get
- Orrery 讨论组电话会议与语音 Agent 调研：`docs/product-planning/idea-讨论组电话会议与语音Agent.md`
- Orrery 飞书 SSO 边界：`docs/feishu-sso.md`
