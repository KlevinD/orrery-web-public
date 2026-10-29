import { afterEach, describe, expect, it, vi } from 'vitest'
import { memoryApi } from '#lib/api/memory'
import { useMemoryRecordsByProfilesQuery } from './memory'

vi.mock('@tanstack/svelte-query', async (original) => ({
  ...await original<typeof import('@tanstack/svelte-query')>(),
  createQuery: (options: () => unknown) => options()
}))

function query(ids: string[]) {
  return useMemoryRecordsByProfilesQuery(() => ids) as unknown as {
    enabled: boolean
    queryFn: (context: { signal: AbortSignal }) => Promise<{ records: Array<{ memory_id: string }> }>
  }
}
afterEach(() => vi.restoreAllMocks())

describe('memory profile loading', () => {
  it('bounds concurrency and preserves profile and record deduplication', async () => {
    let release!: () => void
    const gate = new Promise<void>((resolve) => { release = resolve })
    const spy = vi.spyOn(memoryApi, 'listRecords').mockImplementation(async () => {
      await gate
      return { records: [{ memory_id: 'same-record' }], total: 1, limit: 50 } as never
    })
    const ids = [' a ', 'a', '', 'b', 'c', 'd', 'e', 'f']
    const signal = new AbortController().signal
    const result = query(ids).queryFn({ signal })
    expect(spy).toHaveBeenCalledTimes(4)
    release()
    expect((await result).records).toHaveLength(1)
    expect(spy).toHaveBeenCalledTimes(6)
    for (const call of spy.mock.calls) expect(call[1]?.signal).toBe(signal)
  })

  it('does not start another batch after cancellation', async () => {
    let release!: () => void
    const gate = new Promise<void>((resolve) => { release = resolve })
    const spy = vi.spyOn(memoryApi, 'listRecords').mockImplementation(async () => {
      await gate
      return { records: [], total: 0, limit: 50 }
    })
    const controller = new AbortController()
    const result = query(['a', 'b', 'c', 'd', 'e']).queryFn({ signal: controller.signal })
    controller.abort()
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' })
    release()
    await rejected
    expect(spy).toHaveBeenCalledTimes(4)
  })

  it('stops after a failed batch and disables empty selections', async () => {
    const spy = vi.spyOn(memoryApi, 'listRecords').mockRejectedValue(new Error('unavailable'))
    await expect(query(['a', 'b', 'c', 'd', 'e']).queryFn({ signal: new AbortController().signal })).rejects.toThrow('unavailable')
    expect(spy).toHaveBeenCalledTimes(4)
    expect(query([]).enabled).toBe(false)
  })
})
