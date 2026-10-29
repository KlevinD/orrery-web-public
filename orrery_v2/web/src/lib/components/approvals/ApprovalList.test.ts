import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ApprovalList from './ApprovalList.svelte'
import { workflowView } from './workflowTestFixture'
import { setLocale } from '#lib/i18n'
import type { ApprovalItemViewModel } from '#lib/types/view-models'

describe('ApprovalList', () => {
  beforeEach(() => {
    setLocale('zh-CN')
  })

  it('keeps the desktop approval queue narrow and gives the detail pane the remaining width', () => {
    render(ApprovalList, { props: { items: [approvalItem()] } })

    expect(document.querySelector('[data-approval-workspace]')?.className)
      .toContain('xl:grid-cols-[18rem_minmax(0,1fr)]')
  })

  it('keeps the detail empty after a decision until another queue item is explicitly selected', async () => {
    render(ApprovalList, { props: { items: [approvalItem()], autoSelectFirst: false } })

    expect(document.querySelector('[data-approval-detail]')?.textContent).toContain('选择一项查看详情')

    await fireEvent.click(screen.getByRole('button', { name: /命令执行确认/ }))

    expect(document.querySelector('[data-approval-detail]')?.textContent).toContain('Claude wants to run npm test.')
  })

  it('renders approval center action labels through i18n and submits the original action', async () => {
    const item = approvalItem()
    const onaction = vi.fn()

    render(ApprovalList, {
      props: {
        items: [item],
        onaction
      }
    })

    expect(screen.getByRole('button', { name: '仅本次批准' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '本会话批准' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '拒绝' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '其他' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Approve once' })).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: '本会话批准' }))

    expect(onaction).toHaveBeenCalledWith(item, expect.objectContaining({ id: 'approve_session', label: 'Approve for session' }))
  })

  it('renders unavailable approvals as expired read-only items', () => {
    render(ApprovalList, {
      props: {
        items: [approvalItem({ status: 'unavailable', statusTone: 'neutral', actions: [] })]
      }
    })

    expect(screen.getAllByText('已失效').length).toBeGreaterThanOrEqual(2)
    expect(document.querySelector('[data-approval-flow]')?.textContent).toContain('审批人')
    expect(screen.queryByText('unavailable')).toBeNull()
    expect(screen.queryByRole('button', { name: '仅本次批准' })).toBeNull()
    expect(screen.queryByRole('button', { name: '拒绝' })).toBeNull()
  })

  it('renders runtime approval actors with their actual business roles', () => {
    render(ApprovalList, {
      props: {
        items: [approvalItem({
          status: 'rejected',
          statusTone: 'danger',
          requestedBy: 'human-ding',
          requestedByName: '丁皮',
          triggeredBy: 'lead-agent',
          triggeredByName: '顾明澈',
          decidedBy: 'human-ding',
          decidedByName: '丁皮',
          assignedTo: 'Not provided',
          actions: []
        })]
      }
    })

    const flow = document.querySelector('[data-approval-flow]')
    expect(flow?.textContent).toContain('业务发起人')
    expect(flow?.textContent).toContain('丁皮')
    expect(flow?.textContent).toContain('执行 / 触发审批')
    expect(flow?.textContent).toContain('顾明澈')
    expect(flow?.textContent).toContain('审批人')
    expect(flow?.textContent).toContain('已拒绝')
  })

  it('switches the detail panel when a queue item is selected', async () => {
    const first = approvalItem()
    const second = approvalItem({
      id: 'approval-2',
      title: 'Second approval',
      summary: 'Review the second request.',
      requestedBy: 'second-agent',
      actions: []
    })

    render(ApprovalList, { props: { items: [first, second] } })

    const detail = document.querySelector('[data-approval-detail]')
    expect(detail?.textContent).toContain('命令执行确认')
    expect(detail?.textContent).toContain('执行确认')

    await fireEvent.click(screen.getByRole('button', { name: /Second approval/ }))

    expect(detail?.textContent).toContain('Second approval')
    expect(detail?.textContent).toContain('second-agent')
    expect(detail?.textContent).toContain('Review the second request.')
    expect(detail?.querySelector('[data-approval-flow]')?.textContent).toContain('second-agent')
    expect(detail?.querySelector('[data-approval-flow]')?.textContent).toContain('operator')
    expect(detail?.querySelector('[data-approval-flow]')?.textContent).not.toContain('后续流向')
  })

  it('shows the request explanation, review provenance, and exact content awaiting approval', () => {
    render(ApprovalList, {
      props: {
        items: [approvalItem({
          details: 'The command only reads source files.',
          riskSummary: 'AI reviewer | low risk | Read-only inspection.',
          payloadPreview: {
            kind: 'command_execution',
            language: 'shell',
            title: 'Command',
            content: 'git status --short',
            fields: []
          }
        })]
      }
    })

    const detail = document.querySelector('[data-approval-detail]')
    expect(detail?.textContent).toContain('Claude wants to run npm test.')
    expect(detail?.textContent).toContain('The command only reads source files.')
    expect(detail?.textContent).toContain('AI reviewer | low risk | Read-only inspection.')
    expect(detail?.textContent).toContain('git status --short')
  })

  it('renders HIL as human intervention context without inventing workflow stages', () => {
    render(ApprovalList, {
      props: {
        items: [approvalItem({
          type: 'hil',
          displayKind: 'hil_decision',
          typeLabel: 'HIL',
          toolName: 'smoke.hil.review',
          title: '发布前人工确认',
          summary: '确认本期文章可以对外发布。',
          details: 'runtime human review is required',
          riskSummary: 'runtime human review is required',
          riskLevel: 'high',
          requestedBy: 'human:H000001',
          assignedTo: 'H000002',
          assignedToName: '屈正'
        })]
      }
    })

    const context = document.querySelector('[data-hil-context]')
    expect(context?.textContent).toContain('人工介入')
    expect(context?.textContent).toContain('自动执行已暂停')
    expect(context?.textContent).toContain('高风险')
    expect(context?.textContent).toContain('屈正')
    expect(context?.textContent).toContain('发起请求')
    expect(context?.textContent).toContain('人工处理')
    expect(context?.querySelectorAll('[data-workforce-avatar]')).toHaveLength(2)
    expect(context?.querySelectorAll('span.absolute.w-px')).toHaveLength(1)
    expect(screen.getByText('确认本期文章可以对外发布。')).toBeTruthy()
    expect(screen.getAllByText('审批发起人').length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText('H000001')).toBeNull()
    expect(screen.queryByText('H000002')).toBeNull()
    expect(screen.queryByText('runtime human review is required')).toBeNull()
    expect(screen.queryByText('smoke.hil.review')).toBeNull()
    expect(document.querySelector('[data-approval-flow]')).toBeNull()
    expect(document.querySelector('[data-approval-action-bar]')).toBeTruthy()
  })

  it('renders canonical fixed approvals without action codes or routing reasons', () => {
    render(ApprovalList, {
      props: {
        items: [approvalItem({
          type: 'approval',
          displayKind: 'approval_decision',
          typeLabel: 'Approval',
          sourceLabel: 'hil:approval',
          title: 'communication.wechat_draft.create',
          summary: '确认后在微信公众号中创建本期文章草稿。',
          details: 'canonical action matched fixed approval route',
          riskSummary: 'forced_marketing_wechat',
          riskLevel: 'high',
          assignedTo: 'human:ding'
        })]
      }
    })

    expect(screen.getAllByText('创建微信公众号草稿').length).toBeGreaterThanOrEqual(2)
    expect(screen.queryByText('该操作符合审批规则，需要人工确认后才能继续。')).toBeNull()
    expect(screen.queryByText('communication.wechat_draft.create')).toBeNull()
    expect(screen.queryByText('canonical action matched fixed approval route')).toBeNull()
    expect(screen.queryByText('forced_marketing_wechat')).toBeNull()
  })

  it('keeps fixed approval as an avatar flow with vertical connectors', () => {
    render(ApprovalList, {
      props: {
        items: [approvalItem({
          type: 'approval',
          displayKind: 'approval_decision',
          typeLabel: 'Approval',
          requestedBy: 'human:mei',
          requestedByName: '梅琳',
          triggeredBy: 'agent:publisher',
          triggeredByName: '发布 Agent',
          assignedTo: 'human:ding',
          assignedToName: '丁皮'
        })]
      }
    })

    const flow = document.querySelector('[data-approval-flow]')
    expect(flow?.textContent).toContain('固定审批流')
    expect(flow?.textContent).toContain('梅琳')
    expect(flow?.textContent).toContain('发布 Agent')
    expect(flow?.textContent).toContain('丁皮')
    expect(flow?.querySelectorAll('[data-workforce-avatar]')).toHaveLength(3)
    expect(flow?.querySelectorAll('span.absolute.w-px')).toHaveLength(2)
  })

  it('does not expose a resolved HIL decider profile ID', () => {
    render(ApprovalList, {
      props: {
        items: [approvalItem({
          type: 'hil',
          displayKind: 'hil_decision',
          typeLabel: 'HIL',
          status: 'approved',
          statusTone: 'success',
          requestedBy: 'human:H000001',
          decidedBy: 'H000002',
          assignedTo: 'H000002',
          assignedToName: '屈正',
          actions: []
        })]
      }
    })

    const context = document.querySelector('[data-hil-context]')
    expect(context?.textContent).toContain('屈正')
    expect(screen.queryByText('H000001')).toBeNull()
    expect(screen.queryByText('H000002')).toBeNull()
  })

  it('renders the linked workflow in the legacy detail pane and emits workflow task decisions', async () => {
    const item = approvalItem()
    const workflow = workflowView()
    workflow.decisionRequestId = item.id
    const onaction = vi.fn()
    const onworkflowdecision = vi.fn()

    render(ApprovalList, {
      props: {
        items: [item],
        workflowByDecisionId: { [item.id]: workflow },
        workflowLoaded: true,
        onaction,
        onworkflowdecision
      }
    })

    expect(screen.getByRole('heading', { name: 'Reviewed article' })).toBeTruthy()
    expect(screen.getByText('市场审批')).toBeTruthy()
    expect(document.querySelector('[data-approval-flow]')).toBeNull()
    expect(screen.queryByRole('button', { name: '仅本次批准' })).toBeNull()

    await fireEvent.click(screen.getByRole('button', { name: '拒绝' }))
    await fireEvent.input(screen.getByRole('textbox', { name: '理由' }), { target: { value: '材料不符合要求' } })
    await fireEvent.click(screen.getByRole('button', { name: '确认拒绝' }))

    expect(onworkflowdecision).toHaveBeenCalledWith({
      taskId: 'task-1', decision: 'reject', reason: '材料不符合要求', expectedVersion: 3
    })
    expect(onaction).not.toHaveBeenCalled()
  })

  it('renders a directly linked workflow when its approval is outside the current queue page', () => {
    const workflow = workflowView()
    workflow.decisionRequestId = 'approval-linked'

    render(ApprovalList, {
      props: {
        items: [approvalItem({ id: 'approval-visible', title: 'Visible approval' })],
        selectedId: workflow.decisionRequestId,
        workflowByDecisionId: { [workflow.decisionRequestId]: workflow },
        workflowLoaded: true
      }
    })

    expect(screen.getByRole('heading', { name: 'Reviewed article' })).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Reviewed title' })).toBeTruthy()
    expect(document.querySelector('[data-approval-detail]')?.textContent).not.toContain('Visible approval')
  })

  it('fails closed when a business approval has no linked workflow', () => {
    const item = approvalItem({
      toolName: 'request_business_approval',
      payloadPreview: {
        kind: 'business_approval', language: 'zh-CN', title: '公众号文章发布审批', content: '',
        fields: [{ key: 'approval_request_kind', value: 'business' }]
      }
    })

    render(ApprovalList, {
      props: { items: [item], workflowByDecisionId: {}, workflowLoaded: true }
    })

    expect(screen.getByRole('alert').textContent).toContain('审批流不可用，暂时不能处理该审批')
    expect(screen.queryByRole('button', { name: '仅本次批准' })).toBeNull()
    expect(screen.queryByRole('button', { name: '拒绝' })).toBeNull()
  })
})

function approvalItem(overrides: Partial<ApprovalItemViewModel> = {}): ApprovalItemViewModel {
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
    actions: [
      {
        id: 'approve_once',
        label: 'Approve once',
        style: 'primary',
        decision: 'approved',
        effect: 'once',
        inputKind: 'message',
        defaultMessage: '',
        method: 'POST',
        path: '/api/approvals/approval-1/decision',
        reasonRequired: false,
        confirmationRequired: false,
        allowsModification: false,
        rememberAllowed: false
      },
      {
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
      },
      {
        id: 'deny',
        label: 'Deny',
        style: 'danger',
        decision: 'denied',
        effect: 'deny',
        inputKind: 'message',
        defaultMessage: '',
        method: 'POST',
        path: '/api/approvals/approval-1/decision',
        reasonRequired: false,
        confirmationRequired: true,
        allowsModification: false,
        rememberAllowed: false
      },
      {
        id: 'other',
        label: 'Other',
        style: 'secondary',
        decision: 'approved',
        effect: 'once',
        inputKind: 'message',
        defaultMessage: '',
        method: 'POST',
        path: '/api/approvals/approval-1/decision',
        reasonRequired: false,
        confirmationRequired: true,
        allowsModification: false,
        rememberAllowed: false
      }
    ],
    ...overrides
  }
}
