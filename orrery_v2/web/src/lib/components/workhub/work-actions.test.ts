import { describe, expect, it } from 'vitest'
import { actorMatches, buildWorkActions, filterWorkActions, legacyApprovalHref, mergeWorkActionWorkflows, workActionHref } from './work-actions'
import type { TodoItemDTO } from '#lib/types/todo'
import type { ApprovalItemViewModel } from '#lib/types/view-models'

const todo = (id: string, status = 'inbox') => ({ todo_id: id, title: '相同标题', status, priority: 'medium', created_by: 'human-1', created_at: '2026-09-10T00:00:00Z' }) as TodoItemDTO
const approval = (id: string, status = 'pending') => ({ id, title: '相同标题', summary: '', status, sourceLabel: 'hil:approval', requestedBy: 'agent:worker', assignedTo: 'human-1', createdAtRaw: '2026-09-10T00:00:00Z', payloadPreview: { fields: [] } }) as unknown as ApprovalItemViewModel

describe('actionable work model', () => {
  it('keeps human completion separate from approval and combines both histories', () => {
    const rows = buildWorkActions([todo('human', 'done')], [approval('draft'), approval('previous', 'approved')], [])
    expect(filterWorkActions(rows, 'pending', []).map(row => row.id)).toEqual(['approval:draft'])
    expect(filterWorkActions(rows, 'history', []).map(row => row.id).sort()).toEqual(['approval:previous', 'todo:human'])
  })
  it('does not collapse independent approvals or human work with identical titles', () => {
    const rows = buildWorkActions([todo('human')], [approval('a'), approval('b'), approval('a')], [])
    expect(rows.map(row => row.id).sort()).toEqual(['approval:a', 'approval:b', 'todo:human'])
  })
  it('names completed handoffs awaiting delivery without asking for a second completion', () => {
    const rows = buildWorkActions([{ ...todo('human', 'done'), source: { conversation_id: 'private', delivery_status: 'pending' } }], [], [])
    expect(rows[0]).toMatchObject({ pending: true, status: 'delivery_pending', todo: { status: 'done' } })
  })
  it('matches exact requester identities without guessing by name', () => {
    const rows = buildWorkActions([todo('mine'), { ...todo('other'), created_by: 'human-10' }], [], [])
    expect(filterWorkActions(rows, 'requested', ['human:human-1']).map(row => row.id)).toEqual(['todo:mine'])
    expect(actorMatches('human-10', ['human-1'])).toBe(false)
  })
  it('routes exact work IDs into the center and preserves workflow/history legacy links', () => {
    expect(new URL(workActionHref('approval:a/b'), 'https://example.test').searchParams.get('action')).toBe('approval:a/b')
    const url = new URL(legacyApprovalHref(new URL('https://example.test/approvals?decision_id=a&workflow_id=w&tab=history')), 'https://example.test')
    expect(url.pathname).toBe('/work-items')
    expect(url.searchParams.get('action')).toBe('approval:a')
    expect(url.searchParams.get('workflow_id')).toBe('w')
    expect(url.searchParams.get('view')).toBe('history')
  })
  it('keeps a council already handled by me in history while other participants continue', () => {
    const rows = buildWorkActions([], [], [{ id: 'd', councilId: 'c', status: 'pending', routeMode: 'council', handled: true, purpose: 'Review', createdAtRaw: '2026-09-10T00:00:00Z' } as any])
    expect(filterWorkActions(rows, 'pending', [])).toHaveLength(0)
    expect(filterWorkActions(rows, 'history', [])[0]).toMatchObject({ id: 'council:c', status: 'handled' })
  })
  it('keeps my completed workflow stage in history without claiming the whole approval passed', () => {
    const workflow = { id: 'w', status: 'running', requester: 'agent:worker', tasks: [{ assignee: 'human:human-1', originalAssignee: 'human:human-1', status: 'approved', actionable: false }], workApproval: approval('d') } as any
    const rows = mergeWorkActionWorkflows([], [workflow], 'history', ['human-1'])
    expect(rows[0]).toMatchObject({ id: 'approval:d', status: 'handled', pending: false, approval: { status: 'pending' } })
    workflow.tasks.push({ assignee: 'human:human-1', status: 'pending', actionable: true })
    expect(mergeWorkActionWorkflows([], [workflow], 'history', ['human-1'])).toHaveLength(0)
  })
})
