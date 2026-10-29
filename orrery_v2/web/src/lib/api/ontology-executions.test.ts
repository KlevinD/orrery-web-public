import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyExecutionsApi } from './ontology-executions'

describe('ontology executions API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('binds agent task and run queries to the selected tenant', async () => {
    await ontologyExecutionsApi.tasks({ tenantId: 'tenant 1', status: 'running', search: 'weekly', limit: 50, cursor: 'opaque' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/executions/tasks?tenant_id=tenant+1&status=running&search=weekly&limit=50&cursor=opaque', undefined)

    await ontologyExecutionsApi.task('tenant 1', 'scheduled-task:1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/executions/tasks/scheduled-task%3A1?tenant_id=tenant+1', undefined)

    await ontologyExecutionsApi.runLineage('tenant 1', 'run/1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/executions/runs/run%2F1/lineage?tenant_id=tenant+1', undefined)
  })

  it('omits the query marker for all-visible task reads', async () => {
    await ontologyExecutionsApi.tasks({ tenantId: '' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/executions/tasks', undefined)
  })
})
