import { apiFetch, type ApiFetchOptions } from './client'
import { createIdempotencyKey } from '#lib/runtime/ids'
import type { components } from '#lib/types/api.generated'
export type ProjectionSummaryDTO = components['schemas']['OntologyProjectionSummaryDTO']
export type ProjectionAuditPageDTO = components['schemas']['OntologyProjectionAuditPageDTO']
export type ProjectionAuditDetailDTO = components['schemas']['OntologyProjectionAuditDetailDTO']
export type ProjectionReplayHistoryPageDTO = components['schemas']['OntologyProjectionReplayHistoryPageDTO']
export type ProjectionMutationResultDTO = components['schemas']['OntologyProjectionMutationResultDTO']

function queryString(query: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) if (value !== undefined && String(value).trim() !== '') params.set(key, String(value))
  return params.toString() ? `?${params.toString()}` : ''
}

function mutationOptions(options: ApiFetchOptions | undefined): ApiFetchOptions {
  return { ...options, idempotencyKey: options?.idempotencyKey?.trim() || createIdempotencyKey() }
}

export const ontologyProjectionsApi = {
  summary: (tenantId: string, options?: ApiFetchOptions) => apiFetch<ProjectionSummaryDTO>(`/api/ontology/projections/summary${queryString({ tenant_id: tenantId.trim() })}`, options),
  audits: (query: { tenantId: string; status?: string; reasonCode?: string; limit?: number; cursor?: string }, options?: ApiFetchOptions) => apiFetch<ProjectionAuditPageDTO>(`/api/ontology/projections/audits${queryString({ tenant_id: query.tenantId.trim(), status: query.status, reason_code: query.reasonCode, limit: query.limit, cursor: query.cursor })}`, options),
  audit: (tenantId: string, auditId: string, options?: ApiFetchOptions) => apiFetch<ProjectionAuditDetailDTO>(`/api/ontology/projections/audits/${encodeURIComponent(auditId)}${queryString({ tenant_id: tenantId.trim() })}`, options),
  replayHistory: (tenantId: string, auditId: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => apiFetch<ProjectionReplayHistoryPageDTO>(`/api/ontology/projections/audits/${encodeURIComponent(auditId)}/replay-history${queryString({ tenant_id: tenantId.trim(), ...query })}`, options),
  replay: (tenantId: string, auditId: string, options?: ApiFetchOptions) => apiFetch<ProjectionMutationResultDTO>(`/api/ontology/projections/audits/${encodeURIComponent(auditId)}/replays${queryString({ tenant_id: tenantId.trim() })}`, { ...mutationOptions(options), method: 'POST', body: '{}' }),
  acknowledge: (tenantId: string, auditId: string, reason: string, options?: ApiFetchOptions) => apiFetch<ProjectionMutationResultDTO>(`/api/ontology/projections/audits/${encodeURIComponent(auditId)}/acknowledgements${queryString({ tenant_id: tenantId.trim() })}`, { ...mutationOptions(options), method: 'POST', body: JSON.stringify({ reason }) })
}
