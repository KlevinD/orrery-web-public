import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'
import { workforceChannelBindingsApi } from './workforce-channel-bindings'

describe('workforceChannelBindingsApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    clearApiBaseUrlForTests()
  })

  it('uses encoded employee and Feishu account ownership routes', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() =>
      Promise.resolve(new Response(JSON.stringify({ agent_profile_id: 'jordan/belfort', accounts: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      }))
    )

    await workforceChannelBindingsApi.list('jordan/belfort')
    await workforceChannelBindingsApi.put('jordan/belfort', 'jordan main', {
      inbound_enabled: true,
      expected_revision: 2,
      expected_owner_agent_profile_id: 'gangbeng'
    })
    await workforceChannelBindingsApi.delete('jordan/belfort', 'jordan main', {
      expected_revision: 3,
      expected_owner_agent_profile_id: 'jordan/belfort'
    })

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/workforce/members/jordan%2Fbelfort/channel-bindings',
      '/api/workforce/members/jordan%2Fbelfort/channel-bindings/feishu/jordan%20main',
      '/api/workforce/members/jordan%2Fbelfort/channel-bindings/feishu/jordan%20main'
    ])
    expect(fetchMock.mock.calls[1][1]).toMatchObject({
      method: 'PUT',
      body: '{"inbound_enabled":true,"expected_revision":2,"expected_owner_agent_profile_id":"gangbeng"}'
    })
    expect(fetchMock.mock.calls[2][1]).toMatchObject({
      method: 'DELETE',
      body: '{"expected_revision":3,"expected_owner_agent_profile_id":"jordan/belfort"}'
    })
  })
})
