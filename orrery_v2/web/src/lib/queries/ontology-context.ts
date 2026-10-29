import { createQuery } from '@tanstack/svelte-query'
import { ontologyContextApi } from '#lib/api/ontology-context'
import { session } from '#lib/stores/session.svelte'
import { ontologyReadRetryOptions } from '#lib/ontology-workbench/query-retry'
import { runOntologySummaryQuery } from '#lib/ontology-workbench/summary-scheduler'

const key = (...parts: unknown[]) => ['ontology', 'context', session.securityContextVersion, ...parts] as const

export function useOntologyContextCompanyOverviewQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({ queryKey: key('company-overview'), queryFn: ({ signal }) => runOntologySummaryQuery(signal, () => ontologyContextApi.companyOverview({ signal })), enabled: enabled(), ...ontologyReadRetryOptions }))
}

export function useOntologyContextOverviewQuery(tenantId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); return { queryKey: key('overview', tenant), queryFn: ({ signal }) => runOntologySummaryQuery(signal, () => ontologyContextApi.overview(tenant, { signal })), enabled: enabled() && tenant.length > 0, ...ontologyReadRetryOptions } })
}

export function useOntologyContextSummaryQuery(tenantId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); return { queryKey: key('summary', tenant), queryFn: ({ signal }) => runOntologySummaryQuery(signal, () => ontologyContextApi.summary(tenant, { signal })), enabled: enabled(), ...ontologyReadRetryOptions } })
}

export function useOntologyContextObjectsQuery(query: () => { tenantId: string; objectType?: string; status?: string; source?: string; search?: string; limit?: number; cursor?: string }, enabled: () => boolean = () => true) {
  return createQuery(() => { const current = query(); return { queryKey: key('objects', current), queryFn: ({ signal }) => ontologyContextApi.objects(current, { signal }), enabled: enabled() } })
}

export function useOntologyContextObjectQuery(tenantId: () => string, objectId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = objectId().trim(); return { queryKey: key('object', tenant, id), queryFn: ({ signal }) => ontologyContextApi.object(tenant, id, { signal }), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyContextFactsQuery(tenantId: () => string, objectId: () => string, query: () => { limit?: number; cursor?: string } = () => ({ limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = objectId().trim(); const current = query(); return { queryKey: key('facts', tenant, id, current), queryFn: ({ signal }) => ontologyContextApi.facts(tenant, id, current, { signal }), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyContextRelationsQuery(tenantId: () => string, objectId: () => string, query: () => { direction?: 'in' | 'out' | 'both'; linkType?: string; limit?: number; cursor?: string } = () => ({ direction: 'both', limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = objectId().trim(); const current = query(); return { queryKey: key('relations', tenant, id, current), queryFn: ({ signal }) => ontologyContextApi.relations(tenant, id, current, { signal }), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyContextEvidenceQuery(tenantId: () => string, objectId: () => string, query: () => { limit?: number; cursor?: string } = () => ({ limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const id = objectId().trim(); const current = query(); return { queryKey: key('evidence', tenant, id, current), queryFn: ({ signal }) => ontologyContextApi.evidence(tenant, id, current, { signal }), enabled: enabled() && tenant.length > 0 && id.length > 0 } })
}

export function useOntologyContextNeighborhoodQuery(query: () => { tenantId: string; rootId: string; linkType?: string; linkTypes?: string[]; depth?: number; maxNodes?: number; maxEdges?: number }, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = query()
    const tenant = current.tenantId.trim()
    const root = current.rootId.trim()
    return {
      queryKey: key('neighborhood', current),
      queryFn: ({ signal }) => ontologyContextApi.neighborhood(current, { signal }),
      enabled: enabled() && tenant.length > 0 && root.length > 0,
      ...ontologyReadRetryOptions
    }
  })
}
