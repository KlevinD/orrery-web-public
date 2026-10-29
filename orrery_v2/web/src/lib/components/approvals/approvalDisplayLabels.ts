import type { Locale } from '#lib/i18n'

type LocalizedLabel = [string, string]

const operationLabels: Record<string, LocalizedLabel> = {
  'business.approval.request': ['发起业务审批', 'Request business approval'],
  'communication.email.send': ['发送外部邮件', 'Send external email'],
  'communication.wechat_draft.create': ['创建微信公众号草稿', 'Create WeChat official account draft'],
  'system.command.execute': ['执行系统命令', 'Execute system command'],
  'system.file.change': ['修改工作区文件', 'Change workspace file'],
  'network.external.access': ['访问外部网络', 'Access external network'],
  'information.read': ['读取信息', 'Read information'],
  'interaction.user_input.request': ['请求用户补充信息', 'Request user input'],
  'tool.external.execute': ['执行外部工具操作', 'Execute external tool action'],
  'tool.unclassified.execute': ['审核未知工具操作', 'Review unclassified tool action'],
  'runtime.approval': ['执行操作确认', 'Review runtime action'],
  'todo.execution_plan.approve': ['审批待办执行计划', 'Review todo execution plan'],
  'responsibility.plan.execute': ['审批职责执行计划', 'Review responsibility execution plan'],
  'customer.update': ['更新客户信息', 'Update customer information'],
  'customer.delete': ['删除客户信息', 'Delete customer information'],
  'mission.plan': ['审批任务计划', 'Review mission plan'],
  'task.complete': ['确认任务完成', 'Confirm task completion'],
  'mcp.tool.call': ['调用外部工具', 'Call external tool']
}

const toolLabels: Record<string, LocalizedLabel> = {
  discussion_talent_list: ['查看人才列表', 'View talent list'],
  exec_command: ['执行命令', 'Execute command'],
  patch_apply: ['修改文件', 'Change file'],
  memory_diary_append: ['写入工作记忆', 'Save working memory'],
  memory_diary_read: ['读取工作记忆', 'Read working memory'],
  memory_search: ['搜索工作记忆', 'Search working memory'],
  knowledge_search: ['搜索组织知识库', 'Search organization knowledge'],
  load_skill: ['加载任务能力', 'Load task capability'],
  enterprise_graph_search_objects: ['查询组织知识图谱', 'Search organization graph'],
  enterprise_graph_build_context_bundle: ['整理组织知识上下文', 'Prepare organization context'],
  scheduled_task_list: ['查看定时任务', 'View scheduled tasks'],
  webfetch: ['访问网页', 'Open webpage'],
  websearch: ['搜索网页', 'Search the web'],
  marketing_wechat_draft_create: ['创建微信公众号草稿', 'Create WeChat official account draft'],
  mcp_marketing_marketing_wechat_draft_create: ['创建微信公众号草稿', 'Create WeChat official account draft']
}

const typeLabels: Record<string, LocalizedLabel> = {
  plan: ['执行计划', 'Plan'],
  command: ['命令执行', 'Command'],
  command_execution: ['命令执行', 'Command'],
  'file change': ['文件变更', 'File change'],
  file_change: ['文件变更', 'File change'],
  question: ['信息确认', 'Question'],
  user_input: ['信息确认', 'Question'],
  tool: ['工具调用', 'Tool'],
  tool_use: ['工具调用', 'Tool'],
  network: ['网络访问', 'Network'],
  network_access: ['网络访问', 'Network'],
  evidence: ['证据复核', 'Evidence review'],
  evidence_review: ['证据复核', 'Evidence review'],
  completion: ['任务验收', 'Task completion'],
  task_completion: ['任务验收', 'Task completion'],
  qa: ['质量审核', 'Quality review'],
  hil: ['人工确认', 'Human review'],
  approval: ['固定审批', 'Fixed approval'],
  permission: ['权限确认', 'Permission review']
}

const statusLabels: Record<string, LocalizedLabel> = {
  pending: ['待审批', 'Pending'],
  open: ['待审批', 'Pending'],
  active: ['进行中', 'Active'],
  running: ['进行中', 'Running'],
  processing: ['处理中', 'Processing'],
  scheduled: ['待开始', 'Scheduled'],
  approved: ['已批准', 'Approved'],
  resolved: ['已处理', 'Resolved'],
  passed: ['已通过', 'Passed'],
  completed: ['已完成', 'Completed'],
  delivered: ['已送达', 'Delivered'],
  denied: ['已拒绝', 'Denied'],
  rejected: ['已拒绝', 'Rejected'],
  failed: ['失败', 'Failed'],
  expired: ['已过期', 'Expired'],
  unavailable: ['已失效', 'Unavailable'],
  cancelled: ['已取消', 'Cancelled'],
  canceled: ['已取消', 'Cancelled'],
  disabled: ['已停用', 'Disabled'],
  transferred: ['已转交', 'Transferred'],
  superseded: ['已被新版本替代', 'Superseded'],
  request_changes: ['待修改', 'Changes requested'],
  changes_requested: ['待修改', 'Changes requested']
}

const riskLabels: Record<string, LocalizedLabel> = {
  low: ['低风险', 'Low risk'],
  medium: ['中风险', 'Medium risk'],
  high: ['高风险', 'High risk'],
  critical: ['严重风险', 'Critical risk']
}

const councilPositionLabels: Record<string, LocalizedLabel> = {
  approve: ['建议通过', 'Recommend approval'],
  reject: ['建议拒绝', 'Recommend rejection'],
  abstain: ['暂不表态', 'Abstain'],
  support: ['支持', 'Support'],
  oppose: ['反对', 'Oppose']
}

const councilVoteLabels: Record<string, LocalizedLabel> = {
  approve: ['已同意', 'Approved'],
  reject: ['已拒绝', 'Rejected'],
  abstain: ['已弃权', 'Abstained']
}

const participantRoleLabels: Record<string, LocalizedLabel> = {
  owner: ['负责人', 'Owner'],
  approver: ['审批人', 'Approver'],
  reviewer: ['审核人', 'Reviewer'],
  chair: ['主持人', 'Chair'],
  advisor: ['顾问', 'Advisor'],
  observer: ['观察人', 'Observer'],
  member: ['参与人', 'Participant']
}

const machineIdentifier = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/i

function localized(value: LocalizedLabel, locale: Locale) {
  return value[locale === 'zh-CN' ? 0 : 1]
}

function englishIdentifierLabel(value: string) {
  const words = value.replaceAll(/[._-]+/g, ' ').trim().toLowerCase()
  return words ? words[0].toUpperCase() + words.slice(1) : value
}

export function approvalOperationLabel(value: string, locale: Locale) {
  const normalized = value.trim().toLowerCase()
  const label = operationLabels[normalized] ?? toolLabels[normalized]
  if (label) return localized(label, locale)
  if (!machineIdentifier.test(normalized)) return value
  return locale === 'zh-CN' ? '需要审批的操作' : englishIdentifierLabel(normalized)
}

export function approvalTitleLabel(value: string, locale: Locale) {
  const normalized = value.trim().replace(/^Todo execution plan:\s*/i, '')
  const exactLabels: Record<string, LocalizedLabel> = {
    'human review request': ['人工审批请求', 'Human review request'],
    'fixed approval request': ['固定审批请求', 'Fixed approval request'],
    'approval decision': ['审批请求', 'Approval request'],
    'hil decision': ['人工审批请求', 'Human review request']
  }
  const exact = exactLabels[normalized.toLowerCase()]
  if (exact) return localized(exact, locale)
  const runtimeTool = normalized.match(/^Runtime approval(?: required for|:)?\s*(.+)$/i)?.[1]
  if (runtimeTool) {
    const tool = approvalOperationLabel(runtimeTool, locale)
    return locale === 'zh-CN' ? `操作确认：${tool}` : `Review action: ${tool}`
  }
  const providerApproval = normalized.match(/^(command|file|tool|network) approval:\s*(.+)$/i)
  if (providerApproval) {
    const labels: Record<string, LocalizedLabel> = {
      command: ['命令执行确认', 'Review command execution'],
      file: ['文件变更确认', 'Review file change'],
      tool: ['工具操作确认', 'Review tool action'],
      network: ['网络访问确认', 'Review network access']
    }
    return localized(labels[providerApproval[1].toLowerCase()], locale)
  }
  return approvalOperationLabel(normalized, locale)
}

export function approvalTypeDisplayLabel(value: string, locale: Locale) {
  const normalized = value.trim().toLowerCase()
  const label = typeLabels[normalized]
  if (label) return localized(label, locale)
  if (!machineIdentifier.test(normalized)) return value
  return locale === 'zh-CN' ? '其他审批' : englishIdentifierLabel(normalized)
}

export function approvalStatusDisplayLabel(value: string, locale: Locale) {
  const normalized = value.trim().toLowerCase()
  if (!normalized) return localized(statusLabels.pending, locale)
  const label = statusLabels[normalized]
  if (label) return localized(label, locale)
  return locale === 'zh-CN' ? '状态待确认' : englishIdentifierLabel(normalized)
}

export function approvalRiskDisplayLabel(value: string, locale: Locale) {
  const label = riskLabels[value.trim().toLowerCase()]
  if (label) return localized(label, locale)
  return locale === 'zh-CN' ? '风险未标注' : 'Risk not specified'
}

export function councilPositionDisplayLabel(value: string, locale: Locale) {
  const normalized = value.trim().toLowerCase()
  const label = councilPositionLabels[normalized]
  if (label) return localized(label, locale)
  return locale === 'zh-CN' ? '已提交意见' : 'Opinion submitted'
}

export function councilVoteDisplayLabel(value: string, locale: Locale) {
  const label = councilVoteLabels[value.trim().toLowerCase()]
  if (label) return localized(label, locale)
  return locale === 'zh-CN' ? '已投票' : 'Vote submitted'
}

export function participantRoleDisplayLabel(value: string, locale: Locale) {
  const normalized = value.trim().toLowerCase()
  if (!normalized) return locale === 'zh-CN' ? '参与人' : 'Participant'
  const label = participantRoleLabels[normalized]
  if (label) return localized(label, locale)
  return locale === 'zh-CN' ? '参与人' : englishIdentifierLabel(normalized)
}

export function approvalActorDisplayName(value: string, locale: Locale) {
  const trimmed = value.trim()
  if (!trimmed || trimmed === 'Not provided') return locale === 'zh-CN' ? '未提供' : 'Not provided'
  if (trimmed === 'todo-automation-agent') return locale === 'zh-CN' ? '自动化智能体' : 'Automation agent'
  const colon = trimmed.indexOf(':')
  if (colon >= 0 && colon < trimmed.length - 1) return trimmed.slice(colon + 1)
  return trimmed
}

export function approvalReferenceLabel(value: string, locale: Locale) {
  return locale === 'zh-CN' ? `审批编号：${value}` : `Approval reference: ${value}`
}

export function workflowRevisionLabel(value: number, locale: Locale) {
  return locale === 'zh-CN' ? `第 ${value} 版` : `Revision ${value}`
}
