import { describe, expect, it } from 'vitest'
import { mapGatewaySettingsCommand, mapGatewaySettingsView, mapRawGatewaySettingsCommand } from './gateway-settings'
import type { GatewaySettingsDTO } from '#lib/types/api'

function createGatewaySettingsDTO(): GatewaySettingsDTO {
  return {
    config_path: 'database:core.gateway_settings',
    raw_yaml: '',
    config: {
      gateway: {
        port: 18789,
        bind_address: '127.0.0.1',
        idle_session_timeout_minutes: 30,
        static_assets_path: '',
        max_per_session_mcp_instances: 5
      },
      adapters: {}
    }
  }
}

describe('gateway settings mapper', () => {
  it('preserves Feishu account configurations across structured edits', () => {
    const dto = createGatewaySettingsDTO()
    dto.config.adapters.feishu = {
      enabled: true,
      default_session_label: 'general',
      accounts: {
        main: {
          enabled: true,
          app_id: 'main-app',
          app_secret: '***',
          default_session_label: 'general',
          connection_mode: 'outbound',
          domain_name: 'feishu'
        }
      }
    }

    const command = mapGatewaySettingsCommand(mapGatewaySettingsView(dto))

    expect(command.config.adapters.feishu?.accounts).toEqual(dto.config.adapters.feishu.accounts)
  })

  it('maps every supported adapter to the view and back', () => {
    const dto = createGatewaySettingsDTO()
    dto.raw_yaml = 'gateway:\n  port: 18789\n'
    dto.config.adapters.feishu = {
      enabled: true,
      app_id: 'app',
      app_secret: 'secret',
      default_session_label: 'general',
      connection_mode: 'webhook',
      domain_name: 'feishu'
    }
    dto.config.adapters.weixin = { enabled: true, extra: { pollIntervalSeconds: 3 } }
    dto.config.adapters.dingtalk = {
      enabled: true,
      token: 'client-secret',
      api_key: 'client-id',
      extra: { robotCode: 'robot' }
    }
    dto.config.adapters.email = {
      enabled: true,
      token: 'smtp-password',
      api_key: 'smtp-user',
      extra: { smtpHost: 'smtp.example.test', smtpPort: 587 }
    }

    const view = mapGatewaySettingsView(dto)
    const command = mapGatewaySettingsCommand(view)

    expect(Object.keys(view.channels).sort()).toEqual(['dingtalk', 'email', 'feishu', 'weixin'])
    expect(command.config.adapters.feishu?.app_id).toBe('app')
    expect(command.config.adapters.feishu?.connection_mode).toBe('stream')
    expect(command.config.adapters.weixin).toEqual(dto.config.adapters.weixin)
    expect(command.config.adapters.dingtalk?.extra).toEqual({ robotCode: 'robot' })
    expect(command.config.adapters.email?.extra).toEqual({ smtpHost: 'smtp.example.test', smtpPort: 587 })
    expect(mapRawGatewaySettingsCommand(view)).toEqual({ raw_yaml: dto.raw_yaml })
  })

  it('round trips editable extra fields for add, update, and delete flows', () => {
    const dto = createGatewaySettingsDTO()
    dto.config.adapters.email = {
      enabled: true,
      token: '',
      api_key: '',
      webhook_url: '',
      extra: { smtpHost: 'smtp.example.test', smtpPort: 587, headers: { tenant: 'acme' } }
    }

    const view = mapGatewaySettingsView(dto)
    const fields = view.channels.email.extraFields
    expect(fields.map((field) => field.key)).toEqual(['smtpHost', 'smtpPort', 'headers'])
    fields[0].value = '"mail.example.test"'
    fields[1].key = ''
    fields.push({ id: 'manual', key: 'from', value: '"noreply@example.test"' })

    expect(mapGatewaySettingsCommand(view).config.adapters.email?.extra).toEqual({
      smtpHost: 'mail.example.test',
      headers: { tenant: 'acme' },
      from: 'noreply@example.test'
    })

    view.channels.email.extraFields = []
    expect(mapGatewaySettingsCommand(view).config.adapters.email?.extra).toEqual({})
  })

  it('never emits global credentials for Weixin', () => {
    const dto = createGatewaySettingsDTO()
    dto.config.adapters.weixin = { enabled: true, extra: { pollIntervalSeconds: 3 } }

    const view = mapGatewaySettingsView(dto)
    view.channels.weixin.token = 'must-not-be-sent'
    view.channels.weixin.apiKey = 'must-not-be-sent'
    view.channels.weixin.webhookUrl = 'https://example.test/must-not-be-sent'

    expect(mapGatewaySettingsCommand(view).config.adapters.weixin).toEqual({
      enabled: true,
      extra: { pollIntervalSeconds: 3 }
    })
  })
})
