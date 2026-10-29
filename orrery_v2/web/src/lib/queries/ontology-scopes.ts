import { createQuery } from '@tanstack/svelte-query'
import { ontologyScopesApi, type OntologyScopeCatalogQuery } from '#lib/api/ontology-scopes'
import { ontologyReadRetryOptions } from '#lib/ontology-workbench/query-retry'
import { session } from '#lib/stores/session.svelte'

export const ontologyScopeQueryKeys = {
  catalog: (query: OntologyScopeCatalogQuery = {}) => [
    'ontology', 'scopes', 'catalog', session.securityContextVersion, query.search ?? '', query.kind ?? '', query.cursor ?? '', query.limit ?? 20
  ] as const,
  resolved: (tenantId: string) => ['ontology', 'scopes', 'resolved', session.securityContextVersion, tenantId] as const
}

export function useOntologyScopeCatalogQuery(query: () => OntologyScopeCatalogQuery = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = query()
    return {
      queryKey: ontologyScopeQueryKeys.catalog(current),
      queryFn: ({ signal }) => ontologyScopesApi.list(current, { signal }),
      enabled: enabled(),
      ...ontologyReadRetryOptions
    }
  })
}

export function useOntologyResolvedScopeQuery(tenantId: () => string, enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = tenantId().trim()
    return {
      queryKey: ontologyScopeQueryKeys.resolved(current),
      queryFn: ({ signal }) => ontologyScopesApi.resolve(current, { signal }),
      enabled: enabled() && current.length > 0
    }
  })
}
