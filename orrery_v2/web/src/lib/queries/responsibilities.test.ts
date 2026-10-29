import { describe, expect, it, vi } from 'vitest'
import { responsibilityApi } from '#lib/api/responsibilities'
import { createResponsibilityMutationRegistry, responsibilityQueryKeys } from './responsibilities'

describe('responsibility queries', () => {
  it('invalidates inbox and detail state after a responsibility control', async () => {
    const invalidateQueries = vi.fn()
    const registry = createResponsibilityMutationRegistry({ invalidateQueries })
    vi.spyOn(responsibilityApi, 'control').mockResolvedValue({ responsibility_id: 'responsibility-1', version: 4 } as never)

    await registry.control({ responsibilityId: 'responsibility-1', action: 'retry', version: 3 })

    expect(responsibilityApi.control).toHaveBeenCalledWith(
      'responsibility-1',
      'retry',
      3,
      {},
      expect.objectContaining({ idempotencyKey: expect.any(String) })
    )
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: responsibilityQueryKeys.root })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: responsibilityQueryKeys.detail('responsibility-1') })
  })

  it('invalidates effective policy and inbox state after policy CAS', async () => {
    const invalidateQueries = vi.fn()
    const registry = createResponsibilityMutationRegistry({ invalidateQueries })
    vi.spyOn(responsibilityApi, 'updatePolicy').mockResolvedValue({ policy_id: 'policy-1', version: 3 } as never)
    const body = {
      autonomy_level: 'execute_scoped' as const,
      max_parallel: 2,
      accept_timeout_seconds: 120,
      heartbeat_timeout_seconds: 90,
      max_attempts: 3,
      retry_backoff_seconds: [30],
      max_cost_per_task: 0,
      max_token_budget: 10000,
      approval_policy: 'only_high_risk' as const,
      allowed_risk_levels: [],
      tool_scopes: [],
      data_scopes: [],
      escalation_chain: [],
      recheck_interval_seconds: 900,
      deadline_warning_seconds: 1800,
      enabled: true
    }

    await registry.updatePolicy({ agentId: 'agent-1', workKind: 'mission_task', version: 2, body })

    expect(responsibilityApi.updatePolicy).toHaveBeenCalledWith(
      'agent-1', 'mission_task', 2, body, expect.objectContaining({ idempotencyKey: expect.any(String) })
    )
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: responsibilityQueryKeys.policy('agent-1', 'mission_task') })
  })
})
