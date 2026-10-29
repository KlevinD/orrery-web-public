import { afterEach, describe, expect, it, vi } from 'vitest'
import { newRequestId } from './request-id'

describe('newRequestId', () => {
  const originalCrypto = globalThis.crypto

  afterEach(() => {
    vi.restoreAllMocks()
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: originalCrypto
    })
  })

  it('uses native randomUUID when available', () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000042')

    expect(newRequestId()).toBe('00000000-0000-4000-8000-000000000042')
  })

  it('falls back when randomUUID is unavailable', () => {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: {
        getRandomValues: (bytes: Uint8Array) => {
          bytes.fill(0)
          return bytes
        }
      }
    })

    expect(newRequestId()).toBe('00000000-0000-4000-8000-000000000000')
  })
})
