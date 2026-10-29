import type {
  OrganizationTreeViewModel,
  RelationshipHierarchyNode,
  WorkforceMemberViewModel
} from '#lib/types/view-models'

type ReportingEdge = { from: string; to: string; lineId: string }

type ReportingNodeDescriptor = {
  id: string
  label: string
  subtitle: string
  detail?: string
  profileId?: string
  human?: boolean
  disabled?: boolean
  context?: string
}

export function filterReportingMembers(
  allMembers: WorkforceMemberViewModel[],
  node?: OrganizationTreeViewModel
): WorkforceMemberViewModel[] {
  const ownerKeys = new Set(
    [node?.ownerProfileId, node?.ownerEmployeeNo, node?.ownerName]
      .map(normalizeIdentifier)
      .filter(Boolean)
  )
  const nodeMemberKeys = new Set(
    (node ? collectMembers(node) : [])
      .flatMap((member) => memberIdentifiers(member))
  )
  const departmentIds = new Set(
    node
      ? flattenOrganizationNodes([node])
          .filter((item) => item.nodeType === 'department')
          .map((item) => item.id)
      : []
  )
  const organizationId = node?.organizationId || (node?.nodeType === 'company' ? node.id : '')

  return allMembers.filter((member) => {
    const isOwner = memberIdentifiers(member).some((key) => ownerKeys.has(key))
    if (member.placementStatus === 'market' && !isOwner) return false
    if (!node) return true
    if (memberIdentifiers(member).some((key) => nodeMemberKeys.has(key))) return true
    if (organizationId && member.organizationId !== organizationId) return false
    return node.nodeType === 'company' || departmentIds.has(member.departmentId) || member.departmentId === node.id
  })
}

export function findMemberByIdentifier(
  allMembers: WorkforceMemberViewModel[],
  identifier: string
): WorkforceMemberViewModel | undefined {
  const key = normalizeIdentifier(identifier)
  if (!key) return undefined
  return allMembers.find((member) => memberIdentifiers(member).includes(key))
}

export function findHierarchyNode(
  nodes: RelationshipHierarchyNode[],
  identifier: string
): RelationshipHierarchyNode | undefined {
  for (const node of nodes) {
    if (node.id === identifier) return node
    const match = findHierarchyNode(node.children ?? [], identifier)
    if (match) return match
  }
  return undefined
}

export function buildReportingHierarchy(
  reportingMembers: WorkforceMemberViewModel[],
  edges: ReportingEdge[],
  organization?: OrganizationTreeViewModel
): RelationshipHierarchyNode[] {
  const descriptorsById = new Map<string, ReportingNodeDescriptor>()
  const aliasesToId = new Map<string, string>()
  const allNodeIds = new Set<string>()

  for (const member of reportingMembers) {
    const descriptor = toReportingDescriptor(member)
    descriptorsById.set(member.id, descriptor)
    allNodeIds.add(member.id)
    for (const identifier of memberIdentifiers(member)) aliasesToId.set(identifier, member.id)
  }

  const owner = resolveOrganizationOwner(organization, reportingMembers)
  if (owner) {
    if (!descriptorsById.has(owner.id)) {
      descriptorsById.set(owner.id, owner.descriptor)
      allNodeIds.add(owner.id)
    }
    for (const identifier of owner.aliases) aliasesToId.set(identifier, owner.id)
  }

  disambiguateLabels(descriptorsById)

  const childrenByManager = new Map<string, string[]>()
  const managedIds = new Set<string>()

  const addExternalManager = (value: string): string => {
    const key = normalizeIdentifier(value)
    if (!key) return ''
    const existing = aliasesToId.get(key)
    if (existing) return existing
    const id = `external:${value.trim()}`
    if (!descriptorsById.has(id)) {
      descriptorsById.set(id, {
        id,
        label: value.trim(),
        subtitle: '汇报关系中的人员',
        profileId: value.trim(),
        human: true
      })
      allNodeIds.add(id)
    }
    aliasesToId.set(key, id)
    return id
  }

  const addRelation = (childValue: string | undefined, managerValue: string | undefined) => {
    const childId = aliasesToId.get(normalizeIdentifier(childValue))
    if (!childId || !hasAssignedManager(managerValue)) return
    const managerId = aliasesToId.get(normalizeIdentifier(managerValue)) || addExternalManager(managerValue || '')
    if (!managerId || managerId === childId) return
    const children = childrenByManager.get(managerId) ?? []
    if (!children.includes(childId)) children.push(childId)
    childrenByManager.set(managerId, children)
    managedIds.add(childId)
  }

  for (const edge of edges) addRelation(edge.from, edge.to)

  // Some payloads expose the manager only on the member row. Keep the graph
  // usable when reporting_edges is empty or uses a different identifier.
  for (const member of reportingMembers) addRelation(member.id, member.managerId)

  let rootIds = Array.from(allNodeIds).filter((id) => !managedIds.has(id))
  if (rootIds.length === 0 && allNodeIds.size > 0) {
    // A cyclic or incomplete manager chain must still have an entry point.
    rootIds = [Array.from(allNodeIds)[0]]
  }

  if (owner) {
    // The organization owner is the top of this organization archive even
    // when a stale manager relation points to an ancestor outside the view.
    for (const [managerId, childIds] of childrenByManager) {
      if (managerId !== owner.id) childrenByManager.set(managerId, childIds.filter((id) => id !== owner.id))
    }
    const ownerChildren = childrenByManager.get(owner.id) ?? []
    for (const id of rootIds) {
      if (id !== owner.id && !ownerChildren.includes(id)) ownerChildren.push(id)
      if (id !== owner.id) managedIds.add(id)
    }
    childrenByManager.set(owner.id, ownerChildren)
    managedIds.delete(owner.id)
    rootIds = [owner.id]
  }

  const reachableIds = new Set<string>()
  const pendingIds = [...rootIds]
  while (pendingIds.length > 0) {
    const id = pendingIds.pop()
    if (!id || reachableIds.has(id)) continue
    reachableIds.add(id)
    pendingIds.push(...(childrenByManager.get(id) ?? []))
  }
  const unreachableIds = Array.from(allNodeIds).filter((id) => !reachableIds.has(id))
  if (unreachableIds.length > 0) {
    if (owner) {
      const ownerChildren = childrenByManager.get(owner.id) ?? []
      for (const id of unreachableIds) {
        if (id !== owner.id && !ownerChildren.includes(id)) ownerChildren.push(id)
      }
      childrenByManager.set(owner.id, ownerChildren)
    } else {
      rootIds = [...rootIds, ...unreachableIds]
    }
  }

  return rootIds.map((id) => toReportingNode(id, descriptorsById, childrenByManager, new Set<string>()))
}

function toReportingDescriptor(member: WorkforceMemberViewModel): ReportingNodeDescriptor {
  return {
    id: member.id,
    label: member.displayName || member.realName || member.profileId || member.id,
    subtitle: `${member.departmentDisplayName || '未设置部门'} / ${member.roleName || member.primaryRoleLabel || '未设置角色'}`,
    detail: member.responsibilityBoundary,
    profileId: member.profileId || member.id,
    human: (member.agentKind || '').toLowerCase() === 'human',
    disabled: !member.enabled,
    context: member.profileId || member.roleName || member.primaryRoleLabel || member.id
  }
}

function resolveOrganizationOwner(
  node: OrganizationTreeViewModel | undefined,
  reportingMembers: WorkforceMemberViewModel[]
): { id: string; aliases: string[]; descriptor: ReportingNodeDescriptor } | undefined {
  if (!node) return undefined
  const ownerValues = [node.ownerProfileId, node.ownerEmployeeNo, node.ownerName]
    .filter((value): value is string => Boolean(value?.trim()))
  const ownerMember = reportingMembers.find((member) => {
    const memberKeys = memberIdentifiers(member)
    return ownerValues.some((value) => {
      const normalized = normalizeIdentifier(value)
      return memberKeys.includes(normalized) || normalizeIdentifier(member.displayName) === normalized || normalizeIdentifier(member.realName) === normalized
    })
  })
  if (ownerMember) return { id: ownerMember.id, aliases: memberIdentifiers(ownerMember), descriptor: toReportingDescriptor(ownerMember) }
  if (ownerValues.length === 0) return undefined

  const ownerId = `owner:${node.id}`
  const aliases = Array.from(new Set([...ownerValues, ownerId].map(normalizeIdentifier).filter(Boolean)))
  const label = node.ownerName?.trim() || node.ownerEmployeeNo?.trim() || node.ownerProfileId?.trim() || '组织负责人'
  return {
    id: ownerId,
    aliases,
    descriptor: {
      id: ownerId,
      label,
      subtitle: `组织负责人 / ${node.ownerEmployeeNo?.trim() || '负责人'}`,
      detail: '负责本组织及下属单位的组织责任',
      profileId: node.ownerProfileId?.trim() || ownerId,
      human: true,
      context: '组织负责人'
    }
  }
}

function disambiguateLabels(descriptorsById: Map<string, ReportingNodeDescriptor>) {
  const counts = new Map<string, number>()
  for (const descriptor of descriptorsById.values()) {
    const key = normalizeIdentifier(descriptor.label)
    counts.set(key, (counts.get(key) ?? 0) + 1)
  }
  for (const descriptor of descriptorsById.values()) {
    if ((counts.get(normalizeIdentifier(descriptor.label)) ?? 0) < 2) continue
    const context = compactContext(descriptor.context || descriptor.profileId || descriptor.id)
    if (context) descriptor.label = `${descriptor.label} · ${context}`
  }
}

function compactContext(value: string): string {
  const normalized = value.trim().toLowerCase()
  if (normalized.includes('a000014') || normalized.includes('website-receptionist-public')) return '公开接待'
  if (normalized.includes('website-knowledge-curator')) return '知识维护'
  if (normalized.includes('a000015') || normalized.includes('website-receptionist-customer')) return '客户接待'
  if (normalized.includes('public')) return '公开接待'
  if (normalized.includes('knowledge')) return '知识维护'
  if (normalized.includes('customer')) return '客户接待'
  return value.trim().length > 24 ? value.trim().slice(0, 24) : value.trim()
}

function toReportingNode(
  id: string,
  descriptorsById: Map<string, ReportingNodeDescriptor>,
  childrenByManager: Map<string, string[]>,
  ancestry: Set<string>
): RelationshipHierarchyNode {
  const descriptor = descriptorsById.get(id)
  if (!descriptor) {
    return { id, label: id, subtitle: '人员档案不可用', kind: 'person', children: [] }
  }
  const nextAncestry = new Set(ancestry).add(id)
  const childIds = (childrenByManager.get(id) ?? []).filter((childId) => !nextAncestry.has(childId))
  return {
    id: descriptor.id,
    label: descriptor.label,
    subtitle: descriptor.subtitle,
    detail: descriptor.detail,
    kind: 'person',
    profileId: descriptor.profileId || descriptor.id,
    human: descriptor.human,
    disabled: descriptor.disabled,
    children: childIds.map((childId) => toReportingNode(childId, descriptorsById, childrenByManager, nextAncestry))
  }
}

function collectMembers(node: OrganizationTreeViewModel): WorkforceMemberViewModel[] {
  return [
    ...(node.directMembers ?? []),
    ...(node.children ?? []).flatMap((child) => collectMembers(child))
  ]
}

function flattenOrganizationNodes(nodes: OrganizationTreeViewModel[]): OrganizationTreeViewModel[] {
  return nodes.flatMap((node) => [node, ...flattenOrganizationNodes(node.children ?? [])])
}

function memberIdentifiers(member: WorkforceMemberViewModel): string[] {
  return [member.id, member.profileId, member.agentInstanceId]
    .map(normalizeIdentifier)
    .filter(Boolean)
}

function normalizeIdentifier(value: string | undefined): string {
  return value?.trim().toLowerCase() || ''
}

function hasAssignedManager(value: string | undefined): boolean {
  const normalized = normalizeIdentifier(value)
  return Boolean(normalized && !['not assigned', 'unassigned', 'none', 'null', 'undefined'].includes(normalized))
}
