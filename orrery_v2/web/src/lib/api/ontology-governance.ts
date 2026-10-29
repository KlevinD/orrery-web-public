import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'
export type GovernanceSummaryDTO = components['schemas']['OntologyGovernanceSummaryDTO']
export type GovernanceProposalPageDTO = components['schemas']['OntologyGovernanceProposalPageDTO']
export type GovernanceProposalDetailDTO = components['schemas']['OntologyGovernanceProposalDetailDTO']
export type GovernanceAnalysisDTO = components['schemas']['OntologyGovernanceAnalysisDTO']
export type GovernanceReceiptPageDTO = components['schemas']['OntologyGovernanceReceiptPageDTO']
export type GovernanceAuditEventPageDTO = components['schemas']['OntologyGovernanceAuditEventPageDTO']
export type GovernanceDecisionRequestDTO = components['schemas']['OntologyGovernanceDecisionRequestDTO']
export type GovernanceDecisionResultDTO = components['schemas']['OntologyGovernanceDecisionResultDTO']
export type GovernancePromotionPlanDTO = components['schemas']['OntologyGovernancePromotionPlanDTO']
export type GovernanceApplyResultDTO = components['schemas']['OntologyGovernanceApplyResultDTO']
export type GovernanceProjectionRefreshResultDTO = components['schemas']['OntologyGovernanceProjectionRefreshResultDTO']

function querySuffix(params: URLSearchParams) {
  return params.toString() ? `?${params.toString()}` : ''
}

export const ontologyGovernanceApi = {
  summary: (tenantId: string, options?: ApiFetchOptions) => {
    const params = new URLSearchParams()
    if (tenantId.trim()) params.set('tenant_id', tenantId.trim())
    return apiFetch<GovernanceSummaryDTO>(`/api/ontology/governance/summary${querySuffix(params)}`, options)
  },
  proposals: (query: { tenantId: string; queue?: 'actionable' | 'all'; status?: string; targetKind?: string; search?: string; limit?: number; cursor?: string }, options?: ApiFetchOptions) => {
    const params = new URLSearchParams()
    if (query.tenantId.trim()) params.set('tenant_id', query.tenantId.trim())
    if (query.queue) params.set('queue', query.queue); if (query.status) params.set('status', query.status); if (query.targetKind) params.set('kind', query.targetKind); if (query.search) params.set('search', query.search)
    if (query.limit) params.set('limit', String(query.limit)); if (query.cursor) params.set('cursor', query.cursor)
    return apiFetch<GovernanceProposalPageDTO>(`/api/ontology/governance/proposals${querySuffix(params)}`, options)
  },
  proposal: (tenantId: string, proposalId: string, options?: ApiFetchOptions) => apiFetch<GovernanceProposalDetailDTO>(`/api/ontology/governance/proposals/${encodeURIComponent(proposalId)}?tenant_id=${encodeURIComponent(tenantId.trim())}`, options),
  analysis: (tenantId: string, proposalId: string, options?: ApiFetchOptions) => apiFetch<GovernanceAnalysisDTO>(`/api/ontology/governance/proposals/${encodeURIComponent(proposalId)}/analysis?tenant_id=${encodeURIComponent(tenantId.trim())}`, options),
  receipts: (tenantId: string, proposalId: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => {
    const params = new URLSearchParams({ tenant_id: tenantId.trim() })
    if (query.limit) params.set('limit', String(query.limit)); if (query.cursor) params.set('cursor', query.cursor)
    return apiFetch<GovernanceReceiptPageDTO>(`/api/ontology/governance/proposals/${encodeURIComponent(proposalId)}/receipts?${params.toString()}`, options)
  },
  auditEvents: (tenantId: string, proposalId: string, query: { limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) => {
    const params = new URLSearchParams({ tenant_id: tenantId.trim() })
    if (query.limit) params.set('limit', String(query.limit)); if (query.cursor) params.set('cursor', query.cursor)
    return apiFetch<GovernanceAuditEventPageDTO>(`/api/ontology/governance/proposals/${encodeURIComponent(proposalId)}/audit-events?${params.toString()}`, options)
  },
  decide: (tenantId: string, proposalId: string, body: GovernanceDecisionRequestDTO, options?: ApiFetchOptions) => apiFetch<GovernanceDecisionResultDTO>(`/api/ontology/governance/proposals/${encodeURIComponent(proposalId)}/decisions?tenant_id=${encodeURIComponent(tenantId.trim())}`, { ...options, method: 'POST', body: JSON.stringify(body) }),
  createPlan: (tenantId: string, proposalId: string, expectedRevision: number, options?: ApiFetchOptions) => apiFetch<GovernancePromotionPlanDTO>(`/api/ontology/governance/proposals/${encodeURIComponent(proposalId)}/plans?tenant_id=${encodeURIComponent(tenantId.trim())}`, { ...options, method: 'POST', body: JSON.stringify({ expected_revision: expectedRevision }) }),
  applyPlan: (tenantId: string, planId: string, idempotencyKey: string, options?: ApiFetchOptions) => apiFetch<GovernanceApplyResultDTO>(`/api/ontology/governance/plans/${encodeURIComponent(planId)}/apply?tenant_id=${encodeURIComponent(tenantId.trim())}`, { ...options, method: 'POST', idempotencyKey, body: JSON.stringify({ idempotency_key: idempotencyKey, confirmed: true }) }),
  retryProjectionRefresh: (tenantId: string, receiptId: string, idempotencyKey: string, options?: ApiFetchOptions) => apiFetch<GovernanceProjectionRefreshResultDTO>(`/api/ontology/governance/receipts/${encodeURIComponent(receiptId)}/projection-refresh-retries?tenant_id=${encodeURIComponent(tenantId.trim())}`, { ...options, method: 'POST', idempotencyKey })
}
