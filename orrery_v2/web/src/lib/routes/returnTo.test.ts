import { describe, expect, it } from 'vitest'
import {
  IDENTITY_LOGIN_RETURN_TO_KEY,
  IDENTITY_LOGIN_RETURN_TO_TTL_MS,
  identityLoginFlowKey,
  identityLoginReturnToFromURL,
  loginURLWithReturnTo,
  passwordChangeURLWithReturnTo,
  prepareIdentityLoginRetryReturnTo,
  safeReturnTo
} from './returnTo'

describe('returnTo helpers', () => {
  it('accepts only local absolute paths', () => {
    expect(safeReturnTo('/api/oauth2/login?login_challenge=login-1')).toBe('/api/oauth2/login?login_challenge=login-1')
    expect(safeReturnTo('https://example.com/callback')).toBe('/')
    expect(safeReturnTo('//example.com/callback')).toBe('/')
    expect(safeReturnTo('/login')).toBe('/')
    expect(safeReturnTo('/oauth2\\consent')).toBe('/')
  })

  it('builds login URLs with a safe continuation target', () => {
    expect(loginURLWithReturnTo(new URL('https://app.example/oauth2/consent?consent_challenge=consent-1'))).toBe(
      '/login?return_to=%2Foauth2%2Fconsent%3Fconsent_challenge%3Dconsent-1'
    )
  })

  it('builds forced-password-change URLs with a safe continuation target', () => {
    expect(passwordChangeURLWithReturnTo(new URL('https://app.example/projects/123?tab=members'))).toBe(
      '/login?change_password=1&return_to=%2Fprojects%2F123%3Ftab%3Dmembers'
    )
    expect(passwordChangeURLWithReturnTo(new URL('https://app.example/login?return_to=%2Fprojects%2F123'))).toBe(
      '/login?change_password=1&return_to=%2Fprojects%2F123'
    )
  })

  it('restores the pending return target from a real Kratos flow URL', () => {
    const values = new Map<string, string>()
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key)
    }

    expect(identityLoginReturnToFromURL(new URL('https://app.example/login?return_to=%2Fmissions'), storage)).toBe('/missions')
    expect(identityLoginReturnToFromURL(new URL('https://app.example/login?flow=flow-1'), storage)).toBe('/missions')
    expect(identityLoginReturnToFromURL(new URL('https://app.example/login'), storage)).toBe('/')
    expect(values.size).toBe(0)
  })

  it('binds a pending return target to one flow and expires it', () => {
    const values = new Map<string, string>()
    const storage = mapStorage(values)
    const startedAt = 10_000

    expect(identityLoginReturnToFromURL(
      new URL('https://app.example/login?return_to=%2Fmissions'),
      storage,
      '/',
      startedAt
    )).toBe('/missions')
    expect(identityLoginReturnToFromURL(
      new URL('https://app.example/login?flow=flow-1'),
      storage,
      '/',
      startedAt + 1
    )).toBe('/missions')
    expect(values.has(IDENTITY_LOGIN_RETURN_TO_KEY)).toBe(false)
    expect(values.has(identityLoginFlowKey('flow-1'))).toBe(true)

    expect(identityLoginReturnToFromURL(
      new URL('https://app.example/login?flow=flow-2'),
      storage,
      '/',
      startedAt + 2
    )).toBe('/')
    expect(identityLoginReturnToFromURL(
      new URL('https://app.example/login?flow=flow-1'),
      storage,
      '/',
      startedAt + IDENTITY_LOGIN_RETURN_TO_TTL_MS + 1
    )).toBe('/')
    expect(values.has(identityLoginFlowKey('flow-1'))).toBe(false)
  })

  it('moves the return target back to pending storage before retrying a flow', () => {
    const values = new Map<string, string>()
    const storage = mapStorage(values)
    const startedAt = 20_000

    identityLoginReturnToFromURL(
      new URL('https://app.example/login?return_to=%2Foauth2%2Fconsent'),
      storage,
      '/',
      startedAt
    )
    identityLoginReturnToFromURL(
      new URL('https://app.example/login?flow=flow-1'),
      storage,
      '/',
      startedAt + 1
    )
    prepareIdentityLoginRetryReturnTo('flow-1', '/oauth2/consent', storage, startedAt + 2)

    expect(values.has(identityLoginFlowKey('flow-1'))).toBe(false)
    expect(identityLoginReturnToFromURL(
      new URL('https://app.example/login?flow=flow-2'),
      storage,
      '/',
      startedAt + 3
    )).toBe('/oauth2/consent')
  })
})

function mapStorage(values: Map<string, string>) {
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key)
  }
}
