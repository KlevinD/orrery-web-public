import { describe, expect, it, vi } from 'vitest'
import { approvalApi } from '#lib/api/approvals'
import { loadNotificationBadgeCounts, loadNotificationApprovalBadgeCounts } from './notification-badges'
import type { ApprovalPageDTO, HILDecisionPageDTO } from '#lib/types/api'

describe('notification badge query', () => {
  it('loads every approval page for the complete inbox', async () => {
    const requests: number[] = []
    vi.spyOn(approvalApi, 'listApprovals').mockImplementation(async (query = '') => {
      const offset = Number(new URLSearchParams(query).get('offset') ?? 0)
      requests.push(offset)
      return { items: Array.from({ length: Math.min(100, 125 - offset) }, (_, i) => businessApproval(String(offset + i), 'qa', 'Review')), page: { total: 125, returned: Math.min(100, 125 - offset) } }
    })
    vi.spyOn(approvalApi, 'listHILDecisions').mockResolvedValue({ items: [], total: 0 })
    const inbox = await loadNotificationApprovalBadgeCounts(undefined, true)
    expect(inbox.allApprovalItems).toHaveLength(125)
    expect(requests).toEqual([0, 100])
  })
  it('merges assigned runtime and fixed HIL approvals with ordinary Todo and QA approvals', async () => {
    vi.spyOn(approvalApi, 'listApprovals').mockResolvedValue({
      items: [
        businessApproval('qa-approval', 'qa', 'QA evidence review'),
        businessApproval('todo-approval', 'todo_execution_plan', 'Todo execution plan'),
        {
          ...businessApproval('legacy-hil-approval', 'tool_access', 'Legacy HIL approval'),
          payload_preview: { fields: { hil_decision_id: 'fixed-decision-1' } }
        }
      ],
      page: { total: 3, returned: 3 }
    } satisfies ApprovalPageDTO)
    vi.spyOn(approvalApi, 'listHILDecisions').mockImplementation(async (query = '') => {
      if (query.includes('mode=council')) return { items: [], total: 0 } satisfies HILDecisionPageDTO
      if (query.includes('mode=hil')) {
        return {
          items: [{
            id: 'runtime-decision-1', tenant_id: 'tenant-1', requester: { kind: 'agent', id: 'runtime-agent' },
            action: 'tool.execute', resource: { kind: 'tool', id: 'tool-1' }, args_hash: 'sha256:runtime', risk_level: 'medium', route: { mode: 'hil', reasons: [] },
            context: { bundle_id: 'bundle-runtime', complete: true }, payload_preview: { approval_assigned_to: 'human-owner' },
            status: 'pending', created_at: '2026-08-14T09:00:00Z'
          }], total: 1
        } satisfies HILDecisionPageDTO
      }
      return {
        items: [{
          id: 'fixed-decision-1', tenant_id: 'tenant-1', requester: { kind: 'service', id: 'responsibility-autoplan' },
          action: 'responsibility.plan.execute', resource: { kind: 'responsibility_autoplan', id: 'autoplan-1' },
          args_hash: 'sha256:proposal', risk_level: 'high', route: { mode: 'approval', reasons: [] },
          context: { bundle_id: 'bundle-1', complete: true },
          payload_preview: { summary: 'Review autonomous plan', approval_assigned_to: 'human-owner' },
          status: 'pending', created_at: '2026-08-14T09:00:00Z'
        }], total: 1
      } satisfies HILDecisionPageDTO
    })

    const badges = await loadNotificationBadgeCounts({ discussions: false, approvals: true })

    expect(approvalApi.listApprovals).toHaveBeenCalledWith('?status=pending', { signal: undefined })
    expect(approvalApi.listHILDecisions).toHaveBeenCalledWith('?mode=hil&status=pending&mine=true&limit=100', { signal: undefined })
    expect(approvalApi.listHILDecisions).toHaveBeenCalledWith('?mode=approval&status=pending&mine=true&limit=100', { signal: undefined })
    expect(badges.approvals).toBe(4)
    expect(badges.hilApprovals).toBe(1)
    expect(badges.approvalItems.map((item) => item.id)).toEqual(['runtime-decision-1', 'fixed-decision-1', 'qa-approval'])
    expect(badges.allApprovalItems.map((item) => item.id)).toEqual([
      'runtime-decision-1', 'fixed-decision-1', 'qa-approval', 'todo-approval'
    ])
    expect(badges.allCouncilItems).toEqual([])
    expect(badges.approvalItems[1]).toMatchObject({ summary: 'Review autonomous plan', assignedTo: 'human-owner' })
  })
})

function businessApproval(id: string, type: string, title: string) {
  return {
    approval_id: id,
    approval_type: type,
    status: 'pending',
    priority: 'normal',
    source_type: type === 'qa' ? 'qa' : 'todo',
    source_id: id,
    title,
    requested_by: 'agent-1',
    available_actions: [],
    created_at: '2026-08-14T09:00:00Z'
  }
}
