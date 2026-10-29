import { describe, expect, it } from 'vitest'
import { ApiClientError } from './api/client'
import { queryClient, retryOntologyQuery, retryTransientQuery, transientQueryRetryDelay } from './query-client'

describe('query defaults', () => {
  it('retries temporary gateway and network failures across an API restart window', () => {
    const badGateway = new ApiClientError({ status: 502, code: 'http_502', message: 'Bad Gateway' })
    const unauthorized = new ApiClientError({ status: 401, code: 'unauthorized', message: 'authorization is required' })
    const notFound = new ApiClientError({ status: 404, code: 'not_found', message: 'resource not found' })

    expect(retryTransientQuery(0, badGateway)).toBe(true)
    expect(retryTransientQuery(3, badGateway)).toBe(true)
    expect(retryTransientQuery(4, badGateway)).toBe(false)
    expect(retryTransientQuery(0, new TypeError('Failed to fetch'))).toBe(true)
    expect(retryTransientQuery(0, unauthorized)).toBe(false)
    expect(retryTransientQuery(0, notFound)).toBe(false)
    expect(transientQueryRetryDelay(0)).toBe(1_000)
    expect(transientQueryRetryDelay(3)).toBe(8_000)
    expect(transientQueryRetryDelay(5)).toBe(8_000)
  })
})

describe('ontology query defaults', () => {
  it('does not reuse protected ontology DTOs across page mounts', () => {
    expect(queryClient.getQueryDefaults(['ontology', 'context'])).toMatchObject({
      staleTime: 0,
      gcTime: 0,
      refetchOnMount: 'always'
    })
  })

  it('does not retry terminal ontology failures', () => {
    const budget = new ApiClientError({ status: 503, code: 'query_budget_exceeded', message: 'query budget exceeded' })
    const dependency = new ApiClientError({ status: 503, code: 'dependency_unavailable', message: 'dependency unavailable' })

    expect(retryOntologyQuery(0, budget)).toBe(false)
    expect(retryOntologyQuery(0, dependency)).toBe(false)
    expect(retryOntologyQuery(0, new Error('temporary failure'))).toBe(true)
    expect(retryOntologyQuery(1, new Error('temporary failure'))).toBe(false)
  })
})
