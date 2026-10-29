import { describe, expect, it } from 'vitest'
import type { WorkforceMemberViewModel } from '#lib/types/view-models'
import type { ScheduledTaskSummaryDTO } from '#lib/types/scheduler'
import { layoutScheduledTasksForGantt } from './scheduledTaskTimeline'
import {
  createScheduledTaskAgentIndex,
  groupScheduledTasksByAgent,
  scheduledTaskExecutor
} from './scheduledTaskAgents'

describe('scheduled task agent ownership', () => {
  const member = {
    id: 'agent-1',
    profileId: 'profile-agent-1',
    agentInstanceId: 'instance-agent-1',
    displayName: '合同审查员',
    realName: '',
    positionDisplayName: '法务专员',
    departmentDisplayName: '法务部'
  } as WorkforceMemberViewModel
  const index = createScheduledTaskAgentIndex([member])

  it('resolves the executor through every workforce agent identifier', () => {
    const executor = scheduledTaskExecutor(task('task-1', 'profile-agent-1'), index, 'zh-CN')
    expect(executor.name).toBe('合同审查员')
    expect(executor.subtitle).toBe('法务专员')
    expect(executor.system).toBe(false)
  })

  it('groups multiple tasks into one agent lane and keeps system work separate', () => {
    const items = layoutScheduledTasksForGantt([
      task('task-1', 'agent-1', 8),
      task('task-2', 'agent-1', 10),
      task('task-3', undefined, 12)
    ])
    const lanes = groupScheduledTasksByAgent(items, index, 'zh-CN')

    expect(lanes).toHaveLength(2)
    expect(lanes[0].executor.name).toBe('合同审查员')
    expect(lanes[0].items).toHaveLength(2)
    expect(lanes[1].executor.name).toBe('系统自动化')
  })
})

function task(id: string, executorAgentId?: string, hour = 8): ScheduledTaskSummaryDTO {
  return {
    scheduled_task_id: id,
    title: id,
    status: 'enabled',
    enabled: true,
    schedule_type: 'cron',
    target_mode: executorAgentId ? 'agent_task' : 'tool_call',
    target_summary: id,
    executor_agent_id: executorAgentId,
    next_run_at: new Date(2026, 7, 1, hour).toISOString()
  }
}
