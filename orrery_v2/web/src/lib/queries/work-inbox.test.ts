import { describe, expect, it } from 'vitest'
import { buildWorkInbox, loadAllPages } from './work-inbox'
import type { TodoItemDTO } from '#lib/types/todo'
import type { ScheduledRunInboxItemDTO } from '#lib/types/scheduler'
import type { NotificationApprovalBadgeCounts } from './notification-badges'

const todo = (id: string, status = 'inbox') => ({
  todo_id: id, title: id, status, priority: 'medium', created_at: '2026-09-05T01:00:00Z'
} as TodoItemDTO)
const run = (id: string, task: string, day: number, attention = false, read = false) => ({
  task_title: task, is_read: read, attention_required: attention,
  run: { scheduled_run_id: id, scheduled_task_id: task, created_at: `2026-09-0${day}T01:00:00Z` }
} as ScheduledRunInboxItemDTO)

describe('unified work inbox', () => {
  it('keeps completed human work pending while conversation delivery needs retry', () => {
    const model = buildWorkInbox([{ ...todo('human', 'done'), source: { conversation_id: 'chat', delivery_status: 'pending', result: 'Resolved' } }], undefined, [])
    expect(model.pending).toHaveLength(1)
    expect(model.pending[0]).toMatchObject({ humanHandoff: true, attention: true })
    expect(model.done).toHaveLength(0)
  })
  it('counts the full pending list and excludes completed and canceled todos', () => {
    const model = buildWorkInbox([...Array.from({ length: 125 }, (_, i) => todo(String(i))), todo('done', 'done'), todo('canceled', 'canceled')], undefined, [])
    expect(model.pending).toHaveLength(125)
    expect(model.done).toHaveLength(2)
  })
  it('groups result history by schedule and suppresses recovered failures', () => {
    const model = buildWorkInbox([], undefined, [run('old', 'daily', 1, true), run('new', 'daily', 5), run('other', 'weekly', 3)])
    expect(model.pending).toHaveLength(0)
    expect(model.results).toHaveLength(2)
    expect(model.unread).toBe(2)
    expect(model.results[0].links.map(item => item.id)).toEqual(['new', 'old'])
  })
  it('does not treat reading a failed report as resolving the failure', () => {
    const model = buildWorkInbox([], undefined, [run('failed', 'daily', 5, true, true)])
    expect(model.pending).toHaveLength(1)
    expect(model.unread).toBe(0)
  })
  it('only merges explicit todo references and preserves distinct approval actions', () => {
    const approval = (id: string, todoId?: string) => ({
      id, title: 'Same title', summary: '', createdAtRaw: '2026-09-05T01:00:00Z',
      payloadPreview: { fields: todoId ? [{ key: 'todo_id', value: todoId }] : [] }
    })
    const approvals = { allApprovalItems: [approval('a', 't'), approval('b', 't'), approval('c'), approval('d')], allCouncilItems: [] } as unknown as NotificationApprovalBadgeCounts
    const model = buildWorkInbox([todo('t')], approvals, [])
    expect(model.pending).toHaveLength(3)
    expect(model.pending.find(item => item.id === 'todo:t')?.links.map(item => item.id)).toEqual(['t', 'a', 'b'])
  })
  it('loads beyond the first page before returning a count', async () => {
    const offsets: number[] = []
    const items = await loadAllPages(async offset => {
      offsets.push(offset)
      return { items: Array.from({ length: Math.min(100, 205 - offset) }, (_, i) => i + offset), total: 205 }
    })
    expect(offsets).toEqual([0, 100, 200])
    expect(items).toHaveLength(205)
  })
  it('rejects an incomplete page instead of silently undercounting', async () => {
    await expect(loadAllPages(async () => ({ items: [], total: 5 }))).rejects.toThrow('Incomplete')
  })
})
