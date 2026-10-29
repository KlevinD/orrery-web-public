import { describe, expect, it } from 'vitest'
import { collapseRepeatedPendingRuntimeApprovals, hilDecisionMissionId, mapHILCouncil, mapHILDecisionPage, mapHILDecisionRequest, mapHILDecisionToApprovalItem } from './hil'
import type { HILCouncilSessionDTO, HILDecisionPageDTO } from '#lib/types/api'

describe('HIL mappers', () => {
  it('maps decision routing and ontology evidence', () => {
    const page = mapHILDecisionPage({
      items: [{
        id: 'decision-1', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'jordan' }, action: 'customer.update',
        resource: { kind: 'customer', id: 'customer-1' }, args_hash: 'sha256:args', risk_level: 'medium',
        route: { mode: 'council', council_effect: 'authorize', reasons: ['multi-party context requires council'] },
        context: { bundle_id: 'bundle-1', complete: true, confidence: 0.87, evidence_refs: ['evidence-1'] },
        status: 'pending', council_id: 'council-1', purpose: 'correct record', created_at: '2026-08-09T09:00:00Z'
      }],
      total: 1
    } satisfies HILDecisionPageDTO)

    expect(page).toHaveLength(1)
    expect(page[0]).toMatchObject({
      id: 'decision-1', councilId: 'council-1', resource: 'customer:customer-1', requester: 'agent:jordan',
      effect: 'authorize', evidenceRefs: ['evidence-1'], contextBundleId: 'bundle-1', contextComplete: true, contextConfidence: 0.87
    })
  })

  it('adapts HIL and fixed approval modes to actionable queue items', () => {
    const item = mapHILDecisionPage({
      items: [{
        id: 'decision-2', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'runner' }, action: 'files.write',
        resource: { kind: 'file', id: 'README.md' }, args_hash: 'sha256:args', risk_level: 'high',
        route: { mode: 'hil', reasons: ['human review required'] },
        context: { bundle_id: 'bundle-2', complete: false, evidence_refs: [] }, status: 'pending',
        purpose: 'Update release notes', created_at: '2026-08-09T09:00:00Z', expires_at: '2026-08-09T10:00:00Z'
      }], total: 1
    } satisfies HILDecisionPageDTO)[0]

    const mapped = mapHILDecisionToApprovalItem(item, 'hil')
    expect(mapped).toMatchObject({
      id: 'decision-2', type: 'hil', status: 'pending', typeLabel: 'HIL', expiresAtRaw: '2026-08-09T10:00:00Z'
    })
    expect(mapped.assignedTo).toBe('Not provided')
    expect(mapped.actions.map((action) => action.id)).toEqual(['approve', 'reject'])
    expect(mapped.actions.every((action) => action.path === '/api/hil/decisions/decision-2/decision')).toBe(true)
  })

  it('preserves Mission associations from HIL resources and payloads', () => {
    const items = mapHILDecisionPage({
      items: [{
        id: 'decision-resource', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'runner' }, action: 'mission.review',
        resource: { kind: 'mission', id: 'mission-resource' }, args_hash: 'sha256:resource', risk_level: 'high',
        route: { mode: 'council', reasons: [] }, context: { bundle_id: 'bundle-resource', complete: true },
        status: 'pending', created_at: '2026-08-09T09:00:00Z'
      }, {
        id: 'decision-payload', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'runner' }, action: 'runtime.approval',
        resource: { kind: 'runtime_approval', id: 'approval-1' }, args_hash: 'sha256:payload', risk_level: 'medium',
        route: { mode: 'hil', reasons: [] }, context: { bundle_id: 'bundle-payload', complete: true },
        payload_preview: { approval_mission_id: 'mission-payload', title: 'Review', summary: 'Review action' },
        status: 'pending', created_at: '2026-08-09T09:00:00Z'
      }], total: 2
    } satisfies HILDecisionPageDTO)

    expect(hilDecisionMissionId(items[0])).toBe('mission-resource')
    expect(mapHILDecisionToApprovalItem(items[1], 'hil').missionId).toBe('mission-payload')
  })

  it('keeps HIL protocol fields out of human-readable approval content', () => {
    const mapped = mapHILDecisionToApprovalItem(mapHILDecisionPage({
      items: [{
        id: 'orrery-readable-hil', tenant_id: 'tenant-1', requester: { kind: 'human', id: 'H000001' },
        action: 'smoke.hil.review', resource: { kind: 'smoke_task', id: 'smoke-hil-quzheng' },
        args_hash: 'sha256:args', risk_level: 'medium',
        route: { mode: 'hil', reasons: ['runtime human review is required'] },
        context: { bundle_id: 'bundle:minvia:internal', complete: false },
        payload_preview: {
          approval_assigned_to: 'H000002', approval_reviewer_name: '屈正', smoke_test: true,
          source: 'codex_smoke', title: 'HIL 点击验收任务',
          summary: '请屈正确认 HIL 审批按钮可正常打开和提交。此任务无外部副作用。'
        },
        purpose: '验证屈正账号的 HIL 审批入口与按钮交互；不执行任何外部操作',
        status: 'pending', created_at: '2026-08-28T12:55:02Z'
      }],
      total: 1
    } satisfies HILDecisionPageDTO)[0], 'hil')

    expect(mapped).toMatchObject({
      title: 'HIL 点击验收任务',
      summary: '请屈正确认 HIL 审批按钮可正常打开和提交。此任务无外部副作用。',
      details: '',
      riskSummary: '',
      assignedTo: 'H000002',
      assignedToName: '屈正'
    })
    expect(mapped.requestedByName).toBeUndefined()
    expect(mapped.payloadPreview.fields.map((field) => field.key)).not.toEqual(expect.arrayContaining([
      'action', 'resource', 'context_bundle_id', 'council_id'
    ]))
  })

  it('shows only the newest repeated pending approval from one responsibility conversation', () => {
    const base = {
      tenant_id: 'tenant-1', requester: { kind: 'agent' as const, id: 'runner' }, action: 'runtime.approval',
      resource: { kind: 'runtime_approval', id: 'approval' }, args_hash: 'sha256:args', risk_level: 'medium' as const,
      route: { mode: 'hil' as const, reasons: ['human review required'] },
      context: { bundle_id: 'bundle-1', complete: true }, purpose: 'Review generated material', status: 'pending' as const
    }
    const items = mapHILDecisionPage({
      items: [{
        ...base, id: 'newest', created_at: '2026-08-20T17:49:02Z',
        payload_preview: {
          approval_conversation_id: 'conversation-1', approval_run_id: 'responsibility-run-new',
          approval_tool_name: 'publish', approval_assigned_to: 'ding', title: 'Enterprise HR/Finance',
          summary: 'Confirm the "11 node" topology.', material_hash: 'sha256:same',
          fields: { account_id: 'wechat-main', intent_id: 'intent-1' }
        }
      }, {
        ...base, id: 'older-repeat', created_at: '2026-08-20T16:16:11Z',
        payload_preview: {
          approval_conversation_id: 'conversation-1', approval_run_id: 'responsibility-run-old',
          approval_tool_name: 'publish', approval_assigned_to: 'ding', title: 'Enterprise HR/Finance',
          summary: 'Confirm the \u201c11 node\u201d topology.', material_hash: 'sha256:same',
          fields: { account_id: 'wechat-main', intent_id: 'intent-1' }
        }
      }, {
        ...base, id: 'direct-run', created_at: '2026-08-20T15:00:00Z',
        payload_preview: {
          approval_conversation_id: 'conversation-1', approval_run_id: 'direct-run-1',
          approval_tool_name: 'publish', approval_assigned_to: 'ding', title: 'Enterprise HR/Finance',
          summary: 'Confirm the "11 node" topology.'
        }
      }, {
        ...base, id: 'changed-material', created_at: '2026-08-20T14:00:00Z',
        payload_preview: {
          approval_conversation_id: 'conversation-1', approval_run_id: 'responsibility-run-changed',
          approval_tool_name: 'publish', approval_assigned_to: 'ding', title: 'Enterprise HR/Finance',
          summary: 'Confirm the "11 node" topology.', material_hash: 'sha256:changed',
          fields: { account_id: 'wechat-main', intent_id: 'intent-1' }
        }
      }],
      total: 4
    } satisfies HILDecisionPageDTO)

    expect(collapseRepeatedPendingRuntimeApprovals(items).map((item) => item.id)).toEqual(['newest', 'direct-run', 'changed-material'])
  })

  it('uses enriched request content and blocks blind approval when readable material is absent', () => {
    const enriched = mapHILDecisionPage({
      items: [{
        id: 'decision-enriched', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'runner' }, action: 'runtime.approval',
        resource: { kind: 'runtime_approval', id: 'approval-1' }, args_hash: 'sha256:args', risk_level: 'medium',
        route: { mode: 'hil', reasons: ['human review required'] }, context: { bundle_id: 'bundle-1', complete: true },
        payload_preview: {
          title: '发布任务复核', content: '内容发布复核', request_context: '查找适合复核本期发布内容的人才。',
          approval_request_id: 'call-secret', material_hash: 'sha256:material'
        },
        purpose: 'Runtime approval required for discussion_talent_list', status: 'pending', created_at: '2026-08-20T09:00:00Z'
      }, {
        id: 'decision-empty', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'runner' }, action: 'runtime.approval',
        resource: { kind: 'runtime_approval', id: 'approval-2' }, args_hash: 'sha256:args-2', risk_level: 'medium',
        route: { mode: 'hil', reasons: ['human review required'] }, context: { bundle_id: 'bundle-2', complete: true },
        purpose: 'Runtime approval required for unknown_tool', status: 'pending', created_at: '2026-08-20T09:00:00Z'
      }], total: 2
    } satisfies HILDecisionPageDTO)

    const visible = mapHILDecisionToApprovalItem(enriched[0], 'hil')
    expect(visible).toMatchObject({ title: '发布任务复核', summary: '内容发布复核' })
    expect(visible.payloadPreview.content).toBe('内容发布复核')
    expect(visible.actions.map((action) => action.id)).toEqual(['approve', 'reject'])

    const empty = mapHILDecisionToApprovalItem(enriched[1], 'hil')
    expect(empty.title).toBe('Runtime approval required for unknown_tool')
    expect(empty.summary).toBe('')
    expect(empty.actions).toEqual([])
  })

  it('keeps autonomous-plan payload preview fields and its assigned approver', () => {
    const payloadPreview = {
      summary: 'Prepare the customer renewal risk assessment.',
      steps: [{ step_id: 'step-1', title: 'Review renewal evidence', risk_level: 'high' }],
      source_refs: ['people:responsibility-binding:binding-1:v2'],
      approval_assigned_to: 'human-owner',
      proposal_digest: 'sha256:proposal'
    }
    const item = mapHILDecisionPage({
      items: [{
        id: 'decision-autoplan', tenant_id: 'tenant-1', requester: { kind: 'service', id: 'responsibility-autoplan' },
        action: 'responsibility.plan.execute', resource: { kind: 'responsibility_autoplan', id: 'autoplan-1' },
        args_hash: 'sha256:proposal', risk_level: 'high', route: { mode: 'approval', reasons: ['scoped execution requires approval'] },
        context: { bundle_id: 'bundle-autoplan', complete: true }, payload_preview: payloadPreview,
        status: 'pending', purpose: 'Approve plan execution', created_at: '2026-08-14T09:00:00Z'
      }], total: 1
    } satisfies HILDecisionPageDTO)[0]

    const mapped = mapHILDecisionToApprovalItem(item, 'approval')

    expect(item.payloadPreview).toEqual(payloadPreview)
    expect(mapped).toMatchObject({
      type: 'approval', summary: payloadPreview.summary, assignedTo: 'human-owner'
    })
    expect(mapped.payloadPreview.content).toBe('1. Review renewal evidence')
    expect(mapped.payloadPreview.fields).toEqual(expect.arrayContaining([
      { key: 'summary', value: payloadPreview.summary },
      { key: 'steps', value: JSON.stringify(payloadPreview.steps) },
      { key: 'source_refs', value: JSON.stringify(payloadPreview.source_refs) },
      { key: 'approval_assigned_to', value: 'human-owner' },
      { key: 'proposal_digest', value: 'sha256:proposal' }
    ]))
  })

  it('maps immutable approval material into primary content and stable fields', () => {
	const item = mapHILDecisionPage({
	  items: [{
		id: 'decision-material', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'jordan' }, action: 'runtime.approval',
		resource: { kind: 'runtime_approval', id: 'approval-1' }, args_hash: 'sha256:args', risk_level: 'high',
		route: { mode: 'approval', reasons: ['human review required'] }, context: { bundle_id: 'bundle-material', complete: true },
		payload_preview: {
		  material_kind: 'wechat_draft', title: 'Reviewed title', summary: 'Reviewed digest', content: '# Reviewed article\n',
		  material_hash: 'sha256:material', content_hash: 'sha256:content',
		  fields: { account_id: 'wechat-main', intent_id: 'intent-1' },
		  artifacts: [{ role: 'cover', path: 'cover.png', size_bytes: 1024, media_type: 'image/png', sha256: 'sha256:cover' }]
		},
		status: 'pending', created_at: '2026-08-17T09:00:00Z'
	  }], total: 1
	} satisfies HILDecisionPageDTO)[0]

	const mapped = mapHILDecisionToApprovalItem(item, 'approval')

	expect(mapped.payloadPreview).toMatchObject({
	  kind: 'wechat_draft', language: 'markdown', title: 'Reviewed title', content: '# Reviewed article\n'
	})
	expect(mapped.payloadPreview.fields).toEqual(expect.arrayContaining([
	  { key: 'account_id', value: 'wechat-main' },
	  { key: 'material_hash', value: 'sha256:material' },
	  { key: 'artifact.cover', value: 'cover.png | image/png | 1024 bytes | sha256:cover' }
	]))
  })

  it('maps HIL approval commands outside the page layer', () => {
    expect(mapHILDecisionRequest({
      approvalId: 'decision-3', actionId: 'reject', actionPath: '/api/hil/decisions/decision-3/decision',
      taskId: '', message: ' Need a stronger evidence trail. '
    })).toEqual({
      decisionId: 'decision-3',
      body: { decision: 'rejected', reason: 'Need a stronger evidence trail.', action_id: 'reject' }
    })
  })

  it('maps quorum, contributions, votes, and dissent', () => {
    const council = mapHILCouncil({
      id: 'council-1', tenant_id: 'tenant-1', request_id: 'decision-1', args_hash: 'sha256:args', risk_level: 'high',
      status: 'passed', effect: 'requires_approval', quorum: 2, approval_threshold: 2,
      participants: [
        { actor: { kind: 'human', id: 'ding' }, display_name: 'Ding', role: 'owner', required: true },
        { actor: { kind: 'agent', id: 'jordan' }, display_name: 'Jordan', required: false }
      ],
      contributions: [
        { actor: { kind: 'human', id: 'ding' }, position: 'approve', reasoning: 'Evidence supports it.', created_at: '2026-08-09T09:10:00Z', updated_at: '2026-08-09T09:10:00Z' }
      ],
      votes: [
        { actor: { kind: 'human', id: 'ding' }, choice: 'approve', created_at: '2026-08-09T09:11:00Z' },
        { actor: { kind: 'agent', id: 'jordan' }, choice: 'reject', created_at: '2026-08-09T09:11:00Z' }
      ],
      dissent: [{ kind: 'agent', id: 'jordan' }], created_at: '2026-08-09T09:00:00Z', updated_at: '2026-08-09T09:11:00Z',
      expires_at: '2026-08-09T10:00:00Z', resolved_at: '2026-08-09T09:11:00Z'
    } satisfies HILCouncilSessionDTO)

    expect(council).toMatchObject({ approvals: 1, rejections: 1, abstentions: 0, effect: 'requires_approval', dissent: ['agent:jordan'] })
    expect(council.participants[0]).toMatchObject({ actorKey: 'human:ding', contributed: true, vote: 'approve' })
  })
})
