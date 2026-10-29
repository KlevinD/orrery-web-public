import { createQuery } from '@tanstack/svelte-query'
import { ontologySchemaApi, type SchemaDefinitionKind, type SchemaDefinitionQuery } from '#lib/api/ontology-schema'
import { session } from '#lib/stores/session.svelte'
import { ontologyReadRetryOptions } from '#lib/ontology-workbench/query-retry'
import { runOntologySummaryQuery } from '#lib/ontology-workbench/summary-scheduler'

const key = (...parts: unknown[]) => ['ontology', 'schema', session.securityContextVersion, ...parts] as const

export function useOntologySchemaSummaryQuery(tenantId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const tenant = tenantId().trim()
    return { queryKey: key('summary', tenant), queryFn: ({ signal }) => runOntologySummaryQuery(signal, () => ontologySchemaApi.summary(tenant, { signal })), enabled: enabled(), ...ontologyReadRetryOptions }
  })
}

export function useOntologySchemaDefinitionsQuery(query: () => SchemaDefinitionQuery, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = query()
    return { queryKey: key('definitions', current), queryFn: () => ontologySchemaApi.definitions(current), enabled: enabled() }
  })
}

export function useOntologySchemaDefinitionQuery(tenantId: () => string, kind: () => SchemaDefinitionKind, id: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const tenant = tenantId().trim(); const definitionKind = kind(); const definitionId = id().trim()
    return { queryKey: key('definition', tenant, definitionKind, definitionId), queryFn: () => ontologySchemaApi.definition(tenant, definitionKind, definitionId), enabled: enabled() && tenant.length > 0 && definitionId.length > 0 }
  })
}

export function useOntologySchemaReferencesQuery(tenantId: () => string, kind: () => SchemaDefinitionKind, id: () => string, query: () => { direction?: 'in' | 'out' | 'both'; limit?: number; cursor?: string } = () => ({ direction: 'both', limit: 25 }), enabled: () => boolean = () => true) {
  return createQuery(() => { const tenant = tenantId().trim(); const definitionKind = kind(); const definitionId = id().trim(); const current = query(); return { queryKey: key('references', tenant, definitionKind, definitionId, current), queryFn: () => ontologySchemaApi.references(tenant, definitionKind, definitionId, current), enabled: enabled() && tenant.length > 0 && definitionId.length > 0 } })
}

export function useOntologySchemaGraphQuery(tenantId: () => string, query: () => { view?: 'semantic' | 'projection'; search?: string; maxNodes?: number } = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const tenant = tenantId().trim(); const current = query()
    return { queryKey: key('graph', tenant, current), queryFn: () => ontologySchemaApi.graph(tenant, current), enabled: enabled() }
  })
}
