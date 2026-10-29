import { apiFetch, type ApiFetchOptions } from '#lib/api/client'
import type {
  BindKnowledgeBaseCommand,
  CatalogReconcileResultDTO,
  CreateKnowledgeBaseCommand,
  CreateKnowledgeDocumentCommand,
  KnowledgeBaseDTO,
  KnowledgeBasePageDTO,
  KnowledgeBindingDTO,
  KnowledgeBindingPageDTO,
  KnowledgeDocumentDTO,
  KnowledgeDocumentPageDTO,
  KnowledgeDocumentRelationPageDTO,
  KnowledgeDocumentProcessingResultDTO,
  KnowledgeJobDTO,
  KnowledgeJobPageDTO,
  KnowledgeSearchPageDTO,
  KnowledgeSemanticSourceDTO,
  KnowledgeRevisionDTO,
  KnowledgeRevisionPageDTO,
  KnowledgeOntologyProjectionDTO,
  SearchKnowledgeCommand,
  UpdateKnowledgeBaseCommand
} from '#lib/types/api/knowledge'

export interface KnowledgeBaseFilters {
  ownerType?: string
  ownerId?: string
}

export interface KnowledgeSearchOptions extends SearchKnowledgeCommand {
  knowledgeBaseId?: string
  knowledgeBaseIds?: string[]
}

export const knowledgeQueryKeys = {
  all: ['knowledge'] as const,
  bases: (filters: KnowledgeBaseFilters = {}) => ['knowledge', 'bases', buildKnowledgeBaseQuery(filters)] as const,
  base: (knowledgeBaseId: string) => ['knowledge', 'base', knowledgeBaseId] as const,
  bindings: (knowledgeBaseId: string) => ['knowledge', 'bindings', knowledgeBaseId] as const,
  documents: (knowledgeBaseId: string) => ['knowledge', 'documents', knowledgeBaseId] as const,
  document: (knowledgeBaseId: string, documentId: string) => ['knowledge', 'document', knowledgeBaseId, documentId] as const,
  documentRelations: (knowledgeBaseId: string, documentId: string, direction = 'both') => ['knowledge', 'document-relations', knowledgeBaseId, documentId, direction] as const,
  documentResult: (knowledgeBaseId: string, documentId: string) => ['knowledge', 'document-result', knowledgeBaseId, documentId] as const,
  jobs: (knowledgeBaseId: string) => ['knowledge', 'jobs', knowledgeBaseId] as const,
  job: (jobId: string) => ['knowledge', 'job', jobId] as const
}

export function listKnowledgeBases(filters: KnowledgeBaseFilters = {}, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeBasePageDTO>(`/api/knowledge-bases${buildKnowledgeBaseQuery(filters)}`, options)
}

export async function loadKnowledgeGraphDocuments(baseIds: string[]) {
  const documents: Record<string, KnowledgeDocumentDTO[]> = {}
  // Bound graph fan-out independently of the number of knowledge bases.
  for (let offset = 0; offset < baseIds.length; offset += 4) {
    const pages = await Promise.all(baseIds.slice(offset, offset + 4).map(async (id) =>
      [id, (await listKnowledgeDocuments(id)).items ?? []] as const
    ))
    for (const [id, items] of pages) documents[id] = items
  }
  return documents
}

export function getKnowledgeBase(knowledgeBaseId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeBaseDTO>(`/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}`, options)
}

export function createKnowledgeBase(body: CreateKnowledgeBaseCommand, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeBaseDTO>('/api/knowledge-bases', jsonMutation('POST', body, options))
}

export function updateKnowledgeBase(knowledgeBaseId: string, body: UpdateKnowledgeBaseCommand, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeBaseDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}`,
    jsonMutation('PATCH', body, options)
  )
}

export function deleteKnowledgeBase(knowledgeBaseId: string, options?: ApiFetchOptions) {
  return apiFetch(`/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}`, { ...options, method: 'DELETE' })
}

export function listKnowledgeBindings(knowledgeBaseId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeBindingPageDTO>(`/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/bindings`, options)
}

export function bindKnowledgeBase(knowledgeBaseId: string, body: BindKnowledgeBaseCommand, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeBindingDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/bindings`,
    jsonMutation('POST', body, options)
  )
}

export function unbindKnowledgeBase(knowledgeBaseId: string, bindingId: string, options?: ApiFetchOptions) {
  return apiFetch(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/bindings/${encodeURIComponent(bindingId)}`,
    { ...options, method: 'DELETE' }
  )
}

export function listKnowledgeDocuments(knowledgeBaseId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeDocumentPageDTO>(`/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents`, options)
}

export function syncKnowledgeDocuments(knowledgeBaseId: string, options?: ApiFetchOptions) {
  return apiFetch<CatalogReconcileResultDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents/sync`,
    jsonMutation('POST', {}, options)
  )
}

export function getKnowledgeDocument(knowledgeBaseId: string, documentId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeDocumentDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents/${encodeURIComponent(documentId)}`,
    options
  )
}

export function createKnowledgeDocument(
  knowledgeBaseId: string,
  body: CreateKnowledgeDocumentCommand,
  options?: ApiFetchOptions
) {
  return apiFetch<KnowledgeDocumentDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents`,
    jsonMutation('POST', body, options)
  )
}

export function uploadKnowledgeDocument(knowledgeBaseId: string, file: File, options?: ApiFetchOptions) {
  const body = new FormData()
  body.set('file', file)
  return apiFetch<KnowledgeDocumentDTO>(`/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents`, {
    ...options,
    method: 'POST',
    body
  })
}

export function deleteKnowledgeDocument(knowledgeBaseId: string, documentId: string, options?: ApiFetchOptions) {
  return apiFetch(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents/${encodeURIComponent(documentId)}`,
    { ...options, method: 'DELETE' }
  )
}

export function refreshKnowledgeDocumentStatus(knowledgeBaseId: string, documentId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeDocumentDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents/${encodeURIComponent(documentId)}/status`,
    options
  )
}

export function reprocessKnowledgeDocument(knowledgeBaseId: string, documentId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeDocumentDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents/${encodeURIComponent(documentId)}/reprocess`,
    jsonMutation('POST', {}, options)
  )
}

export function getKnowledgeDocumentProcessingResult(
  knowledgeBaseId: string,
  documentId: string,
  params: { pages?: string } = {},
  options?: ApiFetchOptions
) {
  const query = params.pages ? `?pages=${encodeURIComponent(params.pages)}` : ''
  return apiFetch<KnowledgeDocumentProcessingResultDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents/${encodeURIComponent(documentId)}/result${query}`,
    options
  )
}

export function listKnowledgeJobs(knowledgeBaseId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeJobPageDTO>(`/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/jobs`, options)
}

export function getKnowledgeJob(jobId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeJobDTO>(`/api/knowledge-jobs/${encodeURIComponent(jobId)}`, options)
}

export function searchKnowledge({ knowledgeBaseId, knowledgeBaseIds, ...body }: KnowledgeSearchOptions, options?: ApiFetchOptions) {
  const path = knowledgeBaseId
    ? `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/search`
    : '/api/knowledge/search'
  const payload = knowledgeBaseId ? body : { ...body, knowledge_base_ids: knowledgeBaseIds ?? body.knowledge_base_ids }
  return apiFetch<KnowledgeSearchPageDTO>(path, jsonMutation('POST', payload, options))
}

export function reindexKnowledgeBase(knowledgeBaseId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeJobDTO>(
    `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/reindex`,
    jsonMutation('POST', {}, options)
  )
}

function workspaceDocumentPath(knowledgeBaseId: string, documentId: string) {
  return `/api/knowledge-bases/${encodeURIComponent(knowledgeBaseId)}/documents/${encodeURIComponent(documentId)}`
}

export function getKnowledgeDocumentSource(knowledgeBaseId: string, documentId: string, revision?: number, options?: ApiFetchOptions) {
  const query = revision ? `?revision=${revision}` : ''
  return apiFetch<KnowledgeSemanticSourceDTO>(`${workspaceDocumentPath(knowledgeBaseId, documentId)}/source${query}`, options)
}

export function updateKnowledgeDocumentSource(knowledgeBaseId: string, documentId: string, expectedRevision: number, content: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeRevisionDTO>(`${workspaceDocumentPath(knowledgeBaseId, documentId)}/source`, jsonMutation('PUT', { expected_revision: expectedRevision, content }, options))
}

export function listKnowledgeDocumentRevisions(knowledgeBaseId: string, documentId: string, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeRevisionPageDTO>(`${workspaceDocumentPath(knowledgeBaseId, documentId)}/revisions`, options)
}

export function publishKnowledgeDocumentRevision(knowledgeBaseId: string, documentId: string, revision: number, options?: ApiFetchOptions) {
  return apiFetch<KnowledgeRevisionDTO>(`${workspaceDocumentPath(knowledgeBaseId, documentId)}/revisions/${revision}`, jsonMutation('PUT', {}, options))
}

export function getKnowledgeDocumentOntology(knowledgeBaseId: string, documentId: string, revision?: number, options?: ApiFetchOptions) {
  const query = revision ? `?revision=${revision}` : ''
  return apiFetch<KnowledgeOntologyProjectionDTO>(`${workspaceDocumentPath(knowledgeBaseId, documentId)}/ontology${query}`, options)
}

export function listKnowledgeDocumentRelations(knowledgeBaseId: string, documentId: string, direction: 'in' | 'out' | 'both' = 'both', options?: ApiFetchOptions) {
  const query = new URLSearchParams({ direction })
  return apiFetch<KnowledgeDocumentRelationPageDTO>(`${workspaceDocumentPath(knowledgeBaseId, documentId)}/relations?${query.toString()}`, options)
}

export function buildKnowledgeBaseQuery(filters: KnowledgeBaseFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'owner_type', filters.ownerType)
  appendParam(params, 'owner_id', filters.ownerId)
  const query = params.toString()
  return query ? `?${query}` : ''
}

function appendParam(params: URLSearchParams, key: string, value: string | undefined) {
  if (value === undefined || value === '') return
  params.set(key, value)
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}
