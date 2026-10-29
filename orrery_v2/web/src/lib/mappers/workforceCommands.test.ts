import { describe, expect, it } from 'vitest'
import {
  mapOrganizationCreateCommand,
  mapOrganizationPositionCreateCommand,
  mapOrganizationUnitCreateCommand,
  mapWorkforceMemberCommand
} from './workforce'

describe('workforce command mappers', () => {
  it('maps workforce member form view model into an API command', () => {
    expect(
      mapWorkforceMemberCommand({
        realName: '',
        displayName: 'Ada Ops',
        agentKind: 'ai',
        employmentType: '',
        organizationId: 'org-1',
        departmentId: 'dept-ops',
        positionId: 'lead',
        roles: 'operator, reviewer',
        functionalRoles: 'engineering-delivery, primary_role, operator',
        workspaceRoot: '/srv/orrery/workspaces/ada-ops',
        runtimeKind: 'openclaw',
        runtimeConfigRef: 'runtime-config-openclaw',
        runtimeConfig: {
          executable: 'openclaw',
          agent_id: 'planner',
          extra_args: ['--channel', 'ops']
        },
        qaEnabled: 'true',
        enabled: 'true'
      })
	    ).toEqual({
	      real_name: 'Ada Ops',
	      display_name: 'Ada Ops',
	      agent_kind: 'ai',
	      employment_type: 'ai_agent',
	      organization_id: 'org-1',
	      department_id: 'dept-ops',
	      position_id: 'lead',
	      role_ids: ['operator', 'reviewer', 'engineering-delivery', 'primary_role'],
	      workspace_root: '/srv/orrery/workspaces/ada-ops',
	      runtime_kind: 'openclaw',
	      runtime_config_ref: 'runtime-config-openclaw',
	      qa_enabled: true,
	      enabled: true
	    })
  })

  it('maps workforce profile detail form fields into an API command', () => {
    expect(
      mapWorkforceMemberCommand({
        displayName: 'Ada',
        mailboxEmail: 'ada.agent@example.test',
        agentKind: 'ai',
        employmentType: '',
        organizationId: 'org-1',
        departmentId: 'dept-ops',
        positionId: 'lead',
        roles: 'operator, reviewer',
        resume: '',
        prompt: 'Act as the planning agent.',
        responsibilityBoundary: 'Plan releases.',
        behaviorBoundary: 'Stay inside approvals.',
        skillRefs: 'skill.plan@^1 as planner [senior]',
        toolRefs: 'tool.git@>=2 as git scope=repo:write policy=approval_required',
        personalMemoryId: 'memory-ada',
        collectiveMemoryRefs: 'memory-org:organization:org',
        qaEnabled: 'true',
        runtimeKind: '',
        runtimeConfigRef: '',
        runtimeConfig: {},
        enabled: 'true'
      })
    ).toMatchObject({
      email: 'ada.agent@example.test',
      system_prompt: 'Act as the planning agent.',
      persona: {
        responsibility_boundary: 'Plan releases.',
        behavior_boundary: 'Stay inside approvals.'
      },
      skill_refs: [{ skill_id: 'skill.plan', version_constraint: '^1', alias: 'planner', level: 'senior' }],
      tool_refs: [{ tool_id: 'tool.git', version_constraint: '>=2', alias: 'git', permission_scope: 'repo:write', binding_policy: 'approval_required' }],
      qa_enabled: true
    })
  })

  it('omits runtime config fields when no provider is selected', () => {
    const command = mapWorkforceMemberCommand({
      realName: '',
      displayName: 'Lin Delivery',
      agentKind: 'human',
      employmentType: '',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'delivery-lead',
      roles: '',
      runtimeKind: '',
      runtimeConfigRef: '',
      runtimeConfig: {},
      enabled: 'true'
    })

    expect(command.runtime_kind).toBeUndefined()
    expect(command.runtime_config_ref).toBeUndefined()
    expect(command.runtime_config).toBeUndefined()
    expect('agent_profile_id' in command).toBe(false)
    expect('agent_id' in command).toBe(false)
    expect('agent_instance_id' in command).toBe(false)
  })

  it('never sends break-glass passwords through workforce commands', () => {
    const command = mapWorkforceMemberCommand({
      agentProfileId: 'human-superadmin',
      displayName: 'superadmin',
      agentKind: 'human',
      accountUsername: 'superadmin',
      accountEmail: 'root@example.test',
      mailboxEmail: 'superadmin.agent@example.test',
      accountEnabled: 'true',
      accountPassword: '123456',
      accountPasswordConfirm: '123456',
      employmentType: '',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'admin',
      roles: 'superadmin, realhumancontributor',
      runtimeKind: '',
      runtimeConfigRef: '',
      runtimeConfig: {},
      enabled: 'true'
    })

    expect(command.user_account).toEqual({
      username: 'superadmin',
      email: 'root@example.test',
      account_enabled: true
    })
    expect(command.email).toBe('superadmin.agent@example.test')
    expect(command.role_ids).toEqual(['superadmin', 'realhumancontributor'])
  })

  it('does not send ordinary People passwords in workforce commands', () => {
    const command = mapWorkforceMemberCommand({
      agentProfileId: 'human-quzheng',
      displayName: '屈正',
      agentKind: 'human',
      accountUsername: 'quzheng',
      accountEnabled: 'true',
      accountPassword: 'must-not-be-sent',
      employmentType: 'human',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'operator',
      roles: 'member',
      runtimeKind: '',
      runtimeConfigRef: '',
      runtimeConfig: {},
      enabled: 'true'
    })

    expect(command.user_account).toEqual({
      username: 'quzheng',
      email: undefined,
      account_enabled: true,
      password: undefined
    })
  })

  it('does not send runtime instance ids in workforce commands', () => {
    const command = mapWorkforceMemberCommand({
      agentProfileId: 'profile-ops',
      agentInstanceId: 'agent-ops-runtime',
      realName: '',
      displayName: 'Ops Agent',
      agentKind: 'ai',
      employmentType: '',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'operator',
      roles: '',
      runtimeKind: '',
      runtimeConfigRef: '',
      runtimeConfig: {},
      enabled: 'true'
    })

    expect(command.agent_profile_id).toBe('profile-ops')
    expect('agent_instance_id' in command).toBe(false)
    expect('agent_id' in command).toBe(false)
  })

  it('uses runtime profiles as the exclusive capability source for agents', () => {
    const command = mapWorkforceMemberCommand({
      realName: '',
      displayName: 'Codex Worker',
      agentKind: 'ai',
      employmentType: '',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'worker',
      roles: '',
      skillRefs: 'skill.plan',
      toolRefs: 'tool.git',
      runtimeKind: 'codex',
      runtimeConfigRef: 'runtime-codex-local',
      runtimeConfig: { executable: 'codex' },
      enabled: 'true'
    })

    expect(command.runtime_kind).toBe('codex')
    expect(command.runtime_config_ref).toBe('runtime-codex-local')
    expect(command.runtime_config).toBeUndefined()
    expect(command.skill_refs).toBeUndefined()
    expect(command.tool_refs).toBeUndefined()
  })

  it('keeps personal assistant skills and tools independently configurable with a runtime profile', () => {
    const command = mapWorkforceMemberCommand({
      agentProfileId: 'personal-assistant-one',
      personalAssistant: true,
      displayName: 'My Assistant',
      agentKind: 'ai',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'assistant',
      roles: '',
      skillRefs: 'skill.plan',
      toolRefs: 'tool.git',
      runtimeKind: 'pi',
      runtimeConfigRef: 'runtime-pi-local',
      runtimeConfig: {},
      enabled: 'true'
    })

    expect(command.runtime_config_ref).toBe('runtime-pi-local')
    expect(command.skill_refs).toEqual([{ skill_id: 'skill.plan' }])
    expect(command.tool_refs).toEqual([{ tool_id: 'tool.git' }])
  })

  it('keeps human workforce commands focused on account and roles only', () => {
    const command = mapWorkforceMemberCommand({
      displayName: 'Operator',
      agentKind: 'human',
      employmentType: '',
      organizationId: 'org-1',
      departmentId: 'dept-ops',
      positionId: 'reviewer',
      roles: 'realhumancontributor',
      resume: 'Reviews release plans.',
      responsibilityBoundary: 'Approve risky changes.',
      skillRefs: 'skill.review',
      toolRefs: 'tool.approval',
      runtimeKind: 'human',
      runtimeConfigRef: 'runtime-human-inbox',
      runtimeConfig: {},
      enabled: 'true'
    })

    expect(command.role_ids).toEqual(['realhumancontributor'])
    expect(command.persona).toEqual({
      backstory: 'Reviews release plans.',
      responsibility_boundary: 'Approve risky changes.',
      behavior_boundary: ''
    })
    expect(command.runtime_kind).toBeUndefined()
    expect(command.runtime_config_ref).toBeUndefined()
    expect(command.skill_refs).toBeUndefined()
    expect(command.tool_refs).toBeUndefined()
  })

  it('maps organization creation names without frontend-generated IDs', () => {
    expect(
      mapOrganizationCreateCommand({
        name: ' Platform ',
        defaultDepartmentName: ' Operations ',
        defaultPositionName: ' Operator '
      })
    ).toEqual({
      name: 'Platform'
    })
  })

  it('maps child unit creation without requiring a default position', () => {
    expect(
      mapOrganizationUnitCreateCommand({
        organizationId: ' org-1 ',
        name: ' Research ',
        parentDepartmentId: ' dept-product '
      })
    ).toEqual({
      organization_id: 'org-1',
      name: 'Research',
      parent_department_id: 'dept-product'
    })
  })

  it('maps organization position creation through the dedicated command', () => {
    expect(
      mapOrganizationPositionCreateCommand({
        organizationId: ' org-1 ',
        departmentId: ' dept-product ',
        name: ' Researcher '
      })
    ).toEqual({
      organization_id: 'org-1',
      department_id: 'dept-product',
      name: 'Researcher'
    })
  })
})
