import { QueryClient } from '@tanstack/svelte-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { developmentProjectApi } from '#lib/api/development-projects'
import type { DevelopmentExecutionPageDTO, DevelopmentExecutionSummaryDTO } from '#lib/types/development-project'
import {
  developmentProjectQueryKeys,
  replaceDevelopmentExecutionRow,
  subscribeDevelopmentProjectExecutionStream
} from './development-projects'

const realtimeMocks = vi.hoisted(() => {
  const listeners = new Map<string, (context: { data: unknown }) => void>()
  const subscription = {
    on: vi.fn((event: string, callback: (context: { data: unknown }) => void) => {
      listeners.set(event, callback)
      return subscription
    }),
    subscribe: vi.fn(),
    unsubscribe: vi.fn()
  }
  const client = {
    newSubscription: vi.fn(() => subscription),
    connect: vi.fn(),
    disconnect: vi.fn()
  }
  return { listeners, subscription, client, createRealtimeClient: vi.fn(() => client) }
})

vi.mock('#lib/realtime/client', () => ({ createRealtimeClient: realtimeMocks.createRealtimeClient }))

describe('development project execution stream', () => {
  const realtimeChannel = 'project:dGVuYW50LWEAcHJvamVjdC0x'

  beforeEach(() => {
    realtimeMocks.listeners.clear()
    realtimeMocks.subscription.on.mockClear()
    realtimeMocks.subscription.subscribe.mockClear()
    realtimeMocks.subscription.unsubscribe.mockClear()
    realtimeMocks.client.newSubscription.mockClear()
    realtimeMocks.client.connect.mockClear()
    realtimeMocks.client.disconnect.mockClear()
    vi.restoreAllMocks()
  })

  it('fetches and replaces only the execution row named by the signal', async () => {
    const client = new QueryClient()
    const initial = executionPage([executionRow('todo-1', 'mission-1', 3), executionRow('todo-2', 'mission-2', 2)])
    const key = developmentProjectQueryKeys.executions('project-1', { limit: 200 })
    client.setQueryData(key, initial)
    vi.spyOn(developmentProjectApi, 'listExecutions').mockResolvedValue(
      executionPage([executionRow('todo-1', 'mission-1', 4, 'blocked')])
    )

    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const unsubscribe = subscribeDevelopmentProjectExecutionStream('project-1', realtimeChannel, client)
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['development-projects', 'executions', 'project-1'] })
    dispatchExecution({ todo_id: 'todo-1', mission_id: 'mission-1', projection_version: 4 })
    await vi.waitFor(() => expect(developmentProjectApi.listExecutions).toHaveBeenCalledWith('project-1', { todoId: 'todo-1', limit: 1 }))

    const updated = client.getQueryData<DevelopmentExecutionPageDTO>(key)
    expect(updated?.items[0].current_phase).toBe('blocked')
    expect(updated?.items[1]).toEqual(initial.items[1])
    expect(updated?.page).toEqual(initial.page)
    unsubscribe()
    expect(realtimeMocks.client.newSubscription).toHaveBeenCalledWith(realtimeChannel)
    expect(realtimeMocks.subscription.unsubscribe).toHaveBeenCalledTimes(1)
    expect(realtimeMocks.client.disconnect).toHaveBeenCalledTimes(1)
  })

  it('does not regress a cached row to an older projection', () => {
    const current = executionPage([executionRow('todo-1', 'mission-1', 7)])
    expect(replaceDevelopmentExecutionRow(current, executionRow('todo-1', 'mission-1', 6))).toBe(current)
  })

  it('adds a newly signaled Todo to an unfiltered first page without refetching the page', async () => {
    const client = new QueryClient()
    const key = developmentProjectQueryKeys.executions('project-1', { limit: 200 })
    client.setQueryData(key, executionPage([executionRow('todo-1', 'mission-1', 1)]))
    vi.spyOn(developmentProjectApi, 'listExecutions').mockResolvedValue(
      executionPage([executionRow('todo-2', 'mission-2', 1, 'blocked')])
    )

    const unsubscribe = subscribeDevelopmentProjectExecutionStream('project-1', realtimeChannel, client)
    dispatchExecution({ todo_id: 'todo-2', mission_id: 'mission-2', projection_version: 1 })
    await vi.waitFor(() => expect(client.getQueryData<DevelopmentExecutionPageDTO>(key)?.items).toHaveLength(2))

    const updated = client.getQueryData<DevelopmentExecutionPageDTO>(key)
    expect(updated?.items.map((item) => item.todo_id)).toEqual(['todo-2', 'todo-1'])
    expect(updated?.page.total).toBe(2)
    unsubscribe()
  })
})

function dispatchExecution(payload: unknown) {
  realtimeMocks.listeners.get('publication')?.({
    data: {
      event_id: `execution-${String((payload as { todo_id?: string }).todo_id ?? 'unknown')}-${String((payload as { projection_version?: number }).projection_version ?? 0)}`,
      type: 'development_project.execution.updated',
      channel: 'project:dGVuYW50LWEAcHJvamVjdC0x',
      aggregate_type: 'project',
      aggregate_id: 'project-1',
      version: Number((payload as { projection_version?: number }).projection_version ?? 1),
      occurred_at: '2026-07-24T10:00:00Z',
      payload
    }
  })
}

function executionPage(items: DevelopmentExecutionSummaryDTO[]): DevelopmentExecutionPageDTO {
  return { items, page: { limit: 200, offset: 0, total: items.length, returned: items.length, has_more: false } }
}

function executionRow(todoId: string, missionId: string, version: number, phase = 'delivery'): DevelopmentExecutionSummaryDTO {
  return {
    todo_id: todoId,
    todo_title: todoId,
    todo_status: 'in_progress',
    automation_mode: 'managed',
    mission_id: missionId,
    current_phase: phase,
    delivery_rate: 0.5,
    total_task_count: 2,
    active_task_count: 1,
    done_task_count: 1,
    blocked_task_count: phase === 'blocked' ? 1 : 0,
    pending_approval_count: 0,
    current_agent_ids: [],
    mission_projection_version: version,
    can_open_control: false,
    can_open_discussion: false
  }
}
