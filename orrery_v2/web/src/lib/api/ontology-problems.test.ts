import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyProblemsApi } from './ontology-problems'

describe('ontology problem hub API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('keeps reads tenant-scoped and encodes filters', async () => {
    await ontologyProblemsApi.list({ tenantId: 'tenant 1', status: 'open', sourceDomain: 'reliability', search: 'provider timeout', limit: 25 })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/problems?tenant_id=tenant+1&status=open&source_domain=reliability&search=provider+timeout&limit=25', undefined)

    await ontologyProblemsApi.problem('tenant 1', 'problem/1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/problems/problem%2F1?tenant_id=tenant+1', undefined)
  })

  it('routes only an owner and generates an idempotency key', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000207')
    await ontologyProblemsApi.route('tenant 1', 'problem/1', { kind: 'team', id: 'platform' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/problems/problem%2F1/route?tenant_id=tenant+1', expect.objectContaining({
      method: 'POST',
      idempotencyKey: '00000000-0000-4000-8000-000000000207',
      body: JSON.stringify({ owner: { kind: 'team', id: 'platform' } })
    }))
  })
})
