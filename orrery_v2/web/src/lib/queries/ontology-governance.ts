import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { ontologyGovernanceApi } from '#lib/api/ontology-governance'
import { session } from '#lib/stores/session.svelte'
import { ontologyReadRetryOptions } from '#lib/ontology-workbench/query-retry'
import { runOntologySummaryQuery } from '#lib/ontology-workbench/summary-scheduler'
export function useOntologyGovernanceSummaryQuery(tenantId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); return { queryKey: ['ontology', 'governance', 'summary', session.securityContextVersion, tenant], queryFn: ({ signal }) => runOntologySummaryQuery(signal, () => ontologyGovernanceApi.summary(tenant, { signal })), enabled: enabled(), ...ontologyReadRetryOptions } })
}

export function useOntologyGovernanceProposalsQuery(query: () => { tenantId: string; queue?: 'actionable' | 'all'; status?: string; targetKind?: string; search?: string; limit?: number; cursor?: string }, enabled: () => boolean = () => true) {
  return createQuery(() => { const current = query(); return { queryKey: ['ontology', 'governance', 'proposals', session.securityContextVersion, current], queryFn: () => ontologyGovernanceApi.proposals(current), enabled: enabled() } })
}

export function useOntologyGovernanceProposalQuery(tenantId: () => string, proposalId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = proposalId().trim(); return { queryKey: ['ontology', 'governance', 'proposal', session.securityContextVersion, tenant, id], queryFn: () => ontologyGovernanceApi.proposal(tenant, id), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyGovernanceAnalysisQuery(tenantId: () => string, proposalId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = proposalId().trim(); return { queryKey: ['ontology', 'governance', 'analysis', session.securityContextVersion, tenant, id], queryFn: () => ontologyGovernanceApi.analysis(tenant, id), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyGovernanceReceiptsQuery(tenantId: () => string, proposalId: () => string, query: () => { limit?: number; cursor?: string } = () => ({ limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = proposalId().trim(); const current = query(); return { queryKey: ['ontology', 'governance', 'receipts', session.securityContextVersion, tenant, id, current], queryFn: () => ontologyGovernanceApi.receipts(tenant, id, current), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyGovernanceAuditEventsQuery(tenantId: () => string, proposalId: () => string, query: () => { limit?: number; cursor?: string } = () => ({ limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = proposalId().trim(); const current = query(); return { queryKey: ['ontology', 'governance', 'audit-events', session.securityContextVersion, tenant, id, current], queryFn: () => ontologyGovernanceApi.auditEvents(tenant, id, current), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

function governanceRoot() {
  return ['ontology', 'governance'] as const
}

export function useOntologyGovernanceDecisionMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; proposalId: string; expectedRevision: number; decision: 'accepted' | 'rejected'; reason: string }) => ontologyGovernanceApi.decide(input.tenantId, input.proposalId, { decision: input.decision, reason: input.reason, expected_revision: input.expectedRevision }),
    onSuccess: () => client.invalidateQueries({ queryKey: governanceRoot() })
  }))
}

export function useOntologyGovernanceCreatePlanMutation() {
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; proposalId: string; expectedRevision: number }) => ontologyGovernanceApi.createPlan(input.tenantId, input.proposalId, input.expectedRevision)
  }))
}

export function useOntologyGovernanceApplyPlanMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; planId: string; idempotencyKey: string }) => ontologyGovernanceApi.applyPlan(input.tenantId, input.planId, input.idempotencyKey),
    onSuccess: () => client.invalidateQueries({ queryKey: governanceRoot() })
  }))
}

export function useOntologyGovernanceProjectionRefreshRetryMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: (input: { tenantId: string; receiptId: string; idempotencyKey: string }) => ontologyGovernanceApi.retryProjectionRefresh(input.tenantId, input.receiptId, input.idempotencyKey),
    onSuccess: () => client.invalidateQueries({ queryKey: governanceRoot() })
  }))
}
