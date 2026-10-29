import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))
vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { ontologyGovernanceApi } from './ontology-governance'

describe('ontology governance API', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('keeps reads and commands tenant scoped and sends only secure command inputs', async () => {
    await ontologyGovernanceApi.proposals({ tenantId: 'tenant 1', queue: 'actionable', search: 'owner', limit: 20, cursor: 'opaque' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/proposals?tenant_id=tenant+1&queue=actionable&search=owner&limit=20&cursor=opaque', undefined)

    await ontologyGovernanceApi.receipts('tenant 1', 'proposal/1', { limit: 10 })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/proposals/proposal%2F1/receipts?tenant_id=tenant+1&limit=10', undefined)

    await ontologyGovernanceApi.auditEvents('tenant 1', 'proposal/1', { limit: 10 })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/proposals/proposal%2F1/audit-events?tenant_id=tenant+1&limit=10', undefined)

    await ontologyGovernanceApi.decide('tenant 1', 'proposal/1', { decision: 'accepted', reason: 'verified', expected_revision: 3 })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/proposals/proposal%2F1/decisions?tenant_id=tenant%201', expect.objectContaining({ method: 'POST', body: JSON.stringify({ decision: 'accepted', reason: 'verified', expected_revision: 3 }) }))

    await ontologyGovernanceApi.createPlan('tenant 1', 'proposal/1', 4)
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/proposals/proposal%2F1/plans?tenant_id=tenant%201', expect.objectContaining({ method: 'POST', body: JSON.stringify({ expected_revision: 4 }) }))

    await ontologyGovernanceApi.applyPlan('tenant 1', 'plan/1', 'retry-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/plans/plan%2F1/apply?tenant_id=tenant%201', expect.objectContaining({ method: 'POST', idempotencyKey: 'retry-key', body: JSON.stringify({ idempotency_key: 'retry-key', confirmed: true }) }))

    await ontologyGovernanceApi.retryProjectionRefresh('tenant 1', 'receipt/1', 'refresh-key')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/receipts/receipt%2F1/projection-refresh-retries?tenant_id=tenant%201', expect.objectContaining({ method: 'POST', idempotencyKey: 'refresh-key' }))
  })

  it('omits the tenant query for all-visible governance reads', async () => {
    await ontologyGovernanceApi.summary('')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/summary', undefined)
    await ontologyGovernanceApi.proposals({ tenantId: '' })
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/ontology/governance/proposals', undefined)
  })
})
