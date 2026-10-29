import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyScopesApi } from './ontology-scopes'

describe('ontology scope API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('sends bounded search and kind filters with request cancellation', async () => {
    const controller = new AbortController()
    await ontologyScopesApi.list({ search: 'launch plan', kind: 'project', cursor: 'opaque', limit: 20 }, { signal: controller.signal })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/ontology/scopes?search=launch+plan&kind=project&cursor=opaque&limit=20',
      { signal: controller.signal }
    )
  })

  it('splits an unfiltered catalog into bounded scope-kind requests', async () => {
    apiFetchMock.mockImplementation((path?: string) => {
      if (!path) return Promise.resolve({ items: [], page: { limit: 100, has_more: false } })
      if (path.includes('kind=organization')) return Promise.resolve({ items: [{ tenant_id: 'org-1', label: 'Organization', kind: 'organization' }], page: { limit: 100, has_more: false } })
      if (path.includes('kind=system')) return Promise.resolve({ items: [{ tenant_id: 'system', label: 'System', kind: 'system' }], page: { limit: 100, has_more: false } })
      return Promise.resolve({ items: [], page: { limit: 100, has_more: false } })
    })

    await expect(ontologyScopesApi.list({ limit: 100 })).resolves.toEqual({
      items: [
        { tenant_id: 'org-1', label: 'Organization', kind: 'organization' },
        { tenant_id: 'system', label: 'System', kind: 'system' }
      ],
      page: { limit: 100, has_more: false }
    })

    expect(apiFetchMock).toHaveBeenCalledTimes(6)
    for (const kind of ['organization', 'project', 'mission', 'resource', 'system', 'custom']) {
      expect(apiFetchMock).toHaveBeenCalledWith(`/api/ontology/scopes?kind=${kind}&limit=100`, undefined)
    }
  })
})
