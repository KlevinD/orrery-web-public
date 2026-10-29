import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { buildMissionListQuery, missionQueryKeys, subscribeMissionStream } from './missions'

const eventListeners = new Map<string, EventListenerOrEventListenerObject>()
const close = vi.fn()

vi.mock('#lib/api/websocket', () => ({
  connectMissionStream: vi.fn(() => ({
    addEventListener: (event: string, listener: EventListenerOrEventListenerObject) => eventListeners.set(event, listener),
    close
  }))
}))

describe('mission queries', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    eventListeners.clear()
    close.mockClear()
  })
  afterEach(() => vi.useRealTimers())

  it('serializes mission list filters', () => {
    expect(buildMissionListQuery({ search: 'launch', status: 'active', limit: 10, offset: 20 })).toBe('?search=launch&status=active&limit=10&offset=20')
  })

  it('scopes mission query keys', () => {
    expect(missionQueryKeys.list({ status: 'active' })).toEqual(['missions', '?status=active'])
    expect(missionQueryKeys.control('m1')).toEqual(['missions', 'm1', 'control'])
    expect(missionQueryKeys.ops('m1')).toEqual(['missions', 'm1', 'ops'])
    expect(missionQueryKeys.team('m1')).toEqual(['missions', 'm1', 'team'])
    expect(missionQueryKeys.tasks('m1')).toEqual(['missions', 'm1', 'tasks'])
    expect(missionQueryKeys.progress('m1')).toEqual(['missions', 'm1', 'progress'])
    expect(missionQueryKeys.evidence('m1')).toEqual(['missions', 'm1', 'evidence'])
    expect(missionQueryKeys.qa('m1')).toEqual(['missions', 'm1', 'qa'])
  })

  it('coalesces mission snapshot and patch bursts without postponing refresh indefinitely', async () => {
    const invalidate = vi.fn()
    const unsubscribe = subscribeMissionStream('m1', invalidate)

    expect(eventListeners.has('snapshot')).toBe(true)
    expect(eventListeners.has('patch')).toBe(true)
    dispatchStreamEvent('snapshot')
    dispatchStreamEvent('patch')

    expect(invalidate).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(250)
    expect(invalidate).toHaveBeenCalledTimes(1)
    dispatchStreamEvent('message')
    await vi.advanceTimersByTimeAsync(250)
    expect(invalidate).toHaveBeenCalledTimes(2)
    unsubscribe()
    expect(close).toHaveBeenCalledTimes(1)
  })

  it('waits for a slow refresh and follows it with one refresh for intervening events', async () => {
    let finish!: () => void
    const invalidate = vi.fn().mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve }))
    const unsubscribe = subscribeMissionStream('m1', invalidate)
    dispatchStreamEvent('patch')
    await vi.advanceTimersByTimeAsync(250)
    for (let i = 0; i < 10; i++) dispatchStreamEvent('patch')
    await vi.advanceTimersByTimeAsync(5_000)
    expect(invalidate).toHaveBeenCalledTimes(1)
    finish()
    await vi.advanceTimersByTimeAsync(250)
    expect(invalidate).toHaveBeenCalledTimes(2)
    unsubscribe()
  })

  it('drops queued events when switching away, including a trailing in-flight refresh', async () => {
    const invalidate = vi.fn()
    let unsubscribe = subscribeMissionStream('m1', invalidate)
    dispatchStreamEvent('patch')
    unsubscribe()
    await vi.advanceTimersByTimeAsync(250)
    expect(invalidate).not.toHaveBeenCalled()

    let finish!: () => void
    invalidate.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve }))
    unsubscribe = subscribeMissionStream('m2', invalidate)
    dispatchStreamEvent('patch')
    await vi.advanceTimersByTimeAsync(250)
    dispatchStreamEvent('patch')
    unsubscribe()
    finish()
    await vi.advanceTimersByTimeAsync(1_000)
    expect(invalidate).toHaveBeenCalledTimes(1)
  })

  it('allows later events to recover from refresh errors', async () => {
    const invalidate = vi.fn().mockRejectedValueOnce(new Error('unavailable')).mockResolvedValue(undefined)
    const unsubscribe = subscribeMissionStream('m1', invalidate)
    dispatchStreamEvent('patch')
    await vi.advanceTimersByTimeAsync(250)
    dispatchStreamEvent('patch')
    await vi.advanceTimersByTimeAsync(250)
    expect(invalidate).toHaveBeenCalledTimes(2)
    unsubscribe()
  })
})

function dispatchStreamEvent(event: string) {
  const listener = eventListeners.get(event)
  if (typeof listener === 'function') {
    listener(new Event(event))
  } else {
    listener?.handleEvent(new Event(event))
  }
}
