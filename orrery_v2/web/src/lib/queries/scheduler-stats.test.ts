import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryObserver } from '@tanstack/query-core'
import { schedulerApi } from '#lib/api/scheduler'
import { createSchedulerMutationRegistry, useScheduledTaskStatsQuery } from './scheduler'
import type { ScheduledTaskStatsDTO } from '#lib/types/scheduler'

vi.mock('@tanstack/svelte-query', async (original) => ({
  ...await original<typeof import('@tanstack/svelte-query')>(),
  createQuery: (options: () => unknown) => options()
}))

function statsQuery(enabled = true) {
  return useScheduledTaskStatsQuery(() => enabled) as unknown as {
    queryKey: readonly unknown[]
    queryFn: (context: { signal: AbortSignal }) => Promise<ScheduledTaskStatsDTO>
    enabled: boolean
  }
}
afterEach(() => vi.restoreAllMocks())

describe('scheduler statistics query', () => {
  it('stays disabled outside task view and uses its own scheduler cache key', () => {
    expect(statsQuery(false).enabled).toBe(false)
    expect(statsQuery().queryKey).toEqual(['scheduler', 'task-stats'])
  })

  it('forwards cancellation when the last observer leaves', () => {
    let signal: AbortSignal | undefined
    vi.spyOn(schedulerApi, 'getTaskStats').mockImplementation((options) => {
      signal = options?.signal ?? undefined
      return new Promise((_resolve, reject) => signal?.addEventListener('abort', () => reject(signal?.reason)))
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    const observer = new QueryObserver(client, statsQuery())
    const unsubscribe = observer.subscribe(() => {})
    expect(signal?.aborted).toBe(false)
    unsubscribe()
    expect(signal?.aborted).toBe(true)
    client.clear()
  })

  it('marks cached statistics stale after a scheduler mutation', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    const query = statsQuery()
    client.setQueryData(query.queryKey, { total: 252, enabled: 251, executors: 2, system: 1, failed: 2 })
    vi.spyOn(schedulerApi, 'setRunReadState').mockResolvedValue({ scheduled_run_id: 'run-1', is_read: true })
    await createSchedulerMutationRegistry(client).setRunReadState('run-1', true)
    expect(client.getQueryState(query.queryKey)?.isInvalidated).toBe(true)
    client.clear()
  })
})
