import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ApprovalDecisionDialog from './ApprovalDecisionDialog.svelte'
import { setLocale } from '#lib/i18n'
import { mapHILDecisionPage, mapHILDecisionToApprovalItem } from '#lib/mappers/hil'
import type { HILDecisionPageDTO } from '#lib/types/api'
import type { ApprovalActionViewModel, ApprovalItemViewModel } from '#lib/types/view-models'

describe('ApprovalDecisionDialog', () => {
  beforeEach(() => {
    setLocale('zh-CN')
  })

  it('translates the selected approval action in the dialog chrome', () => {
    render(ApprovalDecisionDialog, {
      props: {
        open: true,
        item: approvalItem(),
        action: approvalAction()
      }
    })

    expect(screen.getByRole('dialog', { name: '本会话批准' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '本会话批准' })).toBeTruthy()
    expect(screen.queryByText('Approve for session')).toBeNull()
    expect(screen.getAllByText('Claude wants to run npm test.')).toHaveLength(1)
  })

  it('renders immutable approval material as the primary review content', () => {
	const mapped = mapHILDecisionToApprovalItem(mapHILDecisionPage({
	  items: [{
		id: 'decision-material', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'jordan' }, action: 'runtime.approval',
		resource: { kind: 'runtime_approval', id: 'approval-1' }, args_hash: 'sha256:args', risk_level: 'high',
		route: { mode: 'approval', reasons: ['human review required'] }, context: { bundle_id: 'bundle-material', complete: true },
		payload_preview: {
		  material_kind: 'wechat_draft', title: 'Reviewed title', content: '# Reviewed article\n', material_hash: 'sha256:material',
		  fields: { account_id: 'wechat-main' }, artifacts: []
		}, status: 'pending', created_at: '2026-08-17T09:00:00Z'
	  }], total: 1
	} satisfies HILDecisionPageDTO)[0], 'approval')

	render(ApprovalDecisionDialog, { props: { open: true, item: mapped, action: mapped.actions[0] } })

	expect(screen.getByRole('heading', { name: 'Reviewed article' })).toBeTruthy()
	expect(screen.getByText('固定审批')).toBeTruthy()
	expect(screen.getByText('高风险')).toBeTruthy()
	expect(screen.queryByText('decision-material')).toBeNull()
	expect(screen.queryByText('runtime.approval')).toBeNull()
	expect(screen.queryByText('account_id')).toBeNull()
	expect(screen.queryByText('wechat-main')).toBeNull()
	expect(screen.queryByText('material_hash')).toBeNull()
	expect(screen.queryByText('sha256:material')).toBeNull()
  })

  it('renders the WeChat draft action in user language', () => {
    const item = approvalItem()
    item.title = 'communication.wechat_draft.create'
    item.type = 'approval'
    item.typeLabel = 'Approval'
    item.riskLevel = 'high'

    render(ApprovalDecisionDialog, { props: { open: true, item, action: approvalAction() } })

    expect(screen.getByText('创建微信公众号草稿')).toBeTruthy()
    expect(screen.getByText('固定审批')).toBeTruthy()
    expect(screen.getByText('高风险')).toBeTruthy()
    expect(screen.queryByText('communication.wechat_draft.create')).toBeNull()
  })

  it('shows only human-readable HIL content in the decision dialog', () => {
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

    render(ApprovalDecisionDialog, { props: { open: true, item: mapped, action: mapped.actions[0] } })

    expect(screen.getAllByText('HIL 点击验收任务').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('请屈正确认 HIL 审批按钮可正常打开和提交。此任务无外部副作用。').length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText('orrery-readable-hil')).toBeNull()
    expect(screen.queryByText('runtime human review is required')).toBeNull()
    expect(screen.queryByText('smoke.hil.review')).toBeNull()
    expect(screen.queryByText('smoke_task:smoke-hil-quzheng')).toBeNull()
    expect(screen.queryByText('bundle:minvia:internal')).toBeNull()
    expect(screen.queryByText('approval_assigned_to')).toBeNull()
    expect(screen.queryByText('H000002')).toBeNull()
  })

  it('shows submission progress and cannot close while the decision is pending', async () => {
    const onclose = vi.fn()
    render(ApprovalDecisionDialog, {
      props: {
        open: true,
        busy: true,
        item: approvalItem(),
        action: approvalAction(),
        onclose
      }
    })

    const submit = screen.getByRole('button', { name: '正在提交' })
    expect(submit.hasAttribute('disabled')).toBe(true)
    const cancel = screen.getByRole('button', { name: '取消' })
    expect(cancel.hasAttribute('disabled')).toBe(true)
    await fireEvent.click(cancel)
    expect(onclose).not.toHaveBeenCalled()
  })
})

function approvalAction(): ApprovalActionViewModel {
  return {
    id: 'approve_session',
    label: 'Approve for session',
    style: 'primary',
    decision: 'approved',
    effect: 'session',
    inputKind: 'message',
    defaultMessage: '',
    method: 'POST',
    path: '/api/approvals/approval-1/decision',
    reasonRequired: false,
    confirmationRequired: false,
    allowsModification: false,
    rememberAllowed: true
  }
}

function approvalItem(): ApprovalItemViewModel {
  return {
    id: 'approval-1',
    type: 'command_execution',
    displayKind: 'command_execution',
    displayTone: 'danger',
    typeLabel: 'Command',
    status: 'pending',
    statusTone: 'warning',
    priority: 'high',
    priorityTone: 'danger',
    missionId: 'discussion-1',
    taskId: 'run-1',
    sourceLabel: 'discussion:discussion-1',
    provider: 'claude',
    toolName: 'exec_command',
    title: 'Command approval: exec_command',
    summary: 'Claude wants to run npm test.',
    details: '',
    riskSummary: '',
    requestedBy: 'claude-agent',
    assignedTo: 'operator',
    organizationCode: 'Not provided',
    organizationName: 'Not provided',
    requestedByActorLabel: 'Not provided',
    targetActorLabel: 'Not provided',
    policyId: 'Not provided',
    riskLevel: 'Not provided',
    decisionEffect: 'Not provided',
    payloadPreview: {
      kind: 'command_execution',
      language: 'shell',
      title: '',
      content: 'npm test',
      fields: []
    },
    choices: [],
    evidenceRefs: [],
    createdAt: 'Jun 02, 2026',
    createdAtRaw: '2026-06-02T09:01:00Z',
    expiresAt: 'Not provided',
    expiresAtRaw: '',
    actions: [approvalAction()]
  }
}
