import { describe, expect, it } from 'vitest'
import { mapApprovalCenterView, mapApprovalDecisionRequest, validateApprovalDecision } from './approval'
import type { ApprovalPageDTO } from '#lib/types/api'

type LegacyApprovalPageDTO = Omit<ApprovalPageDTO, 'items'> & {
  items?: Array<NonNullable<ApprovalPageDTO['items']>[number] | Record<string, unknown>>
}

const approvals: ApprovalPageDTO = {
  items: [
    {
      approval_id: 'tool_access:mission-1:tool-1',
      approval_type: 'tool_access',
      display_kind: 'command_execution',
      status: 'pending',
      priority: 'high',
      mission_id: 'mission-1',
      source_type: 'tool',
      source_id: 'tool-1',
      provider: 'codex',
      tool_name: 'exec_command',
      title: 'Tool access',
      summary: 'Agent requests deploy tool.',
      risk_summary: 'Runs a command in the workspace.',
      requested_by: 'agent-a',
      assigned_to: 'ops-lead',
      organization_code: '01.01',
      organization_name: 'AI Platform',
      requested_by_actor: { actor_type: 'agent', actor_id: 'agent-a' },
      target_actor: { actor_type: 'human', actor_id: 'human-ops' },
      policy_id: 'risk:high',
      risk_level: 'high',
      decision_effect: 'approve_session',
      payload_preview: {
        kind: 'command_execution',
        language: 'shell',
        content: 'go test ./...',
        fields: { approval_session_id: 'thread-1' }
      },
      expires_at: '2026-06-01T02:00:00Z',
      available_actions: [
        {
          action_id: 'approve_once',
          label: 'Approve once',
          style: 'primary',
          decision: 'approved',
          effect: 'once',
          input_kind: 'message',
          method: 'POST',
          path: '/api/approvals/tool_access:mission-1:tool-1/decision',
          requires_body: true,
          confirmation_required: true,
          reason_required: false
        },
        {
          action_id: 'approve_session',
          label: 'Approve for session',
          style: 'primary',
          decision: 'approved',
          effect: 'session',
          input_kind: 'message',
          method: 'POST',
          path: '/api/approvals/tool_access:mission-1:tool-1/decision',
          requires_body: true,
          confirmation_required: true,
          reason_required: false,
          remember_allowed: true
        },
        {
          action_id: 'deny',
          label: 'Deny',
          style: 'danger',
          decision: 'denied',
          effect: 'deny',
          input_kind: 'message',
          method: 'POST',
          path: '/api/approvals/tool_access:mission-1:tool-1/decision',
          requires_body: true,
          confirmation_required: true,
          reason_required: false
        },
        {
          action_id: 'other',
          label: 'Other',
          style: 'secondary',
          decision: 'approved',
          effect: 'once',
          input_kind: 'message',
          method: 'POST',
          path: '/api/approvals/tool_access:mission-1:tool-1/decision',
          requires_body: true,
          confirmation_required: true,
          reason_required: false
        }
      ],
      evidence_refs: ['ev-1'],
      created_at: '2026-06-01T01:00:00Z'
    }
  ],
  page: { total: 1, returned: 1 }
}

describe('approval mapper', () => {
  it('maps backend-provided actions without inventing buttons', () => {
    const view = mapApprovalCenterView(approvals)

    expect(view.summary.pending).toBe(1)
    expect(view.items[0]).toMatchObject({
      id: 'tool_access:mission-1:tool-1',
      displayKind: 'command_execution',
      typeLabel: 'Command',
      priorityTone: 'danger',
      statusTone: 'warning',
      createdAtRaw: '2026-06-01T01:00:00Z',
      organizationCode: '01.01',
      organizationName: 'AI Platform',
      requestedByActorLabel: 'agent:agent-a',
      targetActorLabel: 'human:human-ops',
      policyId: 'risk:high',
      riskLevel: 'high',
      decisionEffect: 'approve_session',
      expiresAtRaw: '2026-06-01T02:00:00Z',
      provider: 'codex',
      toolName: 'exec_command',
      riskSummary: 'Runs a command in the workspace.'
    })
    expect(view.items[0].payloadPreview).toMatchObject({
      kind: 'command_execution',
      language: 'shell',
      content: 'go test ./...'
    })
    expect(view.items[0].payloadPreview.fields).toEqual([{ key: 'approval_session_id', value: 'thread-1' }])
    expect(view.items[0].actions).toEqual([
      expect.objectContaining({ id: 'approve_once', label: 'Approve once', reasonRequired: false, effect: 'once' }),
      expect.objectContaining({ id: 'approve_session', label: 'Approve for session', reasonRequired: false, rememberAllowed: true, effect: 'session' }),
      expect.objectContaining({ id: 'deny', label: 'Deny', reasonRequired: false, inputKind: 'message' }),
      expect.objectContaining({ id: 'other', label: 'Other', reasonRequired: false, inputKind: 'message' })
    ])
  })

  it('keeps plan approvals on the two actions provided by the backend', () => {
    const view = mapApprovalCenterView({
      items: [
        {
          approval_id: 'todo-plan:plan-1',
          approval_type: 'todo_execution_plan',
          display_kind: 'plan_approval',
          status: 'pending',
          priority: 'normal',
          source_type: 'todo',
          source_id: 'plan-1',
          provider: 'todo_automation',
          title: 'Todo execution plan: Campaign review',
          requested_by: 'todo-automation-agent',
          available_actions: [
            {
              action_id: 'approve',
              label: 'Approve plan',
              method: 'POST',
              path: '/api/approvals/todo-plan:plan-1/decision',
              requires_body: true,
              confirmation_required: false,
              reason_required: false
            },
            {
              action_id: 'reject',
              label: 'Reject',
              style: 'danger',
              method: 'POST',
              path: '/api/approvals/todo-plan:plan-1/decision',
              requires_body: true,
              confirmation_required: true,
              reason_required: false
            }
          ],
          created_at: '2026-08-02T11:08:00Z'
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies ApprovalPageDTO)

    expect(view.items[0].actions.map((action) => action.id)).toEqual(['approve', 'reject'])
  })

  it('uses a translatable sentinel when approval summary is missing', () => {
    const view = mapApprovalCenterView({
      items: [
        {
          approval_id: 'approval-1',
          approval_type: 'milestone_signoff',
          status: 'pending',
          priority: 'normal',
          source_type: 'mission',
          source_id: 'mission-1',
          title: 'Milestone',
          requested_by: 'agent-a',
          available_actions: [],
          created_at: '2026-06-01T01:00:00Z'
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies ApprovalPageDTO)

    expect(view.items[0].summary).toBe('Not provided')
  })

  it('preserves the canonical discussion id for discussion approvals', () => {
    const view = mapApprovalCenterView({
      items: [{
        approval_id: 'approval-discussion', approval_type: 'tool_access', status: 'pending', priority: 'normal',
        source_type: 'discussion', source_id: 'conversation-todo-1', title: 'Todo plan approval', requested_by: 'agent-a',
        available_actions: [], created_at: '2026-06-01T01:00:00Z'
      }],
      page: { total: 1, returned: 1 }
    } satisfies ApprovalPageDTO)

    expect(view.items[0].discussionId).toBe('conversation-todo-1')
  })

  it('keeps discussion business initiator, trigger agent, and decider distinct', () => {
    const view = mapApprovalCenterView({
      items: [{
        approval_id: 'approval-discussion-close',
        approval_type: 'tool_use',
        status: 'rejected',
        priority: 'normal',
        source_type: 'discussion',
        source_id: 'conversation-test',
        tool_name: 'discussion_close',
        title: 'Finish discussion',
        requested_by: 'human-ding',
        triggered_by: 'lead-agent',
        decided_by: 'human-ding',
        requested_by_actor: { actor_type: 'human', actor_id: 'human-ding', display_name: '丁皮' },
        triggered_by_actor: { actor_type: 'agent', actor_id: 'lead-agent', display_name: '顾明澈' },
        decided_by_actor: { actor_type: 'human', actor_id: 'human-ding', display_name: '丁皮' },
        available_actions: [],
        created_at: '2026-08-08T01:00:00Z'
      }],
      page: { total: 1, returned: 1 }
    } satisfies ApprovalPageDTO)

    expect(view.items[0]).toMatchObject({
      requestedBy: 'human-ding',
      requestedByName: '丁皮',
      triggeredBy: 'lead-agent',
      triggeredByName: '顾明澈',
      decidedBy: 'human-ding',
      decidedByName: '丁皮'
    })
  })

  it('does not render inbox notifications as approval center items', () => {
    const view = mapApprovalCenterView({
      items: [
        {
          approval_id: 'inbox:orrery-2aef',
          approval_type: 'inbox_action',
          status: 'pending',
          priority: 'normal',
          mission_id: 'mission-1',
          source_type: 'inbox',
          source_id: 'orrery-2aef',
          title: 'Inbox action',
          summary: 'discussion runtime unavailable',
          requested_by: 'superadmin',
          assigned_to: 'superadmin',
          available_actions: [
            {
              action_id: 'read',
              label: 'Mark read',
              method: 'POST',
              path: '/api/approvals/inbox:orrery-2aef/decision',
              requires_body: true,
              confirmation_required: true,
              reason_required: false
            }
          ],
          created_at: '2026-06-14T01:00:00Z'
        },
        approvals.items![0]
      ],
      page: { total: 2, returned: 2 }
    } as LegacyApprovalPageDTO as ApprovalPageDTO)

    expect(view.items.map((item) => item.id)).toEqual(['tool_access:mission-1:tool-1'])
    expect(view.summary.total).toBe(1)
  })

  it('uses translatable sentinels for missing approval scope metadata', () => {
    const view = mapApprovalCenterView({
      items: [
        {
          approval_id: 'approval-1',
          approval_type: 'milestone_signoff',
          status: 'pending',
          priority: 'normal',
          source_type: 'mission',
          source_id: 'mission-1',
          title: 'Milestone',
          summary: 'Check milestone',
          requested_by: 'agent-a',
          available_actions: [],
          created_at: ''
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies ApprovalPageDTO)

    expect(view.items[0]).toMatchObject({
      missionId: 'Not provided',
      assignedTo: 'Not provided',
      createdAt: 'Not provided'
    })
  })

  it('requires a message when the selected action is other', () => {
    const action = mapApprovalCenterView(approvals).items[0].actions[3]

    expect(validateApprovalDecision(action, '')).toBe('approvals.reasonRequired')
    expect(validateApprovalDecision(action, 'Looks valid.')).toBeUndefined()
  })

  it('normalizes stale discussion approval actions to the discussion decision path', () => {
    const view = mapApprovalCenterView({
      items: [
        {
          approval_id: 'discussion-runtime-approval-conv-1-run-1-cmd-1',
          approval_type: 'tool_use',
          display_kind: 'tool_use',
          status: 'pending',
          priority: 'normal',
          mission_id: 'mission-1',
          source_type: 'discussion',
          source_id: 'conv-1',
          provider: 'claude',
          tool_name: 'WebFetch',
          title: 'Runtime approval: WebFetch',
          summary: 'Claude wants to fetch a URL.',
          requested_by: 'claude',
          available_actions: [
            {
              action_id: 'approve',
              label: 'Approve',
              method: 'POST',
              path: '/api/approvals/stale/decision',
              requires_body: true,
              confirmation_required: true,
              reason_required: false
            }
          ],
          created_at: '2026-06-01T01:00:00Z'
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies ApprovalPageDTO)

    expect(view.items[0].actions.map((action) => action.id)).toEqual(['approve_once', 'approve_session', 'deny', 'other'])
    expect(view.items[0].actions.every((action) => action.path === '/api/discussions/conv-1/approvals/discussion-runtime-approval-conv-1-run-1-cmd-1/decision')).toBe(true)
  })

  it('keeps unavailable discussion approvals read-only even when stale actions are present', () => {
    const view = mapApprovalCenterView({
      items: [
        {
          approval_id: 'discussion-runtime-approval-conv-1-run-1-cmd-1',
          approval_type: 'tool_use',
          display_kind: 'tool_use',
          status: 'unavailable',
          priority: 'normal',
          mission_id: 'mission-1',
          source_type: 'discussion',
          source_id: 'conv-1',
          provider: 'claude',
          tool_name: 'WebSearch',
          title: 'Runtime approval: WebSearch',
          summary: 'The runtime request is no longer active.',
          requested_by: 'claude',
          available_actions: [
            {
              action_id: 'approve',
              label: 'Approve',
              method: 'POST',
              path: '/api/approvals/stale/decision',
              requires_body: true,
              confirmation_required: true,
              reason_required: false
            }
          ],
          created_at: '2026-06-01T01:00:00Z'
        }
      ],
      page: { total: 1, returned: 1 }
    } satisfies ApprovalPageDTO)

    expect(view.summary.pending).toBe(0)
    expect(view.summary.decided).toBe(1)
    expect(view.items[0]).toMatchObject({
      status: 'unavailable',
      statusTone: 'neutral',
      actions: []
    })
  })

  it('maps approval decision forms to backend command bodies outside the page layer', () => {
    const command = mapApprovalDecisionRequest({
      approvalId: 'approval-1',
      actionPath: '/api/discussions/conv-1/approvals/cmd-1/decision',
      actionId: 'approve',
      taskId: 'task-1',
      message: '  Looks valid.  ',
      modifiedInput: '  go test ./...  ',
      choiceId: 'choice-1',
      remember: true
    })

    expect(command).toEqual({
      approvalId: 'approval-1',
      actionPath: '/api/discussions/conv-1/approvals/cmd-1/decision',
      body: {
        action_id: 'approve',
        task_id: 'task-1',
        message: 'Looks valid.',
        modified_input: 'go test ./...',
        choice_id: 'choice-1',
        remember: true
      }
    })
  })
})
