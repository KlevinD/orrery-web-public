import { describe, expect, it } from 'vitest'
import {
  copyGatewaySettingsView,
  gatewayChannelFieldGroup,
  gatewayChannelHasConfig,
  gatewayExtraFieldLabel,
  gatewayPrimaryFieldValue,
  visibleGatewayChannels,
  availableGatewayChannels
} from './gatewaySettingsHelpers'
import type { GatewaySettingsViewModel } from '#lib/types/view-models'

const gatewaySettings = {
  configPath: '/tmp/gateway.yaml',
  rawYaml: '',
  server: {
    port: '8080',
    bindAddress: '127.0.0.1',
    idleSessionTimeoutMinutes: '30',
    staticAssetsPath: './static',
    maxPerSessionMcpInstances: '4'
  },
  channels: {
    feishu: {
      key: 'feishu',
      label: 'Feishu',
      enabled: true,
      token: 'secret',
      apiKey: '',
      webhookUrl: '',
      extraText: '',
      extraFields: [{ id: 'encrypt', key: 'encryptKey', value: 'encrypted' }]
    },
    webhook: {
      key: 'webhook',
      label: 'Webhook',
      enabled: false,
      token: '',
      apiKey: '',
      webhookUrl: '',
      extraText: '',
      extraFields: []
    }
  }
} satisfies GatewaySettingsViewModel

describe('gatewaySettingsHelpers', () => {
  it('deep-copies named Feishu accounts', () => {
    const source: GatewaySettingsViewModel = {
      ...gatewaySettings,
      feishuAccounts: {
        main: {
          enabled: true,
          app_id: 'main-app',
          app_secret: '********',
          default_session_label: 'general',
          connection_mode: 'stream',
          domain_name: 'feishu'
        }
      }
    }
    const copy = copyGatewaySettingsView(source)
    copy.feishuAccounts!.main!.app_id = 'changed'

    expect(source.feishuAccounts!.main!.app_id).toBe('main-app')
  })

  it('copies gateway settings without sharing nested channel fields', () => {
    const copy = copyGatewaySettingsView(gatewaySettings)

    copy.channels.feishu.extraFields[0].value = 'changed'

    expect(gatewaySettings.channels.feishu.extraFields[0].value).toBe('encrypted')
    expect(copy.channels.feishu.extraFields[0]).not.toBe(gatewaySettings.channels.feishu.extraFields[0])
  })

  it('derives visible and available channels from configured channel state', () => {
    expect(visibleGatewayChannels(gatewaySettings)).toEqual([
      expect.objectContaining({ key: 'feishu', label: 'Feishu', channel: gatewaySettings.channels.feishu })
    ])
    expect(availableGatewayChannels(gatewaySettings)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ value: 'weixin', label: 'Weixin' }),
        expect.objectContaining({ value: 'dingtalk', label: 'DingTalk' })
      ])
    )
		expect(availableGatewayChannels(gatewaySettings)).not.toEqual(
			expect.arrayContaining([expect.objectContaining({ value: 'webhook' })])
		)
  })

  it('keeps Feishu visible when only named accounts are configured', () => {
    const settings: GatewaySettingsViewModel = {
      ...gatewaySettings,
      channels: {
        ...gatewaySettings.channels,
        feishu: {
          ...gatewaySettings.channels.feishu,
          enabled: false,
          token: '',
          apiKey: '',
          extraFields: []
        }
      },
      feishuAccounts: {
        main: {
          enabled: false,
          app_id: 'main-app',
          default_session_label: 'general',
          connection_mode: 'outbound',
          domain_name: 'feishu'
        }
      }
    }

    expect(visibleGatewayChannels(settings).map((item) => item.key)).toContain('feishu')
  })

  it('identifies configured channels from enabled status or credentials', () => {
    expect(gatewayChannelHasConfig(gatewaySettings.channels.feishu)).toBe(true)
    expect(gatewayChannelHasConfig(gatewaySettings.channels.webhook)).toBe(false)
    expect(gatewayChannelHasConfig({ ...gatewaySettings.channels.webhook, webhookUrl: ' https://hook.example.test ' })).toBe(true)
  })

  it('returns channel field metadata and values', () => {
    expect(gatewayChannelFieldGroup('feishu').title).toBe('Feishu')
    expect(gatewayChannelFieldGroup('unknown').title).toBe('Generic Adapter')
    expect(gatewayPrimaryFieldValue(gatewaySettings.channels.feishu, 'token')).toBe('secret')
    expect(gatewayExtraFieldLabel('feishu', { id: 'encrypt', key: 'encryptKey', value: '' })).toBe('Encrypt Key')
    expect(gatewayExtraFieldLabel('feishu', { id: 'custom', key: 'customField', value: '' })).toBe('customField')
  })
})
