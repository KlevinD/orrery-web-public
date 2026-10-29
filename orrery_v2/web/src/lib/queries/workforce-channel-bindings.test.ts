import { describe, expect, it, vi } from 'vitest'
import { workforceChannelBindingsApi } from '#lib/api/workforce-channel-bindings'
import {
  invalidateWorkforceChannelBindingOwners,
  workforceChannelBindingsQueryKeys,
  workforceChannelBindingsQueryOptions
} from './workforce-channel-bindings'

describe('workforce channel binding queries', () => {
  it('keeps account ownership caches scoped by employee', async () => {
    vi.spyOn(workforceChannelBindingsApi, 'list').mockResolvedValue({ agent_profile_id: 'jordan-belfort', accounts: [] })
    const options = workforceChannelBindingsQueryOptions('jordan-belfort')
    expect(options.queryKey).toEqual(['workforce-channel-bindings', 'jordan-belfort'])
    expect(options.enabled).toBe(true)
    await expect(options.queryFn()).resolves.toEqual({ agentProfileId: 'jordan-belfort', accounts: [] })
  })

  it('invalidates both the new and previous owner after a move', async () => {
    const client = { invalidateQueries: vi.fn().mockResolvedValue(undefined) }
    await invalidateWorkforceChannelBindingOwners(client as never, 'jordan-belfort', 'gangbeng')
    expect(client.invalidateQueries).toHaveBeenCalledWith({ queryKey: workforceChannelBindingsQueryKeys.detail('jordan-belfort') })
    expect(client.invalidateQueries).toHaveBeenCalledWith({ queryKey: workforceChannelBindingsQueryKeys.detail('gangbeng') })
  })
})
