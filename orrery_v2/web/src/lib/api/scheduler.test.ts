import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiFetchMock } = vi.hoisted(() => ({ apiFetchMock: vi.fn() }))

vi.mock('./client', () => ({ apiFetch: apiFetchMock }))

import { schedulerApi } from './scheduler'

describe('scheduler API client', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('reads task statistics without downloading a task page and forwards cancellation', async () => {
    const signal = new AbortController().signal
    await schedulerApi.getTaskStats({ signal })
    expect(apiFetchMock).toHaveBeenCalledWith('/api/scheduled-tasks/summary', { signal })
  })

  it('reads scheduled run artifacts as authenticated blobs', async () => {
    await schedulerApi.readRunArtifact('task/1', 'run:1', 'artifact 1', 'inline')

    expect(apiFetchMock).toHaveBeenCalledWith(
      '/api/scheduled-tasks/task%2F1/runs/run%3A1/artifacts/content?artifact_id=artifact+1&disposition=inline',
      { responseType: 'blob' }
    )
  })

  it('keeps slash-containing artifact IDs in the query string', () => {
    expect(schedulerApi.runArtifactURL('task-1', 'run-1', 'report/weekly', 'attachment')).toBe(
      '/api/scheduled-tasks/task-1/runs/run-1/artifacts/content?artifact_id=report%2Fweekly&disposition=attachment'
    )
  })

  it('lists paginated responsibility evaluation items with encoded run identities', async () => {
    await schedulerApi.listResponsibilityEvaluations(
      'task/1',
      'run:1',
      '?status=failed&cursor=item%2F20&limit=20'
    )

    expect(apiFetchMock).toHaveBeenCalledWith(
      '/api/scheduled-tasks/task%2F1/runs/run%3A1/responsibility-evaluations?status=failed&cursor=item%2F20&limit=20',
      undefined
    )
  })

  it('lists the result inbox and persists personal read state', async () => {
    await schedulerApi.listRunInbox('?read_state=unread&limit=30')
    await schedulerApi.setRunReadState('run/report:1', true)

    expect(apiFetchMock).toHaveBeenNthCalledWith(1, '/api/scheduled-runs?read_state=unread&limit=30', undefined)
    expect(apiFetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/scheduled-runs/run%2Freport%3A1/read',
      { method: 'PATCH', body: JSON.stringify({ read: true }) }
    )
  })
})
