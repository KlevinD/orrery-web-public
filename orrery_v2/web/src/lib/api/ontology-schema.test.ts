import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologySchemaApi } from './ontology-schema'

describe('ontology schema API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('omits the query marker for all-visible top-level reads', async () => {
    await ontologySchemaApi.summary('')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/schema/summary', undefined)
    await ontologySchemaApi.definitions({ tenantId: '' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/schema/definitions', undefined)
    await ontologySchemaApi.graph('')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/schema/graph', undefined)
  })
})
