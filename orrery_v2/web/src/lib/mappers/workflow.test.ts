import { describe, expect, it } from 'vitest'
import { mapHILWorkflowInstance } from './workflow'
import type { HILWorkflowInstanceDetailDTO } from '#lib/types/api'

describe('HIL workflow mapper', () => {
  it('maps immutable material, stages, owned tasks, delivery receipts, and audit history', () => {
    const mapped = mapHILWorkflowInstance(workflowDetail(), ['ding'])

    expect(mapped).toMatchObject({
      id: 'workflow-1', status: 'running', revision: 2, version: 3, predecessorInstanceId: 'workflow-0',
      decisionRequestId: 'decision-1', title: 'Reviewed title', requester: 'agent:jordan',
      toolName: 'mcp_marketing_marketing_wechat_draft_create'
    })
    expect(mapped.material).toMatchObject({ content: '# Reviewed article\n', kind: 'wechat_draft' })
    expect(mapped.stages).toEqual(expect.arrayContaining([
      expect.objectContaining({ key: 'review', status: 'active', participantLabels: ['human:ding'] })
    ]))
    expect(mapped.tasks[0]).toMatchObject({ id: 'task-1', assignee: 'human:ding', actionable: true, status: 'pending' })
    expect(mapped.activeTask?.id).toBe('task-1')
    expect(mapped.notifications[0]).toMatchObject({ status: 'delivered', providerReceipt: 'om_message_1' })
    expect(mapped.audit[0]).toMatchObject({ eventType: 'workflow_viewed', actor: 'human:ding' })
    expect(mapped.receipts[0]).toMatchObject({ effect: 'authorize', materialHash: 'sha256:material' })
  })

  it('treats null receipts from older API responses as an empty list', () => {
    const detail = workflowDetail()
    ;(detail as unknown as { receipts: null }).receipts = null

    expect(mapHILWorkflowInstance(detail).receipts).toEqual([])
  })

  it('maps business approval material into readable sections and artifacts', () => {
	const detail = workflowDetail()
	detail.case.action_type_id = 'business_approval'
	detail.case.tool_name = 'request_business_approval'
	detail.decision.action = 'business.approval.request'
	detail.decision.context.evidence_refs = ['knowledge:campaign-brief']
	detail.decision.payload_preview = {
	  approval_request_kind: 'business', approval_tool_name: 'request_business_approval', material_hash: 'sha256:material',
	  business_approval_material: {
		language: 'zh-CN', title: '公众号文章发布审批', decision_request: '是否批准发布本期公众号文章？',
		business_context: '本周产品更新需要对外同步。', proposed_action: '使用主账号发布已完成校对的文章。',
		expected_result: '用户能在公众号看到本期更新。', impact_scope: '公众号主账号及其订阅用户。',
		risks: [{ description: '内容表述可能产生歧义。', mitigation: '发布前由市场负责人完成终审。' }]
	  },
	  evidence_refs: ['artifact:copy-review'],
	  artifacts: [{ label: '公众号终稿', url: 'https://example.test/final', kind: 'document' }]
	}

	const mapped = mapHILWorkflowInstance(detail, ['ding'])

	expect(mapped.title).toBe('公众号文章发布审批')
	expect(mapped.summary).toBe('是否批准发布本期公众号文章？')
	expect(mapped.isBusinessApproval).toBe(true)
	expect(mapped.materialComplete).toBe(true)
	expect(mapped.businessMaterial).toMatchObject({
	  language: 'zh-CN', decisionRequest: '是否批准发布本期公众号文章？', businessContext: '本周产品更新需要对外同步。'
	})
	expect(mapped.businessMaterial?.risks).toEqual([{ description: '内容表述可能产生歧义。', mitigation: '发布前由市场负责人完成终审。' }])
	expect(mapped.evidenceRefs).toEqual(['knowledge:campaign-brief', 'artifact:copy-review'])
	expect(mapped.artifacts).toEqual([{ label: '公众号终稿', url: 'https://example.test/final', kind: 'document' }])
  })

  it('marks legacy pending business workflows without required material as incomplete', () => {
	const detail = workflowDetail()
	detail.case.action_type_id = 'business_approval'
	detail.case.tool_name = 'request_business_approval'
	detail.decision.payload_preview = { approval_request_kind: 'business', material_hash: 'sha256:legacy' }

	const mapped = mapHILWorkflowInstance(detail, ['ding'])

	expect(mapped.isBusinessApproval).toBe(true)
	expect(mapped.materialComplete).toBe(false)
	expect(mapped.businessMaterial).toBeUndefined()
	expect(mapped.activeTask).toBeUndefined()
	expect(mapped.tasks[0].actionable).toBe(false)
  })

  it('uses allowlisted runtime intent as readable approval content', () => {
    const detail = workflowDetail()
    detail.decision.payload_preview = {
      approval_summary: '批准执行客户续约风险评估。',
      business_direction: '基于客户证据形成续约建议。',
      content: { internal_state: 'must-not-render' },
      approval_request_id: 'request_internal_12345678'
    }

    const mapped = mapHILWorkflowInstance(detail, ['ding'])

    expect(mapped.summary).toBe('批准执行客户续约风险评估。')
    expect(mapped.material.content).toBe('批准执行客户续约风险评估。')
    expect(mapped.material.content).not.toContain('request_internal_12345678')
    expect(mapped.material.content).not.toContain('must-not-render')
  })
})

function workflowDetail(): HILWorkflowInstanceDetailDTO {
  return {
    case: {
      id: 'case-1', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'jordan' }, action_type_id: 'tool_use',
      tool_name: 'mcp_marketing_marketing_wechat_draft_create', resource: { kind: 'runtime_approval', id: 'approval-1' }, created_at: '2026-08-17T09:00:00Z'
    },
    instance: {
      id: 'workflow-1', tenant_id: 'tenant-1', case_id: 'case-1', revision: 2, decision_request_id: 'decision-1',
      template_version_id: 'template-version-1', predecessor_instance_id: 'workflow-0', definition_hash: 'sha256:definition',
      material_hash: 'sha256:material', args_hash: 'sha256:args', status: 'running', current_stage_key: 'review', version: 3,
      created_at: '2026-08-17T09:00:00Z', updated_at: '2026-08-17T09:10:00Z', expires_at: '2026-08-17T10:00:00Z'
    },
    decision: {
      id: 'decision-1', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'jordan' }, action: 'runtime.approval',
      resource: { kind: 'runtime_approval', id: 'approval-1' }, args_hash: 'sha256:args', risk_level: 'high',
      route: { mode: 'approval', reasons: ['human review required'] }, context: { bundle_id: 'bundle-1', complete: true },
      payload_preview: {
        material_kind: 'wechat_draft', title: 'Reviewed title', content: '# Reviewed article\n', material_hash: 'sha256:material',
        fields: { account_id: 'wechat-main' }, artifacts: []
      }, status: 'pending', created_at: '2026-08-17T09:00:00Z'
    },
    stages: [{
      id: 'stage-1', tenant_id: 'tenant-1', workflow_instance_id: 'workflow-1',
      definition: { key: 'review', name: 'Marketing review', order: 1, type: 'approval', approval: { strategy: 'single', assignees: [{ kind: 'explicit_human', id: 'ding' }], deadline_minutes: 60 } },
      resolved_participants: [{ kind: 'human', id: 'ding' }], status: 'active', escalation_index: 0,
      created_at: '2026-08-17T09:00:00Z', activated_at: '2026-08-17T09:00:00Z', deadline_at: '2026-08-17T10:00:00Z'
    }],
    tasks: [{
      id: 'task-1', tenant_id: 'tenant-1', workflow_instance_id: 'workflow-1', stage_instance_id: 'stage-1', seat_key: 'review:ding',
      original_assignee: { kind: 'human', id: 'ding' }, current_assignee: { kind: 'human', id: 'ding' }, status: 'pending', transfer_count: 0,
      created_at: '2026-08-17T09:00:00Z', updated_at: '2026-08-17T09:00:00Z', deadline_at: '2026-08-17T10:00:00Z'
    }],
    audit: [{
      id: 'audit-1', tenant_id: 'tenant-1', case_id: 'case-1', workflow_instance_id: 'workflow-1', actor: { kind: 'human', id: 'ding' },
      event_type: 'workflow_viewed', created_at: '2026-08-17T09:05:00Z'
    }],
    notifications: [{
      id: 'notification-1', tenant_id: 'tenant-1', workflow_instance_id: 'workflow-1', channel: 'feishu', recipient: { kind: 'human', id: 'ding' },
      event_type: 'task_assigned', status: 'delivered', attempt_count: 1, provider_receipt: 'om_message_1',
      created_at: '2026-08-17T09:00:00Z', updated_at: '2026-08-17T09:01:00Z', delivered_at: '2026-08-17T09:01:00Z'
    }],
    receipts: [{
      id: 'receipt-1', tenant_id: 'tenant-1', request_id: 'decision-1', workflow_instance_id: 'workflow-1', args_hash: 'sha256:args',
      material_hash: 'sha256:material', effect: 'authorize', single_use: true, created_at: '2026-08-17T09:15:00Z'
    }]
  }
}
