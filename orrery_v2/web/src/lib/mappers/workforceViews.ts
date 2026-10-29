import type {
  OrgGraphDTO,
  OrganizationNodeDTO,
  PermissionSummaryDTO,
  RoleAssignmentDTO,
  WorkforceMemoryProfileRefDTO,
  WorkforceMemberDTO,
  WorkforcePageDTO
} from '#lib/types/api'
import type {
  OrganizationPermissionSummaryViewModel,
  OrganizationPositionViewModel,
  OrganizationTreeViewModel,
  OrganizationUnitViewModel,
  OrgGraphViewModel,
  SquadDirectoryViewModel,
  WorkforceMemberViewModel,
  WorkforceViewModel
} from '#lib/types/view-models'

export function mapWorkforceView(dto: WorkforcePageDTO): WorkforceViewModel {
  const members = (dto.members ?? []).map(mapMember)

  return {
    summary: {
      total: members.length,
      enabled: members.filter((member) => member.enabled).length,
      disabled: members.filter((member) => !member.enabled).length,
      ai: members.filter((member) => member.agentKind === 'ai').length,
      human: members.filter((member) => member.agentKind === 'human').length
    },
    members
  }
}

export function mapOrgGraphView(dto: OrgGraphDTO): OrgGraphViewModel {
  const members = (dto.members ?? []).map(mapMember)
  const edges = (dto.reporting_edges ?? []).map((edge) => ({
    from: edge.from_agent_id,
    to: edge.to_manager_agent_id,
    lineId: edge.reporting_line_id || `${edge.from_agent_id}->${edge.to_manager_agent_id}`
  }))

  const positionsByDepartment = positionsByDepartmentFromDTO(dto, members)
  const membersByNode = membersByOrganizationNode(members)
  const codedOrganizations = (dto.organization_nodes?.length ?? 0) > 0
    ? mapOrganizationNodes(dto.organization_nodes ?? [], positionsByDepartment, membersByNode)
    : []
  const organizations = codedOrganizations.length > 0
    ? mergeOrganizationTrees(codedOrganizations, buildOrganizationTree(dto, members))
    : buildOrganizationTree(dto, members)
  const summary = dto.summary
  const organizationNodeCount = summary?.organization_count ?? countOrganizationNodes(organizations)
  const activeOrganizationCount = summary?.active_count ?? countOrganizationsByStatus(organizations, 'active')
  const inactiveOrganizationCount = summary?.inactive_count ?? Math.max(organizationNodeCount - activeOrganizationCount, 0)

  return {
    summary: {
      organizations: organizations.length,
      departments: dto.departments?.length ?? 0,
      positions: dto.positions?.length ?? 0,
      members: members.length,
      edges: edges.length,
      organizationNodes: organizationNodeCount,
      rootCount: summary?.root_count ?? organizations.length,
      activeOrganizations: activeOrganizationCount,
      inactiveOrganizations: inactiveOrganizationCount,
      totalMemberCount: summary?.total_member_count ?? organizations.reduce((sum, organization) => sum + organization.memberCount, 0),
      roleAssignmentCount: summary?.role_assignment_count ?? organizations.reduce((sum, organization) => sum + countRoleAssignments(organization), 0),
      pendingApprovalCount: summary?.pending_approval_count ?? 0
    },
    organizations,
    departments: (dto.departments ?? []).map((department) => ({
      id: department.department_id,
      organizationId: department.organization_id,
      memberCount: department.member_count
    })),
    positions: (dto.positions ?? []).map((position) => ({
      id: position.position_id,
      departmentId: position.department_id,
      memberCount: position.member_count
    })),
    members,
    edges,
    chartOption: {
      series: [
        {
          type: 'graph',
          roam: true,
          layout: 'force',
          data: members.map((member) => ({
            name: member.displayName,
            id: member.id,
            category: member.enabled ? 0 : 1,
            value: member.departmentId
          })),
          links: edges.map((edge) => ({ source: edge.from, target: edge.to })),
          categories: [{ name: 'Enabled' }, { name: 'Disabled' }],
          label: { show: true, color: '#e5edf6' },
          force: { repulsion: 160, edgeLength: 90 }
        }
      ]
    }
  }
}

function mapOrganizationNodes(
  nodes: OrganizationNodeDTO[],
  positionsByDepartment: Map<string, OrganizationPositionViewModel[]>,
  membersByNode: Map<string, WorkforceMemberViewModel[]>
): OrganizationTreeViewModel[] {
  return nodes.map((node) => mapOrganizationNode(node, node.id, positionsByDepartment, membersByNode)).sort(compareOrganizationNodes)
}

function mapOrganizationNode(
  node: OrganizationNodeDTO,
  rootOrganizationId: string,
  positionsByDepartment: Map<string, OrganizationPositionViewModel[]>,
  membersByNode: Map<string, WorkforceMemberViewModel[]>
): OrganizationTreeViewModel {
  const children = (node.children ?? []).map((child) => mapOrganizationNode(child, rootOrganizationId, positionsByDepartment, membersByNode)).sort(compareOrganizationNodes)
  const memberCount = node.total_member_count ?? node.direct_member_count ?? 0

  return {
    id: node.id,
    organizationId: rootOrganizationId,
    nodeType: node.node_type || 'department',
    code: node.code || '',
    displayCode: node.display_code || node.code || '',
    name: node.name || node.id,
    level: node.level ?? 0,
    status: node.status || 'active',
    ownerProfileId: node.owner_profile_id || '',
    ownerEmployeeNo: node.owner_employee_no || '',
    ownerName: node.owner_name || '',
    knowledgeBaseId: node.knowledge_base_id || '',
    parentId: node.parent_id || '',
    parentCode: node.parent_code || '',
    parentDisplayCode: node.parent_display_code || node.parent_code || '',
    parentName: node.parent_name || '',
    directMemberCount: node.direct_member_count ?? 0,
    totalMemberCount: memberCount,
    memberCount,
    unitCount: node.child_count ?? children.length,
    childCount: node.child_count ?? children.length,
    directMembers: membersByNode.get(node.id) ?? [],
    permissionSummary: mapPermissionSummary(node.permission_summary),
    roleAssignments: (node.role_assignments ?? []).map(mapRoleAssignment),
    approvalPolicyRefs: node.approval_policy_refs ?? [],
    positions: node.node_type === 'department' ? (positionsByDepartment.get(node.id) ?? []) : [],
    units: children.map((child) => organizationNodeToUnit(child)),
    children
  }
}

function organizationNodeToUnit(node: OrganizationTreeViewModel): OrganizationUnitViewModel {
  const childUnits = (node.children ?? []).map((child) => organizationNodeToUnit(child))
  return {
    id: node.id,
    name: node.name,
    organizationId: node.organizationId || node.id,
    parentId: node.parentId || '',
    defaultPositionId: '',
    code: node.code,
    displayCode: node.displayCode,
    status: node.status,
    ownerProfileId: node.ownerProfileId,
    ownerEmployeeNo: node.ownerEmployeeNo,
    ownerName: node.ownerName,
    knowledgeBaseId: node.knowledgeBaseId,
    directMemberCount: node.directMemberCount,
    totalMemberCount: node.totalMemberCount,
    memberCount: node.memberCount,
    directMembers: node.directMembers ?? [],
    positions: node.positions ?? [],
    children: childUnits
  }
}

function mapPermissionSummary(summary: PermissionSummaryDTO | undefined): OrganizationPermissionSummaryViewModel {
  return {
    effectiveRoleCount: summary?.effective_role_count ?? 0,
    directRoleCount: summary?.direct_role_count ?? 0,
    inheritedRoleCount: summary?.inherited_role_count ?? 0,
    keyPermissions: summary?.key_permissions ?? [],
    denyPolicyCount: summary?.deny_policy_count ?? 0,
    approvalRequiredCount: summary?.approval_required_count ?? 0
  }
}

function mapRoleAssignment(assignment: RoleAssignmentDTO) {
  return {
    assignmentId: assignment.assignment_id || '',
    actor: {
      actorType: assignment.actor?.actor_type || '',
      actorId: assignment.actor?.actor_id || ''
    },
    roleId: assignment.role_id || '',
    organizationNodeId: assignment.organization_node_id || '',
    organizationCode: assignment.organization_code || '',
    scopeMode: assignment.scope_mode || '',
    source: assignment.source || '',
    startsAt: assignment.starts_at || '',
    expiresAt: assignment.expires_at || '',
    assignedBy: assignment.assigned_by || ''
  }
}

function buildOrganizationTree(dto: OrgGraphDTO, members: WorkforceMemberViewModel[]): OrganizationTreeViewModel[] {
  const organizationIds = new Set<string>()
  for (const organization of dto.organizations ?? []) {
    organizationIds.add(organization.organization_id)
  }
  for (const department of dto.departments ?? []) {
    if (department.organization_id) organizationIds.add(department.organization_id)
  }
  for (const member of members) {
    if (member.organizationId) organizationIds.add(member.organizationId)
  }

  const unitsById = new Map<string, OrganizationUnitViewModel>()
  for (const department of dto.departments ?? []) {
    unitsById.set(department.department_id, {
      id: department.department_id,
      name: department.name || department.department_id,
      organizationId: department.organization_id,
      parentId: department.parent_department_id || '',
      defaultPositionId: department.default_position_id || '',
      ownerProfileId: department.owner_profile_id || '',
      ownerEmployeeNo: department.owner_employee_no || '',
      ownerName: department.owner_name || '',
      memberCount: 0,
      positions: [],
      children: []
    })
  }

  for (const member of members) {
    if (!member.departmentId || unitsById.has(member.departmentId)) continue
    unitsById.set(member.departmentId, {
      id: member.departmentId,
      name: member.departmentDisplayName || member.departmentId,
      organizationId: member.organizationId,
      parentId: '',
      defaultPositionId: '',
      memberCount: 0,
      positions: [],
      children: []
    })
  }

  const positionsByDepartment = positionsByDepartmentFromDTO(dto, members)

  for (const unit of unitsById.values()) {
    unit.positions = positionsByDepartment.get(unit.id) ?? []
    unit.memberCount = unit.positions.reduce((sum, position) => sum + position.members.length, 0)
  }

  for (const unit of unitsById.values()) {
    if (!unit.parentId) continue
    const parent = unitsById.get(unit.parentId)
    if (parent) parent.children.push(unit)
  }

  for (const unit of unitsById.values()) {
    unit.memberCount = totalUnitMembers(unit)
  }

  const organizationNames = new Map((dto.organizations ?? []).map((organization) => [organization.organization_id, organization.name]))
  const organizationOwners = new Map((dto.organizations ?? []).map((organization) => [organization.organization_id, {
    profileId: organization.owner_profile_id || '',
    employeeNo: organization.owner_employee_no || '',
    name: organization.owner_name || ''
  }]))
  const organizationKnowledgeBases = new Map((dto.organizations ?? []).map((organization) => [organization.organization_id, organization.knowledge_base_id || '']))
  return Array.from(organizationIds)
    .sort()
    .map((id) => {
      const units = Array.from(unitsById.values())
        .filter((unit) => unit.organizationId === id && !unit.parentId)
        .sort(compareByName)
      return {
        id,
        organizationId: id,
        nodeType: 'company',
        code: '',
        displayCode: '',
        name: organizationNames.get(id) || id,
        level: 1,
        status: 'active',
        ownerProfileId: organizationOwners.get(id)?.profileId || '',
        ownerEmployeeNo: organizationOwners.get(id)?.employeeNo || '',
        ownerName: organizationOwners.get(id)?.name || '',
        knowledgeBaseId: organizationKnowledgeBases.get(id) || '',
        parentId: '',
        parentCode: '',
        parentDisplayCode: '',
        parentName: '',
        directMemberCount: 0,
        totalMemberCount: units.reduce((sum, unit) => sum + unit.memberCount, 0),
        unitCount: Array.from(unitsById.values()).filter((unit) => unit.organizationId === id).length,
        memberCount: units.reduce((sum, unit) => sum + unit.memberCount, 0),
        childCount: units.length,
        directMembers: members.filter((member) => member.organizationId === id && !member.departmentId),
        permissionSummary: mapPermissionSummary(undefined),
        roleAssignments: [],
        approvalPolicyRefs: [],
        positions: [],
        units,
        children: units.map((unit) => unitToOrganizationNode(unit))
      }
    })
}

function mergeOrganizationTrees(primary: OrganizationTreeViewModel[], supplement: OrganizationTreeViewModel[]): OrganizationTreeViewModel[] {
  const merged = primary.slice()
  for (const node of supplement) {
    const existing = merged.find((candidate) => candidate.id === node.id)
    if (existing) {
      mergeOrganizationNode(existing, node)
    }
  }
  return merged.sort(compareOrganizationNodes)
}

function mergeOrganizationNode(target: OrganizationTreeViewModel, supplement: OrganizationTreeViewModel) {
  target.ownerProfileId ||= supplement.ownerProfileId
  target.ownerEmployeeNo ||= supplement.ownerEmployeeNo
  target.ownerName ||= supplement.ownerName
  target.knowledgeBaseId ||= supplement.knowledgeBaseId
  target.directMemberCount = Math.max(target.directMemberCount ?? 0, supplement.directMemberCount ?? 0)
  target.totalMemberCount = Math.max(target.totalMemberCount ?? 0, supplement.totalMemberCount ?? 0)
  target.memberCount = Math.max(target.memberCount, supplement.memberCount)

  const membersByKey = new Map<string, WorkforceMemberViewModel>()
  for (const member of [...(target.directMembers ?? []), ...(supplement.directMembers ?? [])]) {
    const key = member.profileId || member.id
    if (!membersByKey.has(key)) membersByKey.set(key, member)
  }
  target.directMembers = Array.from(membersByKey.values())

  const targetChildren = target.children ?? []
  for (const child of supplement.children ?? []) {
    const existingChild = targetChildren.find((candidate) => candidate.id === child.id)
    if (existingChild) mergeOrganizationNode(existingChild, child)
    else targetChildren.push(child)
  }
  target.children = targetChildren.sort(compareOrganizationNodes)
  target.childCount = Math.max(target.childCount ?? 0, target.children.length)
  target.unitCount = Math.max(target.unitCount, target.children.length)
  target.units = target.children.map((child) => organizationNodeToUnit(child))
}

function unitToOrganizationNode(unit: OrganizationUnitViewModel): OrganizationTreeViewModel {
  const children = unit.children.map((child) => unitToOrganizationNode(child))
  return {
    id: unit.id,
    organizationId: unit.organizationId,
    nodeType: 'department',
    code: unit.code || '',
    displayCode: unit.displayCode || unit.code || '',
    name: unit.name,
    level: 0,
    status: unit.status || 'active',
    ownerProfileId: unit.ownerProfileId || '',
    ownerEmployeeNo: unit.ownerEmployeeNo || '',
    ownerName: unit.ownerName || '',
    knowledgeBaseId: unit.knowledgeBaseId || '',
    parentId: unit.parentId,
    parentCode: '',
    parentDisplayCode: '',
    parentName: '',
    directMemberCount: unit.directMemberCount ?? unit.memberCount,
    totalMemberCount: unit.totalMemberCount ?? unit.memberCount,
    memberCount: unit.memberCount,
    unitCount: children.length,
    childCount: children.length,
    directMembers: (unit.positions ?? []).flatMap((position) => position.members),
    permissionSummary: mapPermissionSummary(undefined),
    roleAssignments: [],
    approvalPolicyRefs: [],
    positions: unit.positions,
    units: children.map((child) => organizationNodeToUnit(child)),
    children
  }
}

function appendPosition(target: Map<string, OrganizationPositionViewModel[]>, position: OrganizationPositionViewModel) {
  const list = target.get(position.departmentId) ?? []
  list.push(position)
  target.set(position.departmentId, list)
}

function positionsByDepartmentFromDTO(dto: OrgGraphDTO, members: WorkforceMemberViewModel[]) {
  const positionsByDepartment = new Map<string, OrganizationPositionViewModel[]>()
  for (const position of dto.positions ?? []) {
    appendPosition(positionsByDepartment, {
      id: position.position_id,
      name: position.name || position.position_id,
      departmentId: position.department_id,
      knowledgeBaseId: position.knowledge_base_id || '',
      memberCount: position.member_count,
      members: []
    })
  }

  for (const member of members) {
    const departmentId = member.departmentId || 'unassigned'
    const existing = positionsByDepartment.get(departmentId)?.find((position) => position.id === member.positionId)
    if (existing) {
      existing.name = member.positionDisplayName || existing.name
      existing.members.push(member)
      existing.memberCount = existing.members.length
      continue
    }
    appendPosition(positionsByDepartment, {
      id: member.positionId || 'unassigned',
      name: member.positionDisplayName || member.positionId || 'unassigned',
      departmentId,
      knowledgeBaseId: '',
      memberCount: 1,
      members: [member]
    })
  }
  return positionsByDepartment
}

function membersByOrganizationNode(members: WorkforceMemberViewModel[]) {
  const byNode = new Map<string, WorkforceMemberViewModel[]>()
  for (const member of members) {
    const nodeId = member.departmentId || member.organizationId
    if (!nodeId) continue
    const list = byNode.get(nodeId) ?? []
    list.push(member)
    byNode.set(nodeId, list)
  }
  return byNode
}

function totalUnitMembers(unit: OrganizationUnitViewModel): number {
  return unit.positions.reduce((sum, position) => sum + position.members.length, 0) + unit.children.reduce((sum, child) => sum + totalUnitMembers(child), 0)
}

function compareByName(left: { name: string }, right: { name: string }) {
  return left.name.localeCompare(right.name)
}

function compareOrganizationNodes(left: OrganizationTreeViewModel, right: OrganizationTreeViewModel) {
  const leftCode = left.code || left.displayCode || left.name
  const rightCode = right.code || right.displayCode || right.name
  return leftCode.localeCompare(rightCode, undefined, { numeric: true })
}

function countOrganizationNodes(nodes: OrganizationTreeViewModel[]): number {
  return nodes.reduce((sum, node) => sum + 1 + countOrganizationNodes(node.children ?? []), 0)
}

function countOrganizationsByStatus(nodes: OrganizationTreeViewModel[], status: string): number {
  return nodes.reduce(
    (sum, node) => sum + (node.status === status ? 1 : 0) + countOrganizationsByStatus(node.children ?? [], status),
    0
  )
}

function countRoleAssignments(node: OrganizationTreeViewModel): number {
  return (node.roleAssignments?.length ?? 0) + (node.children ?? []).reduce((sum, child) => sum + countRoleAssignments(child), 0)
}

export function mapSquadDirectoryView(dto: OrgGraphDTO): SquadDirectoryViewModel {
  const members = (dto.members ?? []).map(mapMember)
  return buildSquadDirectoryView(
    members,
    new Map((dto.departments ?? []).map((department) => [department.department_id, department.organization_id]))
  )
}

export function mapSquadDirectoryFromOrgGraphView(view: OrgGraphViewModel): SquadDirectoryViewModel {
  return buildSquadDirectoryView(
    view.members,
    new Map(view.departments.map((department) => [department.id, department.organizationId]))
  )
}

function buildSquadDirectoryView(
  members: WorkforceMemberViewModel[],
  organizationIdByDepartment: Map<string, string>
): SquadDirectoryViewModel {
  const departmentIds = new Set(organizationIdByDepartment.keys())
  for (const member of members) {
    departmentIds.add(member.departmentId || 'unassigned')
  }

  const squads = Array.from(departmentIds)
    .sort()
    .map((departmentId) => {
      const squadMembers = members.filter((member) => (member.departmentId || 'unassigned') === departmentId)
      const humanCount = squadMembers.filter((member) => member.agentKind === 'human').length
      const agentCount = squadMembers.length - humanCount
      const active = squadMembers.some((member) => member.enabled)
      const organizationId = squadMembers[0]?.organizationId || organizationIdByDepartment.get(departmentId)

      return {
        id: departmentId,
        name: `${departmentId} squad`,
        status: active ? 'active' : 'forming',
        humanCount,
        agentCount,
        missionId: organizationId || 'unassigned',
        members: squadMembers.map((member) => ({
          id: member.id,
          profileId: member.profileId,
          name: member.displayName,
          role: member.positionId || member.roles[0] || member.agentKind,
          source: member.agentKind === 'human' ? ('human' as const) : ('agent' as const),
          status: member.enabled ? 'active' : 'disabled'
        }))
      }
    })

  return {
    summary: {
      squads: squads.length,
      humans: squads.reduce((sum, squad) => sum + squad.humanCount, 0),
      agents: squads.reduce((sum, squad) => sum + squad.agentCount, 0),
      active: squads.filter((squad) => squad.status === 'active').length
    },
    squads
  }
}

function mapMember(member: WorkforceMemberDTO): WorkforceMemberViewModel {
  const enabled = member.enabled !== false
  const backstory = member.persona?.backstory || ''
  const roleLabels = readableRoleLabels(member.role_ids ?? [])
  const functionalRoles = member.functional_role_ids ?? []
  const functionalRoleLabels = readableRoleLabels(functionalRoles.filter(isVisibleFunctionalRole))
  const roleName = member.role_name || ''
  const primaryRoleLabel = roleName || member.position_display_name || member.position_id || roleLabels[0] || 'Not provided'
  const placementStatus = member.placement_status || 'internal'
  const userAccount = {
    username: member.user_account?.username || '',
    nickname: member.user_account?.nickname || '',
    email: member.user_account?.email || '',
    role: member.user_account?.role || '',
    accountEnabled: member.user_account?.account_enabled === true,
    permissionScope: member.user_account?.permission_scope ?? []
  }

  return {
    id: member.agent_id || member.agent_profile_id,
    profileId: member.agent_profile_id,
    personalAssistant: member.personal_assistant === true,
    personalAssistantOwnerId: member.personal_assistant_owner_id || '',
    agentInstanceId: member.agent_instance_id || '',
    realName: member.real_name,
    displayName: member.display_name || userAccount.username || member.real_name || member.agent_profile_id,
    agentKind: member.agent_kind || 'Not provided',
    userAccount,
    accountProjection: {
      status: member.account_projection?.status || '',
      revision: member.account_projection?.revision ?? 0,
      kratosIdentityId: member.account_projection?.kratos_identity_id || ''
    },
    employmentType: member.employment_type || 'Not provided',
    placementStatus,
    organizationId: member.organization_id || '',
    departmentId: member.department_id || '',
    positionId: member.position_id || '',
    departmentDisplayName: member.department_display_name || member.department_id || 'Not provided',
    positionDisplayName: member.position_display_name || member.position_id || 'Not provided',
    roleName,
    roleLabels,
    primaryRoleLabel,
    capabilityTags: member.capability_tags ?? [],
    roles: member.role_ids ?? [],
    functionalRoles,
    functionalRoleLabels,
    managerId: member.manager_agent_id || 'Not assigned',
    authorityScope: member.authority_scope || 'Not provided',
    supervisionPolicy: member.supervision_policy || 'Not provided',
    workspaceRoot: member.workspace_root || '',
    runtimeKind: member.runtime_kind || 'System managed',
    runtimeConfigRef: member.runtime_config_ref || 'Not provided',
    runtimeConfig: member.runtime_config ?? {},
    baseAgent: {
      inheritsBaseAgent: member.base_agent?.inherits_base_agent ?? false,
      auditRequired: member.base_agent?.audit_required ?? false,
      systemPrompt: member.base_agent?.system_prompt || '',
      hasSystemPrompt: member.base_agent?.has_system_prompt ?? false,
      hasLlmApiKeyRef: member.base_agent?.has_llm_api_key_ref ?? false,
      llmApiKeyRef: member.base_agent?.llm_api_key_ref || '',
      skillScope: member.base_agent?.skill_scope || '',
      toolScope: member.base_agent?.tool_scope || '',
      mcpScope: member.base_agent?.mcp_scope || ''
    },
    resume: backstory,
    prompt: member.base_agent?.system_prompt || '',
    responsibilityBoundary: member.persona?.responsibility_boundary || '',
    behaviorBoundary: member.persona?.behavior_boundary || '',
    knowledgeBaseId: member.knowledge_base_id || '',
    skillRefs: (member.skill_refs ?? []).map((ref) => ({
      skillId: ref.skill_id,
      versionConstraint: ref.version_constraint || '',
      alias: ref.alias || '',
      level: ref.level || '',
      evidenceRefs: ref.evidence_refs ?? []
    })),
    toolRefs: (member.tool_refs ?? []).map((ref) => ({
      toolId: ref.tool_id,
      versionConstraint: ref.version_constraint || '',
      alias: ref.alias || '',
      permissionScope: ref.permission_scope || '',
      bindingPolicy: ref.binding_policy || ''
    })),
    personalMemoryRef: mapMemoryRef(member.personal_memory_ref),
    collectiveMemoryRefs: (member.collective_memory_refs ?? []).map(mapMemoryRef),
    qaEnabled: member.qa_enabled === true,
    enabled,
    statusLabel: enabled ? 'Enabled' : 'Disabled',
    statusTone: enabled ? 'success' : 'danger',
    email: member.email || 'Not provided',
    availabilityStatus: member.availability_status || (enabled ? 'unknown' : 'disabled'),
    releasedFromOrganizationId: member.released_from_organization_id || '',
    releasedFromDepartmentId: member.released_from_department_id || '',
    releasedFromPositionId: member.released_from_position_id || '',
    releasedAt: member.released_at || '',
    releasedReason: member.released_reason || ''
  }
}

function readableRoleLabels(roleIds: string[]) {
  return roleIds.map(readableRoleLabel).filter(Boolean)
}

function isVisibleFunctionalRole(roleId: string) {
  return !['primary_role', 'human_agent', 'digital_agent', 'superadmin'].includes(roleId.trim().toLowerCase())
}

function readableRoleLabel(roleId: string) {
  const id = roleId.trim()
  if (!id) return ''
  if (/^[a-z]+$/i.test(id) && id.length <= 4) return id.toUpperCase()
  return id
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function mapMemoryRef(ref: WorkforceMemoryProfileRefDTO | undefined) {
  return {
    memoryProfileId: ref?.memory_profile_id || '',
    ownerAgentId: ref?.owner_agent_id || '',
    memoryKind: ref?.memory_kind || '',
    accessScope: ref?.access_scope || '',
    retrievalPolicy: ref?.retrieval_policy || '',
    compressionPolicy: ref?.compression_policy || ''
  }
}
