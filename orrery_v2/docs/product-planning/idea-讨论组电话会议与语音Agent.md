# TODO: 讨论组电话会议与语音 Agent

调研日期：2026-06-07

## 背景

讨论组现在承担 Mission 作战室职责，文本消息、mention、reply、inbox 和 digest 是协作事实的入口。下一阶段可以给讨论组增加实时语音能力：用户用自然语言讲话，Agent 也用语音回复，同时把会议中的关键内容沉淀成可审计的讨论摘要、决策、阻塞和待响应项。

这里需要把三件事拆开：

1. 实时会议：多人 WebRTC 或电话会议，负责音频传输、静音、成员上下线、录音。
2. 语音 Agent：把某个 Agent 包装成会议参与者，能听、理解、调用工具，并用语音回答。
3. 讨论组沉淀：会议结束或关键节点把转写、摘要、决策、待办写回 Discussion，而不是把全量音频或全量转写直接塞进默认消息流。

## 推荐方案

先做“Web 内语音会议 + Agent 语音成员”，再补“真实电话号码拨入/拨出”。

第一阶段优先用 LiveKit / Agora / 腾讯 RTC 这一类 RTC 服务承载讨论组音频房间。Go 后端只签发房间 token、记录 `VoiceConferenceSession` 与 provider refs；Svelte 端负责加入房间、静音、成员状态、邀请 Agent。Agent 作为一个受控参与者加入房间，使用 OpenAI Realtime API 的 speech-to-speech 能力响应用户。会议内容只通过转写片段、摘要和人工确认后的决策写入讨论组。

第二阶段再接 PSTN/SIP，让外部用户可以通过电话号码进入讨论组。OpenAI Realtime 已支持 SIP 连接，官方文档建议用 Twilio 这类 SIP trunk provider 把电话号码流量转为 SIP；Twilio 也有 Conference 能力，适合需要传统电话会议号码的场景。

## 备选方案

### A. RTC 房间 + OpenAI Realtime 语音 Agent

优点：最贴合当前桌面/Web 产品；能支持多人讨论、前端成员状态、后续视频/屏幕共享；Agent 能作为组员出现。LiveKit 可云托管也可自托管，Agora/腾讯 RTC 在亚洲网络和移动端生态更成熟。

缺点：不是传统电话号码会议；如果要电话拨入，还要额外接 SIP/PSTN。

适合：Orrery 讨论组主场景、内部协作、桌面端用户、Agent 公司作战室。

### B. Twilio Conference + SIP/OpenAI Realtime

优点：真实电话号码、拨入拨出、会议控制、录音、回调事件成熟；Twilio Conference 单会议最多 250 人，支持 PSTN、SIP、Voice Client 入会。

缺点：电话费用、Conference participant-minute、录音/转写、Agent 推理费用会叠加；国内号码和合规需要另做供应商选择。

适合：客户会议、外部人员只会打电话、电话客服式 Agent。

### C. STT + LLM + TTS 拆分流水线

优点：可替换模型，成本可控，能用更便宜的 STT/TTS 或本地模型；会议转写和摘要链路更清楚。

缺点：端到端延迟通常高于 speech-to-speech；打断、语气、自然轮次、工具调用透明度需要自己拼。

适合：Agent 不需要频繁插话，只做会议纪要、阶段性总结、低成本旁听。

## 成本粗算

### OpenAI 语音 Agent

OpenAI `gpt-realtime-2` 官方模型页显示：文本 input $4 / 1M tokens、文本 output $24 / 1M tokens；音频 input $32 / 1M audio tokens、cached audio input $0.40 / 1M tokens、音频 output $64 / 1M audio tokens。OpenAI 2026-05-07 发布说明还列出 `GPT-Realtime-Whisper` 为 $0.017 / minute，`GPT-Realtime-Translate` 为 $0.034 / minute。

建议成本公式：

```text
voice_agent_cost =
  audio_input_tokens / 1_000_000 * 32
  + cached_audio_input_tokens / 1_000_000 * 0.40
  + audio_output_tokens / 1_000_000 * 64
  + text_input_tokens / 1_000_000 * 4
  + text_output_tokens / 1_000_000 * 24
```

注意：Realtime 语音不是稳定的“每分钟固定价格”，真实费用取决于说话时长、静音、输出长度、上下文、工具调用和缓存命中。MVP 必须记录 provider usage，按 discussion / mission / agent 汇总账单。

### WebRTC/RTC 会议

LiveKit Cloud 当前公开价格显示：WebRTC participant minutes 在 Build 计划含 5,000 分钟，Ship 计划含 150,000 分钟后 $0.0005 / min，Scale 计划含 1.5M 分钟后 $0.0004 / min；Agent session minutes 在 Build 含 1,000 分钟，Ship 含 5,000 分钟后 $0.01 / min，Scale 含 50,000 分钟后 $0.01 / min。LiveKit media server 和 Agents framework 也可自托管。

Agora Voice Calling 公开价格中，Cloud Proxy 自动模式免费；强制 UDP 或 TCP/TLS 443 的 voice proxy 是 $0.99 / 1000 participant minutes，并有 $500 最低月费规则。腾讯 RTC 国际站公开口径也给出 RTC/Call/Conference 音视频会议能力和每月 10,000 免费分钟；中文定价页显示音频时长约 $0.99 / 1000 minutes。

### 电话拨入/拨出

Twilio US voice 当前价格：本地电话拨出 $0.0140 / min，本地电话接入 $0.0085 / min，本地号码 $1.15 / month。Twilio Conference 前 100k 分钟 US 区域为 $0.0018 / participant-minute，录音 $0.0025 / recording-minute。

示例：5 个真人 + 1 个 Agent，30 分钟 US 电话会议，仅算 Twilio conference fee：

```text
6 participants * 30 min * $0.0018 = $0.324
```

如果 5 个真人都从 US local number 拨入，还要加：

```text
5 callers * 30 min * $0.0085 = $1.275
```

可选录音：

```text
30 min * $0.0025 = $0.075
```

以上还不含 OpenAI Realtime 语音 Agent、转写、摘要、号码月租、跨国电话、税费和供应商折扣。

## 产品边界

- 讨论组仍是协作事实入口；语音会议只是同步沟通层。
- 默认不把全量会议转写作为普通消息刷屏，只生成 `VoiceTranscriptSegment`、会议摘要、决策候选、阻塞候选和待响应 mention。
- Agent 默认不应一直听全场，优先用 push-to-talk、明确 `@Agent`、主持人点名、唤醒词或发言权控制，降低成本和误触发。
- 会议录音、转写和摘要必须有参会者可见的 consent 状态。
- 真人用户仍应映射为 `RealHumanAgent` 或 user/member ref，Agent 语音回复必须走与文本 Agent 一致的权限、工具和审计边界。

## 技术拆解 TODO

### 领域模型

- [ ] 在 discussion 语义中补充 `VoiceConferenceSession`、`VoiceParticipant`、`VoiceTranscriptSegment`、`VoiceAgentTurn`、`VoiceConferenceSummary`。
- [ ] Session 只保存 provider room/call refs、状态、参与者、录音/转写 artifact refs、usage refs；不保存本地绝对路径。
- [ ] 定义 `voice_session.started`、`voice_participant.joined`、`voice_agent.turn_completed`、`voice_summary.posted` 等事件候选。
- [ ] 明确会议摘要进入 `DiscussionDigest` 的压缩规则。

### 应用服务与接口

- [ ] 增加 `VoiceConferenceService`：`CreateSession`、`IssueJoinToken`、`InviteAgent`、`DialOut`、`EndSession`、`PostSummary`。
- [ ] provider adapter 走接口隔离：`RTCProvider`、`TelephonyProvider`、`VoiceAgentProvider`。
- [ ] OpenAPI 增加 `/api/discussions/{id}/voice-sessions`、`/join-token`、`/invite-agent`、`/end`。
- [ ] WebSocket patch 增加 voice session 状态、participant 状态、agent speaking 状态。

### 前端

- [ ] 在 `DiscussionPanel` 增加电话/语音会议入口、入会状态、静音、离开、成员列表。
- [ ] 支持邀请某个 Agent 进入会议，并显示 Agent 正在听、正在思考、正在说话。
- [ ] 会议结束后展示摘要草稿，允许用户确认后写入讨论组。
- [ ] 预算提示：预计 participant-minutes、Agent 语音使用量、录音/转写开关。

### Agent Runtime

- [ ] 给 Agent profile 增加 voice capability：voice model、voice name、turn policy、interrupt policy、cost cap。
- [ ] Realtime session instructions 必须注入 DiscussionDigest、当前 Mission 目标、Agent 角色、可调用工具和权限边界。
- [ ] Agent 语音输出后生成可审计文本摘要，作为普通 discussion message 或 reply 的来源。
- [ ] 工具调用仍走现有 permission / approval / audit，不允许语音绕过文本通道的权限模型。

### 成本与风控

- [ ] 每个 voice session 记录 provider usage：participant minutes、agent session minutes、OpenAI audio/text tokens、STT/TTS minutes、recording minutes。
- [ ] 设置 discussion / mission / workspace 级预算上限，超过时自动禁用 Agent 常听或要求确认。
- [ ] 给 Agent 增加 `voice_cost_profile`，用于调度时评估是否允许语音参与。
- [ ] 记录用户 consent、录音保留期限、转写可见性、删除策略。

### 验收

- [ ] 3 个真人浏览器加入同一个 discussion voice session。
- [ ] 1 个 Agent 被邀请入会，能听到用户自然语言并语音回复。
- [ ] 用户 `@Agent` 后才触发 Agent 响应；普通闲聊不触发或仅转写。
- [ ] 会议结束生成摘要草稿，确认后写回 discussion message。
- [ ] 预算计量能显示本次会议的 participant-minutes 和 Agent usage。
- [ ] 非讨论组成员不能加入会议或获取 join token。
- [ ] Agent 语音工具调用仍触发审批与审计。

## 未决问题

- 首选供应商：LiveKit、Agora、腾讯 RTC、Twilio，还是先抽象接口并本地 fake adapter？
- 第一阶段是否必须支持真实电话号码，还是先只支持应用内语音房间？
- Agent 是默认旁听，还是必须被点名或 `@mention` 后才接收音频？
- 是否需要多人说话人分离和逐字稿，还是只做会议摘要与关键决策？
- 中国大陆号码、录音告知、跨境数据和企业合规是否属于首批范围？

## 参考资料

- OpenAI Realtime conversations: https://developers.openai.com/api/docs/guides/realtime-conversations
- OpenAI Realtime SIP: https://developers.openai.com/api/docs/guides/realtime-sip
- OpenAI `gpt-realtime-2` model pricing: https://developers.openai.com/api/docs/models/gpt-realtime-2
- OpenAI 2026-05-07 voice models announcement: https://openai.com/index/advancing-voice-intelligence-with-new-models-in-the-api/
- LiveKit pricing: https://livekit.com/pricing
- Twilio Conference pricing: https://www.twilio.com/en-us/voice/conference/pricing
- Twilio US Voice pricing: https://www.twilio.com/en-us/voice/pricing/us
- Twilio Conference docs: https://www.twilio.com/docs/voice/twiml/conference
- Agora Voice Calling pricing: https://www.agora.io/en/pricing/voice-calling
- Tencent RTC product/pricing overview: https://www.tencentcloud.com/product/trtc
