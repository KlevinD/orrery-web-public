import { apiFetch, type ApiFetchOptions } from './client'
import type {
  PermissionEffectiveGrantPageDTO,
  PermissionPackageCapabilityPageDTO,
  PermissionPackagePageDTO,
  PermissionPolicyBatchDTO,
  PermissionPolicyBatchResultDTO,
  PermissionRolePageDTO,
  PutRoleAssignmentsCommand,
  RoleDefinitionPageDTO,
  RoleAssignmentPageDTO,
  TalentMarketImportPreviewResponseDTO,
  WorkforceAvatarDefaultDTO,
  WorkforceMemberDTO,
  PeopleResponsibilityProfileDTO
} from '#lib/types/api'

export const operationsApi = {
  listWorkforceMembers: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/workforce/members${query}`, options),
  listTalentMarketMembers: (query = '', options?: ApiFetchOptions) =>
    apiFetch(`/api/talent-market/members${query}`, options),
  previewTalentMarketImport: async (file: File, options?: ApiFetchOptions) => {
    const form = new FormData()
    form.set('file', file)
    return normalizeTalentMarketImportPreview(await apiFetch('/api/talent-market/import/preview', { ...options, method: 'POST', body: form }))
  },
  commitTalentMarketImport: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/talent-market/import/commit', jsonMutation('POST', body, options)),
  exportTalentMarket: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch<Blob>('/api/talent-market/export', { ...jsonMutation('POST', body, options), responseType: 'blob' }),
  deleteTalentMarketMember: (agentProfileId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/talent-market/members/${agentProfileId}`, { ...options, method: 'DELETE' }),
  deleteTalentMarketMembers: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/talent-market/members/delete-batch', jsonMutation('POST', body, options)),
  listWorkforceAvatarDefaults: (options?: ApiFetchOptions) =>
    apiFetch<WorkforceAvatarDefaultDTO[]>('/api/workforce/avatar-defaults', options),
  getWorkforceAvatarDefault: (defaultId: string, options?: ApiFetchOptions) =>
    apiFetch<Blob>(`/api/workforce/avatar-defaults/${encodeURIComponent(defaultId)}`, { ...options, responseType: 'blob' }),
  createWorkforceMember: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch<WorkforceMemberDTO>('/api/workforce/members', jsonMutation('POST', body, options)),
  upsertWorkforceMember: (agentProfileId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch<WorkforceMemberDTO>(`/api/workforce/members/${agentProfileId}`, jsonMutation('PATCH', body, options)),
  getWorkforceAvatar: (agentProfileId: string, options?: ApiFetchOptions) =>
    apiFetch<Blob | undefined>(`/api/workforce/members/${encodeURIComponent(agentProfileId)}/avatar`, { ...options, cache: 'no-store', responseType: 'blob' }),
  uploadWorkforceAvatar: (agentProfileId: string, file: File, options?: ApiFetchOptions) => {
    const form = new FormData()
    form.set('file', file)
    return apiFetch(`/api/workforce/members/${encodeURIComponent(agentProfileId)}/avatar`, { ...options, method: 'PUT', body: form })
  },
  setWorkforceDefaultAvatar: (agentProfileId: string, defaultId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/workforce/members/${encodeURIComponent(agentProfileId)}/avatar/default`, jsonMutation('PUT', { default_id: defaultId }, options)),
  resetWorkforceAvatar: (agentProfileId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/workforce/members/${encodeURIComponent(agentProfileId)}/avatar`, { ...options, method: 'DELETE' }),
  releaseWorkforceMemberToMarket: (agentProfileId: string, body: unknown = {}, options?: ApiFetchOptions) =>
    apiFetch(`/api/workforce/members/${agentProfileId}/release-to-market`, jsonMutation('POST', body, options)),
  hireTalentFromMarket: (agentProfileId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/talent-market/members/${agentProfileId}/hire`, jsonMutation('POST', body, options)),
  enableWorkforceMember: (agentProfileId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/workforce/members/${agentProfileId}/enable`, { ...options, method: 'POST' }),
  disableWorkforceMember: (agentProfileId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/workforce/members/${agentProfileId}/disable`, { ...options, method: 'POST' }),
  getOrgGraph: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/org/graph${query}`, options),
  getAgentResponsibilityProfile: (agentProfileId: string, historyLimit = 20, options?: ApiFetchOptions) =>
    apiFetch<PeopleResponsibilityProfileDTO>(`/api/responsibility-profiles/agents/${encodeURIComponent(agentProfileId)}${responsibilityProfileQuery(historyLimit)}`, options),
  getOrganizationResponsibilityProfile: (organizationScopeId: string, historyLimit = 20, options?: ApiFetchOptions) =>
    apiFetch<PeopleResponsibilityProfileDTO>(`/api/responsibility-profiles/organizations/${encodeURIComponent(organizationScopeId)}${responsibilityProfileQuery(historyLimit)}`, options),
  listOrgRoles: (options?: ApiFetchOptions) => apiFetch<RoleDefinitionPageDTO>('/api/org/roles', options),
  listOrgRoleAssignments: (query = '', options?: ApiFetchOptions) =>
    apiFetch<RoleAssignmentPageDTO>(`/api/org/role-assignments${query}`, options),
  putOrgRoleAssignments: (body: PutRoleAssignmentsCommand, options?: ApiFetchOptions) =>
    apiFetch<RoleAssignmentPageDTO>('/api/org/role-assignments', jsonMutation('PUT', body, options)),
  deleteOrgRoleAssignment: (assignmentId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/org/role-assignments/${encodeURIComponent(assignmentId)}`, { ...options, method: 'DELETE' }),
  createOrganization: (body: unknown, options?: ApiFetchOptions) => apiFetch('/api/org', jsonMutation('POST', body, options)),
  createOrganizationUnit: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/org/units', jsonMutation('POST', body, options)),
  createOrganizationPosition: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/org/positions', jsonMutation('POST', body, options)),
  deleteOrganization: (organizationId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/org/${organizationId}`, { ...options, method: 'DELETE' }),
  deleteOrganizationUnit: (departmentId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/org/units/${departmentId}`, { ...options, method: 'DELETE' }),
  deleteOrganizationPosition: (positionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/org/positions/${positionId}`, { ...options, method: 'DELETE' }),
  updateOrganizationOwner: (organizationNodeId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/org/nodes/${encodeURIComponent(organizationNodeId)}/owner`, jsonMutation('PUT', body, options)),
  getPermissionMatrix: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/permissions/matrix${query}`, options),
  decidePermission: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/permissions/decisions', jsonMutation('POST', body, options)),
  getPermissionPolicy: (options?: ApiFetchOptions) => apiFetch('/api/permissions/policy', options),
  updatePermissionPolicy: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/permissions/policy', jsonMutation('PUT', body, options)),
  listPermissionRoles: (query = '', options?: ApiFetchOptions) =>
    apiFetch<PermissionRolePageDTO>(`/api/permissions/roles${query}`, options),
  listPermissionPackages: (query = '', options?: ApiFetchOptions) =>
    apiFetch<PermissionPackagePageDTO>(`/api/permissions/packages${query}`, options),
  listPermissionPackageCapabilities: (packageId: string, query = '', options?: ApiFetchOptions) =>
    apiFetch<PermissionPackageCapabilityPageDTO>(`/api/permissions/packages/${encodeURIComponent(packageId)}/capabilities${query}`, options),
  listEffectivePermissionGrants: (query = '', options?: ApiFetchOptions) =>
    apiFetch<PermissionEffectiveGrantPageDTO>(`/api/permissions/effective-grants${query}`, options),
  applyPermissionPolicyBatch: (body: PermissionPolicyBatchDTO, options?: ApiFetchOptions) =>
    apiFetch<PermissionPolicyBatchResultDTO>('/api/permissions/policy/batch', jsonMutation('PUT', body, options)),
  listAuditLog: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/audit-log${query}`, options),
  listAuthIPLocks: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/audit/ip-locks${query}`, options),
  unlockAuthIPLock: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch('/api/audit/ip-locks/unlock', jsonMutation('POST', body, options)),
  listEvaluations: (query = '', options?: ApiFetchOptions) => apiFetch(`/api/evaluations${query}`, options)
}

function responsibilityProfileQuery(historyLimit: number) {
  const normalizedLimit = Number.isFinite(historyLimit) ? Math.min(100, Math.max(0, Math.trunc(historyLimit))) : 20
  const params = new URLSearchParams({ history_limit: String(normalizedLimit) })
  return `?${params.toString()}`
}

function normalizeTalentMarketImportPreview(preview: unknown): TalentMarketImportPreviewResponseDTO {
  const source = (preview ?? {}) as Partial<TalentMarketImportPreviewResponseDTO>
  const items = Array.isArray(source.items) ? source.items : []

  return {
    ...source,
    items: items.map((item) => {
      const itemSource = item as typeof item & { conflicts?: unknown }
      return {
        ...item,
        conflicts: Array.isArray(itemSource.conflicts) ? itemSource.conflicts : []
      }
    }),
    conflicts: Array.isArray(source.conflicts) ? source.conflicts : []
  }
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}
