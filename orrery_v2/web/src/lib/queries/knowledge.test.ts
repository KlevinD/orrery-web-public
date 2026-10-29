import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  buildKnowledgeBaseQuery,
  createKnowledgeBase,
  deleteKnowledgeBase,
  deleteKnowledgeDocument,
  getKnowledgeDocumentProcessingResult,
  listKnowledgeBases,
  loadKnowledgeGraphDocuments,
  reindexKnowledgeBase,
  refreshKnowledgeDocumentStatus,
  reprocessKnowledgeDocument,
  searchKnowledge,
  syncKnowledgeDocuments,
  updateKnowledgeBase,
  uploadKnowledgeDocument
} from './knowledge'

const { apiFetchMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn()
}))

vi.mock('#lib/api/client', () => ({
  apiFetch: apiFetchMock
}))

describe('knowledge queries', () => {
  it('bounds graph document requests to batches of four and preserves every base', async () => {
    let release!: () => void
    const gate = new Promise<void>((resolve) => { release = resolve })
    apiFetchMock.mockImplementation(async () => {
      await gate
      return { items: [] }
    })
    const ids = Array.from({ length: 9 }, (_, index) => `base-${index}`)
    const result = loadKnowledgeGraphDocuments(ids)
    expect(apiFetchMock).toHaveBeenCalledTimes(4)
    release()
    expect(Object.keys(await result)).toEqual(ids)
    expect(apiFetchMock).toHaveBeenCalledTimes(9)
  })

  it('stops scheduling later graph batches after a failure', async () => {
    apiFetchMock.mockRejectedValue(new Error('unavailable'))
    await expect(loadKnowledgeGraphDocuments(['a', 'b', 'c', 'd', 'e'])).rejects.toThrow('unavailable')
    expect(apiFetchMock).toHaveBeenCalledTimes(4)
  })

  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('serializes knowledge base owner filters', () => {
    expect(buildKnowledgeBaseQuery({ ownerType: 'agent', ownerId: 'agent-alpha' })).toBe(
      '?owner_type=agent&owner_id=agent-alpha'
    )
  })

  it('lists knowledge bases with owner filters', async () => {
    await listKnowledgeBases({ ownerType: 'human', ownerId: 'human-operator' })

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases?owner_type=human&owner_id=human-operator', undefined)
  })

  it('posts JSON when creating a knowledge base', async () => {
    await createKnowledgeBase({
      name: 'Ops Knowledge',
      description: 'Runbooks',
      owner_type: 'organization',
      owner_id: 'org-core',
      visibility: 'private'
    })

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases', {
      method: 'POST',
      body: JSON.stringify({
        name: 'Ops Knowledge',
        description: 'Runbooks',
        owner_type: 'organization',
        owner_id: 'org-core',
        visibility: 'private'
      })
    })
  })

  it('deletes a knowledge base through the production API path', async () => {
    await deleteKnowledgeBase('kb-1')

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases/kb-1', {
      method: 'DELETE'
    })
  })

  it('updates knowledge base basic information through the production API path', async () => {
    await updateKnowledgeBase('kb-1', {
      name: 'Ops Knowledge',
      description: 'Runbooks',
      visibility: 'public'
    })

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases/kb-1', {
      method: 'PATCH',
      body: JSON.stringify({
        name: 'Ops Knowledge',
        description: 'Runbooks',
        visibility: 'public'
      })
    })
  })

  it('uploads documents as multipart form data', async () => {
    const file = new File(['hello'], 'guide.md', { type: 'text/markdown' })

    await uploadKnowledgeDocument('kb-1', file)

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases/kb-1/documents', {
      method: 'POST',
      body: expect.any(FormData)
    })
    const body = apiFetchMock.mock.calls[0]?.[1]?.body as FormData
    expect(body.get('file')).toBe(file)
  })

  it('searches a single knowledge base or all visible knowledge', async () => {
    await searchKnowledge({ query: 'release process', knowledgeBaseId: 'kb-1', limit: 5 })
    await searchKnowledge({ query: 'incident', knowledgeBaseIds: ['kb-1', 'kb-2'] })

    expect(apiFetchMock).toHaveBeenNthCalledWith(1, '/api/knowledge-bases/kb-1/search', {
      method: 'POST',
      body: JSON.stringify({ query: 'release process', limit: 5 })
    })
    expect(apiFetchMock).toHaveBeenNthCalledWith(2, '/api/knowledge/search', {
      method: 'POST',
      body: JSON.stringify({ query: 'incident', knowledge_base_ids: ['kb-1', 'kb-2'] })
    })
  })

  it('creates reindex jobs for a knowledge base', async () => {
    await reindexKnowledgeBase('kb-1')

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases/kb-1/reindex', {
      method: 'POST',
      body: JSON.stringify({})
    })
  })

  it('syncs the remote document catalog for a knowledge base', async () => {
    await syncKnowledgeDocuments('kb-personal')

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases/kb-personal/documents/sync', {
      method: 'POST',
      body: JSON.stringify({})
    })
  })

  it('refreshes, reprocesses, and previews document processing through production API paths', async () => {
    await refreshKnowledgeDocumentStatus('kb-1', 'doc-1')
    await reprocessKnowledgeDocument('kb-1', 'doc-1')
    await getKnowledgeDocumentProcessingResult('kb-1', 'doc-1', { pages: '1' })

    expect(apiFetchMock).toHaveBeenNthCalledWith(1, '/api/knowledge-bases/kb-1/documents/doc-1/status', undefined)
    expect(apiFetchMock).toHaveBeenNthCalledWith(2, '/api/knowledge-bases/kb-1/documents/doc-1/reprocess', {
      method: 'POST',
      body: JSON.stringify({})
    })
    expect(apiFetchMock).toHaveBeenNthCalledWith(3, '/api/knowledge-bases/kb-1/documents/doc-1/result?pages=1', undefined)
  })

  it('deletes documents through the production API path', async () => {
    await deleteKnowledgeDocument('kb-1', 'doc-1')

    expect(apiFetchMock).toHaveBeenCalledWith('/api/knowledge-bases/kb-1/documents/doc-1', {
      method: 'DELETE'
    })
  })
})
