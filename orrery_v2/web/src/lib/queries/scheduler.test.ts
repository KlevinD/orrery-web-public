import { describe, expect, it, vi } from 'vitest'
import { schedulerApi } from '#lib/api/scheduler'
import {
  buildResponsibilityEvaluationItemsQuery,
  buildScheduledRunInboxQuery,
  buildScheduledRunQuery,
  buildScheduledTaskQuery,
  createScheduledMission,
  createSchedulerMutationRegistry,
  schedulerQueryKeys
} from './scheduler'
import type { ScheduledRunDTO, ScheduledTaskDTO } from '#lib/types/scheduler'

describe('scheduler queries', () => {
  it('creates scheduled missions with independent occurrences', async () => {
    vi.spyOn(schedulerApi, 'createTask').mockResolvedValue(scheduledTaskFixture({ mission_id: 'mission-new' }))
    const id = await createScheduledMission({ title: 'Daily work', description: '', prompt: '', runAt: '2026-10-19T01:00:00Z', agentId: 'agent', taskGoal: 'Do work', idempotencyKey: 'independent-mission' })
    expect(id).toBe('mission-new')
    expect(schedulerApi.createTask).toHaveBeenLastCalledWith(expect.objectContaining({ policy: { misfire: 'run_once', concurrency: 'allow_parallel' } }), { idempotencyKey: 'independent-mission' })
  })

  it('serializes scheduled task filters and cache keys', () => {
    expect(schedulerQueryKeys.stats).toEqual(['scheduler', 'task-stats'])
    expect(buildScheduledTaskQuery({
      includeArchived: true,
      expandOccurrences: true,
      occurrenceTimezone: 'Asia/Shanghai',
      q: 'digest',
      status: 'enabled',
      sort: 'next_run_desc',
      from: '2026-06-08T00:00:00.000Z',
      to: '2026-06-09T00:00:00.000Z',
      limit: 50,
      offset: 100
    })).toBe(
      '?include_archived=true&expand_occurrences=true&occurrence_timezone=Asia%2FShanghai&q=digest&status=enabled&sort=next_run_desc&from=2026-06-08T00%3A00%3A00.000Z&to=2026-06-09T00%3A00%3A00.000Z&limit=50&offset=100'
    )
    expect(schedulerQueryKeys.list({ includeArchived: true })).toEqual([
      'scheduler',
      'tasks',
      '?include_archived=true'
    ])
    expect(buildScheduledRunQuery({ status: 'failed', limit: 10, offset: 20 })).toBe('?status=failed&limit=10&offset=20')
    expect(schedulerQueryKeys.runs('sched-1', { status: 'failed' })).toEqual(['scheduler', 'runs', 'sched-1', '?status=failed'])
    expect(buildScheduledRunInboxQuery({
      taskId: 'task/1', runId: 'run:1', status: 'failed', readState: 'unread', attention: true, limit: 30, offset: 60
    })).toBe('?task_id=task%2F1&run_id=run%3A1&status=failed&read_state=unread&attention=true&limit=30&offset=60')
    expect(schedulerQueryKeys.inbox({ readState: 'unread' })).toEqual(['scheduler', 'run-inbox', '?read_state=unread'])
    expect(buildResponsibilityEvaluationItemsQuery({ status: 'failed', cursor: 'item/20', limit: 20 })).toBe(
      '?status=failed&cursor=item%2F20&limit=20'
    )
    expect(schedulerQueryKeys.responsibilityEvaluations('sched-1', 'run-1', { cursor: 'item/20' })).toEqual([
      'scheduler',
      'responsibility-evaluations',
      'sched-1',
      'run-1',
      '?cursor=item%2F20'
    ])
  })

  it('includes archived rows automatically when filtering archived tasks', () => {
    expect(buildScheduledTaskQuery({ status: 'archived' })).toBe('?include_archived=true&status=archived')
  })

  it('invalidates scheduler lists and runs after mutations', async () => {
    const invalidateQueries = vi.fn()
    const registry = createSchedulerMutationRegistry({ invalidateQueries } as any)

    vi.spyOn(schedulerApi, 'createTask').mockResolvedValue(scheduledTaskFixture({ status: 'enabled' }))
    vi.spyOn(schedulerApi, 'updateTask').mockResolvedValue(scheduledTaskFixture({ status: 'disabled', enabled: false }))
    vi.spyOn(schedulerApi, 'cancelTask').mockResolvedValue(scheduledTaskFixture({ status: 'archived', enabled: false }))
    vi.spyOn(schedulerApi, 'runNow').mockResolvedValue(scheduledRunFixture({ status: 'completed' }))
    vi.spyOn(schedulerApi, 'setRunReadState').mockResolvedValue({ scheduled_run_id: 'run-1', is_read: true })

    await registry.createTask({ title: 'Digest', schedule: { type: 'once', run_at: '2026-06-08T10:30:00Z' }, target: { mode: 'discussion_workflow', message: 'Run' } })
    await registry.updateTask('sched-1', { enabled: false })
    await registry.cancelTask('sched-1')
    await registry.runNow('sched-1', true)
    await registry.setRunReadState('run-1', true)

    expect(schedulerApi.createTask).toHaveBeenCalledWith(expect.objectContaining({ title: 'Digest' }), expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(schedulerApi.updateTask).toHaveBeenCalledWith('sched-1', { enabled: false }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(schedulerApi.cancelTask).toHaveBeenCalledWith('sched-1', expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(schedulerApi.runNow).toHaveBeenCalledWith('sched-1', { force: true }, expect.objectContaining({ idempotencyKey: expect.any(String) }))
    expect(schedulerApi.setRunReadState).toHaveBeenCalledWith('run-1', true)
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: schedulerQueryKeys.root })
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: schedulerQueryKeys.runs('sched-1') })
  })
})

function scheduledTaskFixture(overrides: Partial<ScheduledTaskDTO> = {}): ScheduledTaskDTO {
  return {
    scheduled_task_id: 'sched-1',
    title: 'Digest',
    schedule: { type: 'once', run_at: '2026-06-08T10:30:00Z' },
    target: { mode: 'discussion_workflow', message: 'Run' },
    policy: { concurrency: 'skip_if_running', misfire: 'run_once' },
    status: 'enabled',
    enabled: true,
    created_by: 'tester',
    created_at: '2026-06-08T09:00:00Z',
    updated_at: '2026-06-08T09:00:00Z',
    ...overrides
  }
}

function scheduledRunFixture(overrides: Partial<ScheduledRunDTO> = {}): ScheduledRunDTO {
  return {
    scheduled_run_id: 'run-1',
    scheduled_task_id: 'sched-1',
    trigger_type: 'manual',
    status: 'completed',
    target_mode: 'discussion_workflow',
    attempt: 1,
    created_at: '2026-06-08T10:30:00Z',
    ...overrides
  }
}
