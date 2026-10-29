import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn()
}))

vi.mock('./client', () => ({
  apiFetch: apiFetchMock
}))

import { missionApi } from './missions'

describe('mission API client', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('calls mission ops projection and agent run control endpoints', async () => {
    await missionApi.getMissionOps('mission-1')
    expect(apiFetchMock).toHaveBeenLastCalledWith('/api/missions/mission-1/ops', undefined)

    await missionApi.startAgentRun(
      'mission-1',
      'task-1',
      { agent_id: 'profile-builder', runtime_kind: 'codex' },
      { idempotencyKey: 'start-1' }
    )
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/missions/mission-1/tasks/task-1/agent-runs',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'start-1',
        body: JSON.stringify({ agent_id: 'profile-builder', runtime_kind: 'codex' })
      })
    )

    await missionApi.cancelAgentRun('mission-1', 'run-1', { reason: 'operator stopped it' }, { idempotencyKey: 'cancel-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/missions/mission-1/agent-runs/run-1/cancel',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'cancel-1',
        body: JSON.stringify({ reason: 'operator stopped it' })
      })
    )

    await missionApi.retryAgentRun('mission-1', 'run-1', { idempotencyKey: 'retry-1' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/missions/mission-1/agent-runs/run-1/retry',
      expect.objectContaining({
        method: 'POST',
        idempotencyKey: 'retry-1'
      })
    )
  })
})
