import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'

type Schemas = components['schemas']
export type ContextCompanyOverviewDTO = Schemas['OntologyContextCompanyOverviewDTO']
export type ContextSummaryDTO = Schemas['OntologyContextSummaryDTO']
export type ContextObjectPageDTO = Schemas['OntologyContextObjectPageDTO']
export type ContextObjectDetailDTO = Schemas['OntologyContextObjectDetailDTO']
export type ContextFactPageDTO = Schemas['OntologyContextFactPageDTO']
export type ContextRelationPageDTO = Schemas['OntologyContextRelationPageDTO']
export type ContextEvidencePageDTO = Schemas['OntologyContextEvidencePageDTO']
export type ContextNeighborhoodDTO = Schemas['OntologyContextNeighborhoodDTO']

function queryString(query: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) if (value !== undefined && String(value).trim() !== '') params.set(key, String(value))
  return params.toString() ? `?${params.toString()}` : ''
}

export const ontologyContextApi = {
  companyOverview: (options?: ApiFetchOptions) => apiFetch<ContextCompanyOverviewDTO>('/api/ontology/context/company-overview', options),
  overview: (tenantId: string, options?: ApiFetchOptions) => apiFetch<ContextCompanyOverviewDTO>(`/api/ontology/context/overview${queryString({ tenant_id: tenantId })}`, options),
  summary: (tenantId: string, options?: ApiFetchOptions) => apiFetch<ContextSummaryDTO>(`/api/ontology/context/summary${queryString({ tenant_id: tenantId })}`, options),
  objects: (query: { tenantId: string; objectType?: string; status?: string; source?: string; search?: string; limit?: number; cursor?: string }, options?: ApiFetchOptions) => apiFetch<ContextObjectPageDTO>(`/api/ontology/context/objects${queryString({ tenant_id: query.tenantId, object_type: query.objectType, status: query.status, source: query.source, search: query.search, limit: query.limit, cursor: query.cursor })}`, options),
  object: (tenantId: string, id: string, options?: ApiFetchOptions) => apiFetch<ContextObjectDetailDTO>(`/api/ontology/context/objects/${encodeURIComponent(id)}${queryString({ tenant_id: tenantId })}`, options),
  facts: (tenantId: string, id: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => apiFetch<ContextFactPageDTO>(`/api/ontology/context/objects/${encodeURIComponent(id)}/facts${queryString({ tenant_id: tenantId, ...query })}`, options),
  relations: (tenantId: string, id: string, query: { direction?: 'in' | 'out' | 'both'; linkType?: string; limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => apiFetch<ContextRelationPageDTO>(`/api/ontology/context/objects/${encodeURIComponent(id)}/relations${queryString({ tenant_id: tenantId, direction: query.direction, link_type: query.linkType, limit: query.limit, cursor: query.cursor })}`, options),
  evidence: (tenantId: string, id: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => apiFetch<ContextEvidencePageDTO>(`/api/ontology/context/objects/${encodeURIComponent(id)}/evidence${queryString({ tenant_id: tenantId, ...query })}`, options),
  neighborhood: (query: { tenantId: string; rootId: string; linkType?: string; linkTypes?: string[]; depth?: number; maxNodes?: number; maxEdges?: number }, options?: ApiFetchOptions) => apiFetch<ContextNeighborhoodDTO>(`/api/ontology/context/neighborhood${queryString({ tenant_id: query.tenantId, root_id: query.rootId, link_type: query.linkType, link_types: query.linkTypes?.join(','), depth: query.depth, max_nodes: query.maxNodes, max_edges: query.maxEdges })}`, options)
}
