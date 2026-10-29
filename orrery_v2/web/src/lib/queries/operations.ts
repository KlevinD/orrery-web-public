import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { operationsApi } from '#lib/api/operations'
import { connectEvaluationStream } from '#lib/api/websocket'
import {
  mapAuditLogView,
  mapAuthIPLockView,
  mapEvaluationCenterView,
  mapPermissionCenterView,
  mapPermissionPolicyCenterView
} from '#lib/mappers/operations'
import type {
  AuditEventPageDTO,
  AuthIPLockPageDTO,
  EvaluationPageDTO,
  PermissionEffectiveGrantPageDTO,
  PermissionMatrixDTO,
  PermissionPackageCapabilityPageDTO,
  PermissionPackagePageDTO,
  PermissionPolicyBatchDTO,
  PermissionPolicyDTO,
  PermissionPolicyViewDTO,
  PermissionRolePageDTO
} from '#lib/types/api'
import type { EvaluationCenterViewModel } from '#lib/types/view-models'

export interface AuditFilters {
  missionId?: string
  actorId?: string
  action?: string
  entityType?: string
  entityId?: string
  result?: string
  severity?: string
  limit?: number
  offset?: number
}

export interface AuthIPLockFilters {
  status?: string
  limit?: number
  offset?: number
}

export interface AuthIPUnlockInput {
  ipAddress: string
  reason?: string
}

export interface EvaluationFilters {
  missionId?: string
  entityType?: string
  entityId?: string
}

export interface PermissionFilters {
  agentId?: string
  toolId?: string
}

export interface PermissionPageFilters {
  roleId?: string
  cursor?: string
  limit?: number
  search?: string
  accessMode?: string
}

export const operationsQueryKeys = {
  all: ['operations'] as const,
  auditRoot: ['operations', 'audit'] as const,
  authIPLocksRoot: ['operations', 'audit', 'ip-locks'] as const,
  permissionRoot: ['operations', 'permissions'] as const,
  evaluationRoot: ['operations', 'evaluations'] as const,
  audit: (filters: AuditFilters = {}) => ['operations', 'audit', buildAuditQuery(filters)] as const,
  authIPLocks: (filters: AuthIPLockFilters = {}) => ['operations', 'audit', 'ip-locks', buildAuthIPLockQuery(filters)] as const,
  evaluations: (filters: EvaluationFilters = {}) => ['operations', 'evaluations', buildEvaluationQuery(filters)] as const,
  permissions: (filters: PermissionFilters = {}) => ['operations', 'permissions', buildPermissionQuery(filters)] as const,
  permissionPolicy: () => ['operations', 'permissions', 'policy'] as const,
  permissionRoles: (filters: PermissionPageFilters = {}) => ['operations', 'permissions', 'roles', buildPermissionPageQuery(filters)] as const,
  permissionPackages: (filters: PermissionPageFilters = {}) => ['operations', 'permissions', 'packages', buildPermissionPageQuery(filters)] as const,
  permissionPackageCapabilities: (packageId: string, filters: PermissionPageFilters = {}) =>
    ['operations', 'permissions', 'packages', packageId, 'capabilities', buildPermissionPageQuery(filters)] as const,
  permissionEffectiveGrants: (filters: PermissionPageFilters = {}) =>
    ['operations', 'permissions', 'effective-grants', buildPermissionPageQuery(filters)] as const
}

export function useAuditLogQuery(filters: () => AuditFilters = () => ({})) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: operationsQueryKeys.audit(current),
      queryFn: async () => mapAuditLogView((await operationsApi.listAuditLog(buildAuditQuery(current))) as AuditEventPageDTO)
    }
  })
}

export function useAuthIPLocksQuery(filters: () => AuthIPLockFilters = () => ({})) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: operationsQueryKeys.authIPLocks(current),
      queryFn: async () => mapAuthIPLockView((await operationsApi.listAuthIPLocks(buildAuthIPLockQuery(current))) as AuthIPLockPageDTO)
    }
  })
}

export function useEvaluationListQuery(filters: () => EvaluationFilters = () => ({})) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: operationsQueryKeys.evaluations(current),
      queryFn: async () =>
        mapEvaluationCenterView((await operationsApi.listEvaluations(buildEvaluationQuery(current))) as EvaluationPageDTO)
    }
  })
}

export function subscribeEvaluationStream(
  filters: EvaluationFilters,
  onView: (view: EvaluationCenterViewModel) => void,
  onError?: (error: string) => void
) {
  const source = connectEvaluationStream(buildEvaluationQuery(filters))
  const handleData = (message: Event) => {
    const dto = parseEvaluationEventSourceMessage(message)
    if (dto) onView(mapEvaluationCenterView(dto))
  }
  source.addEventListener('snapshot', handleData)
  source.addEventListener('update', handleData)
  source.addEventListener('error', (message) => {
    onError?.(parseEvaluationErrorMessage(message) ?? 'evaluation stream error')
  })
  return () => source.close()
}

function parseEvaluationEventSourceMessage(message: Event): EvaluationPageDTO | undefined {
  const data = (message as MessageEvent).data
  if (typeof data !== 'string' || data.trim() === '') return undefined
  try {
    return JSON.parse(data) as EvaluationPageDTO
  } catch {
    return undefined
  }
}

function parseEvaluationErrorMessage(message: Event) {
  const data = (message as MessageEvent).data
  if (typeof data !== 'string' || data.trim() === '') return undefined
  try {
    const parsed = JSON.parse(data) as { message?: string }
    return parsed.message
  } catch {
    return data
  }
}

export function usePermissionMatrixQuery(filters: () => PermissionFilters = () => ({})) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: operationsQueryKeys.permissions(current),
      queryFn: async () =>
        mapPermissionCenterView((await operationsApi.getPermissionMatrix(buildPermissionQuery(current))) as PermissionMatrixDTO)
    }
  })
}

export function usePermissionPolicyQuery() {
  return createQuery(() => ({
    queryKey: operationsQueryKeys.permissionPolicy(),
    queryFn: async () => mapPermissionPolicyCenterView((await operationsApi.getPermissionPolicy()) as PermissionPolicyViewDTO)
  }))
}

export function createPermissionPolicyMutationRegistry(client: { invalidateQueries: (options: { queryKey: readonly string[] }) => unknown }) {
  return {
    save: async (policy: PermissionPolicyDTO) => {
      const saved = (await operationsApi.updatePermissionPolicy(policy)) as PermissionPolicyViewDTO
      client.invalidateQueries({ queryKey: operationsQueryKeys.permissionRoot })
      return mapPermissionPolicyCenterView(saved)
    }
  }
}

export function usePermissionPolicySaveMutation() {
  const client = useQueryClient()
  const registry = createPermissionPolicyMutationRegistry(client)
  return createMutation(() => ({ mutationFn: registry.save }))
}

export function usePermissionRolesQuery(filters: () => PermissionPageFilters) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: operationsQueryKeys.permissionRoles(current),
      queryFn: () => operationsApi.listPermissionRoles(buildPermissionPageQuery(current)) as Promise<PermissionRolePageDTO>
    }
  })
}

export function usePermissionPackagesQuery(filters: () => PermissionPageFilters, enabled: () => boolean) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: operationsQueryKeys.permissionPackages(current),
      queryFn: () => operationsApi.listPermissionPackages(buildPermissionPageQuery(current)) as Promise<PermissionPackagePageDTO>,
      enabled: enabled()
    }
  })
}

export function usePermissionPackageCapabilitiesQuery(
  packageId: () => string,
  filters: () => PermissionPageFilters,
  enabled: () => boolean
) {
  return createQuery(() => {
    const currentPackageId = packageId()
    const current = filters()
    return {
      queryKey: operationsQueryKeys.permissionPackageCapabilities(currentPackageId, current),
      queryFn: () => operationsApi.listPermissionPackageCapabilities(currentPackageId, buildPermissionPageQuery(current)) as Promise<PermissionPackageCapabilityPageDTO>,
      enabled: enabled() && Boolean(currentPackageId)
    }
  })
}

export function usePermissionEffectiveGrantsQuery(filters: () => PermissionPageFilters, enabled: () => boolean) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: operationsQueryKeys.permissionEffectiveGrants(current),
      queryFn: () => operationsApi.listEffectivePermissionGrants(buildPermissionPageQuery(current)) as Promise<PermissionEffectiveGrantPageDTO>,
      enabled: enabled()
    }
  })
}

export function createPermissionBatchMutationRegistry(client: { invalidateQueries: (options: { queryKey: readonly string[] }) => unknown }) {
  return {
    save: async (batch: PermissionPolicyBatchDTO) => {
      const saved = await operationsApi.applyPermissionPolicyBatch(batch)
      client.invalidateQueries({ queryKey: operationsQueryKeys.permissionRoot })
      return saved
    }
  }
}

export function usePermissionPolicyBatchMutation() {
  const client = useQueryClient()
  const registry = createPermissionBatchMutationRegistry(client)
  return createMutation(() => ({ mutationFn: registry.save }))
}

export function useAuthIPUnlockMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (input: AuthIPUnlockInput) =>
      operationsApi.unlockAuthIPLock({
        ip_address: input.ipAddress,
        reason: input.reason ?? ''
      }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: operationsQueryKeys.authIPLocksRoot })
      client.invalidateQueries({ queryKey: operationsQueryKeys.auditRoot })
    }
  }))
}

export function buildAuditQuery(filters: AuditFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'mission_id', filters.missionId)
  appendParam(params, 'actor_id', filters.actorId)
  appendParam(params, 'action', filters.action)
  appendParam(params, 'entity_type', filters.entityType)
  appendParam(params, 'entity_id', filters.entityId)
  appendParam(params, 'result', filters.result)
  appendParam(params, 'severity', filters.severity)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  return queryString(params)
}

export function buildAuthIPLockQuery(filters: AuthIPLockFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'status', filters.status)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  return queryString(params)
}

export function buildEvaluationQuery(filters: EvaluationFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'mission_id', filters.missionId)
  appendParam(params, 'entity_type', filters.entityType)
  appendParam(params, 'entity_id', filters.entityId)
  return queryString(params)
}

export function buildPermissionQuery(filters: PermissionFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'agent_id', filters.agentId)
  appendParam(params, 'tool_id', filters.toolId)
  return queryString(params)
}

export function buildPermissionPageQuery(filters: PermissionPageFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'role_id', filters.roleId)
  appendParam(params, 'cursor', filters.cursor)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'q', filters.search)
  appendParam(params, 'access_mode', filters.accessMode)
  return queryString(params)
}

function appendParam(params: URLSearchParams, key: string, value: string | number | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}

function queryString(params: URLSearchParams) {
  const query = params.toString()
  return query ? `?${query}` : ''
}
