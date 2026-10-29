import { describe, expect, it, vi } from 'vitest'
import { OntologySummaryScheduler, ontologySummaryConcurrency } from './summary-scheduler'

describe('OntologySummaryScheduler', () => {
  it('serializes page summaries and removes aborted queued work', async () => {
    expect(ontologySummaryConcurrency).toBe(1)
    const scheduler = new OntologySummaryScheduler(ontologySummaryConcurrency)
    const releases: Array<() => void> = []
    let active = 0
    let maximum = 0
    const task = vi.fn(() => new Promise<number>((resolve) => {
      active++
      maximum = Math.max(maximum, active)
      releases.push(() => {
        active--
        resolve(active)
      })
    }))

    const first = scheduler.run(new AbortController().signal, task)
    const second = scheduler.run(new AbortController().signal, task)
    const queuedController = new AbortController()
    const aborted = scheduler.run(queuedController.signal, task)
    const fourth = scheduler.run(new AbortController().signal, task)

    await vi.waitFor(() => expect(task).toHaveBeenCalledTimes(1))
    queuedController.abort()
    await expect(aborted).rejects.toMatchObject({ name: 'AbortError' })
    releases.shift()?.()
    await first
    await vi.waitFor(() => expect(task).toHaveBeenCalledTimes(2))
    expect(maximum).toBe(1)
    releases.shift()?.()
    await second
    await vi.waitFor(() => expect(task).toHaveBeenCalledTimes(3))
    releases.shift()?.()
    await fourth
  })
})
