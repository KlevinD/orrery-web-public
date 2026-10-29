import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'

type ScopeCatalogPageDTO = components['schemas']['OntologyScopeCatalogPageDTO']
type ResolvedScopeDTO = components['schemas']['OntologyResolvedScopeDTO']
type OntologyScopeKind = components['schemas']['OntologyScopeCatalogItemDTO']['kind']

const ontologyScopeKinds: OntologyScopeKind[] = ['organization', 'project', 'mission', 'resource', 'system', 'custom']

export interface OntologyScopeCatalogQuery {
  search?: string
  kind?: OntologyScopeKind
  cursor?: string
  limit?: number
}

function queryString(query: OntologyScopeCatalogQuery) {
  const params = new URLSearchParams()
  if (query.search?.trim()) params.set('search', query.search.trim())
  if (query.kind) params.set('kind', query.kind)
  if (query.cursor?.trim()) params.set('cursor', query.cursor.trim())
  if (query.limit) params.set('limit', String(query.limit))
  const value = params.toString()
  return value ? `?${value}` : ''
}

export const ontologyScopesApi = {
  list(query: OntologyScopeCatalogQuery = {}, options?: ApiFetchOptions) {
    if (!query.kind && !query.cursor) return listAllOntologyScopes(query, options)
    return apiFetch<ScopeCatalogPageDTO>(`/api/ontology/scopes${queryString(query)}`, options)
  },
  resolve(tenantId: string, options?: ApiFetchOptions) {
    const params = new URLSearchParams({ tenant_id: tenantId.trim() })
    return apiFetch<ResolvedScopeDTO>(`/api/ontology/scopes/resolve?${params.toString()}`, options)
  }
}

export async function listAllOntologyScopes(query: Omit<OntologyScopeCatalogQuery, 'kind' | 'cursor'> = {}, options?: ApiFetchOptions): Promise<ScopeCatalogPageDTO> {
  const pages = await Promise.all(
    ontologyScopeKinds.map((kind) => ontologyScopesApi.list({ ...query, kind, limit: query.limit ?? 100 }, options))
  )
  const seen = new Set<string>()
  const items = pages.flatMap((page) => page.items).filter((item) => {
    if (seen.has(item.tenant_id)) return false
    seen.add(item.tenant_id)
    return true
  })
  return { items, page: { limit: query.limit ?? 100, has_more: pages.some((page) => page.page.has_more) } }
}

export type { ScopeCatalogPageDTO, ResolvedScopeDTO, OntologyScopeKind }
