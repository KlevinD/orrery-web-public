import type {
  ConcurrencyPolicy,
  CreateScheduledTaskRequest,
  ScheduledTaskDTO,
  ScheduledTaskTarget,
  UpdateScheduledTaskRequest
} from '#lib/types/scheduler'

export type ScheduledTaskScheduleMode = 'once' | 'cron'
export type ScheduledTaskCronPreset = 'hourly' | 'daily' | 'weekly' | 'monthly' | 'custom'
export type ScheduledTaskTargetMode =
  | 'discussion_workflow'
  | 'agent_task'
  | 'tool_call'
  | 'memory_experience_distillation'
  | 'memory_world_consolidation'
  | 'memory_lifecycle_sweep'
  | 'todo_automation_scan'
export type ScheduledTaskDiscussionMode = 'existing' | 'new'

export interface ScheduledTaskSelectOption {
  label: string
  value: string
  goal?: string
  inputSchema?: Record<string, unknown>
}

export interface ScheduledTaskForm {
  title: string
  description: string
  scheduleMode: ScheduledTaskScheduleMode
  runAtLocal: string
  cronPreset: ScheduledTaskCronPreset
  cronTime: string
  cronWeekday: string
  cronMonthDay: string
  customCron?: string
  timezone: string
  targetMode: ScheduledTaskTargetMode
  discussionMode: ScheduledTaskDiscussionMode
  leadAgentId?: string
  conversationId: string
  initialMemberIds: string[]
  allowMemberAdditions: boolean
  message: string
  agentId: string
  taskGoal: string
  prompt: string
  toolId: string
  toolArguments: Record<string, unknown>
  memoryTopic: string
  sourceScopeKind: string
  sourceScopeId: string
  targetScopeKind: string
  targetScopeId: string
  memoryRecordLimit: string
  enabled: boolean
  concurrency: ConcurrencyPolicy
}

export function createScheduledTaskForm(): ScheduledTaskForm {
  return {
    title: '',
    description: '',
    scheduleMode: 'once',
    runAtLocal: defaultRunAtLocal(),
    cronPreset: 'daily',
    cronTime: '09:00',
    cronWeekday: '1',
    cronMonthDay: '1',
    customCron: '',
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
    targetMode: 'discussion_workflow',
    discussionMode: 'existing',
    leadAgentId: '',
    conversationId: '',
    initialMemberIds: [],
    allowMemberAdditions: true,
    message: '',
    agentId: '',
    taskGoal: '',
    prompt: '',
    toolId: '',
    toolArguments: {},
    memoryTopic: '',
    sourceScopeKind: 'mission',
    sourceScopeId: '',
    targetScopeKind: 'project',
    targetScopeId: '',
    memoryRecordLimit: '50',
    enabled: true,
    concurrency: 'allow_parallel'
  }
}

export function mapScheduledTaskFormToPayload(form: ScheduledTaskForm): CreateScheduledTaskRequest {
  const payload: CreateScheduledTaskRequest = {
    title: form.title.trim(),
    enabled: form.enabled,
    schedule: form.scheduleMode === 'once'
      ? {
          type: 'once',
          run_at: toUtcISOString(form.runAtLocal)
        }
      : {
          type: 'cron',
          cron: cronExpression(form),
          timezone: form.timezone.trim()
        },
    target: mapTarget(form),
    policy: {
      concurrency: form.concurrency,
      misfire: 'run_once'
    }
  }

  const description = form.description.trim()
  if (description) {
    payload.description = description
  }

  return payload
}

export function mapScheduledTaskFormToUpdatePayload(form: ScheduledTaskForm): UpdateScheduledTaskRequest {
  return mapScheduledTaskFormToPayload(form)
}

export function validateScheduledTaskForm(form: ScheduledTaskForm) {
  const errors: string[] = []
  if (!form.title.trim()) {
    errors.push('请输入任务标题。')
  }
  if (form.scheduleMode === 'once' && !form.runAtLocal.trim()) {
    errors.push('请选择执行时间。')
  }
  if (form.scheduleMode === 'cron') {
    const cronErrors = errors.length
    if (form.cronPreset !== 'custom' && !form.cronTime.trim()) {
      errors.push('请选择周期执行时间。')
    }
    if (form.cronPreset === 'weekly' && !form.cronWeekday.trim()) {
      errors.push('请选择星期。')
    }
    if (form.cronPreset === 'monthly' && !form.cronMonthDay.trim()) {
      errors.push('请选择每月执行日期。')
    }
    if (!cronExpression(form) && errors.length === cronErrors) {
      errors.push('请输入 Cron 表达式。')
    }
    if (!form.timezone.trim()) {
      errors.push('请输入时区。')
    }
  }
  if (form.targetMode === 'discussion_workflow') {
    if (form.discussionMode === 'existing' && !form.conversationId.trim()) {
      errors.push('请选择讨论组。')
    }
    if (!form.leadAgentId?.trim()) {
      errors.push('请选择主导 Agent。')
    }
    if (!form.message.trim()) {
      errors.push('请输入执行消息。')
    }
  } else if (form.targetMode === 'agent_task') {
    if (!form.agentId.trim()) {
      errors.push('请选择 Agent。')
    }
    if (!form.taskGoal.trim()) {
      errors.push('请输入任务目标。')
    }
  } else if (form.targetMode === 'tool_call') {
    if (!form.toolId.trim()) {
      errors.push('请选择工具或 MCP 工具。')
    }
  } else if (form.targetMode === 'memory_experience_distillation' || form.targetMode === 'memory_world_consolidation') {
    if (!form.memoryTopic.trim()) errors.push('请输入记忆主题。')
    if (!form.sourceScopeKind.trim()) errors.push('请选择来源范围。')
    if (!form.sourceScopeId.trim()) errors.push('请输入来源范围 ID。')
    if (!form.targetScopeKind.trim()) errors.push('请选择目标范围。')
    if (!form.targetScopeId.trim()) errors.push('请输入目标范围 ID。')
  } else if (form.targetMode === 'memory_lifecycle_sweep') {
    if (!form.targetScopeKind.trim()) errors.push('请选择清理范围。')
    if (!form.targetScopeId.trim()) errors.push('请输入清理范围 ID。')
  }
  return errors
}

export function formFromScheduledTask(task: ScheduledTaskDTO): ScheduledTaskForm {
  const form = createScheduledTaskForm()
  form.title = task.title
  form.description = task.description ?? ''
  form.enabled = task.enabled
  form.scheduleMode = task.schedule.type
  form.timezone = task.schedule.timezone ?? form.timezone
  form.runAtLocal = task.schedule.run_at ? toDatetimeLocal(task.schedule.run_at) : form.runAtLocal
  form.concurrency = task.policy.concurrency ?? 'allow_parallel'

  if (task.schedule.type === 'cron') {
    const cron = parseCronExpression(task.schedule.cron ?? '')
    form.cronPreset = cron.preset
    form.cronTime = cron.time
    form.cronWeekday = cron.weekday
    form.cronMonthDay = cron.monthDay
    form.customCron = form.cronPreset === 'custom' ? (task.schedule.cron ?? '') : ''
  }

  form.targetMode = editableTargetMode(task.target.mode)
  form.leadAgentId = task.target.lead_agent_id ?? ''
  form.conversationId = task.target.discussion_id ?? ''
  form.discussionMode = form.conversationId ? 'existing' : 'new'
  form.initialMemberIds = dedupeStrings(task.target.initial_member_ids ?? [])
  form.allowMemberAdditions = task.target.allow_member_additions ?? true
  form.message = task.target.message ?? ''
  form.agentId = task.target.agent_id ?? ''
  form.taskGoal = task.target.task_goal ?? ''
  form.prompt = task.target.prompt ?? ''
  form.toolId = task.target.tool_id ?? ''
  form.toolArguments = { ...(task.target.tool_arguments ?? {}) }
  form.memoryTopic = task.target.memory_topic ?? ''
  form.sourceScopeKind = task.target.source_scope_kind ?? form.sourceScopeKind
  form.sourceScopeId = task.target.source_scope_id ?? ''
  form.targetScopeKind = task.target.target_scope_kind ?? form.targetScopeKind
  form.targetScopeId = task.target.target_scope_id ?? ''
  form.memoryRecordLimit = task.target.memory_record_limit ? String(task.target.memory_record_limit) : form.memoryRecordLimit
  return form
}

function editableTargetMode(mode: string): ScheduledTaskTargetMode {
  switch (mode) {
    case 'discussion_workflow':
    case 'agent_task':
    case 'tool_call':
    case 'memory_experience_distillation':
    case 'memory_world_consolidation':
    case 'memory_lifecycle_sweep':
    case 'todo_automation_scan':
      return mode
    default:
      return 'discussion_workflow'
  }
}

export function cronExpression(form: ScheduledTaskForm) {
  const [hour, minute] = timeParts(form.cronTime)
  if (form.cronPreset === 'custom') {
    return form.customCron?.trim() ?? ''
  }
  if (form.cronPreset === 'hourly') {
    return '0 * * * *'
  }
  if (hour === '' || minute === '') {
    return ''
  }
  if (form.cronPreset === 'daily') {
    return `${minute} ${hour} * * *`
  }
  if (form.cronPreset === 'weekly') {
    const weekday = normalizedInteger(form.cronWeekday, 0, 6)
    return weekday === '' ? '' : `${minute} ${hour} * * ${weekday}`
  }
  if (form.cronPreset === 'monthly') {
    const monthDay = normalizedInteger(form.cronMonthDay, 1, 31)
    return monthDay === '' ? '' : `${minute} ${hour} ${monthDay} * *`
  }
  return ''
}

function mapTarget(form: ScheduledTaskForm): ScheduledTaskTarget {
  if (form.targetMode === 'todo_automation_scan') {
    return { mode: 'todo_automation_scan' }
  }

  if (form.targetMode === 'discussion_workflow') {
    const target: ScheduledTaskTarget = {
      mode: 'discussion_workflow',
      message: form.message.trim()
    }
    assignIfPresent(target, 'lead_agent_id', form.leadAgentId)
    if (form.discussionMode === 'existing') {
      assignIfPresent(target, 'discussion_id', form.conversationId)
    } else {
      const initialMemberIds = dedupeStrings(form.initialMemberIds)
      if (initialMemberIds.length > 0) {
        target.initial_member_ids = initialMemberIds
      }
      target.allow_member_additions = form.allowMemberAdditions
    }
    return target
  }

  if (form.targetMode === 'agent_task') {
    const target: ScheduledTaskTarget = {
      mode: 'agent_task',
      agent_id: form.agentId.trim(),
      task_goal: form.taskGoal.trim()
    }
    assignIfPresent(target, 'prompt', form.prompt)
    return target
  }

  if (form.targetMode === 'memory_experience_distillation' || form.targetMode === 'memory_world_consolidation') {
    const target: ScheduledTaskTarget = {
      mode: form.targetMode,
      memory_topic: form.memoryTopic.trim(),
      source_scope_kind: form.sourceScopeKind.trim(),
      source_scope_id: form.sourceScopeId.trim(),
      target_scope_kind: form.targetScopeKind.trim(),
      target_scope_id: form.targetScopeId.trim()
    }
    assignMemoryRecordLimit(target, form.memoryRecordLimit)
    return target
  }

  if (form.targetMode === 'memory_lifecycle_sweep') {
    const target: ScheduledTaskTarget = {
      mode: 'memory_lifecycle_sweep',
      target_scope_kind: form.targetScopeKind.trim(),
      target_scope_id: form.targetScopeId.trim()
    }
    assignMemoryRecordLimit(target, form.memoryRecordLimit)
    return target
  }

  return {
    mode: 'tool_call',
    tool_id: form.toolId.trim(),
    tool_arguments: { ...form.toolArguments }
  }
}

function assignMemoryRecordLimit(target: ScheduledTaskTarget, value: string) {
  const parsed = Number.parseInt(value.trim(), 10)
  if (Number.isInteger(parsed) && parsed > 0) target.memory_record_limit = parsed
}

function assignIfPresent(target: ScheduledTaskTarget, key: keyof ScheduledTaskTarget, value: string | undefined) {
  const trimmed = value?.trim()
  if (trimmed) {
    target[key] = trimmed as never
  }
}

function dedupeStrings(values: string[]) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const trimmed = value.trim()
    if (!trimmed || seen.has(trimmed)) continue
    seen.add(trimmed)
    out.push(trimmed)
  }
  return out
}

function toUtcISOString(value: string) {
  const trimmed = value.trim()
  if (!trimmed) {
    return ''
  }
  const withSeconds = trimmed.length === 16 ? `${trimmed}:00` : trimmed
  return new Date(withSeconds).toISOString()
}

function toDatetimeLocal(value: string) {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return ''
  }
  return toDatetimeLocalValue(parsed)
}

function defaultRunAtLocal() {
  const date = new Date(Date.now() + 60 * 60 * 1000)
  date.setMinutes(0, 0, 0)
  return toDatetimeLocalValue(date)
}

function parseCronExpression(expr: string): {
  preset: ScheduledTaskCronPreset
  time: string
  weekday: string
  monthDay: string
} {
  const fields = expr.trim().split(/\s+/)
  const fallback = {
    preset: 'custom' as ScheduledTaskCronPreset,
    time: '09:00',
    weekday: '1',
    monthDay: '1'
  }
  if (fields.length !== 5) return fallback
  const [minute, hour, dayOfMonth, month, dayOfWeek] = fields
  if (minute === '0' && hour === '*' && dayOfMonth === '*' && month === '*' && dayOfWeek === '*') {
    return { preset: 'hourly', time: '09:00', weekday: '1', monthDay: '1' }
  }
  const time = cronTimeValue(hour, minute)
  if (!time || month !== '*') return { ...fallback, time: time || fallback.time }
  if (dayOfMonth === '*' && dayOfWeek === '*') {
    return { preset: 'daily', time, weekday: '1', monthDay: '1' }
  }
  if (dayOfMonth === '*' && normalizedInteger(dayOfWeek, 0, 6) !== '') {
    return { preset: 'weekly', time, weekday: dayOfWeek, monthDay: '1' }
  }
  if (dayOfWeek === '*' && normalizedInteger(dayOfMonth, 1, 31) !== '') {
    return { preset: 'monthly', time, weekday: '1', monthDay: dayOfMonth }
  }
  return { ...fallback, time }
}

function timeParts(value: string): [hour: string, minute: string] {
  const match = value.trim().match(/^(\d{1,2}):(\d{2})$/)
  if (!match) return ['', '']
  const hour = normalizedInteger(match[1], 0, 23)
  const minute = normalizedInteger(match[2], 0, 59)
  return [hour, minute]
}

function cronTimeValue(hour: string, minute: string) {
  const normalizedHour = normalizedInteger(hour, 0, 23)
  const normalizedMinute = normalizedInteger(minute, 0, 59)
  if (normalizedHour === '' || normalizedMinute === '') return ''
  return `${normalizedHour.padStart(2, '0')}:${normalizedMinute.padStart(2, '0')}`
}

function normalizedInteger(value: string, min: number, max: number) {
  const trimmed = value.trim()
  if (!/^\d+$/.test(trimmed)) return ''
  const parsed = Number(trimmed)
  if (!Number.isInteger(parsed) || parsed < min || parsed > max) return ''
  return String(parsed)
}

function toDatetimeLocalValue(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  return `${year}-${month}-${day}T${hour}:${minute}`
}
