import { apiFetch, type ApiFetchOptions } from './client'
import type { components } from '#lib/types/api.generated'

type Schemas = components['schemas']
export type SchemaSummaryDTO = Schemas['OntologySchemaSummaryDTO']
export type SchemaDefinitionPageDTO = Schemas['OntologySchemaDefinitionPageDTO']
export type SchemaDefinitionDetailDTO = Schemas['OntologySchemaDefinitionDetailDTO']
export type SchemaDefinitionReferencePageDTO = Schemas['OntologySchemaDefinitionReferencePageDTO']
export type SchemaGraphDTO = Schemas['OntologySchemaGraphDTO']
export type SchemaDefinitionKind = Schemas['OntologySchemaDefinitionItemDTO']['kind']

export interface SchemaDefinitionQuery {
  tenantId: string
  kind?: SchemaDefinitionKind
  origin?: 'system' | 'tenant'
  status?: 'active' | 'disabled'
  search?: string
  limit?: number
  cursor?: string
}

function queryString(query: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && String(value).trim() !== '') params.set(key, String(value))
  }
  return params.toString() ? `?${params.toString()}` : ''
}

export const ontologySchemaApi = {
  summary(tenantId: string, options?: ApiFetchOptions) {
    return apiFetch<SchemaSummaryDTO>(`/api/ontology/schema/summary${queryString({ tenant_id: tenantId.trim() })}`, options)
  },
  definitions(query: SchemaDefinitionQuery, options?: ApiFetchOptions) {
    return apiFetch<SchemaDefinitionPageDTO>(`/api/ontology/schema/definitions${queryString({
      tenant_id: query.tenantId.trim(), kind: query.kind, origin: query.origin, status: query.status,
      search: query.search, limit: query.limit, cursor: query.cursor
    })}`, options)
  },
  definition(tenantId: string, kind: SchemaDefinitionKind, id: string, options?: ApiFetchOptions) {
    return apiFetch<SchemaDefinitionDetailDTO>(`/api/ontology/schema/definitions/${encodeURIComponent(kind)}/${encodeURIComponent(id)}${queryString({ tenant_id: tenantId.trim() })}`, options)
  },
  references(tenantId: string, kind: SchemaDefinitionKind, id: string, query: { direction?: 'in' | 'out' | 'both'; limit?: number; cursor?: string } = {}, options?: ApiFetchOptions) {
    return apiFetch<SchemaDefinitionReferencePageDTO>(`/api/ontology/schema/definitions/${encodeURIComponent(kind)}/${encodeURIComponent(id)}/references${queryString({ tenant_id: tenantId.trim(), ...query })}`, options)
  },
  graph(tenantId: string, query: { view?: 'semantic' | 'projection'; search?: string; maxNodes?: number } = {}, options?: ApiFetchOptions) {
    return apiFetch<SchemaGraphDTO>(`/api/ontology/schema/graph${queryString({ tenant_id: tenantId.trim(), view: query.view, search: query.search, max_nodes: query.maxNodes })}`, options)
  }
}
