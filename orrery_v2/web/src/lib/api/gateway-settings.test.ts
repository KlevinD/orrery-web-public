import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'
import { gatewaySettingsApi } from './gateway-settings'

describe('gatewaySettingsApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    clearApiBaseUrlForTests()
  })

  it('uses owner-scoped Feishu registration endpoints', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ registration_id: 'freg/one', status: 'pending', expires_at: '2026-07-15T12:00:00Z' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' }
        })
      )
    )

    await gatewaySettingsApi.getAccounts()
    await gatewaySettingsApi.startFeishuRegistration('jordan-belfort')
    await gatewaySettingsApi.getFeishuRegistration('freg/one')
    await gatewaySettingsApi.cancelFeishuRegistration('freg/one')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/settings/gateway/accounts',
      '/api/settings/gateway/feishu/registrations',
      '/api/settings/gateway/feishu/registrations/freg%2Fone',
      '/api/settings/gateway/feishu/registrations/freg%2Fone'
    ])
    expect(fetchMock.mock.calls[1][1]).toMatchObject({ method: 'POST', body: '{"account_id":"jordan-belfort"}' })
    expect(fetchMock.mock.calls[3][1]?.method).toBe('DELETE')
  })
})
