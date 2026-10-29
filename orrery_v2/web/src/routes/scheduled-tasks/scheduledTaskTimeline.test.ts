import { describe, expect, it } from 'vitest'
import type { ScheduledTaskSummaryDTO } from '#lib/types/scheduler'
import { layoutScheduledTasksForGantt, scheduledTasksForDay, startOfLocalWeek } from './scheduledTaskTimeline'

describe('scheduled task gantt layout', () => {
  it('keeps simultaneous tasks as separate rows at the same time position', () => {
    const items = layoutScheduledTasksForGantt([
      task('task-2', 'Second task', localTime(8, 0)),
      task('task-1', 'First task', localTime(8, 0)),
      task('task-3', 'Later task', localTime(9, 0))
    ])

    expect(items.map((item) => item.task.scheduled_task_id)).toEqual(['task-1', 'task-2', 'task-3'])
    expect(items[0].timePercent).toBe(items[1].timePercent)
    expect(items[2].timePercent).toBeGreaterThan(items[1].timePercent)
  })

  it('sorts task rows chronologically regardless of input order', () => {
    const items = layoutScheduledTasksForGantt([
      task('task-2', 'Noon task', localTime(12, 0)),
      task('task-1', 'Morning task', localTime(8, 0))
    ])

    expect(items.map((item) => item.task.scheduled_task_id)).toEqual(['task-1', 'task-2'])
  })

  it('keeps cards inside the grid while preserving exact milestone positions', () => {
    const items = layoutScheduledTasksForGantt([
      task('task-1', 'Midnight task', localTime(0, 0)),
      task('task-2', 'Late task', localTime(23, 59)),
      task('task-3', 'Invalid task', 'not-a-date'),
      task('task-4', 'Unscheduled task')
    ])

    expect(items).toHaveLength(2)
    expect(items.map((item) => item.cardPercent)).toEqual([13, 87])
    expect(items[0].timePercent).toBe(0)
    expect(items[1].timePercent).toBeGreaterThan(99)
  })

  it('uses expanded schedule occurrences when selecting Friday tasks', () => {
    const friday = new Date(2026, 7, 14)
    const daily = task('daily-task', 'Daily task', localTime(13, 0))
    daily.schedule_occurrences = [
      new Date(2026, 7, 13, 9, 0).toISOString(),
      new Date(2026, 7, 14, 9, 0).toISOString()
    ]

    const tasks = scheduledTasksForDay([daily], friday)

    expect(tasks).toHaveLength(1)
    expect(tasks[0].scheduled_task_id).toBe('daily-task')
    expect(new Date(tasks[0].next_run_at ?? '').getDate()).toBe(14)
  })

  it('falls back to next run when an older API omits expanded occurrences', () => {
    const friday = new Date(2026, 7, 14)
    const legacyTask = task('legacy-task', 'Legacy task', new Date(2026, 7, 14, 11, 30).toISOString())

    const tasks = scheduledTasksForDay([legacyTask], friday)

    expect(tasks).toHaveLength(1)
    expect(tasks[0].scheduled_task_id).toBe('legacy-task')
  })

  it('starts the schedule view on Monday', () => {
    const monday = startOfLocalWeek(new Date(2026, 7, 12, 15, 30))

    expect(monday.getDay()).toBe(1)
    expect(monday.getDate()).toBe(10)
    expect(monday.getHours()).toBe(0)
  })
})

function localTime(hour: number, minute: number) {
  return new Date(2026, 6, 23, hour, minute).toISOString()
}

function task(id: string, title: string, nextRunAt?: string): ScheduledTaskSummaryDTO {
  return {
    scheduled_task_id: id,
    title,
    status: 'enabled',
    enabled: true,
    schedule_type: 'cron',
    target_mode: 'agent_task',
    target_summary: 'Agent task',
    next_run_at: nextRunAt
  }
}
