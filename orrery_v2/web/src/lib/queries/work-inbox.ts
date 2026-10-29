import { createQuery } from '@tanstack/svelte-query'
import { withinHistoryWindow, type HistoryWindow } from './history-window'
import { todoApi } from '#lib/api/todos'
import { schedulerApi } from '#lib/api/scheduler'
import { loadNotificationApprovalBadgeCounts } from './notification-badges'
import type { ScheduledRunInboxItemDTO } from '#lib/types/scheduler'
import type { TodoItemDTO } from '#lib/types/todo'
import type { NotificationApprovalBadgeCounts } from './notification-badges'

export interface WorkInboxAccess { todos: boolean; approvals: boolean; scheduler: boolean; identity: string }
export interface WorkItem {
  humanHandoff?: boolean
  id: string; title: string; reason: string; date: string; kind: 'todo' | 'approval' | 'council' | 'result'
  priority: number; done: boolean; unread: boolean; attention: boolean
  links: Array<{ id: string; title: string; href: string }>
}

export async function loadAllPages<T>(load: (offset: number) => Promise<{ items: T[]; total: number }>): Promise<T[]> {
  const items: T[] = []
  for (let pageNumber = 0; pageNumber < 100; pageNumber++) {
    const page = await load(items.length)
    items.push(...page.items)
    if (items.length >= page.total) return items
    if (!page.items.length) throw new Error('Incomplete inbox response')
  }
  throw new Error('Inbox exceeds the supported page limit')
}

export function useWorkInboxSources(access: () => WorkInboxAccess, includeCompleted: () => boolean = () => false) {
  const todos = createQuery(() => ({
    queryKey: ['todos', 'work-inbox', access().identity], enabled: access().todos,
    staleTime: 30_000, refetchInterval: 60_000, refetchIntervalInBackground: false,
    queryFn: ({ signal }) => loadAllPages(async (offset) => {
      const page = await todoApi.listTodos(`?mine=true&include_completed=false&include_pending_delivery=true&limit=100&offset=${offset}`, { signal })
      return { items: page.todos ?? [], total: page.total }
    })
  }))
  const completed = createQuery(() => ({
    queryKey: ['todos', 'work-inbox-completed', access().identity], enabled: access().todos && includeCompleted(),
    staleTime: 60_000,
    queryFn: ({ signal }) => loadAllPages(async (offset) => {
      const page = await todoApi.listTodos(`?mine=true&include_completed=true&limit=100&offset=${offset}`, { signal })
      return { items: page.todos ?? [], total: page.total }
    })
  }))
  const approvals = createQuery(() => ({
    queryKey: ['notification-badges', 'work-inbox', access().identity], enabled: access().approvals,
    staleTime: 30_000, refetchInterval: 60_000, refetchIntervalInBackground: false,
    queryFn: ({ signal }) => loadNotificationApprovalBadgeCounts(signal, true)
  }))
  const results = createQuery(() => ({
    queryKey: ['scheduler', 'work-inbox', access().identity], enabled: access().scheduler,
    staleTime: 30_000, refetchInterval: 60_000, refetchIntervalInBackground: false,
    queryFn: ({ signal }) => loadAllPages(async (offset) => {
      const page = await schedulerApi.listRunInbox(`?group_by_task=true&limit=100&offset=${offset}`, { signal })
      return { items: page.items ?? [], total: page.total }
    })
  }))
  return { todos, approvals, results, completed }
}

export function buildWorkInbox(todos: TodoItemDTO[], approvals: NotificationApprovalBadgeCounts | undefined, results: ScheduledRunInboxItemDTO[]) {
  const pending = new Map<string, WorkItem>()
  const done: WorkItem[] = []
  const approvalTodos = new Map(todos.filter(todo => todo.automation?.approval_id).map(todo => [todo.automation!.approval_id!, todo.todo_id]))
  for (const todo of todos) {
    const item: WorkItem = {
      humanHandoff: Boolean(todo.source),
      id: `todo:${todo.todo_id}`, title: todo.title, reason: todo.blocked_reason || todo.description || '',
      date: todo.updated_at || todo.created_at, kind: 'todo', priority: ({ urgent: 0, high: 1, medium: 2, low: 3 })[todo.priority] ?? 2,
      done: ['done', 'canceled'].includes(todo.status), unread: false, attention: todo.status === 'blocked' || todo.source?.delivery_status === 'pending',
      links: [{ id: todo.todo_id, title: 'todo', href: `/work-items?view=inbox&action=${encodeURIComponent(`todo:${todo.todo_id}`)}` }]
    }
    if (item.done && !item.attention) done.push(item)
    else pending.set(item.id, item)
  }
  for (const approval of approvals?.allApprovalItems ?? []) {
    const todoID = approval.payloadPreview.fields.find((field) => field.key === 'todo_id')?.value || approvalTodos.get(approval.id)
    const key = todoID && pending.has(`todo:${todoID}`) ? `todo:${todoID}` : `approval:${approval.hilDecisionId || approval.id}`
    const mode = approval.sourceLabel === 'hil:hil' ? 'hil' : 'approval'
    const link = { id: approval.hilDecisionId || approval.id, title: approval.title, href: `/work-items?view=inbox&action=${encodeURIComponent(`approval:${approval.hilDecisionId || approval.id}`)}` }
    const previous = pending.get(key)
    if (previous) {
      if (!previous.links.some((item) => item.id === link.id)) previous.links.push(link)
      previous.attention = true
      previous.priority = 0
    } else pending.set(key, {
      id: key, title: approval.title, reason: approval.summary, date: approval.createdAtRaw,
      kind: 'approval', priority: 0, done: false, unread: false, attention: true, links: [link]
    })
  }
  for (const council of approvals?.allCouncilItems ?? []) {
    pending.set(`council:${council.id}`, {
      id: `council:${council.id}`, title: council.action, reason: council.purpose, date: council.createdAtRaw,
      kind: 'council', priority: 0, done: false, unread: false, attention: true,
      links: [{ id: council.id, title: 'council', href: `/work-items?view=inbox&action=${encodeURIComponent(`council:${council.councilId}`)}` }]
    })
  }
  const groups = new Map<string, WorkItem>()
  const ordered = [...results].sort((a, b) => Date.parse(b.run.created_at) - Date.parse(a.run.created_at))
  for (const result of ordered) {
    const run = result.run
    const id = `result:${run.scheduled_task_id}`
    const link = { id: run.scheduled_run_id, title: run.created_at, href: `/scheduled-tasks?view=results&task=${encodeURIComponent(run.scheduled_task_id)}&run=${encodeURIComponent(run.scheduled_run_id)}` }
    const previous = groups.get(id)
    if (previous) {
      if (!previous.links.some((item) => item.id === link.id)) previous.links.push(link)
      continue
    }
    groups.set(id, {
      id, title: result.task_title, reason: run.result?.summary || run.failure_reason || '', date: run.completed_at || run.created_at,
      kind: 'result', priority: 1, done: !result.attention_required, unread: !result.is_read,
      attention: result.attention_required, links: [link]
    })
  }
  // Only the latest outcome can demand attention; recovered historical failures stay in history.
  for (const result of groups.values()) {
    if (result.attention) pending.set(result.id, result)
  }
  const order = (a: WorkItem, b: WorkItem) => a.priority - b.priority || Date.parse(b.date) - Date.parse(a.date) || a.id.localeCompare(b.id)
  return {
    pending: [...pending.values()].sort(order),
    results: [...groups.values()].sort((a, b) => Date.parse(b.date) - Date.parse(a.date)),
    done: done.sort(order), unread: [...groups.values()].filter((item) => item.unread).length
  }
}
export function filterProcessedWorkHistory(items: WorkItem[], range: HistoryWindow, now: number): WorkItem[] {
  return items.filter(item => !item.done || item.attention || item.unread || withinHistoryWindow(item.date, range, now))
}
