import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { responsibilityApi } from '#lib/api/responsibilities'
import { createIdempotencyKey } from '#lib/runtime/ids'
import type {
  ResolveResponsibilityBlockerRequest,
  ResponsibilityControlAction,
  ResponsibilityControlRequest,
  ResponsibilityFilters,
  ResponsibilityPolicyUpdateRequest,
  ResponsibilityWorkKind
} from '#lib/types/responsibility'

interface QueryClientLike {
  invalidateQueries: (options: { queryKey: readonly unknown[] }) => unknown
}

export const responsibilityQueryKeys = {
  root: ['responsibilities'] as const,
  inbox: (filters: ResponsibilityFilters = {}) => ['responsibilities', 'inbox', normalizeFilters(filters)] as const,
  detail: (responsibilityId: string) => ['responsibilities', 'detail', responsibilityId] as const,
  policy: (agentId: string, workKind: ResponsibilityWorkKind) => ['responsibilities', 'policy', agentId, workKind] as const
}

export function useResponsibilityInboxQuery(filters: () => ResponsibilityFilters = () => ({})) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: responsibilityQueryKeys.inbox(current),
      queryFn: () => responsibilityApi.list(current),
      refetchInterval: 30_000
    }
  })
}

export function useResponsibilityDetailQuery(responsibilityId: () => string) {
  return createQuery(() => {
    const id = responsibilityId().trim()
    return {
      queryKey: responsibilityQueryKeys.detail(id),
      queryFn: () => responsibilityApi.get(id),
      enabled: id.length > 0,
      refetchInterval: id.length > 0 ? 15_000 : false
    }
  })
}

export function useResponsibilityPolicyQuery(
  agentId: () => string,
  workKind: () => ResponsibilityWorkKind = () => 'mission_task'
) {
  return createQuery(() => {
    const agent = agentId().trim()
    const kind = workKind()
    return {
      queryKey: responsibilityQueryKeys.policy(agent, kind),
      queryFn: () => responsibilityApi.getPolicy(agent, kind),
      enabled: agent.length > 0
    }
  })
}

export function createResponsibilityMutationRegistry(client: QueryClientLike) {
  const invalidateResponsibility = (responsibilityId: string) => {
    client.invalidateQueries({ queryKey: responsibilityQueryKeys.root })
    client.invalidateQueries({ queryKey: responsibilityQueryKeys.detail(responsibilityId) })
  }
  return {
    control: async (input: {
      responsibilityId: string
      action: ResponsibilityControlAction
      version: number
      body?: ResponsibilityControlRequest
    }) => {
      const result = await responsibilityApi.control(
        input.responsibilityId,
        input.action,
        input.version,
        input.body ?? {},
        { idempotencyKey: createIdempotencyKey() }
      )
      invalidateResponsibility(input.responsibilityId)
      return result
    },
    resolveBlocker: async (input: {
      responsibilityId: string
      blockerId: string
      version: number
      body: ResolveResponsibilityBlockerRequest
    }) => {
      const result = await responsibilityApi.resolveBlocker(input.blockerId, input.version, input.body, {
        idempotencyKey: createIdempotencyKey()
      })
      invalidateResponsibility(input.responsibilityId)
      return result
    },
    updatePolicy: async (input: {
      agentId: string
      workKind: ResponsibilityWorkKind
      version: number
      body: ResponsibilityPolicyUpdateRequest
    }) => {
      const result = await responsibilityApi.updatePolicy(input.agentId, input.workKind, input.version, input.body, {
        idempotencyKey: createIdempotencyKey()
      })
      client.invalidateQueries({ queryKey: responsibilityQueryKeys.root })
      client.invalidateQueries({ queryKey: responsibilityQueryKeys.policy(input.agentId, input.workKind) })
      return result
    }
  }
}

export function useResponsibilityCommandMutations() {
  const client = useQueryClient()
  const registry = createResponsibilityMutationRegistry(client)
  return {
    control: createMutation(() => ({ mutationFn: registry.control })),
    resolveBlocker: createMutation(() => ({ mutationFn: registry.resolveBlocker })),
    updatePolicy: createMutation(() => ({ mutationFn: registry.updatePolicy }))
  }
}

function normalizeFilters(filters: ResponsibilityFilters) {
  return {
    agentId: filters.agentId?.trim() ?? '',
    states: [...(filters.states ?? [])].sort(),
    limit: filters.limit ?? 100
  }
}
