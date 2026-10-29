import type {
  GatewayAdaptersConfigDTO,
  GatewayConfigDTO,
  GatewayFeishuAccountDTO,
  GatewayFeishuAdapterDTO,
  GatewayPlatformAdapterDTO,
  GatewayWeixinAdapterDTO,
  GatewaySettingsDTO,
  GatewaySettingsRawUpdateRequest,
  GatewaySettingsUpdateRequest
} from '#lib/types/api'
import type {
  GatewayChannelSettingsViewModel,
  GatewayExtraFieldViewModel,
  GatewayFeishuAccountViewModel,
  GatewaySettingsViewModel
} from '#lib/types/view-models'

export const gatewayChannelKeys = [
  ['feishu', 'feishu', 'Feishu'],
  ['weixin', 'weixin', 'Weixin'],
  ['dingtalk', 'dingtalk', 'DingTalk'],
  ['email', 'email', 'Email']
] as const

export const productionGatewayChannelKeys = gatewayChannelKeys.filter(
  ([frontendKey]) => frontendKey === 'feishu' || frontendKey === 'weixin' || frontendKey === 'dingtalk'
)

type FrontendChannelKey = (typeof gatewayChannelKeys)[number][0]
type BackendChannelKey = Exclude<(typeof gatewayChannelKeys)[number][1], 'feishu'>

export function mapGatewaySettingsView(dto: GatewaySettingsDTO): GatewaySettingsViewModel {
  const channels: Record<string, GatewayChannelSettingsViewModel> = {}
  const feishu = feishuAdapter(dto.config.adapters.feishu)
  channels.feishu = {
    key: 'feishu',
    label: 'Feishu',
    enabled: feishu.enabled,
    token: feishu.app_secret ?? '',
    apiKey: feishu.app_id ?? '',
    webhookUrl: '',
    ...extraView({
      appId: feishu.app_id ?? '',
      encryptKey: feishu.encrypt_key ?? '',
      verifyToken: feishu.verify_token ?? '',
      defaultSessionLabel: feishu.default_session_label ?? 'general',
      connectionMode: feishu.connection_mode ?? 'stream',
      domainName: feishu.domain_name ?? 'feishu'
    }, shouldShowAdapterExtras(feishu.enabled, feishu.app_secret ?? '', feishu.app_id ?? '', ''))
  }
  for (const [frontendKey, backendKey, label] of gatewayChannelKeys) {
    if (backendKey === 'feishu') continue
    const adapter = platformAdapter(dto.config.adapters[backendKey])
    channels[frontendKey] = {
      key: frontendKey,
      label,
      enabled: adapter.enabled,
      token: backendKey === 'weixin' ? '' : (adapter.token ?? ''),
      apiKey: backendKey === 'weixin' ? '' : (adapter.api_key ?? ''),
      webhookUrl: backendKey === 'weixin' ? '' : (adapter.webhook_url ?? ''),
      ...extraView(
        adapter.extra ?? {},
        shouldShowAdapterExtras(adapter.enabled, adapter.token ?? '', adapter.api_key ?? '', adapter.webhook_url ?? '')
      )
    }
  }

  return {
    configPath: dto.config_path,
    rawYaml: dto.raw_yaml,
    server: {
      port: String(dto.config.gateway.port),
      bindAddress: dto.config.gateway.bind_address,
      idleSessionTimeoutMinutes: String(dto.config.gateway.idle_session_timeout_minutes),
      staticAssetsPath: dto.config.gateway.static_assets_path ?? '',
      maxPerSessionMcpInstances: String(dto.config.gateway.max_per_session_mcp_instances)
    },
    channels,
    feishuAccounts: mapFeishuAccountsView(feishu.accounts)
  }
}

export function mapGatewaySettingsCommand(view: GatewaySettingsViewModel): GatewaySettingsUpdateRequest {
  const adapters: GatewayAdaptersConfigDTO = {
    feishu: { ...mapFeishuChannel(channel(view, 'feishu')), accounts: mapFeishuAccountsCommand(view.feishuAccounts) }
  }

  for (const [frontendKey, backendKey] of gatewayChannelKeys) {
    if (backendKey === 'feishu') continue
    if (backendKey === 'weixin') {
      adapters.weixin = mapWeixinChannel(channel(view, frontendKey))
      continue
    }
    adapters[backendKey] = mapPlatformChannel(channel(view, frontendKey))
  }

  return {
    config: {
      gateway: {
        port: Number(view.server.port),
        bind_address: view.server.bindAddress,
        idle_session_timeout_minutes: Number(view.server.idleSessionTimeoutMinutes),
        static_assets_path: view.server.staticAssetsPath,
        max_per_session_mcp_instances: Number(view.server.maxPerSessionMcpInstances)
      },
      adapters
    } satisfies GatewayConfigDTO
  }
}

export function mapRawGatewaySettingsCommand(view: GatewaySettingsViewModel): GatewaySettingsRawUpdateRequest {
  return { raw_yaml: view.rawYaml }
}

function channel(view: GatewaySettingsViewModel, key: FrontendChannelKey | 'feishu'): GatewayChannelSettingsViewModel {
  return (
    view.channels[key] ?? {
      key,
      label: key,
      enabled: false,
      token: '',
      apiKey: '',
      webhookUrl: '',
      extraText: '{}',
      extraFields: []
    }
  )
}

function mapPlatformChannel(view: GatewayChannelSettingsViewModel): GatewayPlatformAdapterDTO {
  return {
    enabled: Boolean(view.enabled),
    token: view.token,
    api_key: view.apiKey,
    webhook_url: view.webhookUrl,
    extra: parseExtraFields(view)
  }
}

function mapWeixinChannel(view: GatewayChannelSettingsViewModel): GatewayWeixinAdapterDTO {
  return {
    enabled: Boolean(view.enabled),
    extra: parseExtraFields(view)
  }
}

function mapFeishuChannel(view: GatewayChannelSettingsViewModel): GatewayFeishuAdapterDTO {
  const extra = parseExtraFields(view)
  return {
    enabled: Boolean(view.enabled),
    app_id: stringValue(extra.appId, view.apiKey),
    app_secret: view.token,
    encrypt_key: stringValue(extra.encryptKey, ''),
    verify_token: stringValue(extra.verifyToken, ''),
    default_session_label: stringValue(extra.defaultSessionLabel, 'general'),
    connection_mode: 'stream',
    domain_name: stringValue(extra.domainName, 'feishu')
  }
}

function mapFeishuAccountsView(
  accounts: Record<string, GatewayFeishuAccountDTO> | undefined
): Record<string, GatewayFeishuAccountViewModel> | undefined {
  if (!accounts) return undefined
  return Object.fromEntries(Object.entries(accounts).map(([accountId, account]) => [accountId, { ...account }]))
}

function mapFeishuAccountsCommand(
  accounts: Record<string, GatewayFeishuAccountViewModel> | undefined
): Record<string, GatewayFeishuAccountDTO> | undefined {
  if (!accounts) return undefined
  return Object.fromEntries(Object.entries(accounts).map(([accountId, account]) => [accountId, { ...account }]))
}

function parseExtra(raw: string): Record<string, unknown> {
  const trimmed = raw.trim()
  if (!trimmed) return {}
  const parsed = JSON.parse(trimmed)
  return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
}

function parseExtraFields(view: GatewayChannelSettingsViewModel): Record<string, unknown> {
  if (!view.extraFields) return parseExtra(view.extraText)
  const extra: Record<string, unknown> = {}
  for (const field of view.extraFields) {
    const key = field.key.trim()
    if (!key) continue
    extra[key] = key === 'agentBindings' || key === 'activeAgentBindings' || key === 'weixinUserBindings' ? normalizeStringBindings(parseExtraValue(field.value)) : parseExtraValue(field.value)
  }
  return extra
}

function parseExtraValue(raw: string): unknown {
  const trimmed = raw.trim()
  if (!trimmed) return ''
  try {
    return JSON.parse(trimmed)
  } catch {
    return raw
  }
}

function extraView(extra: Record<string, unknown>, showFields = true): { extraText: string; extraFields: GatewayExtraFieldViewModel[] } {
  if (!showFields) {
    return {
      extraText: JSON.stringify(extra, null, 2),
      extraFields: []
    }
  }
  const entries = Object.entries(extra).filter(([key, value]) => !isDefaultExtraValue(key, value))
  return {
    extraText: JSON.stringify(extra, null, 2),
    extraFields: entries.map(([key, value], index) => ({
      id: `${key}-${index}`,
      key,
      value: JSON.stringify(value, null, 2)
    }))
  }
}

function normalizeStringBindings(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const out: Record<string, string> = {}
  for (const [senderId, agentId] of Object.entries(value)) {
    const sender = senderId.trim()
    const agent = typeof agentId === 'string' ? agentId.trim() : String(agentId ?? '').trim()
    if (!sender || !agent) continue
    out[sender] = agent
  }
  return out
}

function shouldShowAdapterExtras(enabled: boolean, token: string, apiKey: string, webhookUrl: string): boolean {
  return enabled || token.trim().length > 0 || apiKey.trim().length > 0 || webhookUrl.trim().length > 0
}

function isEmptyExtraValue(value: unknown): boolean {
  if (value === undefined || value === null) return true
  if (typeof value === 'string') return value.trim().length === 0
  return false
}

function isDefaultExtraValue(key: string, value: unknown): boolean {
  if (isEmptyExtraValue(value)) return true
  return (
    (key === 'defaultSessionLabel' && value === 'general') ||
    (key === 'connectionMode' && value === 'stream') ||
    (key === 'domainName' && value === 'feishu')
  )
}

function platformAdapter(value: GatewayAdaptersConfigDTO[BackendChannelKey]): GatewayPlatformAdapterDTO {
  return value ?? { enabled: false, token: '', api_key: '', webhook_url: '', extra: {} }
}

function feishuAdapter(value: GatewayAdaptersConfigDTO['feishu']): GatewayFeishuAdapterDTO {
  return (
    value ?? {
      enabled: false,
      app_id: '',
      app_secret: '',
      encrypt_key: '',
      verify_token: '',
      default_session_label: 'general',
      connection_mode: 'stream',
      domain_name: 'feishu'
    }
  )
}

function stringValue(value: unknown, fallback: string): string {
  return typeof value === 'string' ? value : fallback
}
