import { apiFetch, type ApiFetchOptions } from './client'
import { createIdempotencyKey } from '#lib/runtime/ids'
import type { components } from '#lib/types/api.generated'

type Schemas = components['schemas']
export type ProblemCaseDTO = Schemas['OntologyProblemCaseDTO']
export type ProblemListDTO = Schemas['OntologyProblemListDTO']
export type ProblemOwnerDTO = Schemas['OntologyProblemActorDTO']
export type ProblemStatus = ProblemCaseDTO['status']

export interface ProblemListQuery {
  tenantId: string
  status?: ProblemStatus | ''
  ownerKind?: string
  ownerId?: string
  sourceDomain?: string
  search?: string
  limit?: number
}

function queryString(query: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) if (value !== undefined && String(value).trim() !== '') params.set(key, String(value))
  return params.toString() ? `?${params.toString()}` : ''
}

export const ontologyProblemsApi = {
  list: (query: ProblemListQuery, options?: ApiFetchOptions) =>
    apiFetch<ProblemListDTO>(`/api/ontology/problems${queryString({
      tenant_id: query.tenantId.trim(), status: query.status, owner_kind: query.ownerKind,
      owner_id: query.ownerId, source_domain: query.sourceDomain, search: query.search, limit: query.limit
    })}`, options),
  problem: (tenantId: string, problemId: string, options?: ApiFetchOptions) =>
    apiFetch<ProblemCaseDTO>(`/api/ontology/problems/${encodeURIComponent(problemId.trim())}${queryString({ tenant_id: tenantId.trim() })}`, options),
  route: (tenantId: string, problemId: string, owner: ProblemOwnerDTO, options?: ApiFetchOptions) =>
    apiFetch<ProblemCaseDTO>(`/api/ontology/problems/${encodeURIComponent(problemId.trim())}/route${queryString({ tenant_id: tenantId.trim() })}`, {
      ...options,
      method: 'POST',
      idempotencyKey: options?.idempotencyKey?.trim() || createIdempotencyKey(),
      body: JSON.stringify({ owner: { kind: owner.kind.trim(), id: owner.id.trim() } })
    })
}
