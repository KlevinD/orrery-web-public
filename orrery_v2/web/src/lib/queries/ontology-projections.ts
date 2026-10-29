import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { ontologyProjectionsApi } from '#lib/api/ontology-projections'
import { session } from '#lib/stores/session.svelte'
import { ontologyReadRetryOptions } from '#lib/ontology-workbench/query-retry'
import { runOntologySummaryQuery } from '#lib/ontology-workbench/summary-scheduler'

const key = (...parts: unknown[]) => ['ontology', 'projections', session.securityContextVersion, ...parts] as const

export function useOntologyProjectionSummaryQuery(tenantId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); return { queryKey: key('summary', tenant), queryFn: ({ signal }) => runOntologySummaryQuery(signal, () => ontologyProjectionsApi.summary(tenant, { signal })), enabled: enabled(), ...ontologyReadRetryOptions } })
}

export function useOntologyProjectionAuditsQuery(query: () => { tenantId: string; status?: string; reasonCode?: string; limit?: number; cursor?: string }, enabled: () => boolean = () => true) {
  return createQuery(() => { const current = query(); return { queryKey: key('audits', current), queryFn: () => ontologyProjectionsApi.audits(current), enabled: enabled() } })
}

export function useOntologyProjectionAuditQuery(tenantId: () => string, auditId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = auditId().trim(); return { queryKey: key('audit', tenant, id), queryFn: () => ontologyProjectionsApi.audit(tenant, id), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyProjectionReplayHistoryQuery(tenantId: () => string, auditId: () => string, query: () => { limit?: number; cursor?: string } = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = auditId().trim(); const current = query(); return { queryKey: key('replay-history', tenant, id, current), queryFn: () => ontologyProjectionsApi.replayHistory(tenant, id, current), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyProjectionReplayMutation() {
  const client = useQueryClient()
  return createMutation(() => ({ mutationFn: ({ tenantId, auditId, idempotencyKey }: { tenantId: string; auditId: string; idempotencyKey: string }) => ontologyProjectionsApi.replay(tenantId, auditId, { idempotencyKey }), onSuccess: (_data, variables) => { client.invalidateQueries({ queryKey: key('audits') }); client.invalidateQueries({ queryKey: key('summary', variables.tenantId) }); client.invalidateQueries({ queryKey: key('audit', variables.tenantId, variables.auditId) }); client.invalidateQueries({ queryKey: key('replay-history', variables.tenantId, variables.auditId) }) } }))
}

export function useOntologyProjectionAcknowledgeMutation() {
  const client = useQueryClient()
  return createMutation(() => ({ mutationFn: ({ tenantId, auditId, reason, idempotencyKey }: { tenantId: string; auditId: string; reason: string; idempotencyKey: string }) => ontologyProjectionsApi.acknowledge(tenantId, auditId, reason, { idempotencyKey }), onSuccess: (_data, variables) => { client.invalidateQueries({ queryKey: key('audits') }); client.invalidateQueries({ queryKey: key('summary', variables.tenantId) }); client.invalidateQueries({ queryKey: key('audit', variables.tenantId, variables.auditId) }); client.invalidateQueries({ queryKey: key('replay-history', variables.tenantId, variables.auditId) }) } }))
}
