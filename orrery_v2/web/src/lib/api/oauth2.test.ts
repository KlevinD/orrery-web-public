import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'
import { oauth2Api } from './oauth2'

describe('oauth2Api', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    clearApiBaseUrlForTests()
  })

  it('reads pending consent by challenge', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          challenge: 'consent-1',
          client: { client_id: 'partner-app', first_party: false },
          requested_scopes: ['openid'],
          grant_scopes: ['openid']
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      )
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000004')

    await oauth2Api.pendingConsent('consent-1')

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/oauth2/consent/pending?consent_challenge=consent-1')
    expect(init?.method).toBeUndefined()
  })

  it('posts explicit consent decisions', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ redirect_to: 'https://hydra.example/oauth2/auth?consent=1' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    ).mockResolvedValueOnce(
      new Response(JSON.stringify({ redirect_to: 'https://hydra.example/oauth2/auth?consent=2' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000005')

    await oauth2Api.acceptConsent('consent-1')
    await oauth2Api.rejectConsent('consent-2')

    expect(fetchMock.mock.calls[0][0]).toBe('/api/oauth2/consent/accept')
    expect(fetchMock.mock.calls[0][1]?.method).toBe('POST')
    expect(fetchMock.mock.calls[0][1]?.body).toBe(JSON.stringify({ consent_challenge: 'consent-1' }))
    expect(fetchMock.mock.calls[1][0]).toBe('/api/oauth2/consent/reject')
    expect(fetchMock.mock.calls[1][1]?.method).toBe('POST')
    expect(fetchMock.mock.calls[1][1]?.body).toBe(JSON.stringify({ consent_challenge: 'consent-2' }))
  })
})
