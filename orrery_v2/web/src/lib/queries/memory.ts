import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { memoryApi } from '#lib/api/memory'

export interface MemoryRecordFilters {
  profileId?: string
  scopeKind?: string
  scopeId?: string
  layer?: string
  topic?: string
  lifecycleStatus?: string
  includeArchived?: boolean
  limit?: number
}

export interface MemoryCandidateFilters {
  missionId?: string
  scopeKind?: string
  scopeId?: string
  layer?: string
  topic?: string
  status?: string
  reviewRequired?: boolean | string
  limit?: number
}

export const memoryQueryKeys = {
  all: ['memory'] as const,
  records: (filters: MemoryRecordFilters = {}) => ['memory', 'records', buildMemoryRecordQuery(filters)] as const,
  record: (memoryId: string) => ['memory', 'record', memoryId] as const,
  candidates: (filters: MemoryCandidateFilters = {}) => ['memory', 'candidates', buildMemoryCandidateQuery(filters)] as const
}

export function useMemoryRecordsQuery(filters: () => MemoryRecordFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = filters()
    const query = buildMemoryRecordQuery(current)
    return {
      queryKey: memoryQueryKeys.records(current),
      queryFn: ({ signal }) => memoryApi.listRecords(query, { signal }),
      enabled: enabled()
    }
  })
}

export function useMemoryRecordsByProfilesQuery(
  profileIds: () => string[],
  filters: () => Omit<MemoryRecordFilters, 'profileId'> = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const ids = [...new Set(profileIds().map((id) => id.trim()).filter(Boolean))].sort()
    const current = filters()
    return {
      queryKey: ['memory', 'records', 'profiles', ids, buildMemoryRecordQuery(current)] as const,
      queryFn: async ({ signal }) => {
        const pages: Awaited<ReturnType<typeof memoryApi.listRecords>>[] = []
        for (let offset = 0; offset < ids.length; offset += 4) {
          signal.throwIfAborted()
          pages.push(...await Promise.all(
            ids.slice(offset, offset + 4).map((profileId) => memoryApi.listRecords(buildMemoryRecordQuery({ ...current, profileId }), { signal }))
          ))
        }
        const records = [...new Map(
          pages.flatMap((page) => page.records ?? []).map((record) => [record.memory_id, record])
        ).values()]
        return {
          records,
          total: records.length,
          limit: current.limit ?? 50
        }
      },
      enabled: enabled() && ids.length > 0
    }
  })
}

export function useMemoryRecordQuery(memoryId: () => string) {
  return createQuery(() => {
    const id = memoryId().trim()
    return {
      queryKey: memoryQueryKeys.record(id),
      queryFn: ({ signal }) => memoryApi.getRecord(id, { signal }),
      enabled: id.length > 0
    }
  })
}

export function useMemoryCandidatesQuery(filters: () => MemoryCandidateFilters = () => ({}), enabled: () => boolean = () => true) {
  return createQuery(() => {
    const current = filters()
    const query = buildMemoryCandidateQuery(current)
    return {
      queryKey: memoryQueryKeys.candidates(current),
      queryFn: ({ signal }) => memoryApi.listCandidates(query, { signal }),
      enabled: enabled()
    }
  })
}

export function useAcceptMemoryCandidateMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ candidateId, profileId, reason = '' }: { candidateId: string; profileId?: string; reason?: string }) =>
      memoryApi.acceptCandidate(candidateId, { profile_id: profileId, reason }),
    onSuccess: () => invalidateMemoryQueries(client)
  }))
}

export function useRejectMemoryCandidateMutation() {
  const client = useQueryClient()
  return createMutation(() => ({
    mutationFn: ({ candidateId, reason }: { candidateId: string; reason: string }) =>
      memoryApi.rejectCandidate(candidateId, { reason }),
    onSuccess: () => invalidateMemoryQueries(client)
  }))
}

function invalidateMemoryQueries(client: ReturnType<typeof useQueryClient>) {
  client.invalidateQueries({ queryKey: memoryQueryKeys.all })
  client.invalidateQueries({ queryKey: ['resources'] })
}

export function buildMemoryRecordQuery(filters: MemoryRecordFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'profile_id', filters.profileId)
  appendParam(params, 'scope_kind', filters.scopeKind)
  appendParam(params, 'scope_id', filters.scopeId)
  appendParam(params, 'layer', filters.layer)
  appendParam(params, 'topic', filters.topic)
  appendParam(params, 'lifecycle_status', filters.lifecycleStatus)
  appendParam(params, 'include_archived', filters.includeArchived)
  appendParam(params, 'limit', filters.limit ?? 50)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function buildMemoryCandidateQuery(filters: MemoryCandidateFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'mission_id', filters.missionId)
  appendParam(params, 'scope_kind', filters.scopeKind)
  appendParam(params, 'scope_id', filters.scopeId)
  appendParam(params, 'layer', filters.layer)
  appendParam(params, 'topic', filters.topic)
  appendParam(params, 'status', filters.status)
  appendParam(params, 'review_required', filters.reviewRequired)
  appendParam(params, 'limit', filters.limit ?? 50)
  const query = params.toString()
  return query ? `?${query}` : ''
}

function appendParam(params: URLSearchParams, key: string, value: string | boolean | number | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}
