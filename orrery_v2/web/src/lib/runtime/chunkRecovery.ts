const chunkLoadPatterns = [
  /failed to fetch dynamically imported module/i,
  /error loading dynamically imported module/i,
  /importing a module script failed/i,
  /loading (?:css )?chunk .+ failed/i,
  /chunkloaderror/i
]

const recoveryStorageKey = 'orrery:chunk-load-recovery-at'
export const chunkRecoveryCooldownMs = 60_000

interface ChunkRecoveryRuntime {
  now?: () => number
  storage?: Pick<Storage, 'getItem' | 'setItem'>
  reload?: () => void
}

export function isChunkLoadError(error: unknown) {
  const messages = errorMessages(error)
  return messages.some((message) => chunkLoadPatterns.some((pattern) => pattern.test(message)))
}

export function recoverFromChunkLoadError(error: unknown, runtime: ChunkRecoveryRuntime = {}) {
  if (!isChunkLoadError(error) || typeof window === 'undefined') return false

  const now = runtime.now?.() ?? Date.now()
  const storage = runtime.storage ?? window.sessionStorage
  const reload = runtime.reload ?? (() => window.location.reload())

  try {
    const previous = Number(storage.getItem(recoveryStorageKey))
    if (Number.isFinite(previous) && previous > 0 && now - previous < chunkRecoveryCooldownMs) return false
    storage.setItem(recoveryStorageKey, String(now))
  } catch {
    return false
  }

  reload()
  return true
}

function errorMessages(error: unknown) {
  const messages: string[] = []
  const seen = new Set<unknown>()
  let current = error

  while (current !== undefined && current !== null && !seen.has(current)) {
    seen.add(current)
    if (current instanceof Error) {
      messages.push(`${current.name}: ${current.message}`)
      current = current.cause
      continue
    }
    messages.push(String(current))
    break
  }

  return messages
}
