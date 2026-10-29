import {
  gatewayChannelFieldGroups,
  genericGatewayFieldGroup,
  type GatewayChannelKey,
  type GatewayFieldGroup,
  type GatewayPrimaryFieldId
} from './settingsConfig'
import { productionGatewayChannelKeys } from '#lib/mappers/gateway-settings'
import type {
  GatewayChannelSettingsViewModel,
  GatewayExtraFieldViewModel,
  GatewayFeishuAccountViewModel,
  GatewaySettingsViewModel
} from '#lib/types/view-models'

export function copyGatewaySettingsView(view: GatewaySettingsViewModel): GatewaySettingsViewModel {
  return {
    ...view,
    server: { ...view.server },
    feishuAccounts: view.feishuAccounts ? copyFeishuAccounts(view.feishuAccounts) : undefined,
    channels: Object.fromEntries(
      Object.entries(view.channels).map(([key, value]) => [
        key,
        {
          ...value,
          extraFields: value.extraFields.map((field) => ({ ...field }))
        }
      ])
    )
  }
}

export function copyFeishuAccounts(
  accounts: Record<string, GatewayFeishuAccountViewModel> | undefined
): Record<string, GatewayFeishuAccountViewModel> {
  return Object.fromEntries(Object.entries(accounts ?? {}).map(([alias, account]) => [alias, { ...account }]))
}

export function gatewayChannelFieldGroup(channelKey: string): GatewayFieldGroup {
  return gatewayChannelFieldGroups[channelKey as GatewayChannelKey] ?? genericGatewayFieldGroup
}

export function visibleGatewayChannels(settings: GatewaySettingsViewModel | undefined): Array<{ key: GatewayChannelKey; label: string; channel: GatewayChannelSettingsViewModel }> {
  if (!settings) return []
  const channels: Array<{ key: GatewayChannelKey; label: string; channel: GatewayChannelSettingsViewModel }> = []
  for (const [channelKey, _backendKey, label] of productionGatewayChannelKeys) {
    const channel = settings.channels[channelKey]
    const hasNamedFeishuAccounts = channelKey === 'feishu' && Object.keys(settings.feishuAccounts ?? {}).length > 0
    if (channel && (gatewayChannelHasConfig(channel) || hasNamedFeishuAccounts)) {
      channels.push({ key: channelKey, label, channel })
    }
  }
  return channels
}

export function availableGatewayChannels(settings: GatewaySettingsViewModel | undefined) {
  if (!settings) return []
  const visible = new Set(visibleGatewayChannels(settings).map((item) => item.key))
  return productionGatewayChannelKeys
    .filter(([channelKey]) => !visible.has(channelKey))
    .map(([channelKey, _backendKey, label]) => ({ value: channelKey, label }))
}

export function gatewayChannelHasConfig(channel: GatewayChannelSettingsViewModel) {
  return (
    channel.enabled ||
    channel.token.trim().length > 0 ||
    channel.apiKey.trim().length > 0 ||
    channel.webhookUrl.trim().length > 0 ||
    channel.extraFields.length > 0
  )
}

export function gatewayPrimaryFieldValue(channel: GatewayChannelSettingsViewModel, fieldId: GatewayPrimaryFieldId) {
  return channel[fieldId]
}

export function gatewayExtraFieldLabel(channelKey: GatewayChannelKey, field: GatewayExtraFieldViewModel) {
  return gatewayChannelFieldGroup(channelKey).extraFields.find((option) => option.key === field.key)?.label ?? field.key
}
