import { createInfiniteQuery, createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { missionApi } from '#lib/api/missions'
import { connectMissionStream } from '#lib/api/websocket'
import { operationsQueryKeys } from '#lib/queries/operations'
import { createIdempotencyKey } from '#lib/runtime/ids'
import {
  mapMissionControlView,
  mapMissionCreateRequest,
  mapMissionDiscussionMessageRequest,
  mapMissionDiscussionReplyRequest,
  mapMissionListView,
  mapMissionOpsView,
  mapMissionQaDecisionRequest,
  mapMissionTaskAssignmentRequest,
  mapMissionTaskCreateRequest,
  mapMissionTaskResultRequest
} from '#lib/mappers/mission'
import { mapDiscussionView } from '#lib/mappers/timeline'
import type {
  CancelAgentRunRequest,
  DiscussionDTO,
  EvidenceDTO,
  MissionDTO,
  MissionControlViewDTO,
  MissionListDTO,
  MissionOpsViewDTO,
  ProgressGraphDTO,
  QADTO,
  StartAgentRunRequest,
  TaskBoardDTO,
  TeamDTO
} from '#lib/types/api'
import type {
  DiscussionMessageFormViewModel,
  DiscussionReplyFormViewModel,
  MissionCreateFormViewModel,
  MissionControlViewModel,
  MissionOpsViewModel,
  MissionQaDecisionFormViewModel,
  MissionTaskAssignmentFormViewModel,
  MissionTaskFormViewModel,
  MissionTaskResultFormViewModel
} from '#lib/types/view-models'

export type MissionControlQueryData = MissionControlViewModel & { raw: MissionControlViewDTO }
export type MissionOpsQueryData = MissionOpsViewModel & { raw: MissionOpsViewDTO }

export interface MissionListFilters {
  search?: string
  status?: string
  limit?: number
  offset?: number
}

export interface MissionConversationCreateCommand {
  title: string
  finalGoal: string
  idempotencyKey: string
  sourcePersonalAssistantConversationId?: string
}

export async function createMissionConversation(command: MissionConversationCreateCommand) {
  const mission = await missionApi.createMission(mapMissionCreateRequest({
    title: command.title,
    finalGoal: command.finalGoal,
    businessDirection: '',
    sourcePersonalAssistantConversationId: command.sourcePersonalAssistantConversationId
  }), { idempotencyKey: command.idempotencyKey }) as MissionDTO
  return mission.mission_id.trim()
}

export async function resolveMissionConversationId(missionId: string) {
  const discussion = await missionApi.getMissionDiscussion(missionId) as DiscussionDTO
  return discussion.conversation_id?.trim() ?? ''
}

export const missionQueryKeys = {
  all: ['missions'] as const,
  list: (filters: MissionListFilters = {}) => ['missions', buildMissionListQuery(filters)] as const,
  control: (missionId: string) => ['missions', missionId, 'control'] as const,
  ops: (missionId: string) => ['missions', missionId, 'ops'] as const,
  team: (missionId: string) => ['missions', missionId, 'team'] as const,
  tasks: (missionId: string) => ['missions', missionId, 'tasks'] as const,
  progress: (missionId: string) => ['missions', missionId, 'progress'] as const,
  evidence: (missionId: string) => ['missions', missionId, 'evidence'] as const,
  qa: (missionId: string) => ['missions', missionId, 'qa'] as const,
  discussion: (missionId: string) => ['missions', missionId, 'discussion'] as const
}

export function useMissionListQuery(
  filters: () => MissionListFilters = () => ({}),
  enabled: () => boolean = () => true,
  refetchInterval: () => number | false = () => false
) {
  return createQuery(() => {
    const current = filters()
    return {
      queryKey: missionQueryKeys.list(current),
      queryFn: async ({ signal }) => mapMissionListView((await missionApi.listMissions(buildMissionListQuery(current), { signal })) as MissionListDTO),
      enabled: enabled(),
      refetchInterval: refetchInterval()
    }
  })
}

export function useMissionInfiniteListQuery(filters: () => MissionListFilters, enabled: () => boolean) {
  return createInfiniteQuery(() => {
    const current = { ...filters(), offset: 0 }
    return {
      queryKey: [...missionQueryKeys.list(current), 'infinite'] as const,
      initialPageParam: 0,
      queryFn: async ({ pageParam, signal }) =>
        mapMissionListView(await missionApi.listMissions(buildMissionListQuery({ ...current, offset: pageParam }), { signal }) as MissionListDTO),
      getNextPageParam: (lastPage, _pages, lastOffset) => {
        const next = lastOffset + lastPage.missions.length
        return lastPage.missions.length > 0 && next < lastPage.summary.total ? next : undefined
      },
      enabled: enabled()
    }
  })
}

export function useMissionControlQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.control(missionId()),
    queryFn: async ({ signal }) => {
      const raw = (await missionApi.getMissionControl(missionId(), { signal })) as MissionControlViewDTO
      return { ...mapMissionControlView(raw), raw } satisfies MissionControlQueryData
    },
    enabled: missionId().length > 0
  }))
}

export function useMissionOpsQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.ops(missionId()),
    queryFn: async ({ signal }) => {
      const raw = (await missionApi.getMissionOps(missionId(), { signal })) as MissionOpsViewDTO
      return { ...mapMissionOpsView(raw), raw } satisfies MissionOpsQueryData
    },
    enabled: missionId().length > 0
  }))
}

export function useMissionDiscussionQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.discussion(missionId()),
    queryFn: async ({ signal }) => mapDiscussionView((await missionApi.getMissionDiscussion(missionId(), { signal })) as DiscussionDTO),
    enabled: missionId().length > 0
  }))
}

export function useMissionTeamQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.team(missionId()),
    queryFn: async ({ signal }) => (await missionApi.getMissionTeam(missionId(), { signal })) as TeamDTO,
    enabled: missionId().length > 0
  }))
}

export function useMissionTasksQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.tasks(missionId()),
    queryFn: async ({ signal }) => (await missionApi.getMissionTasks(missionId(), { signal })) as TaskBoardDTO,
    enabled: missionId().length > 0
  }))
}

export function useMissionProgressQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.progress(missionId()),
    queryFn: async ({ signal }) => (await missionApi.getMissionProgress(missionId(), { signal })) as ProgressGraphDTO,
    enabled: missionId().length > 0
  }))
}

export function useMissionEvidenceQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.evidence(missionId()),
    queryFn: async ({ signal }) => (await missionApi.getMissionEvidence(missionId(), { signal })) as EvidenceDTO,
    enabled: missionId().length > 0
  }))
}

export function useMissionQAQuery(missionId: () => string) {
  return createQuery(() => ({
    queryKey: missionQueryKeys.qa(missionId()),
    queryFn: async ({ signal }) => (await missionApi.getMissionQA(missionId(), { signal })) as QADTO,
    enabled: missionId().length > 0
  }))
}

export function useMissionCreateMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: MissionCreateFormViewModel) =>
      missionApi.createMission(mapMissionCreateRequest(form), { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => client.invalidateQueries({ queryKey: missionQueryKeys.all })
  }))
}

export function useMissionDeleteMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (missionId: string) => missionApi.deleteMission(missionId, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: missionQueryKeys.all })
      client.invalidateQueries({ queryKey: operationsQueryKeys.all })
    }
  }))
}

export function useTaskCreateMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: MissionTaskFormViewModel) =>
      missionApi.createTask(missionId(), mapMissionTaskCreateRequest(form), { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => invalidateMissionWorkspace(client, missionId())
  }))
}

export function useAgentRunStartMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (command: { taskId: string; body: StartAgentRunRequest }) =>
      missionApi.startAgentRun(missionId(), command.taskId, command.body, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => invalidateMissionWorkspace(client, missionId())
  }))
}

export function useAgentRunCancelMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (command: { runId: string; body?: CancelAgentRunRequest }) =>
      missionApi.cancelAgentRun(missionId(), command.runId, command.body ?? {}, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => invalidateMissionWorkspace(client, missionId())
  }))
}

export function useAgentRunRetryMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (command: { runId: string }) =>
      missionApi.retryAgentRun(missionId(), command.runId, { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => invalidateMissionWorkspace(client, missionId())
  }))
}

export function useTaskAssignMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: MissionTaskAssignmentFormViewModel) => {
      const command = mapMissionTaskAssignmentRequest(form)
      return missionApi.assignTask(missionId(), command.taskId, command.body, { idempotencyKey: createIdempotencyKey() })
    },
    onSuccess: () => invalidateMissionWorkspace(client, missionId())
  }))
}

export function useTaskSubmitResultMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: MissionTaskResultFormViewModel) => {
      const command = mapMissionTaskResultRequest(form)
      return missionApi.submitTaskResult(missionId(), command.taskId, command.body, { idempotencyKey: createIdempotencyKey() })
    },
    onSuccess: () => invalidateMissionWorkspace(client, missionId())
  }))
}

export function useQADecisionMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: MissionQaDecisionFormViewModel) => {
      const command = mapMissionQaDecisionRequest(form)
      return missionApi.submitQADecision(missionId(), command.qaId, command.body, { idempotencyKey: createIdempotencyKey() })
    },
    onSuccess: () => invalidateMissionWorkspace(client, missionId())
  }))
}

export function useDiscussionMessageMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: DiscussionMessageFormViewModel) =>
      missionApi.postDiscussionMessage(missionId(), mapMissionDiscussionMessageRequest(form), { idempotencyKey: createIdempotencyKey() }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: missionQueryKeys.discussion(missionId()) })
      invalidateMissionWorkspace(client, missionId())
    }
  }))
}

export function useDiscussionReplyMutation(missionId: () => string) {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (form: DiscussionReplyFormViewModel) => {
      const command = mapMissionDiscussionReplyRequest(form)
      return missionApi.replyDiscussionMessage(missionId(), command.messageId, command.body, { idempotencyKey: createIdempotencyKey() })
    },
    onSuccess: () => {
      client.invalidateQueries({ queryKey: missionQueryKeys.discussion(missionId()) })
      invalidateMissionWorkspace(client, missionId())
    }
  }))
}

export function subscribeMissionStream(missionId: string, invalidate: () => void | Promise<unknown>) {
  if (!missionId) return () => {}
  const source = connectMissionStream(missionId)
  let timer: ReturnType<typeof setTimeout> | undefined
  let refreshing = false
  let dirty = false
  let closed = false

  function schedule() {
    if (closed) return
    dirty = true
    if (timer !== undefined || refreshing) return
    timer = setTimeout(flush, 250)
  }

  async function flush() {
    timer = undefined
    if (closed) return
    dirty = false
    refreshing = true
    try {
      await invalidate()
    } catch {
      // Query observers own error display and retry; later events can refresh again.
    } finally {
      refreshing = false
      if (dirty) schedule()
    }
  }

  source.addEventListener('message', schedule)
  source.addEventListener('snapshot', schedule)
  source.addEventListener('patch', schedule)
  return () => {
    closed = true
    if (timer !== undefined) clearTimeout(timer)
    source.close()
  }
}

export function buildMissionListQuery(filters: MissionListFilters = {}) {
  const params = new URLSearchParams()
  appendParam(params, 'search', filters.search)
  appendParam(params, 'status', filters.status)
  appendParam(params, 'limit', filters.limit)
  appendParam(params, 'offset', filters.offset)
  const query = params.toString()
  return query ? `?${query}` : ''
}

function appendParam(params: URLSearchParams, key: string, value: string | number | undefined) {
  if (value === undefined || value === '') return
  params.set(key, String(value))
}

function invalidateMissionWorkspace(client: ReturnType<typeof useQueryClient>, missionId: string) {
  client.invalidateQueries({ queryKey: missionQueryKeys.control(missionId) })
  client.invalidateQueries({ queryKey: missionQueryKeys.ops(missionId) })
  client.invalidateQueries({ queryKey: missionQueryKeys.team(missionId) })
  client.invalidateQueries({ queryKey: missionQueryKeys.tasks(missionId) })
  client.invalidateQueries({ queryKey: missionQueryKeys.progress(missionId) })
  client.invalidateQueries({ queryKey: missionQueryKeys.evidence(missionId) })
  client.invalidateQueries({ queryKey: missionQueryKeys.qa(missionId) })
}
