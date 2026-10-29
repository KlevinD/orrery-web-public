import { afterEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import { applySession, clearSession, session } from '#lib/stores/session.svelte'
import { ApiClientError, apiFetch, setForbiddenHandlerForTests, setUnauthorizedHandlerForTests } from './client'
import { clearApiBaseUrlForTests, setApiBaseUrl } from '#lib/runtime/api-base'

describe('apiFetch', () => {
  const originalCrypto = globalThis.crypto

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: originalCrypto
    })
    clearSession()
    clearApiBaseUrlForTests()
    setUnauthorizedHandlerForTests(undefined)
    setForbiddenHandlerForTests(undefined)
    setLocale('zh-CN')
  })

  it('injects request id, JSON headers, idempotency key and abort signal', async () => {
    setLocale('en-US')
    applyTestSession()
    const signal = new AbortController().signal
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000002')

    const result = await apiFetch<{ ok: boolean }>('/api/missions', {
      method: 'POST',
      body: JSON.stringify({ title: 'Launch' }),
      idempotencyKey: 'idem-1',
      signal
    })

    expect(result).toEqual({ ok: true })
    const [, init] = fetchMock.mock.calls[0]
    const headers = init?.headers as Headers
    expect(fetchMock.mock.calls[0][0]).toBe('/api/missions')
    expect(init?.signal).toBe(signal)
    expect(headers.get('Accept')).toBe('application/json')
    expect(headers.get('Accept-Language')).toBe('en-US')
    expect(headers.get('Content-Type')).toBe('application/json')
    expect(headers.get('X-Request-ID')).toBe('00000000-0000-4000-8000-000000000002')
    expect(headers.get('Idempotency-Key')).toBe('idem-1')
    expect(headers.get('Authorization')).toBeNull()
    expect(init?.credentials).toBe('include')
    expect(headers.get('X-User-ID')).toBe(session.actorId)
    expect(headers.get('X-User-Display-Name')).toBe(encodeURIComponent(session.displayName))
    expect(headers.get('X-Viewer-Kind')).toBe(session.viewerKind)
    expect(headers.get('X-Agent-Profile-ID')).toBe(session.user.agentProfileId)
    expect(headers.get('X-Permission-Scope')).toBe(session.permissionScope.join(','))
  })

  it('still sends requests when crypto.randomUUID is unavailable', async () => {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: {
        getRandomValues: (bytes: Uint8Array) => {
          bytes.fill(0)
          return bytes
        }
      }
    })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await apiFetch('/api/auth/login/begin', { method: 'POST', body: JSON.stringify({ username: 'superadmin' }) })

    const [, init] = fetchMock.mock.calls[0]
    const headers = init?.headers as Headers
    expect(headers.get('X-Request-ID')).toBe('00000000-0000-4000-8000-000000000000')
  })

  it('prefixes requests with the runtime API base URL', async () => {
    setApiBaseUrl('http://127.0.0.1:49152')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await apiFetch('/api/health')

    expect(fetchMock.mock.calls[0][0]).toBe('http://127.0.0.1:49152/api/health')
  })

  it('throws normalized backend errors with correlation id', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ code: 'permission_denied', message: 'No access', correlation_id: 'corr-7' }), {
        status: 403,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(apiFetch('/api/permissions/matrix')).rejects.toMatchObject({
      name: 'ApiClientError',
      status: 403,
      code: 'permission_denied',
      message: 'No access',
      correlationId: 'corr-7'
    } satisfies Partial<ApiClientError>)
  })

  it('notifies the forbidden handler with the failed API path', async () => {
    const handler = vi.fn()
    setForbiddenHandlerForTests(handler)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ code: 'forbidden', message: 'scope access denied' }), {
        status: 403,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(apiFetch('/api/ontology/context/objects?tenant_id=tenant-1')).rejects.toMatchObject({ status: 403 })
    expect(handler).toHaveBeenCalledWith(expect.objectContaining({ status: 403 }), '/api/ontology/context/objects?tenant_id=tenant-1')
  })

  it('notifies the ontology access handler for non-disclosing not-found responses only', async () => {
    const handler = vi.fn()
    setForbiddenHandlerForTests(handler)
    vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ code: 'unavailable', message: 'resource unavailable' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(apiFetch('/api/ontology/context/objects/missing?tenant_id=tenant-1')).rejects.toMatchObject({ status: 404 })
    expect(handler).toHaveBeenCalledWith(expect.objectContaining({ status: 404 }), '/api/ontology/context/objects/missing?tenant_id=tenant-1')

    handler.mockClear()
    await expect(apiFetch('/api/projects/missing')).rejects.toMatchObject({ status: 404 })
    expect(handler).not.toHaveBeenCalled()
  })

  it('notifies the global unauthorized handler when backend requires authorization', async () => {
    const handler = vi.fn()
    setUnauthorizedHandlerForTests(handler)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ code: 'unauthorized', message: 'authorization is required', correlation_id: 'corr-auth' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(apiFetch('/api/workforce/members')).rejects.toMatchObject({
      status: 401,
      code: 'unauthorized',
      message: 'authorization is required'
    } satisfies Partial<ApiClientError>)

    expect(handler).toHaveBeenCalledWith(
      expect.objectContaining({
        status: 401,
        code: 'unauthorized',
        message: 'authorization is required',
        correlationId: 'corr-auth'
      })
    )
  })

  it('does not notify the global unauthorized handler for ordinary login failures', async () => {
    const handler = vi.fn()
    setUnauthorizedHandlerForTests(handler)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ code: 'unauthorized', message: '用户名或密码不正确' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(apiFetch('/api/auth/login/complete', { method: 'POST' })).rejects.toMatchObject({
      status: 401,
      code: 'unauthorized',
      message: '用户名或密码不正确'
    } satisfies Partial<ApiClientError>)

    expect(handler).not.toHaveBeenCalled()
  })

  it('summarizes non-json error bodies instead of exposing fallback HTML', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('<!doctype html><html><body>fallback</body></html>', {
        status: 404,
        statusText: 'Not Found',
        headers: { 'Content-Type': 'text/html' }
      })
    )

    await expect(apiFetch('/api/org/graph')).rejects.toMatchObject({
      status: 404,
      code: 'http_404',
      message: 'Not Found'
    } satisfies Partial<ApiClientError>)
  })

  it('returns undefined for empty success bodies', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 204 }))

    await expect(apiFetch('/api/resources/mcp-servers/mcp-1/disable', { method: 'POST' })).resolves.toBeUndefined()
  })

  it('does not force JSON content type for multipart form uploads', async () => {
    const body = new FormData()
    body.set('package', new File(['zip'], 'skill.zip'))
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await apiFetch('/api/resources/skills/import', { method: 'POST', body })

    const [, init] = fetchMock.mock.calls[0]
    const headers = init?.headers as Headers
    expect(init?.body).toBe(body)
    expect(headers.get('Content-Type')).toBeNull()
  })

  it('returns blobs for binary download responses', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('skill', {
        status: 200,
        headers: { 'Content-Type': 'application/zip' }
      })
    )

    const result = await apiFetch<Blob>('/api/resources/skills/skill.review/export', { responseType: 'blob' })

    const [, init] = fetchMock.mock.calls[0]
    const headers = init?.headers as Headers
    expect(typeof result.text).toBe('function')
    await expect(result.text()).resolves.toBe('skill')
    expect(headers.get('Accept')).toBe('application/octet-stream')
  })
})

function applyTestSession() {
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
}
