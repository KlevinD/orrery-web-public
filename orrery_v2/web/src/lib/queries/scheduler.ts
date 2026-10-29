import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { schedulerApi } from '#lib/api/scheduler'
import { createIdempotencyKey } from '#lib/runtime/ids'
import type {
  CreateScheduledTaskRequest,
  ResponsibilityEvaluationItemPageDTO,
  ResponsibilityEvaluationItemStatus,
  RunStatus,
  ScheduledRunInboxPageDTO,
  ScheduledRunReadFilter,
  RunScheduledTaskNowRequest,
  ScheduledRunPageDTO,
  ScheduledTaskPageDTO,
  ScheduledTaskSort,
  ScheduledTaskStatus,
  UpdateScheduledTaskRequest
} from '#lib/types/scheduler'

export interface ScheduledTaskFilters {
  includeArchived?: boolean | string
  expandOccurrences?: boolean
  occurrenceTimezone?: string
  q?: string
  status?: ScheduledTaskStatus | ''
  sort?: ScheduledTaskSort | ''
  from?: string
  to?: string
  limit?: number
  offset?: number
}

export interface ScheduledRunFilters {
  runId?: string
  status?: RunStatus | ''
  limit?: number
  offset?: number
}

export interface ScheduledRunInboxFilters {
  taskId?: string
  runId?: string
  status?: RunStatus | ''
  readState?: ScheduledRunReadFilter | ''
  attention?: boolean
  limit?: number
  offset?: number
}

export interface ResponsibilityEvaluationItemFilters {
  status?: ResponsibilityEvaluationItemStatus | ''
  cursor?: string
  limit?: number
}

export interface ScheduledMissionCreateCommand {
  title: string
  description: string
  runAt: string
  timezone?: string
  agentId: string
  taskGoal: string
  prompt: string
  idempotencyKey: string
}

export async function createScheduledMission(command: ScheduledMissionCreateCommand) {
  const task = await schedulerApi.createTask({
    title: command.title,
    description: command.description,
    schedule: {
      type: 'once',
      run_at: command.runAt,
      ...(command.timezone ? { timezone: command.timezone } : {})
    },
    target: {
      mode: 'agent_task',
      agent_id: command.agentId,
      task_goal: command.taskGoal,
      prompt: command.prompt
    },
    policy: {
      misfire: 'run_once',
      concurrency: 'allow_parallel'
    },
    enabled: true
  }, { idempotencyKey: command.idempotencyKey })
  return task.mission_id?.trim() ?? ''
}

interface QueryClientLike {
  invalidateQueries: (options: { queryKey: readonly unknown[] }) => unknown
}

export const schedulerQueryKeys = {
  root: ['scheduler'] as const,
  stats: ['scheduler', 'task-stats'] as const,
  list: (filters: ScheduledTaskFilters = {}) => ['scheduler', 'tasks', buildScheduledTaskQuery(filters)] as const,
  detail: (scheduledTaskID: string) => ['scheduler', 'task', scheduledTaskID] as const,
  runs: (scheduledTaskID: string, filters: ScheduledRunFilters = {}) =>
    ['scheduler', 'runs', scheduledTaskID, buildScheduledRunQuery(filters)] as const,
  inbox: (filters: ScheduledRunInboxFilters = {}) =>
    ['scheduler', 'run-inbox', buildScheduledRunInboxQuery(filters)] as const,
  responsibilityEvaluations: (
    scheduledTaskID: string,
    scheduledRunID: string,
    filters: ResponsibilityEvaluationItemFilters = {}
  ) => [
    'scheduler',
    'responsibility-evaluations',
    scheduledTaskID,
    scheduledRunID,
    buildResponsibilityEvaluationItemsQuery(filters)
  ] as const
}

export function useScheduledTaskStatsQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: schedulerQueryKeys.stats,
    queryFn: ({ signal }) => schedulerApi.getTaskStats({ signal }),
    enabled: enabled()
  }))
}

export function useScheduledTasksQuery(
  filters: () => ScheduledTaskFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: schedulerQueryKeys.list(current),
      queryFn: async ({ signal }) => schedulerApi.listTasks(buildScheduledTaskQuery(current), { signal }) as Promise<ScheduledTaskPageDTO>,
      enabled: enabled()
    }
  })
}

export function useScheduledTaskQuery(scheduledTaskID: () => string) {
  return createQuery(() => {
    const id = scheduledTaskID().trim()
    return {
      queryKey: schedulerQueryKeys.detail(id),
      queryFn: async ({ signal }) => schedulerApi.getTask(id, { signal }),
      enabled: id.length > 0
    }
  })
}

export function useScheduledTaskRunsQuery(scheduledTaskID: () => string, filters: () => ScheduledRunFilters = () => ({})) {
  return createQuery(() => {
    const id = scheduledTaskID().trim()
    const current = filters()
    return {
      queryKey: schedulerQueryKeys.runs(id, current),
      queryFn: async ({ signal }) => schedulerApi.listRuns(id, buildScheduledRunQuery(current), { signal }) as Promise<ScheduledRunPageDTO>,
      enabled: id.length > 0
    }
  })
}

export function useScheduledRunInboxQuery(
  filters: () => ScheduledRunInboxFilters = () => ({}),
  enabled: () => boolean = () => true,
  refetchInterval: () => number | false = () => false
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: schedulerQueryKeys.inbox(current),
      queryFn: async ({ signal }) => schedulerApi.listRunInbox(
        buildScheduledRunInboxQuery(current),
        { signal }
      ) as Promise<ScheduledRunInboxPageDTO>,
      enabled: enabled(),
      refetchInterval: refetchInterval(),
      refetchIntervalInBackground: false
    }
  })
}

export function useResponsibilityEvaluationItemsQuery(
  scheduledTaskID: () => string,
  scheduledRunID: () => string,
  filters: () => ResponsibilityEvaluationItemFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const taskID = scheduledTaskID().trim()
    const runID = scheduledRunID().trim()
    const current = filters()
    return {
      queryKey: schedulerQueryKeys.responsibilityEvaluations(taskID, runID, current),
      queryFn: async ({ signal }) => schedulerApi.listResponsibilityEvaluations(
        taskID,
        runID,
        buildResponsibilityEvaluationItemsQuery(current),
        { signal }
      ) as Promise<ResponsibilityEvaluationItemPageDTO>,
      enabled: enabled() && taskID.length > 0 && runID.length > 0
    }
  })
}

export function createSchedulerMutationRegistry(client: QueryClientLike) {
  const invalidateRoot = () => client.invalidateQueries({ queryKey: schedulerQueryKeys.root })
  const idempotency = createIdempotencyKey

  return {
    createTask: async (body: CreateScheduledTaskRequest) => {
      const result = await schedulerApi.createTask(body, { idempotencyKey: idempotency() })
      invalidateRoot()
      return result
    },
    updateTask: async (scheduledTaskID: string, body: UpdateScheduledTaskRequest) => {
      const result = await schedulerApi.updateTask(scheduledTaskID, body, { idempotencyKey: idempotency() })
      invalidateRoot()
      return result
    },
    cancelTask: async (scheduledTaskID: string) => {
      const result = await schedulerApi.cancelTask(scheduledTaskID, { idempotencyKey: idempotency() })
      invalidateRoot()
      return result
    },
    runNow: async (scheduledTaskID: string, force = false) => {
      const body: RunScheduledTaskNowRequest = { force }
      const result = await schedulerApi.runNow(scheduledTaskID, body, { idempotencyKey: idempotency() })
      invalidateRoot()
      client.invalidateQueries({ queryKey: schedulerQueryKeys.runs(scheduledTaskID) })
      return result
    },
    setRunReadState: async (scheduledRunID: string, read: boolean) => {
      const result = await schedulerApi.setRunReadState(scheduledRunID, read)
      invalidateRoot()
      return result
    }
  }
}

export function useSchedulerCommandMutations() {
  const client = useQueryClient()
  const registry = createSchedulerMutationRegistry(client)

  return {
    createTask: createMutation(() => ({ mutationFn: registry.createTask })),
    updateTask: createMutation(() => ({
      mutationFn: ({ scheduledTaskID, body }: { scheduledTaskID: string; body: UpdateScheduledTaskRequest }) =>
        registry.updateTask(scheduledTaskID, body)
    })),
    cancelTask: createMutation(() => ({ mutationFn: registry.cancelTask })),
    runNow: createMutation(() => ({
      mutationFn: ({ scheduledTaskID, force }: { scheduledTaskID: string; force?: boolean }) =>
        registry.runNow(scheduledTaskID, force ?? false)
    })),
    setRunReadState: createMutation(() => ({
      mutationFn: ({ scheduledRunID, read }: { scheduledRunID: string; read: boolean }) =>
        registry.setRunReadState(scheduledRunID, read)
    }))
  }
}

export function buildScheduledTaskQuery(filters: ScheduledTaskFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'include_archived', filters.includeArchived || filters.status === 'archived' ? true : undefined)
  appendParam(params, 'expand_occurrences', filters.expandOccurrences)
  appendParam(params, 'occurrence_timezone', filters.occurrenceTimezone)
  appendParam(params, 'q', filters.q)
  appendParam(params, 'status', filters.status)
  appendParam(params, 'sort', filters.sort)
  appendParam(params, 'from', filters.from)
  appendParam(params, 'to', filters.to)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function buildScheduledRunQuery(filters: ScheduledRunFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'run_id', filters.runId?.trim())
  appendParam(params, 'status', filters.status)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function buildScheduledRunInboxQuery(filters: ScheduledRunInboxFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'task_id', filters.taskId?.trim())
  appendParam(params, 'run_id', filters.runId?.trim())
  appendParam(params, 'status', filters.status)
  appendParam(params, 'read_state', filters.readState)
  appendParam(params, 'attention', filters.attention)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

export function buildResponsibilityEvaluationItemsQuery(filters: ResponsibilityEvaluationItemFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'status', filters.status?.trim())
  appendParam(params, 'cursor', filters.cursor?.trim())
  appendParam(params, 'limit', filters.limit)
  const query = params.toString()
  return query ? `?${query}` : ''
}

function appendParam(params: URLSearchParams, key: string, value: string | number | boolean | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}
