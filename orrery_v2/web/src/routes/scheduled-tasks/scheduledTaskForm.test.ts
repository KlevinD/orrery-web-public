import { describe, expect, it } from 'vitest'
import {
  createScheduledTaskForm,
  formFromScheduledTask,
  mapScheduledTaskFormToPayload,
  mapScheduledTaskFormToUpdatePayload,
  validateScheduledTaskForm,
  type ScheduledTaskForm
} from './scheduledTaskForm'

describe('scheduled task form', () => {
  it('defaults new and policy-less schedules to independent occurrences', () => {
    const form = createScheduledTaskForm()
    expect(form.concurrency).toBe('allow_parallel')
    expect(mapScheduledTaskFormToPayload(form).policy?.concurrency).toBe('allow_parallel')
    const task = {
      scheduled_task_id: 'legacy', title: 'Legacy', enabled: true, status: 'enabled',
      schedule: { type: 'cron', cron: '0 9 * * *', timezone: 'UTC' },
      target: { mode: 'discussion_workflow', message: 'Run' }, policy: {},
      created_by: 'operator', created_at: '2026-10-18T00:00:00Z', updated_at: '2026-10-18T00:00:00Z'
    } as const
    expect(formFromScheduledTask(task).concurrency).toBe('allow_parallel')
  })

  it('maps Todo automation scheduled tasks without changing their target mode', () => {
    const form = formFromScheduledTask({
      scheduled_task_id: 'todo-agent-automation-periodic-scan',
      title: 'Todo Agent periodic review',
      schedule: { type: 'cron', cron: '*/5 * * * *', timezone: 'Asia/Shanghai' },
      target: { mode: 'todo_automation_scan' },
      policy: { concurrency: 'skip_if_running', misfire: 'run_once' },
      status: 'enabled',
      enabled: true,
      created_by: 'system:todo-automation-bootstrap',
      created_at: '2026-07-24T08:00:00Z',
      updated_at: '2026-07-24T08:00:00Z'
    })

    expect(form.targetMode).toBe('todo_automation_scan')
    expect(mapScheduledTaskFormToUpdatePayload(form).target).toEqual({ mode: 'todo_automation_scan' })
  })

  it('maps a one-time discussion workflow form to API payload', () => {
    const form: ScheduledTaskForm = {
      title: 'Daily digest',
      description: 'Team update',
      scheduleMode: 'once',
      runAtLocal: '2026-06-08T10:30',
      cronPreset: 'daily',
      cronTime: '09:00',
      cronWeekday: '1',
      cronMonthDay: '1',
      timezone: 'Asia/Shanghai',
      targetMode: 'discussion_workflow',
      discussionMode: 'existing',
      leadAgentId: 'agent-lead',
      conversationId: 'discussion-existing',
      initialMemberIds: [],
      allowMemberAdditions: true,
      message: 'Summarize yesterday.',
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
      concurrency: 'skip_if_running'
    }

    expect(mapScheduledTaskFormToPayload(form)).toEqual({
      title: 'Daily digest',
      description: 'Team update',
      enabled: true,
      schedule: {
        type: 'once',
        run_at: new Date('2026-06-08T10:30:00').toISOString()
      },
      target: {
        mode: 'discussion_workflow',
        lead_agent_id: 'agent-lead',
        discussion_id: 'discussion-existing',
        message: 'Summarize yesterday.'
      },
      policy: {
        concurrency: 'skip_if_running',
        misfire: 'run_once'
      }
    })
  })

  it('maps a new discussion workflow with initial members and member add policy', () => {
    const form = emptyScheduledTaskForm()
    form.title = 'Daily digest'
    form.runAtLocal = '2026-06-08T10:30'
    form.discussionMode = 'new'
    form.leadAgentId = 'agent-lead'
    form.initialMemberIds = ['agent-lead', 'agent-worker', 'agent-lead', '']
    form.allowMemberAdditions = false
    form.message = 'Summarize yesterday.'

    expect(validateScheduledTaskForm(form)).toEqual([])
    expect(mapScheduledTaskFormToPayload(form).target).toEqual({
      mode: 'discussion_workflow',
      lead_agent_id: 'agent-lead',
      initial_member_ids: ['agent-lead', 'agent-worker'],
      allow_member_additions: false,
      message: 'Summarize yesterday.'
    })
  })

  it('maps a recurring agent task form to cron payload', () => {
    const form: ScheduledTaskForm = {
      title: 'Weekly feedback scan',
      description: '',
      scheduleMode: 'cron',
      runAtLocal: '',
      cronPreset: 'weekly',
      cronTime: '14:45',
      cronWeekday: '5',
      cronMonthDay: '1',
      timezone: 'Asia/Shanghai',
      targetMode: 'agent_task',
      discussionMode: 'existing',
      conversationId: '',
      initialMemberIds: [],
      allowMemberAdditions: true,
      message: '',
      agentId: 'agent-researcher',
      taskGoal: 'Summarize customer feedback',
      prompt: 'Return ranked actions',
      toolId: '',
      toolArguments: {},
      memoryTopic: '',
      sourceScopeKind: 'mission',
      sourceScopeId: '',
      targetScopeKind: 'project',
      targetScopeId: '',
      memoryRecordLimit: '50',
      enabled: false,
      concurrency: 'allow_parallel'
    }

    expect(mapScheduledTaskFormToPayload(form)).toEqual({
      title: 'Weekly feedback scan',
      enabled: false,
      schedule: {
        type: 'cron',
        cron: '45 14 * * 5',
        timezone: 'Asia/Shanghai'
      },
      target: {
        mode: 'agent_task',
        agent_id: 'agent-researcher',
        task_goal: 'Summarize customer feedback',
        prompt: 'Return ranked actions'
      },
      policy: {
        concurrency: 'allow_parallel',
        misfire: 'run_once'
      }
    })
  })

  it('reports missing target fields before submit', () => {
    const form = emptyScheduledTaskForm()
    form.title = 'Incomplete'
    form.scheduleMode = 'cron'
    form.targetMode = 'agent_task'

    expect(validateScheduledTaskForm(form)).toEqual([
      '请选择 Agent。',
      '请输入任务目标。'
    ])
  })

  it('allows agent task without mission, runtime, or existing task', () => {
    const form = emptyScheduledTaskForm()
    form.title = 'Direct agent run'
    form.scheduleMode = 'cron'
    form.targetMode = 'agent_task'
    form.agentId = 'agent-worker'
    form.taskGoal = 'Summarize customer feedback'

    expect(validateScheduledTaskForm(form)).toEqual([])
    expect(mapScheduledTaskFormToPayload(form).target).toEqual({
      mode: 'agent_task',
      agent_id: 'agent-worker',
      task_goal: 'Summarize customer feedback'
    })
  })

  it('maps a tool call target with typed arguments', () => {
    const form = emptyScheduledTaskForm()
    form.title = 'Create todo'
    form.runAtLocal = '2026-06-08T10:30'
    form.targetMode = 'tool_call'
    form.toolId = 'todo.create'
    form.toolArguments = { title: 'Ship scheduler tool call', priority: 2 }

    expect(validateScheduledTaskForm(form)).toEqual([])
    expect(mapScheduledTaskFormToPayload(form).target).toEqual({
      mode: 'tool_call',
      tool_id: 'todo.create',
      tool_arguments: {
        title: 'Ship scheduler tool call',
        priority: 2
      }
    })
  })

  it('maps a weekly evaluation request to a discussion workflow payload', () => {
    const form = emptyScheduledTaskForm()
    form.title = '每周 Agent 综合评价'
    form.scheduleMode = 'cron'
    form.cronPreset = 'weekly'
    form.cronTime = '09:00'
    form.cronWeekday = '1'
    form.timezone = 'Asia/Shanghai'
    form.targetMode = 'discussion_workflow'
    form.discussionMode = 'existing'
    form.conversationId = 'discussion-1'
    form.leadAgentId = 'review-agent'
    form.message = '对 profile-agent-1 开展综合评价：请基于评估中心真实任务和运行 Token，调用 evaluation_review 工具写入结论与人天估算。'

    expect(mapScheduledTaskFormToPayload(form)).toEqual({
      title: '每周 Agent 综合评价',
      enabled: true,
      schedule: {
        type: 'cron',
        cron: '0 9 * * 1',
        timezone: 'Asia/Shanghai'
      },
      target: {
        mode: 'discussion_workflow',
        discussion_id: 'discussion-1',
        lead_agent_id: 'review-agent',
        message: '对 profile-agent-1 开展综合评价：请基于评估中心真实任务和运行 Token，调用 evaluation_review 工具写入结论与人天估算。'
      },
      policy: {
        concurrency: 'skip_if_running',
        misfire: 'run_once'
      }
    })
  })

  it('reports missing discussion and agent selections before submit', () => {
    const form = emptyScheduledTaskForm()
    form.title = 'Incomplete discussion'
    form.runAtLocal = '2026-06-08T10:30'
    form.message = 'Run the discussion workflow.'

    expect(validateScheduledTaskForm(form)).toEqual([
      '请选择讨论组。',
      '请选择主导 Agent。'
    ])
  })

  it('reports missing cron time parts before submit', () => {
    const form = emptyScheduledTaskForm()
    form.title = 'Monthly summary'
    form.scheduleMode = 'cron'
    form.cronPreset = 'monthly'
    form.cronTime = ''
    form.cronMonthDay = ''
    form.conversationId = 'discussion-1'
    form.discussionMode = 'existing'
    form.leadAgentId = 'agent-lead'
    form.message = 'Run summary.'

    expect(validateScheduledTaskForm(form)).toEqual([
      '请选择周期执行时间。',
      '请选择每月执行日期。'
    ])
  })

  it('round-trips configurable cron schedules from existing tasks', () => {
    const form = formFromScheduledTask({
      scheduled_task_id: 'scheduled-1',
      title: 'Monthly summary',
      schedule: {
        type: 'cron',
        cron: '30 16 15 * *',
        timezone: 'Asia/Shanghai'
      },
      target: {
        mode: 'agent_task',
        agent_id: 'agent-researcher',
        task_goal: 'Summarize customer feedback'
      },
      policy: {
        concurrency: 'skip_if_running'
      },
      status: 'enabled',
      enabled: true,
      created_by: 'user-1',
      created_at: '2026-06-08T10:30:00.000Z',
      updated_at: '2026-06-08T10:30:00.000Z'
    })

    expect(form.cronPreset).toBe('monthly')
    expect(form.cronTime).toBe('16:30')
    expect(form.cronMonthDay).toBe('15')
  })

  it('preserves and maps memory distillation targets', () => {
    const form = formFromScheduledTask({
      scheduled_task_id: 'memory-distill-1',
      title: '经验记忆提炼',
      schedule: { type: 'cron', cron: '0 2 * * *', timezone: 'Asia/Shanghai' },
      target: {
        mode: 'memory_experience_distillation',
        memory_topic: '客户反馈',
        source_scope_kind: 'mission',
        source_scope_id: 'mission-1',
        target_scope_kind: 'project',
        target_scope_id: 'project-1',
        memory_record_limit: 25
      },
      policy: { concurrency: 'skip_if_running' },
      status: 'enabled',
      enabled: true,
      created_by: 'user-1',
      created_at: '2026-06-08T10:30:00.000Z',
      updated_at: '2026-06-08T10:30:00.000Z'
    })

    expect(form.targetMode).toBe('memory_experience_distillation')
    expect(validateScheduledTaskForm(form)).toEqual([])
    expect(mapScheduledTaskFormToUpdatePayload(form).target).toEqual({
      mode: 'memory_experience_distillation',
      memory_topic: '客户反馈',
      source_scope_kind: 'mission',
      source_scope_id: 'mission-1',
      target_scope_kind: 'project',
      target_scope_id: 'project-1',
      memory_record_limit: 25
    })
  })
})

function emptyScheduledTaskForm(): ScheduledTaskForm {
  return {
    title: '',
    description: '',
    scheduleMode: 'once',
    runAtLocal: '',
    cronPreset: 'daily',
    cronTime: '09:00',
    cronWeekday: '1',
    cronMonthDay: '1',
    timezone: 'UTC',
    targetMode: 'discussion_workflow',
    discussionMode: 'existing',
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
    concurrency: 'skip_if_running'
  }
}
