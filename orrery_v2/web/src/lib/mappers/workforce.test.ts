import { describe, expect, it } from 'vitest'
import {
  mapOrgGraphView,
  mapOrganizationCreateCommand,
  mapSquadDirectoryFromOrgGraphView,
  mapSquadDirectoryView,
  mapTalentMarketHireCommand,
  mapWorkforceView
} from './workforce'
import type { OrgGraphDTO, WorkforcePageDTO } from '#lib/types/api'

describe('workforce mappers', () => {
  it('creates organizations without default department or position fields', () => {
    expect(mapOrganizationCreateCommand({
      name: ' 平台中心 ',
      defaultDepartmentName: ' 不应提交 ',
      defaultPositionName: ' 不应提交 '
    })).toEqual({ name: '平台中心' })
  })

  it('maps workforce members without fabricating missing backend profile fields', () => {
    const view = mapWorkforceView({
      members: [
        {
          agent_id: 'profile-a',
          agent_profile_id: 'profile-a',
          agent_instance_id: 'agent-a-runtime',
          real_name: 'Ada Ops',
          display_name: '',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'pos-lead',
          department_display_name: 'Operations',
          position_display_name: 'Lead Operator',
          role_name: 'Incident lead',
          availability_status: 'busy',
          capability_tags: ['incident response', 'approval routing'],
          role_ids: ['operator'],
          functional_role_ids: ['engineering-delivery', 'primary_role'],
          enabled: false
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies WorkforcePageDTO)

    expect(view.summary.disabled).toBe(1)
    expect(view.members[0]).toMatchObject({
      id: 'profile-a',
      profileId: 'profile-a',
      agentInstanceId: 'agent-a-runtime',
      displayName: 'Ada Ops',
      statusLabel: 'Disabled',
      statusTone: 'danger',
      departmentId: 'dept-ops',
      departmentDisplayName: 'Operations',
      positionDisplayName: 'Lead Operator',
      roleName: 'Incident lead',
      roleLabels: ['Operator'],
      primaryRoleLabel: 'Incident lead',
      functionalRoles: ['engineering-delivery', 'primary_role'],
      functionalRoleLabels: ['Engineering Delivery'],
      availabilityStatus: 'busy',
      capabilityTags: ['incident response', 'approval routing'],
      email: 'Not provided'
    })
  })

  it('maps market talent placement and hire command fields', () => {
    const view = mapWorkforceView({
      members: [
        {
          agent_id: 'profile-market',
          agent_profile_id: 'profile-market',
          agent_instance_id: 'agent-market',
          real_name: 'Market Agent',
          display_name: 'Market Agent',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          placement_status: 'market',
          enabled: false,
          released_from_organization_id: 'org-old',
          released_from_department_id: 'dept-old',
          released_from_position_id: 'pos-old',
          released_at: '2026-06-05T08:00:00Z',
          released_reason: 'team changed'
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies WorkforcePageDTO)

    expect(view.members[0]).toMatchObject({
      placementStatus: 'market',
      organizationId: '',
      departmentId: '',
      positionId: '',
      releasedFromOrganizationId: 'org-old',
      releasedFromDepartmentId: 'dept-old',
      releasedFromPositionId: 'pos-old',
      releasedAt: '2026-06-05T08:00:00Z',
      releasedReason: 'team changed'
    })

    expect(
      mapTalentMarketHireCommand({
        agentProfileId: 'profile-market',
        organizationId: ' org-1 ',
        departmentId: ' dept-ops ',
        positionId: ' pos-operator ',
        roles: 'operator, reviewer',
        managerId: ' manager-agent ',
        reportingLineId: ' line-ops '
      })
    ).toEqual({
      agent_profile_id: 'profile-market',
      organization_id: 'org-1',
      department_id: 'dept-ops',
      position_id: 'pos-operator',
      role_ids: ['operator', 'reviewer'],
      manager_agent_id: 'manager-agent',
      reporting_line_id: 'line-ops'
    })
  })

  it('maps role ids into visible people-directory labels without built-in role names', () => {
    const view = mapWorkforceView({
      members: [
        {
          agent_id: 'sre-agent',
          agent_profile_id: 'sre-agent',
          real_name: '顾明澈',
          display_name: '顾明澈',
          agent_kind: 'ai',
          employment_type: 'system',
          organization_id: 'org-agent-coordination',
          department_id: 'dept-agent-coordination',
          position_id: 'position-agent-coordinator',
          department_display_name: 'Agent Coordination',
          position_display_name: 'Coordination Agent',
          role_name: 'Reliability owner',
          role_ids: ['sre'],
          enabled: true
        },
        {
          agent_id: 'flow-agent',
          agent_profile_id: 'flow-agent',
          real_name: '秦若兰',
          display_name: '秦若兰',
          agent_kind: 'ai',
          employment_type: 'system',
          organization_id: 'org-agent-coordination',
          department_id: 'dept-agent-coordination',
          position_id: 'position-agent-coordinator',
          department_display_name: 'Agent Coordination',
          position_display_name: 'Coordination Agent',
          role_name: 'Workflow facilitator',
          role_ids: ['workflow-facilitator'],
          enabled: true
        }
      ],
      page: { total: 2, returned: 2 }
    } satisfies WorkforcePageDTO)

    expect(view.members[0]).toMatchObject({
      displayName: '顾明澈',
      roleLabels: ['SRE'],
      primaryRoleLabel: 'Reliability owner'
    })
    expect(view.members[1]).toMatchObject({
      displayName: '秦若兰',
      roleLabels: ['Workflow Facilitator'],
      primaryRoleLabel: 'Workflow facilitator'
    })
  })

  it('maps workforce profile details for human resumes and agent template locks', () => {
    const view = mapWorkforceView({
      members: [
        {
          agent_id: 'profile-worker',
          agent_profile_id: 'profile-worker',
          agent_instance_id: 'agent-worker',
          real_name: 'Worker Agent',
          display_name: 'Worker',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'pos-worker',
          role_ids: ['worker'],
          enabled: true,
          persona: {
            backstory: 'You implement backend changes with tests.',
            responsibility_boundary: 'Own backend task delivery.',
            behavior_boundary: 'Do not modify unrelated files.'
          },
          workspace_root: '/srv/orrery/workspaces/profile-worker',
          runtime_kind: 'codex',
          runtime_config_ref: 'runtime-codex',
          runtime_config: { model: 'codex-fixture' },
          base_agent: {
            inherits_base_agent: true,
            audit_required: true,
            system_prompt: '你负责执行后端任务并提交测试证据。',
            has_system_prompt: true,
            has_llm_api_key_ref: true,
            llm_api_key_ref: 'secret://system/llm-api-key',
            skill_scope: 'profile-skills',
            tool_scope: 'profile-tools',
            mcp_scope: 'profile-mcp'
          },
          skill_refs: [
            {
              skill_id: 'skill.tdd',
              version_constraint: '^1.0.0',
              alias: 'TDD',
              level: 'senior',
              evidence_refs: ['evidence.test-log']
            }
          ],
          tool_refs: [
            {
              tool_id: 'tool.git',
              version_constraint: '>=2',
              alias: 'Git',
              permission_scope: 'repo:write',
              binding_policy: 'approval_required'
            }
          ],
          personal_memory_ref: {
            memory_profile_id: 'memory-worker',
            owner_agent_id: 'agent-worker',
            memory_kind: 'personal',
            access_scope: 'agent'
          },
          collective_memory_refs: [{ memory_profile_id: 'memory-engineering', memory_kind: 'organization' }],
          qa_enabled: true
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies WorkforcePageDTO)

    expect(view.members[0]).toMatchObject({
      resume: 'You implement backend changes with tests.',
      prompt: '你负责执行后端任务并提交测试证据。',
      responsibilityBoundary: 'Own backend task delivery.',
      behaviorBoundary: 'Do not modify unrelated files.',
      workspaceRoot: '/srv/orrery/workspaces/profile-worker',
      runtimeKind: 'codex',
      runtimeConfigRef: 'runtime-codex',
      runtimeConfig: { model: 'codex-fixture' },
      baseAgent: {
        inheritsBaseAgent: true,
        auditRequired: true,
        systemPrompt: '你负责执行后端任务并提交测试证据。',
        hasSystemPrompt: true,
        hasLlmApiKeyRef: true,
        llmApiKeyRef: 'secret://system/llm-api-key',
        skillScope: 'profile-skills',
        toolScope: 'profile-tools',
        mcpScope: 'profile-mcp'
      },
      skillRefs: [
        {
          skillId: 'skill.tdd',
          versionConstraint: '^1.0.0',
          alias: 'TDD',
          level: 'senior',
          evidenceRefs: ['evidence.test-log']
        }
      ],
      toolRefs: [
        {
          toolId: 'tool.git',
          versionConstraint: '>=2',
          alias: 'Git',
          permissionScope: 'repo:write',
          bindingPolicy: 'approval_required'
        }
      ],
      personalMemoryRef: {
        memoryProfileId: 'memory-worker',
        ownerAgentId: 'agent-worker',
        memoryKind: 'personal',
        accessScope: 'agent'
      },
      collectiveMemoryRefs: [{ memoryProfileId: 'memory-engineering', memoryKind: 'organization' }],
      qaEnabled: true
    })
  })

  it('uses translatable sentinels when workforce member kind metadata is missing', () => {
    const view = mapWorkforceView({
      members: [
        {
          agent_id: 'profile-a',
          agent_profile_id: 'profile-a',
          agent_instance_id: 'agent-a',
          real_name: 'Ada Ops',
          agent_kind: '',
          employment_type: '',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'pos-lead',
          position_display_name: 'Lead Operator',
          role_ids: [],
          enabled: true
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies WorkforcePageDTO)

    expect(view.members[0]).toMatchObject({
      agentKind: 'Not provided',
      employmentType: 'Not provided'
    })
  })

  it('maps org graph nodes, edges, and chart option from backend graph DTO', () => {
    const view = mapOrgGraphView({
      organizations: [{ organization_id: 'org-1', name: 'Orrery' }],
      departments: [{ department_id: 'dept-ops', organization_id: 'org-1', member_count: 2 }],
      positions: [{ position_id: 'pos-lead', department_id: 'dept-ops', member_count: 1 }],
      reporting_edges: [{ from_agent_id: 'profile-a', to_manager_agent_id: 'profile-b', reporting_line_id: 'line-1' }],
      members: [
        {
          agent_id: 'profile-a',
          agent_profile_id: 'profile-a',
          agent_instance_id: 'agent-a',
          real_name: 'Ada Ops',
          display_name: 'Ada',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'pos-lead',
          position_display_name: 'Lead Operator',
          role_ids: [],
          enabled: true
        }
      ]
    } satisfies OrgGraphDTO)

    expect(view.summary.members).toBe(1)
    expect(view.organizations[0]).toMatchObject({
      id: 'org-1',
      name: 'Orrery',
      memberCount: 1,
      units: [
        expect.objectContaining({
          id: 'dept-ops',
          name: 'dept-ops',
          positions: [
            expect.objectContaining({
              id: 'pos-lead',
              name: 'Lead Operator',
              members: [expect.objectContaining({ displayName: 'Ada' })]
            })
          ]
        })
      ]
    })
    expect(view.edges[0]).toEqual({ from: 'profile-a', to: 'profile-b', lineId: 'line-1' })
    expect(view.chartOption.series?.[0]?.data).toEqual(expect.arrayContaining([expect.objectContaining({ name: 'Ada' })]))
  })

  it('prefers coded organization nodes for tree details and permission summaries', () => {
    const view = mapOrgGraphView({
      organizations: [{ organization_id: 'legacy-org', name: 'Legacy Org' }],
      organization_nodes: [
        {
          id: 'org-1',
          node_type: 'company',
          code: '01',
          display_code: '01',
          name: 'Orrery',
          level: 1,
          status: 'active',
          direct_member_count: 2,
          total_member_count: 5,
          child_count: 1,
          permission_summary: {
            effective_role_count: 3,
            direct_role_count: 1,
            inherited_role_count: 2,
            key_permissions: ['org.read', 'agent.run'],
            deny_policy_count: 0,
            approval_required_count: 1
          },
          role_assignments: [
            {
              assignment_id: 'assign-owner',
              actor: { actor_type: 'profile', actor_id: 'profile-owner' },
              role_id: 'org.owner',
              organization_node_id: 'org-1',
              organization_code: '01',
              scope_mode: 'subtree',
              source: 'owner'
            }
          ],
          approval_policy_refs: ['approval.high-risk'],
          children: [
            {
              id: 'dept-ai',
              node_type: 'department',
              code: '0101',
              display_code: '01.01',
              name: 'AI 平台',
              level: 2,
              status: 'active',
              parent_id: 'org-1',
              parent_code: '01',
              parent_display_code: '01',
              parent_name: 'Orrery',
              owner_profile_id: 'profile-ai-owner',
              owner_employee_no: 'E001',
              owner_name: '林知远',
              direct_member_count: 1,
              total_member_count: 3,
              child_count: 0,
              permission_summary: {
                effective_role_count: 1,
                direct_role_count: 1,
                inherited_role_count: 0,
                key_permissions: ['agent.run'],
                deny_policy_count: 1,
                approval_required_count: 0
              }
            }
          ]
        }
      ],
      summary: {
        organization_count: 2,
        root_count: 1,
        active_count: 2,
        inactive_count: 0,
        total_member_count: 5,
        role_assignment_count: 1,
        pending_approval_count: 1
      },
      departments: [],
      positions: [],
      reporting_edges: [],
      members: []
    } satisfies OrgGraphDTO)

    expect(view.summary).toMatchObject({
      organizations: 1,
      organizationNodes: 2,
      rootCount: 1,
      activeOrganizations: 2,
      inactiveOrganizations: 0,
      totalMemberCount: 5,
      roleAssignmentCount: 1,
      pendingApprovalCount: 1
    })
    expect(view.organizations).toHaveLength(1)
    expect(view.organizations[0]).toMatchObject({
      id: 'org-1',
      organizationId: 'org-1',
      nodeType: 'company',
      code: '01',
      displayCode: '01',
      name: 'Orrery',
      level: 1,
      status: 'active',
      ownerEmployeeNo: '',
      ownerName: '',
      directMemberCount: 2,
      totalMemberCount: 5,
      memberCount: 5,
      childCount: 1,
      unitCount: 1,
      permissionSummary: {
        effectiveRoleCount: 3,
        directRoleCount: 1,
        inheritedRoleCount: 2,
        keyPermissions: ['org.read', 'agent.run'],
        denyPolicyCount: 0,
        approvalRequiredCount: 1
      },
      approvalPolicyRefs: ['approval.high-risk']
    })
    expect(view.organizations[0].roleAssignments![0]).toMatchObject({
      assignmentId: 'assign-owner',
      actor: { actorType: 'profile', actorId: 'profile-owner' },
      roleId: 'org.owner',
      organizationCode: '01',
      scopeMode: 'subtree',
      source: 'owner'
    })
    expect(view.organizations[0].children![0]).toMatchObject({
      id: 'dept-ai',
      organizationId: 'org-1',
      nodeType: 'department',
      code: '0101',
      displayCode: '01.01',
      parentId: 'org-1',
      parentCode: '01',
      parentDisplayCode: '01',
      parentName: 'Orrery',
      ownerEmployeeNo: 'E001',
      ownerName: '林知远',
      directMemberCount: 1,
      totalMemberCount: 3
    })
    expect(view.organizations[0].units[0]).toMatchObject({
      id: 'dept-ai',
      name: 'AI 平台',
      organizationId: 'org-1',
      parentId: 'org-1',
      memberCount: 3
    })
  })

  it('builds a nested organization tree from parent department references', () => {
    const view = mapOrgGraphView({
      organizations: [{ organization_id: 'org-1', name: 'Orrery' }],
      departments: [
        { department_id: 'dept-product', organization_id: 'org-1', name: '产品部', member_count: 1 },
        {
          department_id: 'dept-ai',
          organization_id: 'org-1',
          name: 'AI 小组',
          parent_department_id: 'dept-product',
          default_position_id: 'pos-researcher',
          member_count: 1
        }
      ],
      members: [
        {
          agent_id: 'profile-a',
          agent_profile_id: 'profile-a',
          agent_instance_id: 'agent-a',
          real_name: 'Ada',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-ai',
          position_id: 'researcher',
          role_ids: ['research'],
          enabled: true
        }
      ]
    } satisfies OrgGraphDTO)

    expect(view.organizations[0].units[0]).toMatchObject({
      id: 'dept-product',
      name: '产品部',
      memberCount: 1,
      children: [
        expect.objectContaining({
          id: 'dept-ai',
          name: 'AI 小组',
          defaultPositionId: 'pos-researcher',
          memberCount: 1,
          positions: [expect.objectContaining({ id: 'researcher', members: [expect.objectContaining({ realName: 'Ada' })] })]
        })
      ]
    })
  })

  it('keeps company detail members scoped to the selected organization node', () => {
    const view = mapOrgGraphView({
      organization_nodes: [
        {
          id: 'org-1',
          node_type: 'company',
          name: '平台中心',
          code: 'ORG1',
          display_code: 'ORG1',
          level: 1,
          status: 'active',
          total_member_count: 1,
          child_count: 1,
          direct_member_count: 0,
          children: [
            {
              id: 'dept-ai',
              node_type: 'department',
              name: 'AI 小组',
              code: 'ORG1-AI',
              display_code: 'ORG1-AI',
              level: 2,
              status: 'active',
              total_member_count: 1,
              child_count: 0,
              direct_member_count: 1
            }
          ]
        },
        {
          id: 'org-2',
          node_type: 'company',
          name: '市场中心',
          code: 'ORG2',
          display_code: 'ORG2',
          level: 1,
          status: 'active',
          total_member_count: 1,
          child_count: 0,
          direct_member_count: 1
        }
      ],
      positions: [
        { position_id: 'pos-ai', department_id: 'dept-ai', name: '算法工程师', member_count: 1 },
        { position_id: 'pos-market', department_id: 'org-2', name: '市场经理', member_count: 1 }
      ],
      members: [
        {
          agent_id: 'profile-ai',
          agent_profile_id: 'profile-ai',
          real_name: 'Ada',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-ai',
          position_id: 'pos-ai',
          enabled: true
        },
        {
          agent_id: 'profile-market',
          agent_profile_id: 'profile-market',
          real_name: 'Ming',
          agent_kind: 'human',
          employment_type: 'employee',
          organization_id: 'org-2',
          department_id: 'org-2',
          position_id: 'pos-market',
          enabled: true
        }
      ]
    } satisfies OrgGraphDTO)

    expect(view.organizations[0].directMembers?.map((member) => member.profileId)).toEqual([])
    expect(view.organizations[0].children?.[0]?.directMembers?.map((member) => member.profileId)).toEqual(['profile-ai'])
    expect(view.organizations[1].directMembers?.map((member) => member.profileId)).toEqual(['profile-market'])
  })

  it('derives mixed squads from backend org graph departments without demo squad mocks', () => {
    const view = mapSquadDirectoryView({
      departments: [
        { department_id: 'dept-ops', organization_id: 'org-1', member_count: 2 },
        { department_id: 'dept-research', organization_id: 'org-1', member_count: 1 }
      ],
      members: [
        {
          agent_id: 'profile-human-1',
          agent_profile_id: 'profile-human-1',
          agent_instance_id: 'human-1',
          real_name: 'Lin Delivery',
          agent_kind: 'human',
          employment_type: 'employee',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'delivery-lead',
          role_ids: ['delivery'],
          enabled: true
        },
        {
          agent_id: 'profile-agent-1',
          agent_profile_id: 'profile-agent-1',
          agent_instance_id: 'agent-1',
          real_name: 'Research Agent',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'researcher',
          role_ids: ['research'],
          enabled: true
        },
        {
          agent_id: 'profile-agent-2',
          agent_profile_id: 'profile-agent-2',
          agent_instance_id: 'agent-2',
          real_name: 'Disabled Agent',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-research',
          position_id: 'analyst',
          role_ids: [],
          enabled: false
        }
      ]
    } satisfies OrgGraphDTO)

    expect(view.summary).toEqual({ squads: 2, humans: 1, agents: 2, active: 1 })
    expect(view.squads[0]).toMatchObject({
      id: 'dept-ops',
      name: 'dept-ops squad',
      status: 'active',
      humanCount: 1,
      agentCount: 1,
      missionId: 'org-1'
    })
    expect(view.squads[0].members).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: 'profile-human-1', profileId: 'profile-human-1', name: 'Lin Delivery', source: 'human', role: 'delivery-lead' }),
        expect.objectContaining({ id: 'profile-agent-1', profileId: 'profile-agent-1', name: 'Research Agent', source: 'agent', role: 'researcher' })
      ])
    )
  })

  it('derives squads from the org graph view model without rehydrating backend DTOs in pages', () => {
    const orgView = mapOrgGraphView({
      departments: [{ department_id: 'dept-ops', organization_id: 'org-1', member_count: 1 }],
      members: [
        {
          agent_id: 'profile-agent-1',
          agent_profile_id: 'profile-agent-1',
          agent_instance_id: 'agent-1',
          real_name: 'Research Agent',
          display_name: 'Research Agent',
          agent_kind: 'ai',
          employment_type: 'ai_agent',
          organization_id: 'org-1',
          department_id: 'dept-ops',
          position_id: 'researcher',
          role_ids: ['research'],
          enabled: true
        }
      ]
    } satisfies OrgGraphDTO)

    const view = mapSquadDirectoryFromOrgGraphView(orgView)

    expect(view.summary).toEqual({ squads: 1, humans: 0, agents: 1, active: 1 })
    expect(view.squads[0]).toMatchObject({
      id: 'dept-ops',
      missionId: 'org-1',
      members: [expect.objectContaining({ id: 'profile-agent-1', source: 'agent', status: 'active' })]
    })
  })
})
