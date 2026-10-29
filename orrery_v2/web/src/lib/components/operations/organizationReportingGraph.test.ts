import { describe, expect, it } from 'vitest'
import type { OrganizationTreeViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'
import { buildReportingHierarchy, filterReportingMembers } from './organizationReportingGraph'

function member(overrides: Partial<WorkforceMemberViewModel> = {}): WorkforceMemberViewModel {
  return {
    id: 'agent-worker',
    profileId: 'profile-worker',
    agentInstanceId: 'instance-worker',
    realName: 'Worker',
    displayName: 'Worker',
    agentKind: 'human',
    userAccount: { username: '', nickname: '', email: '', role: '', accountEnabled: true, permissionScope: [] },
    employmentType: 'employee',
    placementStatus: 'internal',
    organizationId: 'org-1',
    departmentId: 'dept-1',
    positionId: 'position-1',
    departmentDisplayName: '运营部',
    positionDisplayName: '',
    roleName: '运营负责人',
    roleLabels: [],
    primaryRoleLabel: '',
    capabilityTags: [],
    roles: [],
    managerId: 'Not assigned',
    authorityScope: '',
    supervisionPolicy: '',
    workspaceRoot: '',
    runtimeKind: '',
    runtimeConfigRef: '',
    runtimeConfig: {},
    baseAgent: {
      inheritsBaseAgent: false,
      auditRequired: false,
      hasSystemPrompt: false,
      hasLlmApiKeyRef: false,
      llmApiKeyRef: '',
      skillScope: '',
      toolScope: '',
      mcpScope: ''
    },
    resume: '',
    prompt: '',
    responsibilityBoundary: '负责运营工作',
    behaviorBoundary: '',
    skillRefs: [],
    toolRefs: [],
    personalMemoryRef: { memoryProfileId: '', ownerAgentId: '', memoryKind: '', accessScope: '', retrievalPolicy: '', compressionPolicy: '' },
    collectiveMemoryRefs: [],
    qaEnabled: false,
    enabled: true,
    statusLabel: 'active',
    statusTone: 'success',
    email: '',
    availabilityStatus: '',
    releasedFromOrganizationId: '',
    releasedFromDepartmentId: '',
    releasedFromPositionId: '',
    releasedAt: '',
    releasedReason: '',
    ...overrides
  }
}

function organization(overrides: Partial<OrganizationTreeViewModel> = {}): OrganizationTreeViewModel {
  return {
    id: 'org-1',
    organizationId: 'org-1',
    nodeType: 'company',
    name: 'Orrery',
    memberCount: 1,
    unitCount: 1,
    units: [],
    children: [],
    ...overrides
  }
}

describe('organization reporting graph', () => {
  it('creates an organization owner node when the owner is not in members', () => {
    const worker = member({ managerId: 'profile-owner' })
    const roots = buildReportingHierarchy(
      [worker],
      [{ from: 'profile-worker', to: 'profile-owner', lineId: 'line-1' }],
      organization({ ownerProfileId: 'profile-owner', ownerEmployeeNo: 'E001', ownerName: '组织负责人' })
    )

    expect(roots).toHaveLength(1)
    expect(roots[0]).toMatchObject({
      id: 'owner:org-1',
      label: '组织负责人',
      subtitle: '组织负责人 / E001',
      children: [expect.objectContaining({
        id: 'agent-worker',
        subtitle: '运营部 / 运营负责人'
      })]
    })
  })

  it('resolves profile and agent identifiers to one hierarchy node', () => {
    const owner = member({ id: 'agent-owner', profileId: 'profile-owner', displayName: 'Owner', roleName: '总负责人' })
    const worker = member({ managerId: 'profile-owner' })
    const roots = buildReportingHierarchy(
      [owner, worker],
      [{ from: 'profile-worker', to: 'agent-owner', lineId: 'line-1' }],
      organization({ ownerProfileId: 'profile-owner', ownerName: 'Owner' })
    )

    expect(roots).toHaveLength(1)
    expect(roots[0]).toMatchObject({
      id: 'agent-owner',
      label: 'Owner',
      children: [expect.objectContaining({ id: 'agent-worker' })]
    })
  })

  it('keeps selected organization members and excludes market members', () => {
    const worker = member()
    const market = member({ id: 'market-worker', profileId: 'market-worker', placementStatus: 'market' })
    const filtered = filterReportingMembers([worker, market], organization({ directMembers: [worker] }))

    expect(filtered.map((item) => item.id)).toEqual(['agent-worker'])
  })

  it('keeps a cyclic manager chain visible instead of dropping all its nodes', () => {
    const first = member({ id: 'agent-a', profileId: 'profile-a', displayName: 'A', managerId: 'profile-b' })
    const second = member({ id: 'agent-b', profileId: 'profile-b', displayName: 'B', managerId: 'profile-a' })
    const roots = buildReportingHierarchy(
      [first, second],
      [
        { from: 'profile-a', to: 'profile-b', lineId: 'line-a' },
        { from: 'profile-b', to: 'profile-a', lineId: 'line-b' }
      ]
    )

    expect(roots).toHaveLength(1)
    expect(roots[0]).toMatchObject({ id: 'agent-a', children: [expect.objectContaining({ id: 'agent-b' })] })
  })

  it('adds a role suffix when distinct profiles share the same display name', () => {
    const publicMax = member({ id: 'website-receptionist-public', profileId: 'website-receptionist-public', displayName: 'MAX' })
    const curatorMax = member({ id: 'website-knowledge-curator', profileId: 'website-knowledge-curator', displayName: 'MAX' })
    const roots = buildReportingHierarchy([publicMax, curatorMax], [])

    expect(roots.map((root) => root.label)).toEqual([
      'MAX · 公开接待',
      'MAX · 知识维护'
    ])
  })
})
