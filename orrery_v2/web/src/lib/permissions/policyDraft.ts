import type { PermissionPolicyDTO } from '#lib/types/api'

export interface RoleDraftInput {
  roleId: string
  labelZh: string
  labelEn: string
  permissionScopes?: string[]
}

export function clonePermissionPolicy(policy: PermissionPolicyDTO = {}): PermissionPolicyDTO {
  return {
    roles: (policy.roles ?? []).map((role) => ({ ...role })),
    role_assignments: (policy.role_assignments ?? []).map((assignment) => ({ ...assignment })),
    grants: (policy.grants ?? []).map((grant) => ({ ...grant }))
  }
}

export function addPolicyRole(policy: PermissionPolicyDTO, input: RoleDraftInput): PermissionPolicyDTO {
  const next = clonePermissionPolicy(policy)
  const roleId = input.roleId.trim()
  if (!roleId || next.roles?.some((role) => role.role_id === roleId)) return next
  next.roles = [
    ...(next.roles ?? []),
    {
      role_id: roleId,
      label_zh: input.labelZh.trim() || roleId,
      label_en: input.labelEn.trim() || roleId
    }
  ]
  const existingGrants = next.grants ?? []
  const permissionScopes = Array.from(new Set((input.permissionScopes ?? []).map((scope) => scope.trim()).filter(Boolean)))
  next.grants = [
    ...existingGrants,
    ...permissionScopes
      .filter((scope) => !existingGrants.some((grant) => grant.subject_kind === 'role' && grant.subject_id === roleId && grant.scope === scope))
      .map((scope) => ({ subject_kind: 'role', subject_id: roleId, scope }))
  ]
  return next
}

export function toggleRoleScopeGrant(policy: PermissionPolicyDTO, roleId: string, scopes: string[], capabilityIds: string[], enabled: boolean): PermissionPolicyDTO {
  return toggleGrant(policy, 'role', roleId, scopes, capabilityIds, enabled)
}

export function toggleRoleAssignment(policy: PermissionPolicyDTO, principalKind: string, principalId: string, roleId: string, enabled: boolean): PermissionPolicyDTO {
  const next = clonePermissionPolicy(policy)
  const kind = normalizeKind(principalKind)
  const id = principalId.trim()
  const role = roleId.trim()
  next.role_assignments = (next.role_assignments ?? []).filter((assignment) => {
    return !(assignment.principal_kind === kind && assignment.principal_id === id && assignment.role_id === role)
  })
  if (enabled && kind && id && role) {
    next.role_assignments.push({ principal_kind: kind, principal_id: id, role_id: role })
  }
  return next
}

function toggleGrant(policy: PermissionPolicyDTO, subjectKind: string, subjectId: string, scopes: string[], capabilityIds: string[], enabled: boolean): PermissionPolicyDTO {
  const next = clonePermissionPolicy(policy)
  const kind = normalizeKind(subjectKind)
  const id = subjectId.trim()
  const permissionScopes = Array.from(new Set(scopes.map((scope) => scope.trim()).filter(Boolean)))
  const coveredScopes = new Set(permissionScopes)
  const coveredCapabilities = new Set(capabilityIds.map((capabilityId) => capabilityId.trim()).filter(Boolean))
  next.grants = (next.grants ?? []).filter((grant) => {
    if (normalizeKind(grant.subject_kind) !== kind || grant.subject_id !== id) return true
    if (grant.scope && coveredScopes.has(grant.scope)) return false
    if (grant.capability_id && coveredCapabilities.has(grant.capability_id)) return false
    return true
  })
  if (enabled && kind && id) {
    next.grants.push(...permissionScopes.map((scope) => ({ subject_kind: kind, subject_id: id, scope })))
  }
  return next
}

function normalizeKind(kind: string) {
  const value = kind.trim().toLowerCase()
  if (value === 'human') return 'user'
  return value
}
