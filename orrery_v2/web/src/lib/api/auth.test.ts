import { afterEach, describe, expect, it, vi } from 'vitest'

import { authApi, authSessionFromDTO, clientPasswordDigest, clientPasswordProof } from './auth'
import { setUnauthorizedHandlerForTests } from './client'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'

describe('auth password proof', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    setUnauthorizedHandlerForTests(undefined)
    clearApiBaseUrlForTests()
  })

  it('hydrates the current browser session from the secure cookie endpoint', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        user: {
          user_id: 'user-1',
          username: 'operator',
          display_name: 'Operator',
          agent_profile_id: 'human-operator'
        },
        permission_scope: ['operations.read']
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    )

    await expect(authApi.currentSession()).resolves.toMatchObject({
      user: { username: 'operator' },
      permission_scope: ['operations.read']
    })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/auth/session')
    expect(fetchMock.mock.calls[0][1]?.credentials).toBe('include')
  })

  it('treats an unauthenticated session probe as an expected empty result', async () => {
    const unauthorizedHandler = vi.fn()
    setUnauthorizedHandlerForTests(unauthorizedHandler)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ code: 'unauthorized', message: 'authorization is required' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(authApi.currentSession()).resolves.toBeUndefined()
    expect(unauthorizedHandler).not.toHaveBeenCalled()
  })

  it('lets the login screen recover when the session endpoint is not mounted', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('404 page not found', {
        status: 404,
        headers: { 'Content-Type': 'text/plain' }
      })
    )

    await expect(authApi.currentSession()).resolves.toBeUndefined()
  })

  it('keeps login failures on the login screen when the backend returns a generic unauthorized response', async () => {
    const unauthorizedHandler = vi.fn()
    setUnauthorizedHandlerForTests(unauthorizedHandler)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ code: 'unauthorized', message: 'authorization is required' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(authApi.completeLogin('superadmin', 'challenge-1', 'wrong-password')).rejects.toMatchObject({
      status: 401,
      code: 'unauthorized',
      message: 'authorization is required'
    })
    expect(unauthorizedHandler).not.toHaveBeenCalled()
  })

  it('preserves the forced-password-change restriction in the client session', () => {
    expect(authSessionFromDTO({
      user: {
        user_id: 'user-1',
        username: 'operator',
        display_name: 'Operator',
        agent_profile_id: 'human-operator'
      },
      permission_scope: [],
      must_change_password: true
    }).mustChangePassword).toBe(true)
  })

  it('preserves the browser identity provider in the client session', () => {
    expect(authSessionFromDTO({
      subject_provider: 'kratos',
      user: {
        user_id: 'user-1',
        username: 'operator',
        display_name: 'Operator',
        agent_profile_id: 'human-operator'
      },
      permission_scope: []
    }).subjectProvider).toBe('kratos')
  })

  it('computes the same digest when Web Crypto subtle is unavailable', async () => {
    vi.stubGlobal('crypto', {})

    await expect(clientPasswordDigest('123456', 'salt-1')).resolves.toBe(
      '1824ce08da9cb9995711b2d90071b9c7f732007425f65756df01012cc19a0c5c'
    )
  })

  it('computes the same challenge proof when Web Crypto subtle is unavailable', async () => {
    vi.stubGlobal('crypto', {})

    await expect(clientPasswordProof('123456', 'salt-1', 'challenge-1')).resolves.toBe(
      '8078e954bddfc8c4e9c3307012ba45e2c23622387a04160892190d29016e6479'
    )
  })

  it('computes the backend-compatible password proof when Web Crypto subtle is unavailable', async () => {
    vi.stubGlobal('crypto', {})

    const salt = '0b9f223994862b63afee9e39e9336bc6'
    const challengeID = 'login-test-challenge'

    await expect(clientPasswordDigest('123456', salt)).resolves.toBe(
      '31135744dd8c4eabd735bebf756df26b4226cd4926290e70da49758f84290145'
    )
    await expect(clientPasswordProof('123456', salt, challengeID)).resolves.toBe(
      'ac373cd08d8278e06ec4c0caf7aebec36fe35b19a000c389d5fb49c01548b754'
    )
  })
})
