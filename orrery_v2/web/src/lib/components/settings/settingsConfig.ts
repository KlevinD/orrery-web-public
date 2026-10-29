import { Bell, BrainCircuit, Cpu, Database, FileText, ImagePlus, Network, Palette, Settings as SettingsIcon, ShieldCheck } from 'lucide-svelte'
import type { TranslationKey } from '#lib/i18n'
import { gatewayChannelKeys } from '#lib/mappers/gateway-settings'
import { runtimeProviderSchemas, type RuntimeProviderKind } from '#lib/runtime/providerConfig'
import type { SettingsLLMProfileViewModel } from '#lib/types/view-models'

export type SettingsTab = 'general' | 'prompt' | 'runtime' | 'approval' | 'llm' | 'image' | 'knowledge' | 'gateway' | 'notifications' | 'appearance'
export type RuntimePanelMode = 'create' | 'edit'
export type LLMPanelMode = 'create' | 'edit'
export type GatewayChannelKey = (typeof gatewayChannelKeys)[number][0]
export type GatewayPrimaryFieldId = 'token' | 'apiKey' | 'webhookUrl'

export type GatewayPrimaryField = {
  id: GatewayPrimaryFieldId
  label: string
  help?: string
  helpKey?: TranslationKey
  placeholder?: string
  required?: boolean
  secret?: boolean
}

export type GatewayExtraFieldOption = {
  key: string
  label: string
  defaultValue?: string
  multiline?: boolean
  hidden?: boolean
  help?: string
  helpKey?: TranslationKey
  placeholder?: string
  options?: Array<{ label: string; value: string }>
}

export type GatewayFieldGroup = {
  title: string
  description?: string
  descriptionKey?: TranslationKey
  setupSteps?: string[]
  setupStepKeys?: TranslationKey[]
  setupLink?: { labelKey: TranslationKey; url: string }
  primaryFields: GatewayPrimaryField[]
  extraFields: GatewayExtraFieldOption[]
}

export { gatewayChannelKeys }

export const tabs: Array<{ id: SettingsTab; label: TranslationKey; icon: typeof SettingsIcon }> = [
  { id: 'general', label: 'settings.general', icon: SettingsIcon },
  { id: 'runtime', label: 'settings.runtime', icon: Cpu },
  { id: 'approval', label: 'settings.runtimeApprovals', icon: ShieldCheck },
  { id: 'llm', label: 'settings.llm', icon: BrainCircuit },
  { id: 'image', label: 'settings.image.title', icon: ImagePlus },
  { id: 'gateway', label: 'settings.gateway', icon: Network },
  { id: 'prompt', label: 'settings.promptBlocks', icon: FileText },
  { id: 'knowledge', label: 'settings.knowledge', icon: Database },
  { id: 'notifications', label: 'settings.notifications', icon: Bell },
  { id: 'appearance', label: 'settings.appearance', icon: Palette }
]

export const runtimeProviderOptionsBase = runtimeProviderSchemas.map((provider) => ({ label: provider.label, value: provider.kind }))

export const runtimeExecutableDefaults: Record<RuntimeProviderKind, string> = {
  managed: '',
  codex: 'codex',
  claude: 'claude',
  cursor: 'cursor-agent',
  gemini: 'gemini',
  openclaw: 'openclaw',
  hermes: 'hermes',
  pi: 'pi',
  acp: ''
}

export const llmProviderOptions = [
  { label: 'OpenAI', value: 'openai' },
  { label: 'Custom OpenAI Compatible', value: 'custom_openai' },
  { label: 'GLM', value: 'glm' },
  { label: 'DeepSeek', value: 'deepseek' },
  { label: 'Qwen', value: 'qwen' },
  { label: 'Moonshot', value: 'moonshot' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Gemini', value: 'gemini' },
  { label: 'Ollama', value: 'ollama' },
  { label: 'Azure OpenAI', value: 'azure_openai' }
]

export const llmProviderDefaults: Record<string, Partial<Pick<SettingsLLMProfileViewModel, 'baseUrl' | 'model' | 'temperature' | 'contextLength'>>> = {
  openai: { baseUrl: 'https://api.openai.com/v1', model: 'gpt-5', temperature: '0.2', contextLength: '16384' },
  custom_openai: { temperature: '0.2', contextLength: '16384' },
  glm: { baseUrl: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4.5', temperature: '0.2', contextLength: '16384' },
  deepseek: { baseUrl: 'https://api.deepseek.com/v1', model: 'deepseek-chat', temperature: '0.2', contextLength: '16384' },
  qwen: { baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', model: 'qwen-max', temperature: '0.2', contextLength: '16384' },
  moonshot: { baseUrl: 'https://api.moonshot.cn/v1', model: 'kimi-k2', temperature: '0.2', contextLength: '16384' },
  anthropic: { baseUrl: 'https://api.anthropic.com/v1', model: 'claude-sonnet-4', temperature: '0.2', contextLength: '16384' },
  gemini: { baseUrl: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-2.5-pro', temperature: '0.2', contextLength: '16384' },
  ollama: { baseUrl: 'http://localhost:11434/v1', model: 'llama3.1', temperature: '0.2', contextLength: '16384' },
  azure_openai: { temperature: '0.2', contextLength: '16384' }
}

export const generalToggles: Array<{
  key: 'autoCreateAgentProfile' | 'autoLinkEvidence' | 'hilDefaultEnabled' | 'memory.enabled'
  title: TranslationKey
  desc: TranslationKey
}> = [
  { key: 'autoCreateAgentProfile', title: 'settings.autoCreateAgentProfile', desc: 'settings.autoCreateAgentProfile.desc' },
  { key: 'autoLinkEvidence', title: 'settings.autoLinkEvidence', desc: 'settings.autoLinkEvidence.desc' },
  { key: 'hilDefaultEnabled', title: 'settings.hilDefaultEnabled', desc: 'settings.hilDefaultEnabled.desc' },
  { key: 'memory.enabled', title: 'settings.memoryEnabled', desc: 'settings.memoryEnabled.desc' }
]

export const customGatewayFieldKey = '__custom__'

export const genericGatewayFieldGroup: GatewayFieldGroup = {
  title: 'Generic Adapter',
  primaryFields: [
    { id: 'token', label: 'Token' },
    { id: 'apiKey', label: 'API Key' },
    { id: 'webhookUrl', label: 'Webhook URL' }
  ],
  extraFields: [
    { key: 'host', label: 'Host' },
    { key: 'port', label: 'Port' },
    { key: 'path', label: 'Path' }
  ]
}

export const gatewayChannelFieldGroups: Partial<Record<GatewayChannelKey, GatewayFieldGroup>> = {
  feishu: {
    title: 'Feishu',
    descriptionKey: 'settings.gatewayGuide.feishu.desc',
    setupStepKeys: [
      'settings.gatewayGuide.feishu.step1',
      'settings.gatewayGuide.feishu.step2',
      'settings.gatewayGuide.feishu.step3'
    ],
    setupLink: {
      labelKey: 'settings.gatewayGuide.openFeishuConsole',
      url: 'https://open.feishu.cn/app'
    },
    primaryFields: [
      { id: 'apiKey', label: 'App ID', required: true, helpKey: 'settings.gatewayField.feishu.appId' },
      { id: 'token', label: 'App Secret', required: true, secret: true, helpKey: 'settings.gatewayField.feishu.appSecret' }
    ],
    extraFields: [
      { key: 'encryptKey', label: 'Encrypt Key', hidden: true },
      { key: 'verifyToken', label: 'Verify Token', hidden: true },
      { key: 'defaultSessionLabel', label: 'Default Session', defaultValue: 'general', hidden: true },
      { key: 'connectionMode', label: 'Connection Mode', defaultValue: 'stream', hidden: true },
      {
        key: 'domainName',
        label: 'Platform',
        defaultValue: 'feishu',
        helpKey: 'settings.gatewayField.feishu.platform',
        options: [
          { label: 'Feishu', value: 'feishu' },
          { label: 'Lark', value: 'lark' }
        ]
      }
    ]
  },
  weixin: {
    title: 'Weixin iLink Bot',
    description: 'Orrery 后端直接调用微信 iLink Bot API 收发消息。系统设置不保存全局微信 Token，每个账号只能由用户在个人管理中扫码绑定。',
    setupSteps: [
      '管理员只负责启用微信 Gateway 和配置系统级连接参数；即使还没有个人账号，也可以先保存并启用。',
      '个人微信扫码、解绑和默认 Bot 选择统一在头像菜单 -> 个人管理完成。',
      '每个登录用户只能看到和管理自己的微信绑定；管理员不会看到微信用户 ID、默认 Bot、当前 Bot 或个人微信会话。',
      '如果接的是默认 iLink Bot/OpenClaw Weixin 协议，Login Base URL、iLink Base URL、botType、getupdates、sendmessage 保持默认值。',
      '微信里发送 /换人 会返回可用 Bot 列表；发送 /换人 botID 或 /换人 名称 可临时切换当前 Bot。',
      '没有个人绑定时只提示未注册/需绑定 Agent，不会自动转给其他 Agent。',
      '其余高级字段只有在微信/iLink 服务方明确要求时才改；不确定就保持默认。'
    ],
    primaryFields: [],
    extraFields: [
      {
        key: 'agentBindings',
        label: 'User-to-Agent Bindings',
        defaultValue: '{}',
        hidden: true,
        multiline: true,
        placeholder: '{\n  "user-1@im.wechat": "agent-staffing"\n}',
        help: '个人隐私绑定，由个人管理页写入；管理员设置页不显示、不编辑。'
      },
      { key: 'activeAgentBindings', label: 'Active Weixin Bot Bindings', defaultValue: '{}', hidden: true },
      { key: 'weixinUserBindings', label: 'Weixin User-to-Human Bindings', defaultValue: '{}', hidden: true },
      { key: 'weixinAccounts', label: 'Weixin Account Tokens', defaultValue: '{}', hidden: true },
      { key: 'loginBaseUrl', label: 'Login Base URL', defaultValue: 'https://ilinkai.weixin.qq.com', help: '扫码登录接口根地址。生产环境只接受微信官方 HTTPS 域名；通常保持默认。' },
      { key: 'ilinkBaseUrl', label: 'iLink Base URL', defaultValue: 'https://ilinkai.weixin.qq.com/ilink/bot', help: '微信 iLink Bot API 根地址。生产环境只接受微信官方 HTTPS 域名；通常保持默认。' },
      { key: 'botType', label: 'Bot Type', defaultValue: '3', help: '扫码登录时传给 get_bot_qrcode 的 bot_type。默认 3，与当前 OpenClaw Weixin 插件保持一致。' },
      { key: 'getUpdatesPath', label: 'Receive Path (getupdates)', defaultValue: 'getupdates', help: '收消息接口路径。默认会请求 <iLink Base URL>/getupdates；只有接入文档给了其他路径才改。' },
      { key: 'sendMessagePath', label: 'Send Path (sendmessage)', defaultValue: 'sendmessage', help: '发消息接口路径。默认会请求 <iLink Base URL>/sendmessage；Orrery 的 Agent 回复最终从这里发回微信。' },
      { key: 'channelVersion', label: 'Channel Version', defaultValue: '2.4.6', help: '写入请求体 base_info.channel_version。默认与当前 OpenClaw Weixin 插件版本一致；按微信/iLink 接入文档要求修改。' },
      { key: 'ilinkAppId', label: 'iLink App ID', defaultValue: 'bot', help: '请求头 iLink-App-Id。默认 bot；只有接入方明确给了其他 App ID 才改。' },
      { key: 'ilinkAppClientVersion', label: 'iLink Client Version', defaultValue: '132102', help: '请求头 iLink-App-ClientVersion。默认 132102，对应 2.4.6；不确定保持默认。' },
      { key: 'xWechatUin', label: 'X-WeChat-UIN', help: '请求头 X-WECHAT-UIN。留空时 Orrery 会在连接启动时生成并在该连接生命周期内复用；只有接入方要求固定 UIN 时才填。' },
      { key: 'skRouteTag', label: 'SK Route Tag', help: '写入 base_info.sk_route_tag 的路由/灰度标记。大多数环境留空。' },
      { key: 'statePath', label: 'State File Path', help: '后端状态文件路径，用来保存轮询 cursor 和已处理消息 ID。留空使用后端默认路径，例如 /var/lib/orrery/config/state/weixin-ilink-state.json；一般不用填。' },
      { key: 'contextTokens', label: 'Context Tokens', defaultValue: '8192', help: '传给 Orrery Agent 会话的上下文预算，不是微信 Token；不确定保持默认。' },
      { key: 'replyChunkSize', label: 'Reply Chunk Size', defaultValue: '1800', help: '微信单条回复过长时按这个字符数切分；不确定保持默认。' },
      { key: 'pollIntervalSeconds', label: 'Poll Interval Seconds', defaultValue: '2', help: '一次 getupdates 返回后等待几秒再轮询；默认 2 秒。' },
      { key: 'requestTimeoutSeconds', label: 'Request Timeout Seconds', defaultValue: '60', help: '调用微信/iLink HTTP API 的超时；默认 60 秒。' },
      { key: 'seenMessageLimit', label: 'Seen Message Limit', defaultValue: '5000', help: '持久化多少条已处理消息 ID 用于重启去重；默认 5000。' }
    ]
  },
  dingtalk: {
    title: 'DingTalk',
    descriptionKey: 'settings.gatewayGuide.dingtalk.desc',
    setupStepKeys: [
      'settings.gatewayGuide.dingtalk.step1',
      'settings.gatewayGuide.dingtalk.step2',
      'settings.gatewayGuide.dingtalk.step3'
    ],
    setupLink: {
      labelKey: 'settings.gatewayGuide.openDingTalkConsole',
      url: 'https://open-dev.dingtalk.com/fe/app'
    },
    primaryFields: [
      { id: 'apiKey', label: 'Client ID', required: true, helpKey: 'settings.gatewayField.dingtalk.clientId' },
      { id: 'token', label: 'Client Secret', required: true, secret: true, helpKey: 'settings.gatewayField.dingtalk.clientSecret' }
    ],
    extraFields: [
      { key: 'robotCode', label: 'Robot Code', helpKey: 'settings.gatewayField.dingtalk.robotCode' },
      { key: 'agentId', label: 'Agent ID', hidden: true }
    ]
  },
  email: {
    title: 'Email',
    primaryFields: [
      { id: 'apiKey', label: 'SMTP User' },
      { id: 'token', label: 'SMTP Password', secret: true }
    ],
    extraFields: [
      { key: 'smtpHost', label: 'SMTP Host' },
      { key: 'smtpPort', label: 'SMTP Port' },
      { key: 'smtpTLS', label: 'SMTP TLS', options: [{ label: 'STARTTLS', value: 'starttls' }, { label: 'Implicit TLS', value: 'implicit' }] },
      { key: 'from', label: 'From Address' }
    ]
  }
}
