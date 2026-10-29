import type { IdeaStatus } from '#lib/types/idea'
import type { AssigneeKind, TodoPriority, TodoStatus } from '#lib/types/todo'
import type { WorkforceMemberViewModel } from '#lib/types/view-models'

export const openTodoStatuses: TodoStatus[] = ['inbox', 'planned', 'in_progress', 'blocked', 'review']
export const todoStatuses: TodoStatus[] = [...openTodoStatuses, 'done', 'canceled']
export const todoPriorities: TodoPriority[] = ['urgent', 'high', 'medium', 'low']

export const ideaStatuses: IdeaStatus[] = [
  'draft',
  'submitted',
  'under_review',
  'accepted',
  'experiment',
  'converted',
  'rejected',
  'archived'
]

const todoTransitions: Record<TodoStatus, TodoStatus[]> = {
  inbox: ['planned', 'in_progress', 'canceled'],
  planned: ['in_progress', 'blocked', 'done', 'canceled'],
  in_progress: ['blocked', 'review', 'done', 'canceled'],
  blocked: ['planned', 'in_progress', 'canceled'],
  review: ['in_progress', 'blocked', 'done', 'canceled'],
  done: ['planned'],
  canceled: ['inbox']
}

const ideaTransitions: Record<IdeaStatus, IdeaStatus[]> = {
  draft: ['submitted', 'archived'],
  submitted: ['under_review', 'accepted', 'experiment', 'rejected', 'archived'],
  under_review: ['submitted', 'accepted', 'experiment', 'rejected', 'archived'],
  accepted: ['experiment', 'rejected', 'archived'],
  experiment: ['accepted', 'rejected', 'archived'],
  converted: [],
  rejected: ['submitted', 'archived'],
  archived: ['submitted']
}

export function allowedTodoStatuses(status: TodoStatus) {
  return [status, ...(todoTransitions[status] ?? [])]
}

export function allowedIdeaStatuses(status: IdeaStatus) {
  return [status, ...(ideaTransitions[status] ?? [])]
}

export function canConvertIdea(status: IdeaStatus) {
  return status === 'accepted' || status === 'experiment'
}

export function canCompleteTodo(status: TodoStatus) {
  return (todoTransitions[status] ?? []).includes('done')
}

export function isTerminalTodo(status: TodoStatus) {
  return status === 'done' || status === 'canceled'
}

export function workforceMemberName(member: WorkforceMemberViewModel) {
  return member.displayName.trim() || member.realName.trim() || member.id
}

export function workforceAssigneeKind(member: WorkforceMemberViewModel): AssigneeKind {
  return member.agentKind.toLowerCase() === 'human' ? 'human' : 'agent'
}

export function normalizeTags(value: string) {
  const seen = new Set<string>()
  const tags: string[] = []
  for (const raw of value.split(/[,\n，、]/g)) {
    const tag = raw.trim()
    const key = tag.toLowerCase()
    if (!tag || seen.has(key)) continue
    seen.add(key)
    tags.push(tag)
  }
  return tags
}

export function dateTimeInputValue(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

export function dateTimeInputISO(value: string) {
  if (!value.trim()) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

export function initials(value: string) {
  const chars = Array.from(value.trim())
  return chars.length ? chars.slice(0, 2).join('').toUpperCase() : '?'
}
