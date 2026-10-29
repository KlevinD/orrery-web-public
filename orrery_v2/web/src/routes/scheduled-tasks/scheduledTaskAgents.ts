import type { WorkforceMemberViewModel } from '#lib/types/view-models'
import type { ScheduledTaskSummaryDTO } from '#lib/types/scheduler'
import type { ScheduledTaskGanttItem } from './scheduledTaskTimeline'

export interface ScheduledTaskExecutor {
  key: string
  id: string
  profileId: string
  name: string
  subtitle: string
  initials: string
  system: boolean
  tone: number
}

export interface ScheduledTaskAgentLane {
  executor: ScheduledTaskExecutor
  items: ScheduledTaskGanttItem[]
}

export type ScheduledTaskAgentIndex = Map<string, WorkforceMemberViewModel>

export function createScheduledTaskAgentIndex(members: WorkforceMemberViewModel[]) {
  const index: ScheduledTaskAgentIndex = new Map()
  for (const member of members) {
    for (const id of [member.id, member.profileId, member.agentInstanceId]) {
      const normalized = id.trim()
      if (normalized) index.set(normalized, member)
    }
  }
  return index
}

export function scheduledTaskExecutor(
  task: ScheduledTaskSummaryDTO,
  agents: ScheduledTaskAgentIndex,
  locale: string
): ScheduledTaskExecutor {
  const id = task.executor_agent_id?.trim() ?? ''
  if (!id) {
    return {
      key: 'system',
      id: '',
      profileId: '',
      name: locale === 'en-US' ? 'System automation' : '系统自动化',
      subtitle: locale === 'en-US' ? 'Platform service' : '平台服务直接执行',
      initials: locale === 'en-US' ? 'SY' : '系',
      system: true,
      tone: 0
    }
  }

  const member = agents.get(id)
  const name = member?.displayName || member?.realName || id
  return {
    key: `agent:${id}`,
    id,
    profileId: member?.profileId || '',
    name,
    subtitle: member?.positionDisplayName || member?.departmentDisplayName || member?.profileId || id,
    initials: initialsFor(name),
    system: false,
    tone: hashTone(id)
  }
}

export function groupScheduledTasksByAgent(
  items: ScheduledTaskGanttItem[],
  agents: ScheduledTaskAgentIndex,
  locale: string
) {
  const lanes = new Map<string, ScheduledTaskAgentLane>()
  for (const item of items) {
    const executor = scheduledTaskExecutor(item.task, agents, locale)
    const lane = lanes.get(executor.key)
    if (lane) lane.items.push(item)
    else lanes.set(executor.key, { executor, items: [item] })
  }

  return Array.from(lanes.values()).sort((left, right) => {
    if (left.executor.system !== right.executor.system) return left.executor.system ? 1 : -1
    return left.executor.name.localeCompare(right.executor.name, locale === 'en-US' ? 'en-US' : 'zh-CN')
  })
}

function initialsFor(name: string) {
  const trimmed = name.trim()
  if (!trimmed) return 'AI'
  if (/^[\u3400-\u9fff]/.test(trimmed)) return trimmed.slice(0, 1)
  const words = trimmed.split(/[\s_-]+/).filter(Boolean)
  return words.slice(0, 2).map((word) => word[0]?.toUpperCase() ?? '').join('') || trimmed.slice(0, 2).toUpperCase()
}

function hashTone(value: string) {
  let hash = 0
  for (const char of value) hash = ((hash << 5) - hash + char.charCodeAt(0)) | 0
  return Math.abs(hash) % 4
}
