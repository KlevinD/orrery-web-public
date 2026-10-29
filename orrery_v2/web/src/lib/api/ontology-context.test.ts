import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyContextApi } from './ontology-context'

describe('ontology context API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('binds object filters and bounded neighborhood inputs to a tenant', async () => {
    await ontologyContextApi.objects({ tenantId: 'tenant 1', objectType: 'mission', status: 'active', source: 'mission', search: 'launch', limit: 50, cursor: 'opaque' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/context/objects?tenant_id=tenant+1&object_type=mission&status=active&source=mission&search=launch&limit=50&cursor=opaque', undefined)

    await ontologyContextApi.neighborhood({ tenantId: 'tenant 1', rootId: 'object/1', linkType: 'depends_on', depth: 2, maxNodes: 120, maxEdges: 240 })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/context/neighborhood?tenant_id=tenant+1&root_id=object%2F1&link_type=depends_on&depth=2&max_nodes=120&max_edges=240', undefined)
    await ontologyContextApi.neighborhood({ tenantId: 'tenant 1', rootId: 'object/1', linkTypes: ['has_task', 'assigned_to'], depth: 2 })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/context/neighborhood?tenant_id=tenant+1&root_id=object%2F1&link_types=has_task%2Cassigned_to&depth=2', undefined)
  })

  it('omits the query marker for all-visible summary and object reads', async () => {
    await ontologyContextApi.companyOverview()
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/context/company-overview', undefined)
    await ontologyContextApi.overview('tenant 1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/context/overview?tenant_id=tenant+1', undefined)
    await ontologyContextApi.summary('')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/context/summary', undefined)
    await ontologyContextApi.objects({ tenantId: '' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/context/objects', undefined)
  })
})
