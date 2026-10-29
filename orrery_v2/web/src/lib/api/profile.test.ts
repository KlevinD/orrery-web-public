import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'
import { profileApi } from './profile'

describe('profileApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    clearApiBaseUrlForTests()
  })

  it('uses current-user Weixin endpoints for profile binding operations', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ tenant_id: 'tenant-a', human_id: 'human-alice', weixin_bound: false, agents: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' }
        })
      )
    )

    await profileApi.getWeixin()
    await profileApi.setWeixinDefaultAgent('agent-one')
    await profileApi.startWeixinLogin({ agent_id: 'agent-one' })
    await profileApi.getWeixinLoginStatus('session/one')
    await profileApi.submitWeixinVerifyCode('session/one', '123456')
    await profileApi.unbindWeixin()
    await profileApi.getGatewayBindings()
    await profileApi.createGatewayBindingCode('feishu', 'main', 'agent-one')
    await profileApi.deleteGatewayBinding('binding/one')

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/profile/weixin',
      '/api/profile/weixin/default-agent',
      '/api/profile/weixin/login/start',
      '/api/profile/weixin/login/session%2Fone',
      '/api/profile/weixin/login/session%2Fone/verify-code',
      '/api/profile/weixin',
      '/api/profile/gateway-bindings',
      '/api/profile/gateway-bindings/feishu/code',
      '/api/profile/gateway-bindings/binding%2Fone'
    ])
    expect(fetchMock.mock.calls[1][1]).toMatchObject({
      method: 'PUT',
      body: JSON.stringify({ agent_id: 'agent-one' })
    })
    expect(fetchMock.mock.calls[2][1]).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ agent_id: 'agent-one' })
    })
    expect(fetchMock.mock.calls[4][1]).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ verify_code: '123456' })
    })
    expect(fetchMock.mock.calls[5][1]?.method).toBe('DELETE')
    expect(fetchMock.mock.calls[7][1]).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ account_id: 'main', agent_id: 'agent-one' })
    })
    expect(fetchMock.mock.calls[8][1]?.method).toBe('DELETE')
  })

  it('uploads and removes the current user personal assistant avatar', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({
        avatar_url: '/api/profile/personal-assistant/avatar?v=etag-one',
        content_type: 'image/png',
        size: 6,
        updated_at: '2026-08-08T06:00:00Z',
        etag: 'etag-one'
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    const file = new File(['avatar'], 'assistant.png', { type: 'image/png' })

    await profileApi.uploadPersonalAssistantAvatar(file)
    await profileApi.deletePersonalAssistantAvatar()

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/profile/personal-assistant/avatar',
      '/api/profile/personal-assistant/avatar'
    ])
    const upload = fetchMock.mock.calls[0][1]
    expect(upload?.method).toBe('PUT')
    expect(upload?.body).toBeInstanceOf(FormData)
    expect((upload?.body as FormData).get('file')).toBe(file)
    expect((upload?.headers as Headers).has('Content-Type')).toBe(false)
    expect(fetchMock.mock.calls[1][1]?.method).toBe('DELETE')
  })
})
