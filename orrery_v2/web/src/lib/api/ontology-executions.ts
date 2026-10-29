import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'

type Schemas = components['schemas']
export type AgentTaskPageDTO = Schemas['OntologyContextObjectPageDTO']
export type AgentExecutionDetailDTO = Schemas['OntologyContextObjectDetailDTO']
export type AgentRunLineageDTO = Schemas['OntologyContextNeighborhoodDTO']

function queryString(query: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && String(value).trim() !== '') params.set(key, String(value))
  }
  return params.toString() ? `?${params.toString()}` : ''
}

export const ontologyExecutionsApi = {
  tasks: (query: { tenantId: string; status?: string; search?: string; limit?: number; cursor?: string }, options?: ApiFetchOptions) =>
    apiFetch<AgentTaskPageDTO>(`/api/ontology/executions/tasks${queryString({ tenant_id: query.tenantId, status: query.status, search: query.search, limit: query.limit, cursor: query.cursor })}`, options),
  task: (tenantId: string, id: string, options?: ApiFetchOptions) =>
    apiFetch<AgentExecutionDetailDTO>(`/api/ontology/executions/tasks/${encodeURIComponent(id)}${queryString({ tenant_id: tenantId })}`, options),
  run: (tenantId: string, id: string, options?: ApiFetchOptions) =>
    apiFetch<AgentExecutionDetailDTO>(`/api/ontology/executions/runs/${encodeURIComponent(id)}${queryString({ tenant_id: tenantId })}`, options),
  runLineage: (tenantId: string, id: string, options?: ApiFetchOptions) =>
    apiFetch<AgentRunLineageDTO>(`/api/ontology/executions/runs/${encodeURIComponent(id)}/lineage${queryString({ tenant_id: tenantId })}`, options)
}
