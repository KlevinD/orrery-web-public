import { describe, expect, it } from 'vitest'
import { formFromMember, memberType, runtimeConfigFromMember } from './workforceMemberForm'
import type { WorkforceMemberViewModel } from '#lib/types/view-models'

function member(overrides: Partial<WorkforceMemberViewModel> = {}): WorkforceMemberViewModel {
  return {
    id: 'member-1',
    profileId: 'agent-1',
    agentInstanceId: 'Not provided',
    realName: 'Not assigned',
    displayName: 'Delivery Agent',
    agentKind: 'ai',
    userAccount: {
      username: '',
      nickname: '',
      email: '',
      role: '',
      accountEnabled: false,
      permissionScope: []
    },
    employmentType: 'ai_agent',
    placementStatus: 'internal',
    organizationId: 'org-1',
    departmentId: 'dept-1',
    positionId: 'pos-1',
    departmentDisplayName: 'Ops',
    positionDisplayName: 'Operator',
    roleName: 'Operator',
    roleLabels: ['Operator'],
    primaryRoleLabel: 'Operator',
    capabilityTags: ['delivery'],
    roles: ['role-operator', 'role-reviewer'],
    functionalRoles: ['engineering-delivery', 'primary_role'],
    functionalRoleLabels: ['Engineering Delivery'],
    managerId: '',
    authorityScope: '',
    supervisionPolicy: '',
    workspaceRoot: '/srv/orrery/workspaces/agent-1',
    runtimeKind: 'codex',
    runtimeConfigRef: 'runtime-codex-local',
    runtimeConfig: { executable: 'codex', args: ['--approval', 'never'], ignored: 1 },
    baseAgent: {
      inheritsBaseAgent: true,
      auditRequired: true,
      hasSystemPrompt: true,
      hasLlmApiKeyRef: false,
      llmApiKeyRef: '',
      skillScope: 'profile_bound',
      toolScope: 'profile_bound',
      mcpScope: 'profile_bound'
    },
    resume: 'Not provided',
    prompt: 'Ship carefully',
    responsibilityBoundary: 'Own delivery',
    behaviorBoundary: 'Ask before risk',
    skillRefs: [
      { skillId: 'planning', versionConstraint: '^1', alias: 'plan', level: 'advanced', evidenceRefs: [] }
    ],
    toolRefs: [
      { toolId: 'shell', versionConstraint: '1.x', alias: 'terminal', permissionScope: 'workspace', bindingPolicy: 'reviewed' }
    ],
    personalMemoryRef: {
      memoryProfileId: 'personal-agent-1',
      ownerAgentId: 'agent-1',
      memoryKind: 'personal',
      accessScope: 'private',
      retrievalPolicy: '',
      compressionPolicy: ''
    },
    collectiveMemoryRefs: [
      {
        memoryProfileId: 'team-memory',
        ownerAgentId: '',
        memoryKind: 'collective',
        accessScope: 'team',
        retrievalPolicy: '',
        compressionPolicy: ''
      }
    ],
    qaEnabled: true,
    enabled: true,
    statusLabel: 'Enabled',
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

describe('workforceMemberForm', () => {
  it('builds edit form values from an agent profile without raw placeholder text', () => {
    expect(formFromMember(member())).toEqual(
      expect.objectContaining({
        agentProfileId: 'agent-1',
        agentInstanceId: '',
        realName: '',
        displayName: 'Delivery Agent',
        agentKind: 'ai',
        employmentType: 'ai_agent',
        roles: 'role-operator, role-reviewer',
        functionalRoles: 'engineering-delivery, primary_role',
        resume: '',
        prompt: 'Ship carefully',
        skillRefs: 'planning@^1 as plan [advanced]',
        toolRefs: 'shell@1.x as terminal scope=workspace policy=reviewed',
        personalMemoryId: 'personal-agent-1',
        collectiveMemoryRefs: 'team-memory:collective:team',
        workspaceRoot: '/srv/orrery/workspaces/agent-1',
        runtimeKind: 'codex',
        runtimeConfigRef: 'runtime-codex-local',
        runtimeConfig: { executable: 'codex', args: ['--approval', 'never'] },
        qaEnabled: 'true',
        enabled: 'true'
      })
    )
  })

  it('classifies human members without special permissions', () => {
    expect(memberType(member({ agentKind: 'human', employmentType: 'human' }))).toBe('human')
    expect(memberType(member({ agentKind: 'ai', employmentType: 'ai_agent' }))).toBe('agent')
  })

  it('keeps only string runtime config values supported by the editor', () => {
    expect(runtimeConfigFromMember({ executable: 'codex', args: ['run'], count: 2, flags: [1] })).toEqual({
      executable: 'codex',
      args: ['run']
    })
  })
})
