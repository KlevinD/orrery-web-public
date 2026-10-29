import { describe, expect, it } from 'vitest'
import {
  mapAuditLogView,
  mapEvaluationCenterView,
  mapPermissionCenterView,
  mapPermissionPolicyCenterView,
  mapPermissionPolicyCommand,
  mapPermissionPolicyUpdateCommand
} from './operations'
import type { AuditEventPageDTO, EvaluationPageDTO, PermissionMatrixDTO } from '#lib/types/api'

describe('operations mappers', () => {
  it('maps audit events with severity tones and metadata', () => {
    const view = mapAuditLogView({
      events: [
        {
          audit_id: 'audit-1',
          actor_id: 'agent-a',
          mission_id: 'mission-1',
          entity_type: 'approval',
          entity_id: 'approval-1',
          action: 'approval.decide',
          result: 'succeeded',
          severity: 'warning',
          correlation_id: 'corr-1',
          summary: 'approved access',
          metadata: { scope: 'deploy:write' },
          occurred_at: '2026-06-01T02:00:00Z'
        }
      ],
      page: { total: 240, limit: 100, offset: 100, returned: 1, has_more: true }
    } satisfies AuditEventPageDTO)

    expect(view.summary.warning).toBe(1)
    expect(view.summary.total).toBe(240)
    expect(view.page).toEqual({ limit: 100, offset: 100, total: 240, returned: 1, hasMore: true })
    expect(view.events[0]).toMatchObject({
      id: 'audit-1',
      actorId: 'agent-a',
      severityTone: 'warning',
      metadataPairs: [{ key: 'scope', value: 'deploy:write' }]
    })
  })

  it('uses translatable sentinels for missing operation timestamps', () => {
    const audit = mapAuditLogView({
      events: [
        {
          audit_id: 'audit-1',
          actor_id: 'agent-a',
          entity_type: 'approval',
          entity_id: 'approval-1',
          action: 'approval.decide',
          result: 'succeeded',
          severity: 'info',
          correlation_id: 'corr-1',
          metadata: {},
          occurred_at: ''
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies AuditEventPageDTO)

    const evaluation = mapEvaluationCenterView({
      items: [
        {
          entity_type: 'agent',
          entity_id: 'agent-a',
          assigned_tasks: 0,
          completed_tasks: 0,
          quality_score: 0
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies EvaluationPageDTO)

    expect(audit.events[0].occurredAt).toBe('Not provided')
    expect(evaluation.items[0].lastActivityAt).toBe('Not provided')
  })

  it('maps evaluations into percent metrics and radar options', () => {
    const view = mapEvaluationCenterView({
      items: [
        {
          entity_type: 'agent',
          entity_id: 'agent-a',
          display_name: '归档智能体',
          role_name: '档案审核员',
          department: '档案管理部',
          runtime_kind: 'codex',
          assigned_tasks: 10,
          completed_tasks: 8,
          token_consumed: 22000,
          average_duration_seconds: 126,
          quality_score: 0.92,
          on_time_rate: 0.75,
          collaboration_score: 0.64,
          efficiency_score: 0.81,
          stability_score: 0.88,
          summary: '结论：当前智能体适合继续观察。',
          capabilities: [{ name: '材料校验', score: 91 }],
          investment: {
            token_consumed: 22000,
            estimated_human_days: 1.8,
            return_multiple: 2.4,
            summary: '综合评价 Agent 估算约 1.8 人天。'
          },
          latest_review: {
            review_id: 'review-1',
            entity_type: 'agent',
            entity_id: 'agent-a',
            summary: '综合评价 Agent 估算约 1.8 人天。',
            estimated_human_days: 1.8,
            evaluation_token_consumed: 3200,
            created_at: '2026-06-02T02:00:00Z'
          },
          task_history: [
            {
              goal: '完成合同档案合规审核',
              responsibility: '核对合同、验收单与审批材料完整性',
              type: '归档审核',
              result: '通过',
              quality: 5,
              token_consumed: 12000,
              duration_seconds: 126,
              risk: '材料缺章',
              time: '2026-06-01T02:00:00Z'
            }
          ],
          trend: [{ date: '06-01', tasks: 1, score: 91, token_consumed: 12000 }],
          representative_cases: [{ title: '合同档案核查', result: '通过', summary: '完成核查', date: '2026-06-01T02:00:00Z' }],
          suggestions: ['补齐异常材料模板'],
          advantages: ['材料核查稳定'],
          risks: ['复杂场景样本不足'],
          scenarios: ['档案合规审核']
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies EvaluationPageDTO)

    expect(view.items[0].metrics.quality).toBe(92)
    expect(view.items[0]).toMatchObject({
      entityId: 'agent-a',
      roleName: '档案审核员',
      department: '档案管理部',
      runtimeKind: 'codex',
      tokenConsumed: 22000,
      averageDurationSeconds: 126,
      summary: '结论：当前智能体适合继续观察。'
    })
    expect(view.items[0].taskHistory[0]).toMatchObject({
      goal: '完成合同档案合规审核',
      responsibility: '核对合同、验收单与审批材料完整性',
      tokenConsumed: 12000,
      risk: '材料缺章'
    })
    expect(view.items[0].investment).toMatchObject({
      estimatedHumanDays: 1.8,
      evaluationTokenConsumed: 3200,
      summary: '综合评价 Agent 估算约 1.8 人天。'
    })
    expect(view.items[0].latestReview).toMatchObject({
      reviewId: 'review-1',
      estimatedHumanDays: 1.8,
      evaluationTokenConsumed: 3200
    })
    expect(view.radarOption.series?.[0]?.data?.[0]?.value).toEqual([80, 92, 75, 64])
    expect(view.radarOption.radar?.indicator?.[0]?.name).toBe('任务完成')
  })

  it('maps permission matrix effective/source fields', () => {
    const view = mapPermissionCenterView({
      rows: [
        {
          agent_id: 'agent-a',
          resources: [
            {
              tool_id: 'tool-deploy',
              tool_name: 'Deploy Tool',
              declarations: [
                {
                  scope: 'deploy:write',
                  risk: 'high',
                  required: true,
                  effective: true,
                  source: 'approval'
                }
              ]
            }
          ]
        }
      ]
    } satisfies PermissionMatrixDTO)

    expect(view.summary.effective).toBe(1)
    expect(view.rows[0].resources[0].declarations[0]).toMatchObject({
      effectiveLabel: 'Allowed',
      source: 'approval',
      riskTone: 'danger'
    })
  })

  it('maps router permission policy into localized role and principal rows', () => {
    const view = mapPermissionPolicyCenterView({
      capabilities: [
        {
          capability_id: 'get.api.permissions.policy.permissions.read',
          domain: 'permissions',
          action: 'read',
          access_mode: 'read',
          scope: 'permissions.read',
          method: 'GET',
          path: '/api/permissions/policy',
          label_zh: '权限策略查看',
          label_en: 'Read permission policy',
          category_zh: '权限',
          category_en: 'Permissions'
        },
        {
          capability_id: 'put.api.permissions.policy.permissions.manage',
          domain: 'permissions',
          action: 'manage',
          access_mode: 'write',
          scope: 'permissions.manage',
          method: 'PUT',
          path: '/api/permissions/policy',
          label_zh: '权限策略编辑',
          label_en: 'Edit permission policy',
          category_zh: '权限',
          category_en: 'Permissions'
        }
      ],
      policy: {
        roles: [{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }],
        role_assignments: [{ principal_kind: 'user', principal_id: 'operator', role_id: 'ops-admin' }],
        grants: [
          {
            subject_kind: 'role',
            subject_id: 'ops-admin',
            capability_id: 'put.api.permissions.policy.permissions.manage'
          },
          {
            subject_kind: 'user',
            subject_id: 'operator',
            capability_id: 'get.api.permissions.policy.permissions.read'
          }
        ]
      },
      effective: [
        {
          principal_kind: 'user',
          principal_id: 'operator',
          capability_id: 'put.api.permissions.policy.permissions.manage',
          scope: 'permissions.manage',
          source: 'role',
          source_id: 'ops-admin'
        }
      ]
    })

    expect(view.summary).toMatchObject({
      capabilities: 2,
      readCapabilities: 1,
      writeCapabilities: 1,
      roles: 1
    })
    expect(view.capabilities[1]).toMatchObject({
      displayName: '权限策略编辑 / Edit permission policy',
      accessLabelZh: '编辑',
      accessLabelEn: 'Edit',
      categoryLabel: '权限 / Permissions'
    })
    expect(view.rolePermissionRows[1].roles[0]).toMatchObject({
      roleId: 'ops-admin',
      granted: true
    })
    expect(view.effective[0]).toMatchObject({
      principalDisplayName: 'operator',
      capabilityDisplayName: '权限策略编辑 / Edit permission policy',
      sourceLabel: '角色：运营管理员 / Operations Admin'
    })
  })

  it('builds a policy command without leaking view-only matrix fields', () => {
    const view = mapPermissionPolicyCenterView({
      capabilities: [],
      policy: {
        roles: [{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }],
        role_assignments: [{ principal_kind: 'user', principal_id: 'operator', role_id: 'ops-admin' }],
        grants: [{ subject_kind: 'role', subject_id: 'ops-admin', scope: 'operations.manage' }]
      },
      effective: []
    })

    expect(mapPermissionPolicyCommand(view)).toEqual({
      roles: [{ role_id: 'ops-admin', label_zh: '运营管理员', label_en: 'Operations Admin' }],
      role_assignments: [{ principal_kind: 'user', principal_id: 'operator', role_id: 'ops-admin' }],
      grants: [{ subject_kind: 'role', subject_id: 'ops-admin', scope: 'operations.manage' }]
    })
  })

  it('keeps built-in templates in editable drafts but out of update commands', () => {
    const view = mapPermissionPolicyCenterView({
      capabilities: [],
      policy: {
        roles: [
          { role_id: 'platform.admin', label_zh: '平台管理员', label_en: 'Platform Administrator', built_in: true },
          { role_id: 'custom.viewer', label_zh: '查看员', label_en: 'Viewer' }
        ],
        role_assignments: [
          { principal_kind: 'user', principal_id: 'admin', role_id: 'platform.admin' },
          { principal_kind: 'user', principal_id: 'viewer', role_id: 'custom.viewer' }
        ],
        grants: [
          { subject_kind: 'role', subject_id: 'platform.admin', scope: 'operations.manage' },
          { subject_kind: 'role', subject_id: 'custom.viewer', scope: 'resource.read' }
        ]
      },
      effective: []
    })

    expect(view.roles.find((role) => role.id === 'platform.admin')?.builtIn).toBe(true)
    expect(mapPermissionPolicyCommand(view)).toEqual({
      roles: [
        { role_id: 'custom.viewer', label_zh: '查看员', label_en: 'Viewer' },
        { role_id: 'platform.admin', label_zh: '平台管理员', label_en: 'Platform Administrator', built_in: true }
      ],
      role_assignments: [
        { principal_kind: 'user', principal_id: 'admin', role_id: 'platform.admin' },
        { principal_kind: 'user', principal_id: 'viewer', role_id: 'custom.viewer' }
      ],
      grants: [
        { subject_kind: 'role', subject_id: 'custom.viewer', scope: 'resource.read' },
        { subject_kind: 'role', subject_id: 'platform.admin', scope: 'operations.manage' }
      ]
    })
    expect(mapPermissionPolicyUpdateCommand(view)).toEqual({
      roles: [{ role_id: 'custom.viewer', label_zh: '查看员', label_en: 'Viewer' }],
      role_assignments: [{ principal_kind: 'user', principal_id: 'viewer', role_id: 'custom.viewer' }],
      grants: [{ subject_kind: 'role', subject_id: 'custom.viewer', scope: 'resource.read' }]
    })
  })

  it('preserves scope grants for policy editing while expanding effective grants', () => {
    const view = mapPermissionPolicyCenterView({
      capabilities: [
        {
          capability_id: 'post.api.workforce.members.workforce.manage',
          domain: 'workforce',
          action: 'manage',
          access_mode: 'write',
          scope: 'workforce.manage',
          method: 'POST',
          path: '/api/workforce/members',
          label_zh: 'Workforce edit',
          label_en: 'Create workforce member',
          category_zh: 'Workforce',
          category_en: 'Workforce'
        },
        {
          capability_id: 'post.api.talent-market.members.delete-batch.workforce.manage',
          domain: 'workforce',
          action: 'manage',
          access_mode: 'write',
          scope: 'workforce.manage',
          method: 'POST',
          path: '/api/talent-market/members/delete-batch',
          label_zh: 'Workforce edit',
          label_en: 'Batch delete talent market members',
          category_zh: 'Workforce',
          category_en: 'Workforce'
        }
      ],
      policy: {
        roles: [{ role_id: 'ops-admin', label_zh: 'Operations Admin', label_en: 'Operations Admin' }],
        role_assignments: [{ principal_kind: 'user', principal_id: 'operator', role_id: 'ops-admin' }],
        grants: [{ subject_kind: 'role', subject_id: 'ops-admin', scope: 'workforce.manage' }]
      },
      effective: []
    })

    expect(mapPermissionPolicyCommand(view).grants).toEqual([
      { subject_kind: 'role', subject_id: 'ops-admin', scope: 'workforce.manage' }
    ])
    expect(view.effective.map((grant) => grant.capabilityId)).toEqual([
      'post.api.talent-market.members.delete-batch.workforce.manage',
      'post.api.workforce.members.workforce.manage'
    ])
  })
})
