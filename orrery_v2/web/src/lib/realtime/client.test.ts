import { describe, expect, it } from 'vitest'
import { buildRealtimeConnectUrl, normalizeRealtimeBaseUrl } from './client'

describe('realtime client helpers', () => {
  it('builds the authenticated realtime connect URL', () => {
    const url = buildRealtimeConnectUrl({
      baseUrl: 'http://127.0.0.1:17321',
      accessToken: 'token-1',
      viewerId: 'user-1',
      viewerKind: 'user',
      permissionScope: ['discussion.read', 'mission.read']
    })

    expect(url).toBe(
      'ws://127.0.0.1:17321/api/realtime/connect?viewer_id=user-1&viewer_kind=user&permission_scope=discussion.read%2Cmission.read'
    )
  })

  it('normalizes browser-origin realtime URLs when the API base is not configured', () => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    expect(normalizeRealtimeBaseUrl('')).toBe(`${protocol}//${window.location.host}/api/realtime/connect`)
    expect(normalizeRealtimeBaseUrl('https://example.test/api')).toBe('wss://example.test/api/realtime/connect')
  })
})
