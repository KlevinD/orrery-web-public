import type { NotificationItem } from '#lib/api/notifications'
import type { ScheduledRunInboxItemDTO } from '#lib/types/scheduler'

export interface WorkActivityItem {
  id: string
  title: string
  summary: string
  date: string
  unread: boolean
  attention: boolean
  notifications: NotificationItem[]
  result?: ScheduledRunInboxItemDTO
}

const runKey = (task: string, run: string) => JSON.stringify([task, run])

export function buildWorkActivity(notifications: NotificationItem[], runs: ScheduledRunInboxItemDTO[]): WorkActivityItem[] {
  const rows = new Map<string, WorkActivityItem>()
  for (const result of runs) {
    const run = result.run
    const id = runKey(run.scheduled_task_id, run.scheduled_run_id)
    rows.set(id, { id, title: result.task_title, summary: run.result?.summary || run.failure_reason || '', date: run.completed_at || run.created_at,
      unread: !result.is_read, attention: result.attention_required, result, notifications: [] })
  }
  for (const notification of notifications) {
    const event = notification.event
    const id = event.task_id && event.run_id ? runKey(event.task_id, event.run_id) : `notification:${notification.id}`
    const row = rows.get(id)
    if (row) {
      if (!row.notifications.some(item => item.id === notification.id)) row.notifications.push(notification)
      row.unread ||= !notification.read
      // Notification delivery/read state does not resolve or create actionable work.
    } else rows.set(id, { id, title: event.title, summary: event.summary, date: event.created_at, unread: !notification.read,
      attention: false, notifications: [notification] })
  }
  return [...rows.values()].sort((a, b) => Date.parse(b.date) - Date.parse(a.date) || a.id.localeCompare(b.id))
}
