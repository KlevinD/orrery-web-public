import { createQuery } from '@tanstack/svelte-query'
import { ontologyLineageApi } from '#lib/api/ontology-lineage'
import { session } from '#lib/stores/session.svelte'
import { ontologyReadRetryOptions } from '#lib/ontology-workbench/query-retry'
import { runOntologySummaryQuery } from '#lib/ontology-workbench/summary-scheduler'
export function useOntologyLineageSummaryQuery(tenantId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); return { queryKey: ['ontology', 'lineage', 'summary', session.securityContextVersion, tenant], queryFn: ({ signal }) => runOntologySummaryQuery(signal, () => ontologyLineageApi.summary(tenant, { signal })), enabled: enabled(), ...ontologyReadRetryOptions } })
}

const key = (...parts: unknown[]) => ['ontology', 'lineage', session.securityContextVersion, ...parts] as const

export function useOntologyLineageProvenanceQuery(tenantId: () => string, factId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const fact = factId().trim(); return { queryKey: key('provenance', tenant, fact), queryFn: ({ signal }) => ontologyLineageApi.provenance(tenant, fact, { signal }), enabled: enabled() && tenant.length > 0 && fact.length > 0 } })
}

export function useOntologyLineageAssertionsQuery(tenantId: () => string, factId: () => string, query: () => { limit?: number; cursor?: string } = () => ({ limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const fact = factId().trim(); const current = query(); return { queryKey: key('assertions', tenant, fact, current), queryFn: ({ signal }) => ontologyLineageApi.assertions(tenant, fact, current, { signal }), enabled: enabled() && tenant.length > 0 && fact.length > 0 } })
}

export function useOntologyLineageEvidenceQuery(tenantId: () => string, factId: () => string, query: () => { limit?: number; cursor?: string } = () => ({ limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const fact = factId().trim(); const current = query(); return { queryKey: key('evidence', tenant, fact, current), queryFn: ({ signal }) => ontologyLineageApi.evidence(tenant, fact, current, { signal }), enabled: enabled() && tenant.length > 0 && fact.length > 0 } })
}

export function useOntologyLineageProjectionTraceQuery(tenantId: () => string, factId: () => string, query: () => { limit?: number; cursor?: string } = () => ({ limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const fact = factId().trim(); const current = query(); return { queryKey: key('projection-trace', tenant, fact, current), queryFn: ({ signal }) => ontologyLineageApi.projectionTrace(tenant, fact, current, { signal }), enabled: enabled() && tenant.length > 0 && fact.length > 0 } })
}
