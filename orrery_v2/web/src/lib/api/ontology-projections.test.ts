import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyProjectionsApi } from './ontology-projections'

describe('ontology projection API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('keeps every read and mutation tenant-scoped', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000174')
    await ontologyProjectionsApi.audits({ tenantId: 'tenant 1', status: 'failed', reasonCode: 'missing', limit: 20, cursor: 'opaque' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/projections/audits?tenant_id=tenant+1&status=failed&reason_code=missing&limit=20&cursor=opaque', undefined)

    await ontologyProjectionsApi.audit('tenant 1', 'audit/1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/projections/audits/audit%2F1?tenant_id=tenant+1', undefined)

    await ontologyProjectionsApi.replayHistory('tenant 1', 'audit/1', { limit: 10 })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/projections/audits/audit%2F1/replay-history?tenant_id=tenant+1&limit=10', undefined)

    await ontologyProjectionsApi.replay('tenant 1', 'audit/1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/projections/audits/audit%2F1/replays?tenant_id=tenant+1', expect.objectContaining({ method: 'POST', body: '{}', idempotencyKey: '00000000-0000-4000-8000-000000000174' }))

    await ontologyProjectionsApi.acknowledge('tenant 1', 'audit/1', 'operator confirmed')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/projections/audits/audit%2F1/acknowledgements?tenant_id=tenant+1', expect.objectContaining({ method: 'POST', body: JSON.stringify({ reason: 'operator confirmed' }), idempotencyKey: '00000000-0000-4000-8000-000000000174' }))
  })

  it('omits the tenant query for all-visible projection reads', async () => {
    await ontologyProjectionsApi.summary('')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/projections/summary', undefined)
    await ontologyProjectionsApi.audits({ tenantId: '' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/projections/audits', undefined)
  })
})
