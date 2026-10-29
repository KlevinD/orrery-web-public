import { operationsApi } from '#lib/api/operations'

interface WorkforceAvatarCacheEntry {
  revision: number
  promise: Promise<string | undefined>
  source?: string
}

const avatarCache = new Map<string, WorkforceAvatarCacheEntry>()

export const workforceAvatarRevisions = $state<Record<string, number>>({})

export function loadWorkforceAvatar(agentProfileId: string, revision = 0): Promise<string | undefined> {
  const profileId = agentProfileId.trim()
  if (!profileId || typeof window === 'undefined') return Promise.resolve(undefined)
  const cached = avatarCache.get(profileId)
  if (cached?.revision === revision) return cached.promise

  const entry: WorkforceAvatarCacheEntry = {
    revision,
    promise: Promise.resolve(undefined)
  }
  entry.promise = operationsApi.getWorkforceAvatar(profileId)
    .then((blob) => {
      if (!(blob instanceof Blob) || blob.size === 0 || typeof URL.createObjectURL !== 'function') return undefined
      const source = URL.createObjectURL(blob)
      if (avatarCache.get(profileId) !== entry) {
        URL.revokeObjectURL(source)
        return undefined
      }
      entry.source = source
      return source
    })
    .catch(() => undefined)
  avatarCache.set(profileId, entry)
  return entry.promise
}

export function refreshWorkforceAvatar(agentProfileId: string) {
  const profileId = agentProfileId.trim()
  if (!profileId) return
  const cached = avatarCache.get(profileId)
  if (cached?.source && typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(cached.source)
  avatarCache.delete(profileId)
  workforceAvatarRevisions[profileId] = (workforceAvatarRevisions[profileId] ?? 0) + 1
}
