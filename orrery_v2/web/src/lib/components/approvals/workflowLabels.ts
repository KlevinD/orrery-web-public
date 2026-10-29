import type { Locale } from '#lib/i18n'

const statusLabels: Record<string, [string, string]> = {
  active: ['进行中', 'Active'],
  approved: ['已批准', 'Approved'],
  cancelled: ['已取消', 'Cancelled'],
  canceled: ['已取消', 'Cancelled'],
  completed: ['已完成', 'Completed'],
  delivered: ['已送达', 'Delivered'],
  expired: ['已过期', 'Expired'],
  failed: ['失败', 'Failed'],
  needs_attention: ['需要处理', 'Needs attention'],
  pending: ['待处理', 'Pending'],
  processing: ['处理中', 'Processing'],
  passed: ['已通过', 'Passed'],
  rejected: ['已拒绝', 'Rejected'],
  request_changes: ['待修改', 'Changes requested'],
  changes_requested: ['待修改', 'Changes requested'],
  resolved: ['已处理', 'Resolved'],
  running: ['进行中', 'Running'],
  scheduled: ['待开始', 'Scheduled'],
  transferred: ['已转交', 'Transferred'],
  disabled: ['已停用', 'Disabled'],
  unavailable: ['已失效', 'Unavailable'],
  superseded: ['已被新修订替代', 'Superseded']
}

const stageLabels: Record<string, [string, string]> = {
  'final approval': ['最终审批', 'Final approval'],
  'human review': ['人工审批', 'Human review'],
  'human-review': ['人工审批', 'Human review'],
  'marketing review': ['市场审批', 'Marketing review']
}

const riskLabels: Record<string, [string, string]> = {
  low: ['低', 'Low'],
  medium: ['中', 'Medium'],
  high: ['高', 'High'],
  critical: ['严重', 'Critical']
}

function knownLabel(value: string, locale: Locale, labels: Record<string, [string, string]>) {
  const normalized = value.trim().toLowerCase()
  const label = labels[normalized]
  return label?.[locale === 'zh-CN' ? 0 : 1]
}

function englishIdentifierLabel(value: string) {
  const normalized = value.trim().toLowerCase()
  const words = normalized.replaceAll(/[._-]+/g, ' ')
  return words ? words[0].toUpperCase() + words.slice(1) : value
}

export function workflowStatusLabel(value: string, locale: Locale) {
  return knownLabel(value, locale, statusLabels)
    ?? (locale === 'zh-CN' ? '状态待确认' : englishIdentifierLabel(value))
}

export function workflowStageLabel(value: string, locale: Locale) {
  const label = knownLabel(value, locale, stageLabels)
  if (label) return label
  if (locale === 'en-US') return englishIdentifierLabel(value)
  return /[\u3400-\u9fff]/.test(value) ? value : '审批阶段'
}

export function workflowRiskLabel(value: string, locale: Locale) {
  return knownLabel(value, locale, riskLabels)
    ?? (locale === 'zh-CN' ? '风险未标注' : 'Risk not specified')
}
