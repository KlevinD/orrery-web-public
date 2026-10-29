import { render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it } from 'vitest'
import CouncilWorkspace from './CouncilWorkspace.svelte'
import { setLocale } from '#lib/i18n'

describe('CouncilWorkspace', () => {
  beforeEach(() => {
    setLocale('zh-CN')
  })

  it('renders quorum and approval handoff without exposing internal evidence references', () => {
    render(CouncilWorkspace, {
      props: {
        selectedId: 'council-1',
        items: [{
          id: 'decision-1', councilId: 'council-1', routeMode: 'approval', councilStage: true, action: 'customer.update', resource: 'customer:customer-1', purpose: 'Correct record',
          requester: 'agent:jordan', riskLevel: 'high', riskTone: 'danger', status: 'resolved', statusTone: 'success', effect: 'requires_approval',
          reasons: ['high-risk council requires fixed approval'], evidenceRefs: ['evidence-1'], contextBundleId: 'bundle-1', contextComplete: true,
          contextConfidence: 0.9, createdAt: 'Aug 9, 09:00', createdAtRaw: '2026-08-09T09:00:00Z'
        }],
        council: {
          id: 'council-1', requestId: 'decision-1', status: 'passed', statusTone: 'success', riskLevel: 'high', effect: 'requires_approval',
          quorum: 2, approvalThreshold: 2, approvals: 2, rejections: 0, abstentions: 0,
          participants: [{ actorKey: 'human:ding', actorId: 'ding', actorKind: 'human', displayName: 'Ding', role: 'owner', required: true, contributed: true, vote: 'approve' }],
          contributions: [{ actorKey: 'human:ding', actorName: 'Ding', position: 'approve', reasoning: 'Evidence supports it.', evidenceRefs: ['evidence-1'], updatedAt: 'Aug 9, 09:10' }],
          dissent: [], expiresAt: 'Aug 9, 10:00', resolvedAt: 'Aug 9, 09:11'
        }
      }
    })

    expect(screen.getAllByText('更新客户信息')).toHaveLength(2)
    expect(screen.queryByText('customer.update')).toBeNull()
    expect(screen.getByText('负责人')).toBeTruthy()
    expect(screen.getByText('建议通过')).toBeTruthy()
    expect(screen.queryByText('evidence-1')).toBeNull()
    expect(screen.getByText(/fixed approval|固定审批/i)).toBeTruthy()
    expect(document.querySelector('[data-council-workspace]')?.className).toContain('xl:grid-cols-[18rem_minmax(0,1fr)]')
    expect(document.querySelector('[data-council-review-layout]')).toBeTruthy()
    expect(document.querySelector('[data-council-quorum]')).toBeTruthy()
    expect(document.querySelector('[data-council-participants] [data-workforce-avatar]')).toBeTruthy()
  })

  it('shows review material and gates voting until the current participant contributes', async () => {
	const item = {
	  id: 'decision-open', councilId: 'council-open', routeMode: 'council', councilStage: true, action: 'runtime.approval', resource: 'runtime_approval:approval-1', purpose: 'Review actual article',
	  requester: 'agent:jordan', riskLevel: 'high', riskTone: 'danger' as const, status: 'pending', statusTone: 'warning' as const, effect: 'authorize',
	  reasons: ['multi-party review'], evidenceRefs: [], contextBundleId: 'bundle-open', contextComplete: true, contextConfidence: 1,
	  payloadPreview: { material_kind: 'wechat_draft', title: 'Reviewed title', content: '# Reviewed article\n', material_hash: 'sha256:material', fields: { account_id: 'wechat-main' }, artifacts: [] },
	  createdAt: 'Aug 17, 09:00', createdAtRaw: '2026-08-17T09:00:00Z'
	}
	const council = {
	  id: 'council-open', requestId: 'decision-open', status: 'open', statusTone: 'warning' as const, riskLevel: 'high', effect: 'authorize',
	  quorum: 1, approvalThreshold: 1, approvals: 0, rejections: 0, abstentions: 0,
	  participants: [{ actorKey: 'human:ding', actorId: 'ding', actorKind: 'human', displayName: 'Ding', role: 'owner', required: true, contributed: false, vote: '' }],
	  contributions: [], dissent: [], expiresAt: 'Aug 17, 10:00', resolvedAt: ''
	}
	const view = render(CouncilWorkspace, { props: { items: [item], selectedId: 'council-open', council, viewerActorIds: ['ding'] } })

	expect(screen.getByRole('heading', { name: 'Reviewed article' })).toBeTruthy()
	expect(screen.queryByText('wechat-main')).toBeNull()
	expect(screen.queryByText('sha256:material')).toBeNull()
	expect(document.querySelector('[data-approval-action-bar]')).toBeTruthy()
	expect((screen.getByRole('button', { name: /同意|Approve/ }) as HTMLButtonElement).disabled).toBe(true)

	await view.rerender({
	  items: [item], selectedId: 'council-open', viewerActorIds: ['ding'],
	  council: { ...council, participants: [{ ...council.participants[0], contributed: true }] }
	})
	expect((screen.getByRole('button', { name: /同意|Approve/ }) as HTMLButtonElement).disabled).toBe(false)
  })
})
