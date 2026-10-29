import { describe, expect, it, vi } from 'vitest'
import { createGovernanceIdempotencyKey, governanceWorkflowKey } from './governance-workflow'

describe('governance workflow identity', () => {
  it('binds local command state to tenant, proposal and revision', () => {
    expect(governanceWorkflowKey({ tenantId: ' tenant-1 ', proposalId: ' proposal-1 ', revision: 2 })).toBe('tenant-1\u0000proposal-1\u00002')
    expect(governanceWorkflowKey({ tenantId: 'tenant-2', proposalId: 'proposal-1', revision: 2 })).not.toBe(governanceWorkflowKey({ tenantId: 'tenant-1', proposalId: 'proposal-1', revision: 2 }))
    expect(governanceWorkflowKey({ tenantId: 'tenant-1', proposalId: 'proposal-1', revision: 3 })).not.toBe(governanceWorkflowKey({ tenantId: 'tenant-1', proposalId: 'proposal-1', revision: 2 }))
  })

  it('creates a random retry key for plan apply', () => {
    const randomUUID = vi.spyOn(crypto, 'randomUUID').mockReturnValue('550e8400-e29b-41d4-a716-446655440000')
    expect(createGovernanceIdempotencyKey()).toBe('550e8400-e29b-41d4-a716-446655440000')
    randomUUID.mockRestore()
  })
})
