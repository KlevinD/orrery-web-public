import { describe, expect, it } from 'vitest'
import { buildWorkActivity } from './work-activity'
import type { NotificationItem } from '#lib/api/notifications'
import type { ScheduledRunInboxItemDTO } from '#lib/types/scheduler'

const notification = (id: string, task = 'task-1', run = 'run-1', read = false) => ({ id, read, event: { task_id: task, run_id: run, title: 'Report', summary: 'Result', created_at: '2026-09-10T02:00:00Z' } } as NotificationItem)
const result = { task_title: 'Report', is_read: false, attention_required: true, run: { scheduled_task_id: 'task-1', scheduled_run_id: 'run-1', created_at: '2026-09-10T02:00:00Z' } } as ScheduledRunInboxItemDTO
describe('work activity identity', () => {
  it('joins notifications to the exact run without collapsing other runs or same-title tasks', () => {
    const rows = buildWorkActivity([notification('one'), notification('copy'), notification('other-run', 'task-1', 'run-2'), notification('other-task', 'task-2')], [result])
    expect(rows).toHaveLength(3)
    expect(rows.find(row => row.result)?.notifications.map(item => item.id)).toEqual(['one', 'copy'])
  })
  it('keeps action status independent of read state and exposes divergent receipts as unread', () => {
    expect(buildWorkActivity([notification('one', 'task-1', 'run-1', true)], [{ ...result, is_read: true }])[0]).toMatchObject({ unread: false, attention: true })
    expect(buildWorkActivity([notification('one')], [{ ...result, is_read: true }])[0].unread).toBe(true)
  })
  it('retains personal reports without scheduler access or run references', () => {
    const rows = buildWorkActivity([notification('one'), notification('no-run', '', '')], [])
    expect(rows).toHaveLength(2)
    expect(rows.every(row => !row.result && !row.attention)).toBe(true)
  })
})
