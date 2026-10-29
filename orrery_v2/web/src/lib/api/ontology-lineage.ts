import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'
export type LineageSummaryDTO = components['schemas']['OntologyLineageSummaryDTO']
export type ProvenanceAnchorDTO = components['schemas']['OntologyLineageProvenanceAnchorDTO']
export type AssertionPageDTO = components['schemas']['OntologyLineageAssertionPageDTO']
export type EvidencePageDTO = components['schemas']['OntologyLineageEvidencePageDTO']
export type ProjectionTracePageDTO = components['schemas']['OntologyLineageProjectionTracePageDTO']

function queryString(query: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) if (value !== undefined && String(value).trim() !== '') params.set(key, String(value))
  return params.toString() ? `?${params.toString()}` : ''
}

export const ontologyLineageApi = {
  summary: (tenantId: string, options?: ApiFetchOptions) => apiFetch<LineageSummaryDTO>(`/api/ontology/lineage/summary${queryString({ tenant_id: tenantId.trim() })}`, options),
  provenance: (tenantId: string, factId: string, options?: ApiFetchOptions) => apiFetch<ProvenanceAnchorDTO>(`/api/ontology/lineage/facts/${encodeURIComponent(factId)}/provenance${queryString({ tenant_id: tenantId.trim() })}`, options),
  assertions: (tenantId: string, factId: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => apiFetch<AssertionPageDTO>(`/api/ontology/lineage/facts/${encodeURIComponent(factId)}/assertions${queryString({ tenant_id: tenantId.trim(), ...query })}`, options),
  evidence: (tenantId: string, factId: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => apiFetch<EvidencePageDTO>(`/api/ontology/lineage/facts/${encodeURIComponent(factId)}/evidence${queryString({ tenant_id: tenantId.trim(), ...query })}`, options),
  projectionTrace: (tenantId: string, factId: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => apiFetch<ProjectionTracePageDTO>(`/api/ontology/lineage/facts/${encodeURIComponent(factId)}/projection-trace${queryString({ tenant_id: tenantId.trim(), ...query })}`, options)
}
