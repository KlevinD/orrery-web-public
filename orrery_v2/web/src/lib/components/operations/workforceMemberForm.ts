import type { WorkforceMemberFormViewModel, WorkforceMemberViewModel } from '#lib/types/view-models'

export function memberType(member: WorkforceMemberViewModel) {
  return member.agentKind.toLowerCase() === 'human' ? 'human' : 'agent'
}

export function formFromMember(member: WorkforceMemberViewModel): WorkforceMemberFormViewModel {
  return {
    agentProfileId: member.profileId,
    personalAssistant: member.personalAssistant,
    agentInstanceId: cleanOptional(member.agentInstanceId),
    realName: cleanOptional(member.realName),
    displayName: cleanOptional(member.displayName),
    mailboxEmail: cleanOptional(member.email),
    agentKind: cleanOptional(member.agentKind) || (memberType(member) === 'human' ? 'human' : 'ai'),
    accountUsername: cleanOptional(member.userAccount.username),
    accountNickname: '',
    accountEmail: cleanOptional(member.userAccount.email),
    accountRole: '',
    accountEnabled: member.userAccount.accountEnabled ? 'true' : 'false',
    accountProjectionStatus: cleanOptional(member.accountProjection?.status),
    accountProjectionRevision: member.accountProjection?.revision ?? 0,
    accountPermissionScope: '',
    accountPassword: '',
    accountPasswordConfirm: '',
    employmentType: cleanOptional(member.employmentType) || (memberType(member) === 'human' ? 'human' : 'ai_agent'),
    organizationId: member.organizationId,
    departmentId: member.departmentId,
    positionId: member.positionId,
    roles: member.roles.join(', '),
    functionalRoles: (member.functionalRoles ?? []).join(', '),
    resume: cleanOptional(member.resume),
    prompt: cleanOptional(member.prompt),
    responsibilityBoundary: cleanOptional(member.responsibilityBoundary),
    behaviorBoundary: cleanOptional(member.behaviorBoundary),
    skillRefs: serializeSkillRefs(member.skillRefs),
    toolRefs: serializeToolRefs(member.toolRefs),
    personalMemoryId: cleanOptional(member.personalMemoryRef.memoryProfileId),
    collectiveMemoryRefs: serializeMemoryRefs(member.collectiveMemoryRefs),
    qaEnabled: member.qaEnabled ? 'true' : 'false',
    workspaceRoot: cleanOptional(member.workspaceRoot),
    runtimeKind: cleanOptional(member.runtimeKind),
    runtimeConfigRef: cleanOptional(member.runtimeConfigRef),
    runtimeConfig: runtimeConfigFromMember(member.runtimeConfig),
    enabled: member.enabled ? 'true' : 'false'
  }
}

function cleanOptional(value: string | undefined) {
  if (!value || value === 'Not provided' || value === 'Not assigned') return ''
  return value
}

function serializeSkillRefs(refs: WorkforceMemberViewModel['skillRefs']) {
  return refs
    .map((ref) =>
      [
        `${ref.skillId}${ref.versionConstraint ? `@${ref.versionConstraint}` : ''}`,
        ref.alias ? `as ${ref.alias}` : '',
        ref.level ? `[${ref.level}]` : ''
      ]
        .filter(Boolean)
        .join(' ')
    )
    .join(', ')
}

function serializeToolRefs(refs: WorkforceMemberViewModel['toolRefs']) {
  return refs
    .map((ref) =>
      [
        `${ref.toolId}${ref.versionConstraint ? `@${ref.versionConstraint}` : ''}`,
        ref.alias ? `as ${ref.alias}` : '',
        ref.permissionScope ? `scope=${ref.permissionScope}` : '',
        ref.bindingPolicy ? `policy=${ref.bindingPolicy}` : ''
      ]
        .filter(Boolean)
        .join(' ')
    )
    .join(', ')
}

function serializeMemoryRefs(refs: WorkforceMemberViewModel['collectiveMemoryRefs']) {
  return refs
    .map((ref) => [ref.memoryProfileId, ref.memoryKind, ref.accessScope].filter(Boolean).join(':'))
    .filter(Boolean)
    .join(', ')
}

export function runtimeConfigFromMember(config: Record<string, unknown>) {
  const values: Record<string, string | string[]> = {}
  for (const [key, value] of Object.entries(config)) {
    if (typeof value === 'string' || (Array.isArray(value) && value.every((item) => typeof item === 'string'))) {
      values[key] = value
    }
  }
  return values
}
