import { describe, expect, it } from 'vitest'
import { ApiClientError } from '#lib/api/client'
import { ontologyReadRetryDelay, retryOntologyRead } from './query-retry'

function apiError(status: number, code: string) {
  return new ApiClientError({ status, code, message: code })
}

describe('ontology query retry policy', () => {
  it('backs off through transient rate limits without retrying indefinitely', () => {
    const error = apiError(429, 'rate_limited')

    expect(retryOntologyRead(0, error)).toBe(true)
    expect(retryOntologyRead(3, error)).toBe(true)
    expect(retryOntologyRead(4, error)).toBe(false)
    expect(ontologyReadRetryDelay(0, error)).toBe(1_000)
    expect(ontologyReadRetryDelay(3, error)).toBe(4_000)
  })

  it('retries temporary query-budget exhaustion twice', () => {
    const error = apiError(503, 'query_budget_exceeded')

    expect(retryOntologyRead(0, error)).toBe(true)
    expect(retryOntologyRead(1, error)).toBe(true)
    expect(retryOntologyRead(2, error)).toBe(false)
    expect(ontologyReadRetryDelay(0, error)).toBe(1_500)
    expect(ontologyReadRetryDelay(2, error)).toBe(4_000)
  })

  it('does not retry aborts and keeps ordinary failures to one retry', () => {
    const aborted = new Error('aborted')
    aborted.name = 'AbortError'

    expect(retryOntologyRead(0, aborted)).toBe(false)
    expect(retryOntologyRead(0, apiError(403, 'forbidden'))).toBe(false)
    expect(retryOntologyRead(0, new Error('network'))).toBe(true)
    expect(retryOntologyRead(1, new Error('network'))).toBe(false)
  })
})
