import { ApiClientError } from '#lib/api/client'

const rateLimitRetries = 4
const queryBudgetRetries = 2

export function retryOntologyRead(failureCount: number, error: Error) {
  if (error.name === 'AbortError') return false
  if (!(error instanceof ApiClientError)) return failureCount < 1
  if (error.status === 429 || error.code === 'rate_limited') return failureCount < rateLimitRetries
  if (error.status === 503 && error.code === 'query_budget_exceeded') return failureCount < queryBudgetRetries
  if (error.status >= 400 && error.status < 500) return false
  return failureCount < 1
}

export function ontologyReadRetryDelay(attemptIndex: number, error: Error) {
  const baseDelay = error instanceof ApiClientError && error.code === 'query_budget_exceeded' ? 1_500 : 1_000
  return Math.min(baseDelay * 2 ** Math.max(0, attemptIndex), 4_000)
}

export const ontologyReadRetryOptions = {
  retry: retryOntologyRead,
  retryDelay: ontologyReadRetryDelay
}
