import type { ScheduledTaskSummaryDTO } from '#lib/types/scheduler'

export interface ScheduledTaskGanttItem {
  task: ScheduledTaskSummaryDTO
  minuteOfDay: number
  timePercent: number
  cardPercent: number
}

const cardEdgeInsetPercent = 13

export function startOfLocalWeek(date: Date) {
  const start = new Date(date.getFullYear(), date.getMonth(), date.getDate())
  const daysSinceMonday = (start.getDay() + 6) % 7
  start.setDate(start.getDate() - daysSinceMonday)
  return start
}

export function scheduledTasksForDay(tasks: ScheduledTaskSummaryDTO[], day: Date) {
  const key = localDateKey(day)
  return tasks
    .flatMap((task) => {
      const occurrence = (task.schedule_occurrences ?? (task.next_run_at ? [task.next_run_at] : [])).find((value) => {
        const date = new Date(value)
        return !Number.isNaN(date.getTime()) && localDateKey(date) === key
      })
      return occurrence ? [{ ...task, next_run_at: occurrence }] : []
    })
    .sort((left, right) => new Date(left.next_run_at ?? 0).getTime() - new Date(right.next_run_at ?? 0).getTime())
}

export function layoutScheduledTasksForGantt(tasks: ScheduledTaskSummaryDTO[]): ScheduledTaskGanttItem[] {
  return scheduledTaskPositions(tasks)
    .map(({ timestamp: _, ...item }) => item)
}

function scheduledTaskPositions(tasks: ScheduledTaskSummaryDTO[]) {
  return tasks
    .flatMap((task) => {
      if (!task.next_run_at) return []
      const date = new Date(task.next_run_at)
      if (Number.isNaN(date.getTime())) return []

      const minuteOfDay = date.getHours() * 60 + date.getMinutes()
      const timePercent = (minuteOfDay / (24 * 60)) * 100
      return [{
        task,
        minuteOfDay,
        timePercent,
        cardPercent: clamp(timePercent, cardEdgeInsetPercent, 100 - cardEdgeInsetPercent),
        timestamp: date.getTime()
      }]
    })
    .sort((left, right) =>
      left.timestamp - right.timestamp ||
      left.task.title.localeCompare(right.task.title) ||
      left.task.scheduled_task_id.localeCompare(right.task.scheduled_task_id)
    )
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value))
}

function localDateKey(date: Date) {
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}
