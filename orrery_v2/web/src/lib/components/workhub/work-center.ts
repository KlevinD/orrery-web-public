import type { IdeaItemDTO, IdeaStatus } from '#lib/types/idea'
import type { TodoItemDTO, TodoPriority, TodoStatus } from '#lib/types/todo'
import type { MissionListItemViewModel } from '#lib/types/view-models/mission'

export type WorkCenterItemKind = 'idea' | 'todo' | 'mission'
export type WorkCenterPhase = 'explore' | 'plan' | 'mission' | 'review' | 'done'

export interface WorkCenterItem {
  id: string
  title: string
  summary: string
  kind: WorkCenterItemKind
  phase: WorkCenterPhase
  status: string
  priority: TodoPriority
  owner: string
  updatedAt: string
  href: string
  progress: number
  missionId?: string
}

export function buildWorkCenterItems(
  todos: readonly TodoItemDTO[],
  ideas: readonly IdeaItemDTO[],
  missions: readonly MissionListItemViewModel[]
): WorkCenterItem[] {
  const seen = new Set<string>()
  return [
    ...ideas.map(mapIdea),
    ...todos.map(mapTodo),
    ...missions.map(mapMission)
  ].filter(item => {
    const key = `${item.kind}:${item.id}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
    .sort((left, right) => sortableDate(right.updatedAt) - sortableDate(left.updatedAt))
}

export function phaseForIdea(status: IdeaStatus): WorkCenterPhase {
  if (status === 'accepted' || status === 'experiment') return 'plan'
  if (status === 'converted' || status === 'rejected' || status === 'archived') return 'done'
  return 'explore'
}

export function phaseForTodo(status: TodoStatus): WorkCenterPhase {
  if (status === 'in_progress') return 'mission'
  if (status === 'blocked' || status === 'review') return 'review'
  if (status === 'done' || status === 'canceled') return 'done'
  return 'plan'
}

function mapIdea(idea: IdeaItemDTO): WorkCenterItem {
  return {
    id: idea.idea_id,
    title: idea.title,
    summary: idea.content ?? '',
    kind: 'idea',
    phase: phaseForIdea(idea.status),
    status: idea.status,
    priority: idea.impact === 'high' ? 'high' : idea.impact === 'low' ? 'low' : 'medium',
    owner: idea.owner_name || idea.proposer_name || '',
    updatedAt: idea.updated_at,
    href: `/ideas/${encodeURIComponent(idea.idea_id)}`,
    progress: ideaProgress(idea.status)
  }
}

function mapTodo(todo: TodoItemDTO): WorkCenterItem {
  const missionId = todo.execution?.current?.mission_id || todo.execution?.current_progress?.mission_id
  return {
    id: todo.todo_id,
    title: todo.title,
    summary: todo.description ?? '',
    kind: 'todo',
    phase: phaseForTodo(todo.status),
    status: todo.status,
    priority: todo.priority,
    owner: todo.assignee_name || todo.proposer_name || '',
    updatedAt: todo.updated_at,
    href: `/todos/${encodeURIComponent(todo.todo_id)}`,
    progress: todo.execution?.current_progress
      ? clampPercent(todo.execution.current_progress.delivery_rate * 100)
      : todoProgress(todo.status),
    missionId
  }
}

function mapMission(mission: MissionListItemViewModel): WorkCenterItem {
  return {
    id: mission.id,
    title: mission.title,
    summary: mission.description,
    kind: 'mission',
    phase: phaseForMission(mission),
    status: mission.status,
    priority: missionPriority(mission),
    owner: mission.createdBy === 'backend' ? '' : mission.createdBy,
    updatedAt: mission.updatedAt,
    href: `/missions/${encodeURIComponent(mission.id)}`,
    progress: clampPercent(mission.deliveryPercent),
    missionId: mission.id
  }
}

function phaseForMission(mission: MissionListItemViewModel): WorkCenterPhase {
  const status = mission.status.toLowerCase()
  const phase = mission.phase.toLowerCase()
  if (['completed', 'done', 'approved', 'canceled', 'cancelled'].includes(status)) return 'done'
  if (
    mission.pendingApprovalCount > 0 ||
    ['review', 'qa', 'acceptance', 'verification'].some((value) => phase.includes(value))
  ) return 'review'
  return 'mission'
}

function missionPriority(mission: MissionListItemViewModel): TodoPriority {
  const risk = mission.riskLevel.toLowerCase()
  if (risk === 'critical') return 'urgent'
  if (risk === 'high' || mission.blockerCount > 0) return 'high'
  if (risk === 'low') return 'low'
  return 'medium'
}

function ideaProgress(status: IdeaStatus) {
  return ({ draft: 8, submitted: 12, under_review: 20, accepted: 30, experiment: 40,
    converted: 100, rejected: 100, archived: 100 })[status]
}

function todoProgress(status: TodoStatus) {
  return ({ inbox: 22, planned: 35, in_progress: 60, blocked: 65, review: 85,
    done: 100, canceled: 100 })[status]
}

function clampPercent(value: number) {
  if (!Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100, Math.round(value)))
}

function sortableDate(value: string) {
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : 0
}
