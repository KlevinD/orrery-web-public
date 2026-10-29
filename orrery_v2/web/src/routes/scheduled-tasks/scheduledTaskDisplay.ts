import type { ScheduledTaskSummaryDTO, TargetMode } from '#lib/types/scheduler'

const builtInTaskTitles: Record<string, string> = {
  'todo-agent-automation-periodic-scan': '待办任务自动巡检',
  'legal-schedule-policy-intelligence-daily': '法律政策情报日报',
  'legal-schedule-contract-queue-daily': '法律合同队列日报',
  'legal-schedule-compliance-risk-weekly': '法律合规风险周报',
  'legal-schedule-operations-summary-daily': '法律运营日报'
}

const targetModeLabels: Record<TargetMode, string> = {
  discussion_workflow: '聊天',
  agent_task: 'Agent',
  tool_call: '工具 / MCP',
  memory_experience_distillation: '经验记忆',
  memory_world_consolidation: '世界记忆',
  memory_lifecycle_sweep: '记忆清理',
  todo_automation_scan: '待办巡检',
  responsibility_evaluation: '责任评估',
  responsibility_reconcile: '责任协调'
}

const scopeLabels: Record<string, string> = {
  agent: 'Agent',
  discussion: '讨论组',
  mission: '任务',
  project: '项目',
  department: '部门',
  organization: '组织'
}

export function scheduledTaskDisplayTitle(task: Pick<ScheduledTaskSummaryDTO, 'scheduled_task_id' | 'title'>, locale: string) {
  if (locale === 'en-US') return task.title
  return builtInTaskTitles[task.scheduled_task_id] ?? task.title
}

export function scheduledTaskTargetLabel(mode: TargetMode) {
  return targetModeLabels[mode]
}

export function scheduledTaskTargetSummary(
  task: Pick<ScheduledTaskSummaryDTO, 'target_mode' | 'target_summary'>,
  locale: string
) {
  if (locale === 'en-US') return task.target_summary

  const summary = task.target_summary.trim()
  switch (task.target_mode) {
    case 'discussion_workflow':
      if (summary === 'new discussion workflow') return '新建聊天工作流'
      return prefixedValue(summary, 'discussion ', '讨论组')
    case 'agent_task':
      return prefixedValue(summary, 'agent ', 'Agent')
    case 'tool_call':
      return prefixedValue(summary, 'tool ', '工具 / MCP')
    case 'memory_experience_distillation':
      return memorySummary(summary, 'experience distillation ', '经验记忆提炼')
    case 'memory_world_consolidation':
      return memorySummary(summary, 'world consolidation ', '世界记忆整合')
    case 'memory_lifecycle_sweep':
      return lifecycleSummary(summary)
    case 'todo_automation_scan':
      return '待办任务自动扫描'
    case 'responsibility_evaluation':
      return responsibilityEvaluationSummary(summary)
    case 'responsibility_reconcile':
      return '责任协调'
  }
}

function responsibilityEvaluationSummary(summary: string) {
  const peopleTenantPrefix = 'responsibility evaluation for People tenant '
  if (summary.startsWith(peopleTenantPrefix)) {
    const tenantID = summary.slice(peopleTenantPrefix.length).trim()
    return tenantID ? `责任评估 · 全部 People 责任 · ${tenantID}` : '责任评估 · 全部 People 责任'
  }
  if (summary === 'responsibility evaluation for all People portfolios') {
    return '责任评估 · 全部 People 责任'
  }
  return prefixedValue(summary, 'responsibility evaluation for agent ', '责任评估')
}

function prefixedValue(summary: string, prefix: string, label: string) {
  const value = summary.startsWith(prefix) ? summary.slice(prefix.length).trim() : summary
  return value && value !== label ? `${label} · ${value}` : label
}

function memorySummary(summary: string, prefix: string, label: string) {
  const value = summary.startsWith(prefix) ? summary.slice(prefix.length).trim() : summary
  const match = value.match(/^(.*?)\s+to\s+(\S+)\s+(.+)$/)
  if (!match) return label
  const [, topic, scope, id] = match
  return `${label} · ${topic} · ${scopeLabels[scope] ?? scope} ${id}`
}

function lifecycleSummary(summary: string) {
  const value = summary.replace(/^memory lifecycle sweep\s*/, '').trim()
  const match = value.match(/^(\S+)\s+(.+)$/)
  if (!match) return '记忆生命周期清理'
  return `记忆生命周期清理 · ${scopeLabels[match[1]] ?? match[1]} ${match[2]}`
}
