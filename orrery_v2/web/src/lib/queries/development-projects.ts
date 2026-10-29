import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { developmentProjectApi, scmConnectionApi } from '#lib/api/development-projects'

import { createRealtimeClient, type RealtimeEnvelope } from '#lib/realtime/client'
import { applyRealtimeEnvelope, type RealtimeSubscriptionState } from '#lib/realtime/discussionSubscription'
import { createIdempotencyKey } from '#lib/runtime/ids'
import type {
  AddDevelopmentProjectMemberRequest,
  BindDevelopmentRepositoryRequest,
  CreateDevelopmentProjectRequest,
  CreateSCMConnectionRequest,
  DevelopmentArtifactFilters,
  DevelopmentProjectFilters,
  DevelopmentExecutionFilters,
  DevelopmentExecutionPageDTO,
  DevelopmentExecutionSignalDTO,
  DevelopmentExecutionSummaryDTO,
  SCMConnectionFilters,
  UpdateDevelopmentProjectMemberRequest,
  UpdateDevelopmentProjectRequest,
  UpdateDevelopmentRepositoryRequest,
  UpdateSCMConnectionRequest
} from '#lib/types/development-project'

export const developmentProjectQueryKeys = {
  root: ['development-projects'] as const,
  list: (filters: DevelopmentProjectFilters) => ['development-projects', 'list', filters] as const,
  detail: (projectId: string) => ['development-projects', 'detail', projectId] as const,
  members: (projectId: string) => ['development-projects', 'members', projectId] as const,
  repositories: (projectId: string) => ['development-projects', 'repositories', projectId] as const,
  executions: (projectId: string, filters: DevelopmentExecutionFilters) => ['development-projects', 'executions', projectId, filters] as const,
  workspaces: (projectId: string, filters: DevelopmentArtifactFilters) => ['development-projects', 'workspaces', projectId, filters] as const,
  runs: (projectId: string, filters: DevelopmentArtifactFilters) => ['development-projects', 'runs', projectId, filters] as const,
  connections: ['scm-connections'] as const,
  connectionList: (filters: SCMConnectionFilters) => ['scm-connections', 'list', filters] as const,
  connectionRepositories: (connectionId: string) => ['scm-connections', 'repositories', connectionId] as const
}

export function useDevelopmentProjectListQuery(
  filters: () => DevelopmentProjectFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: developmentProjectQueryKeys.list(current),
      enabled: enabled(),
      queryFn: () => developmentProjectApi.list(current)
    }
  })
}

export function useDevelopmentProjectDetailQuery(projectId: () => string) {
  return createQuery(() => {
    const id = projectId()
    return { queryKey: developmentProjectQueryKeys.detail(id), enabled: Boolean(id), queryFn: () => developmentProjectApi.get(id) }
  })
}

export function useDevelopmentProjectMembersQuery(projectId: () => string) {
  return createQuery(() => {
    const id = projectId()
    return { queryKey: developmentProjectQueryKeys.members(id), enabled: Boolean(id), queryFn: () => developmentProjectApi.listMembers(id) }
  })
}

export function useDevelopmentProjectRepositoriesQuery(projectId: () => string) {
  return createQuery(() => {
    const id = projectId()
    return { queryKey: developmentProjectQueryKeys.repositories(id), enabled: Boolean(id), queryFn: () => developmentProjectApi.listRepositories(id) }
  })
}

export function useDevelopmentProjectExecutionsQuery(
  projectId: () => string,
  filters: () => DevelopmentExecutionFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const id = projectId()
    const current = filters()
    return {
      queryKey: developmentProjectQueryKeys.executions(id, current),
      enabled: Boolean(id) && enabled(),
      queryFn: () => developmentProjectApi.listExecutions(id, current)
    }
  })
}

export function useDevelopmentProjectWorkspacesQuery(
  projectId: () => string,
  filters: () => DevelopmentArtifactFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const id = projectId()
    const current = filters()
    return {
      queryKey: developmentProjectQueryKeys.workspaces(id, current),
      enabled: Boolean(id) && enabled(),
      queryFn: () => developmentProjectApi.listWorkspaces(id, current)
    }
  })
}

export function useDevelopmentProjectRunsQuery(
  projectId: () => string,
  filters: () => DevelopmentArtifactFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const id = projectId()
    const current = filters()
    return {
      queryKey: developmentProjectQueryKeys.runs(id, current),
      enabled: Boolean(id) && enabled(),
      queryFn: () => developmentProjectApi.listRuns(id, current)
    }
  })
}

export function subscribeDevelopmentProjectExecutionStream(
  projectId: string,
  realtimeChannel: string,
  client: ReturnType<typeof useQueryClient>,
  onError?: (error: unknown) => void
) {
  projectId = projectId.trim()
  realtimeChannel = realtimeChannel.trim()
  if (!projectId || !realtimeChannel) return () => {}
  const realtime = createRealtimeClient()
  const subscription = realtime.newSubscription(realtimeChannel)
  const latest = new Map<string, DevelopmentExecutionSignalDTO>()
  let closed = false
  let state: RealtimeSubscriptionState | undefined
  const recover = () => {
    void client.invalidateQueries({ queryKey: ['development-projects', 'executions', projectId] })
  }
  subscription.on('publication', (context: { data: RealtimeEnvelope }) => {
    state = applyRealtimeEnvelope(state, context.data, {
      onEvent: (envelope) => {
        const signal = parseDevelopmentExecutionSignal(envelope, projectId, realtimeChannel)
        if (!signal) return
        const known = latest.get(signal.todo_id)
        if (known && known.mission_id === signal.mission_id && known.projection_version >= signal.projection_version) return
        latest.set(signal.todo_id, signal)
        void refreshDevelopmentExecutionRow(projectId, signal, client, latest, () => closed).catch(onError)
      },
      onRecover: recover
    })
  })
  subscription.on('error', (error: unknown) => {
    recover()
    onError?.(error)
  })
  subscription.subscribe()
  realtime.connect()
  recover()
  return () => {
    closed = true
    subscription.unsubscribe()
    realtime.disconnect()
  }
}

async function refreshDevelopmentExecutionRow(
  projectId: string,
  signal: DevelopmentExecutionSignalDTO,
  client: ReturnType<typeof useQueryClient>,
  latest: Map<string, DevelopmentExecutionSignalDTO>,
  isClosed: () => boolean
) {
  const page = await developmentProjectApi.listExecutions(projectId, { todoId: signal.todo_id, limit: 1 })
  if (isClosed()) return
  const current = latest.get(signal.todo_id)
  if (!current || current.mission_id !== signal.mission_id || current.projection_version !== signal.projection_version) return
  const row = page.items.find((item) => item.todo_id === signal.todo_id)
  if (!row || row.mission_id !== signal.mission_id || (row.mission_projection_version ?? 0) < signal.projection_version) return
  const cachedPages = client.getQueriesData<DevelopmentExecutionPageDTO>({
    queryKey: ['development-projects', 'executions', projectId]
  })
  for (const [queryKey, cached] of cachedPages) {
    let patched = replaceDevelopmentExecutionRow(cached, row)
    if (patched === cached && cached && !cached.items.some((item) => item.todo_id === row.todo_id)) {
      patched = insertDevelopmentExecutionRow(cached, row, queryKey[3] as DevelopmentExecutionFilters | undefined)
    }
    if (patched !== cached) client.setQueryData(queryKey, patched)
  }
}

export function replaceDevelopmentExecutionRow(
  page: DevelopmentExecutionPageDTO | undefined,
  row: DevelopmentExecutionSummaryDTO
): DevelopmentExecutionPageDTO | undefined {
  if (!page) return page
  const index = page.items.findIndex((item) => item.todo_id === row.todo_id)
  if (index < 0) return page
  const current = page.items[index]
  if (
    current.mission_id === row.mission_id &&
    (current.mission_projection_version ?? 0) > (row.mission_projection_version ?? 0)
  ) return page
  const items = page.items.slice()
  items[index] = row
  return { ...page, items }
}

function insertDevelopmentExecutionRow(
  page: DevelopmentExecutionPageDTO,
  row: DevelopmentExecutionSummaryDTO,
  filters: DevelopmentExecutionFilters = {}
): DevelopmentExecutionPageDTO {
  if (
    (filters.offset ?? 0) !== 0 || filters.todoId || filters.todoStatus || filters.missionStatus || filters.assigneeId ||
    filters.blocked !== undefined || filters.pendingApproval !== undefined
  ) return page
  const limit = filters.limit ?? page.page.limit ?? 0
  const items = [...page.items, row].sort(compareDevelopmentExecutionRows)
  if (limit > 0 && items.length > limit) items.length = limit
  const total = (page.page.total ?? page.items.length) + 1
  return {
    ...page,
    items,
    page: { ...page.page, total, returned: items.length, has_more: total > items.length }
  }
}

function compareDevelopmentExecutionRows(left: DevelopmentExecutionSummaryDTO, right: DevelopmentExecutionSummaryDTO) {
  const rank = (item: DevelopmentExecutionSummaryDTO) => item.blocked_task_count > 0 ? 0 : item.pending_approval_count > 0 ? 1 : item.active_task_count > 0 ? 2 : 3
  const difference = rank(left) - rank(right)
  if (difference !== 0) return difference
  const activity = Date.parse(right.last_activity_at ?? '') - Date.parse(left.last_activity_at ?? '')
  if (Number.isFinite(activity) && activity !== 0) return activity
  return left.todo_id.localeCompare(right.todo_id)
}

function parseDevelopmentExecutionSignal(envelope: RealtimeEnvelope, projectId: string, realtimeChannel: string): DevelopmentExecutionSignalDTO | undefined {
  if (envelope.type !== 'development_project.execution.updated' || envelope.channel !== realtimeChannel || envelope.aggregate_id !== projectId) return undefined
  const signal = envelope.payload as Partial<DevelopmentExecutionSignalDTO>
  if (
    typeof signal.todo_id !== 'string' || signal.todo_id.trim() === '' ||
    typeof signal.mission_id !== 'string' || signal.mission_id.trim() === '' ||
    typeof signal.projection_version !== 'number' || !Number.isInteger(signal.projection_version) || signal.projection_version < 1
  ) return undefined
  return signal as DevelopmentExecutionSignalDTO
}

export function useSCMConnectionListQuery(
  filters: () => SCMConnectionFilters = () => ({}),
  enabled: () => boolean = () => true
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: developmentProjectQueryKeys.connectionList(current),
      enabled: enabled(),
      queryFn: () => scmConnectionApi.list(current)
    }
  })
}

export function useSCMConnectionRepositoriesQuery(connectionId: () => string) {
  return createQuery(() => {
    const id = connectionId()
    return { queryKey: developmentProjectQueryKeys.connectionRepositories(id), enabled: Boolean(id), queryFn: () => scmConnectionApi.listRepositories(id) }
  })
}

export function useDevelopmentProjectMutations() {
  const client = useQueryClient()
  const invalidateProject = (projectId?: string) => {
    void client.invalidateQueries({ queryKey: developmentProjectQueryKeys.root })
    if (projectId) {
      void client.invalidateQueries({ queryKey: developmentProjectQueryKeys.detail(projectId) })
      void client.invalidateQueries({ queryKey: developmentProjectQueryKeys.members(projectId) })
      void client.invalidateQueries({ queryKey: developmentProjectQueryKeys.repositories(projectId) })
    }
  }
  return {
    create: createMutation(() => ({
      mutationFn: (body: CreateDevelopmentProjectRequest) => developmentProjectApi.create(body, { idempotencyKey: createIdempotencyKey() }),
      onSuccess: () => invalidateProject()
    })),
    update: createMutation(() => ({
      mutationFn: ({ projectId, version, body }: { projectId: string; version: number; body: UpdateDevelopmentProjectRequest }) => developmentProjectApi.update(projectId, version, body, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    archive: createMutation(() => ({
      mutationFn: ({ projectId, version }: { projectId: string; version: number }) => developmentProjectApi.archive(projectId, version, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    addMember: createMutation(() => ({
      mutationFn: ({ projectId, projectVersion, body }: { projectId: string; projectVersion: number; body: AddDevelopmentProjectMemberRequest }) => developmentProjectApi.addMember(projectId, projectVersion, body, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    updateMember: createMutation(() => ({
      mutationFn: ({ projectId, memberId, projectVersion, body }: { projectId: string; memberId: string; projectVersion: number; body: UpdateDevelopmentProjectMemberRequest }) => developmentProjectApi.updateMember(projectId, memberId, projectVersion, body, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    removeMember: createMutation(() => ({
      mutationFn: ({ projectId, memberId, projectVersion, memberVersion }: { projectId: string; memberId: string; projectVersion: number; memberVersion: number }) => developmentProjectApi.removeMember(projectId, memberId, projectVersion, memberVersion, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    bindRepository: createMutation(() => ({
      mutationFn: ({ projectId, projectVersion, body }: { projectId: string; projectVersion: number; body: BindDevelopmentRepositoryRequest }) => developmentProjectApi.bindRepository(projectId, projectVersion, body, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    updateRepository: createMutation(() => ({
      mutationFn: ({ projectId, bindingId, projectVersion, body }: { projectId: string; bindingId: string; projectVersion: number; body: UpdateDevelopmentRepositoryRequest }) => developmentProjectApi.updateRepository(projectId, bindingId, projectVersion, body, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    verifyRepository: createMutation(() => ({
      mutationFn: ({ projectId, bindingId, projectVersion, bindingVersion }: { projectId: string; bindingId: string; projectVersion: number; bindingVersion: number }) => developmentProjectApi.verifyRepository(projectId, bindingId, projectVersion, bindingVersion, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    })),
    removeRepository: createMutation(() => ({
      mutationFn: ({ projectId, bindingId, projectVersion, bindingVersion }: { projectId: string; bindingId: string; projectVersion: number; bindingVersion: number }) => developmentProjectApi.removeRepository(projectId, bindingId, projectVersion, bindingVersion, { idempotencyKey: createIdempotencyKey() }),
      onSettled: (_data, _error, input) => invalidateProject(input.projectId)
    }))
  }
}

export function useSCMConnectionMutations() {
  const client = useQueryClient()
  const invalidate = () => void client.invalidateQueries({ queryKey: developmentProjectQueryKeys.connections })
  return {
    create: createMutation(() => ({
      mutationFn: (body: CreateSCMConnectionRequest) => scmConnectionApi.create(body, { idempotencyKey: createIdempotencyKey() }),
      onSuccess: invalidate
    })),
    update: createMutation(() => ({
      mutationFn: ({ connectionId, version, body }: { connectionId: string; version: number; body: UpdateSCMConnectionRequest }) => scmConnectionApi.update(connectionId, version, body, { idempotencyKey: createIdempotencyKey() }),
      onSettled: invalidate
    })),
    verify: createMutation(() => ({
      mutationFn: ({ connectionId, version }: { connectionId: string; version: number }) => scmConnectionApi.verify(connectionId, version, { idempotencyKey: createIdempotencyKey() }),
      onSettled: invalidate
    })),
    rotate: createMutation(() => ({
      mutationFn: ({ connectionId, version, secretRef }: { connectionId: string; version: number; secretRef: string }) => scmConnectionApi.rotate(connectionId, version, secretRef, { idempotencyKey: createIdempotencyKey() }),
      onSettled: invalidate
    })),
    revoke: createMutation(() => ({
      mutationFn: ({ connectionId, version }: { connectionId: string; version: number }) => scmConnectionApi.revoke(connectionId, version, { idempotencyKey: createIdempotencyKey() }),
      onSettled: invalidate
    }))
  }
}
