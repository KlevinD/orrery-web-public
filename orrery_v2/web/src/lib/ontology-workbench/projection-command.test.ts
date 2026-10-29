import { describe, expect, it } from 'vitest'
import { ApiClientError } from '#lib/api/client'
import { shouldRotateProjectionCommandKey } from './projection-command'

describe('projection command idempotency', () => {
  it('rotates after a terminal server command result', () => {
    for (const code of ['dependency_unavailable', 'state_conflict', 'unavailable']) {
      expect(shouldRotateProjectionCommandKey(new ApiClientError({ status: 503, code, message: code }))).toBe(true)
    }
  })

  it('keeps the key for an ambiguous transport failure', () => {
    expect(shouldRotateProjectionCommandKey(new TypeError('network request failed'))).toBe(false)
    expect(shouldRotateProjectionCommandKey(new ApiClientError({ status: 502, code: 'http_502', message: 'Bad gateway' }))).toBe(false)
  })
})
