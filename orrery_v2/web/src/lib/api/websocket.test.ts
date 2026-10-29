import { afterEach, describe, expect, it, vi } from 'vitest'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import { connectEvaluationStream, connectMissionStream } from './websocket'

describe('stream API clients', () => {
  const originalEventSource = globalThis.EventSource

  afterEach(() => {
    vi.restoreAllMocks()
    globalThis.EventSource = originalEventSource
    clearSession()
  })

  it('connects to mission and evaluation stream endpoints', () => {
    const urls: string[] = []
    const options: EventSourceInit[] = []
    applySession({
      accessToken: 'token-1',
      expiresAt: '',
      permissionScope: ['operations.read', 'mission.read'],
      user: {
        userId: 'superadmin',
        username: 'superadmin',
        displayName: 'superadmin',
        nickname: 'superadmin',
        email: '',
        agentProfileId: 'human-superadmin',
        role: 'superadmin'
      }
    })
    globalThis.EventSource = vi.fn(function (url: string | URL, init?: EventSourceInit) {
      urls.push(String(url))
      options.push(init ?? {})
      return {} as EventSource
    }) as unknown as typeof EventSource

    connectMissionStream('mission-1')
    connectEvaluationStream('?mission_id=mission-1')

    expect(urls).toEqual([
      '/api/missions/mission-1/stream?viewer_id=superadmin&viewer_kind=user&permission_scope=operations.read%2Cmission.read',
      '/api/evaluations/stream?mission_id=mission-1&viewer_id=superadmin&viewer_kind=user&permission_scope=operations.read%2Cmission.read'
    ])
    expect(options).toEqual([{ withCredentials: true }, { withCredentials: true }])
  })
})
