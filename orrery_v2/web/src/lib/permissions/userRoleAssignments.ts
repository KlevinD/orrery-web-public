import type { RoleAssignmentDTO, RoleDefinitionDTO } from '#lib/types/api'
import type { OrganizationTreeViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

export function humanAssignableRoles(roles: RoleDefinitionDTO[] = []) {
  return roles.filter((role) => role.assignable_actor_types.some((kind) => ['human', 'user'].includes(kind.trim().toLowerCase())))
}

export function assignedUserRoleIds(workforceRoleIds: string[] = [], assignments: RoleAssignmentDTO[] = []) {
  return canonicalRoleIds([
    ...workforceRoleIds,
    ...assignments.map((assignment) => assignment.role_id ?? '')
  ])
}

export function roleSelectionChanged(baseline: string[], selected: string[], assignableRoleIds: string[]) {
  const assignable = new Set(assignableRoleIds)
  const before = canonicalRoleIds(baseline.filter((roleId) => assignable.has(roleId)))
  const after = canonicalRoleIds(selected.filter((roleId) => assignable.has(roleId)))
  return before.join('\n') !== after.join('\n')
}

export function userRoleAssignmentChanges(
  baseline: string[],
  selected: string[],
  assignments: RoleAssignmentDTO[]
) {
  const before = new Set(canonicalRoleIds(baseline))
  const after = new Set(canonicalRoleIds(selected))
  return {
    addedRoleIds: Array.from(after).filter((roleId) => !before.has(roleId)),
    removableAssignments: assignments.filter((assignment) =>
      Boolean(assignment.assignment_id) &&
      assignment.source !== 'system' &&
      before.has(assignment.role_id ?? '') &&
      !after.has(assignment.role_id ?? '')
    )
  }
}

export function systemManagedRoleIds(assignments: RoleAssignmentDTO[] = []) {
  return new Set(assignments.filter((assignment) => assignment.source === 'system').map((assignment) => assignment.role_id ?? ''))
}

export function buildDirectUserRoleAssignment(
  member: WorkforceMemberViewModel,
  role: RoleDefinitionDTO,
  organizations: OrganizationTreeViewModel[]
): RoleAssignmentDTO | undefined {
  const assignment: RoleAssignmentDTO = {
    actor: { actor_type: 'human', actor_id: member.profileId },
    role_id: role.role_id,
    source: 'direct'
  }

  if (role.category.trim().toLowerCase() === 'platform') {
    return { ...assignment, scope_mode: 'platform' }
  }

  const scope = findOrganizationScope(organizations, [member.departmentId, member.organizationId])
  if (!scope?.code) return undefined
  return {
    ...assignment,
    organization_node_id: scope.id,
    organization_code: scope.code,
    scope_mode: 'self'
  }
}

function findOrganizationScope(organizations: OrganizationTreeViewModel[], candidateIds: string[]) {
  for (const candidateId of candidateIds.map((id) => id.trim()).filter(Boolean)) {
    const match = findOrganizationNode(organizations, candidateId)
    if (match) return match
  }
  return undefined
}

function findOrganizationNode(nodes: OrganizationTreeViewModel[], id: string): OrganizationTreeViewModel | undefined {
  for (const node of nodes) {
    if (node.id === id) return node
    const child = findOrganizationNode(node.children ?? [], id)
    if (child) return child
  }
  return undefined
}

function canonicalRoleIds(roleIds: string[]) {
  return Array.from(new Set(roleIds.map((roleId) => roleId.trim().toLowerCase()).filter(Boolean))).sort()
}
