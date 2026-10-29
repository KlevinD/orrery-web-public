import { QueryClient } from '@tanstack/svelte-query'
import { ApiClientError } from '#lib/api/client'

const TRANSIENT_QUERY_STATUSES = new Set([502, 503, 504])
const TRANSIENT_QUERY_RETRY_LIMIT = 4

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: retryTransientQuery,
      retryDelay: transientQueryRetryDelay
    }
  }
})

queryClient.setQueryDefaults(['ontology'], {
  staleTime: 0,
  gcTime: 0,
  refetchOnMount: 'always',
  retry: retryOntologyQuery
})

export function retryTransientQuery(failureCount: number, error: Error) {
  if (error instanceof ApiClientError && !TRANSIENT_QUERY_STATUSES.has(error.status)) return false
  return failureCount < TRANSIENT_QUERY_RETRY_LIMIT
}

export function transientQueryRetryDelay(failureCount: number) {
  return Math.min(1_000 * 2 ** failureCount, 8_000)
}

export function retryOntologyQuery(failureCount: number, error: Error) {
  if (error instanceof ApiClientError && (error.code === 'query_budget_exceeded' || error.code === 'dependency_unavailable')) return false
  return failureCount < 1
}

export function clearSessionQueryState() {
  queryClient.clear()
}
