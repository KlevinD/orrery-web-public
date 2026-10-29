import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { ontologyProblemsApi, type ProblemListQuery, type ProblemOwnerDTO } from '#lib/api/ontology-problems'
import { session } from '#lib/stores/session.svelte'

const root = () => ['ontology', 'problems'] as const
const key = (...parts: unknown[]) => [...root(), session.securityContextVersion, ...parts] as const

export function useOntologyProblemsQuery(query: () => ProblemListQuery, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = query()
    return {
      queryKey: key('list', current),
      queryFn: () => ontologyProblemsApi.list(current),
      enabled: enabled() && current.tenantId.trim().length > 0
    }
  })
}

export function useOntologyProblemQuery(tenantId: () => string, problemId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const tenant = tenantId().trim()
    const problem = problemId().trim()
    return {
      queryKey: key('detail', tenant, problem),
      queryFn: () => ontologyProblemsApi.problem(tenant, problem),
      enabled: enabled() && tenant.length > 0 && problem.length > 0
    }
  })
}

export function useOntologyProblemRouteMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; problemId: string; owner: ProblemOwnerDTO; idempotencyKey?: string }) =>
      ontologyProblemsApi.route(input.tenantId, input.problemId, input.owner, { idempotencyKey: input.idempotencyKey }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: root() })
      void client.invalidateQueries({ queryKey: ['ontology', 'context'] })
    }
  }))
}
