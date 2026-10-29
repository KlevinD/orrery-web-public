import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyLineageApi } from './ontology-lineage'

describe('ontology lineage API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('omits the query marker for the all-visible summary', async () => {
    await ontologyLineageApi.summary('')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/lineage/summary', undefined)
  })
})
