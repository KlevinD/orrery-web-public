import type {
  ResponsibilityInboxItemDTO,
  ResponsibilityLane
} from '#lib/types/responsibility'

export interface ResponsibilitySourceLink {
  kind: 'todo' | 'scheduler' | 'mission'
  href: string
}

export function responsibilitySourceLinks(item: ResponsibilityInboxItemDTO): ResponsibilitySourceLink[] {
  const links: ResponsibilitySourceLink[] = []
  if (item.source_kind === 'todo_plan_step' && item.source_id) {
    links.push({ kind: 'todo', href: `/todos/${encodeURIComponent(item.source_id)}` })
  }
  if (item.source_kind === 'scheduler_agent_task' && item.source_id) {
    const params = new URLSearchParams({ task: item.source_id })
    if (item.source_step_id) params.set('run', item.source_step_id)
    links.push({ kind: 'scheduler', href: `/scheduled-tasks?${params.toString()}` })
  }
  if (item.mission_id) {
    links.push({ kind: 'mission', href: `/missions/${encodeURIComponent(item.mission_id)}` })
  }
  return links
}

export function filterResponsibilities(
  items: ResponsibilityInboxItemDTO[],
  lane: ResponsibilityLane | 'all',
  search: string
) {
  const query = search.trim().toLocaleLowerCase()
  return items.filter((item) => {
    if (lane !== 'all' && item.lane !== lane) return false
    if (!query) return true
    return [item.title, item.goal, item.accountable_agent, item.responsibility_id, ...item.risk_reasons]
      .some((value) => value.toLocaleLowerCase().includes(query))
  })
}

export function responsibilityHealthTone(health: ResponsibilityInboxItemDTO['health']) {
  if (health === 'critical') return 'danger' as const
  if (health === 'at_risk') return 'warning' as const
  return 'success' as const
}

export function percentage(value: number) {
  return `${Math.round(Math.max(0, Math.min(1, value)) * 100)}%`
}

export function durationLabel(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0s'
  if (seconds < 60) return `${Math.round(seconds)}s`
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`
  if (seconds < 86400) return `${(seconds / 3600).toFixed(seconds < 36000 ? 1 : 0)}h`
  return `${(seconds / 86400).toFixed(seconds < 864000 ? 1 : 0)}d`
}
