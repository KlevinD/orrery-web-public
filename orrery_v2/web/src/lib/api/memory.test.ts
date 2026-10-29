import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({
  apiFetchMock: vi.fn()
}))

vi.mock('./client', () => ({
  apiFetch: apiFetchMock
}))

import { memoryApi } from './memory'

describe('memory API client', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('accepts and rejects scoped memory candidates', async () => {
    await memoryApi.acceptCandidate('candidate/1', { profile_id: 'memory-mission-1', reason: 'reviewed' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/memory/candidates/candidate%2F1/accept',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ profile_id: 'memory-mission-1', reason: 'reviewed' })
      })
    )

    await memoryApi.rejectCandidate('candidate/1', { reason: 'insufficient evidence' })
    expect(apiFetchMock).toHaveBeenLastCalledWith(
      '/api/memory/candidates/candidate%2F1/reject',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ reason: 'insufficient evidence' })
      })
    )
  })
})
