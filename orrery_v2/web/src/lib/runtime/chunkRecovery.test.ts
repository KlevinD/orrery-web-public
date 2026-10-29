import { describe, expect, it, vi } from 'vitest'
import { chunkRecoveryCooldownMs, isChunkLoadError, recoverFromChunkLoadError } from './chunkRecovery'

function memoryStorage() {
  const values = new Map<string, string>()
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value)
  }
}

describe('chunk recovery', () => {
  it('recognizes browser dynamic import failures and nested causes', () => {
    expect(isChunkLoadError(new TypeError('Failed to fetch dynamically imported module: /_app/immutable/nodes/44.js'))).toBe(true)
    expect(isChunkLoadError(new Error('navigation failed', { cause: new Error('ChunkLoadError: loading chunk 44 failed') }))).toBe(true)
    expect(isChunkLoadError(new Error('API request failed'))).toBe(false)
  })

  it('reloads once during the cooldown window', () => {
    const storage = memoryStorage()
    const reload = vi.fn()
    const error = new TypeError('Failed to fetch dynamically imported module')

    expect(recoverFromChunkLoadError(error, { now: () => 10_000, storage, reload })).toBe(true)
    expect(recoverFromChunkLoadError(error, { now: () => 10_000 + chunkRecoveryCooldownMs - 1, storage, reload })).toBe(false)
    expect(reload).toHaveBeenCalledTimes(1)

    expect(recoverFromChunkLoadError(error, { now: () => 10_000 + chunkRecoveryCooldownMs, storage, reload })).toBe(true)
    expect(reload).toHaveBeenCalledTimes(2)
  })

  it('does not risk a reload loop when session storage is unavailable', () => {
    const reload = vi.fn()
    const storage = {
      getItem: () => {
        throw new Error('storage denied')
      },
      setItem: vi.fn()
    }

    expect(recoverFromChunkLoadError(new Error('ChunkLoadError'), { storage, reload })).toBe(false)
    expect(reload).not.toHaveBeenCalled()
  })
})
