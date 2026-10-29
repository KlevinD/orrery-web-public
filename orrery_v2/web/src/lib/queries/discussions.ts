import { createInfiniteQuery, createMutation, createQuery } from '@tanstack/svelte-query'
import { ApiClientError } from '#lib/api/client'
import { discussionApi } from '#lib/api/discussions'
import {
  appendPendingDiscussionMessage,
  applyDiscussionStreamEvent,
  discussionStreamEventIsCorrelated,
  mapDiscussionListView,
  mapDiscussionAttachment,
  mapDiscussionMessagePageView,
  mapDiscussionMessageRequest,
  mapDiscussionRunDebugView,
  mapDiscussionView
} from '#lib/mappers/discussions'
import type {
  DiscussionDTO,
  DiscussionAttachmentDTO,
  DiscussionAttachmentDraftDTO,
  DiscussionListViewModel,
  DiscussionHILMode,
  DiscussionHILModeUpdateDTO,
  DiscussionMessageFormViewModel,
  DiscussionMessagePageDTO,
  DiscussionPageDTO,
  DiscussionRunDebugDTO,
  DiscussionStreamEventDTO,
  DiscussionViewModel
} from '#lib/types/discussions'
import { createRealtimeClient } from '#lib/realtime/client'
import {
  createDiscussionRealtimeSubscription,
  discussionStreamEventFromEnvelope
} from '#lib/realtime/discussionSubscription'
import { hasSession } from '#lib/stores/session.svelte'
import { createIdempotencyKey } from '#lib/runtime/ids'

export const discussionQueryKeys = {
  all: ['discussions'] as const,
  list: (page = 1, pageSize = 20) => ['discussions', 'list', page, pageSize] as const,
  detail: (discussionId: string) => ['discussions', discussionId] as const,
  messages: (discussionId: string, limit = 20) => ['discussions', discussionId, 'messages', limit] as const,
  debug: (discussionId: string, runId = 'latest') => ['discussions', discussionId, 'debug', runId] as const
}

export function retryDiscussionQuery(failureCount: number, error: Error) {
  if (error instanceof ApiClientError && error.status === 403) return false
  return failureCount < 1
}

export function useDiscussionListQuery(
  page: () => number = () => 1,
  pageSize: () => number = () => 20,
  enabled: () => boolean = () => true
) {
  return createQuery(() => ({
    queryKey: discussionQueryKeys.list(page(), pageSize()),
    queryFn: async ({ signal }) => mapDiscussionListView((await discussionApi.listDiscussions({ page: page(), pageSize: pageSize() }, { signal })) as DiscussionPageDTO),
    enabled: enabled()
  }))
}

export function useDiscussionListInfiniteQuery(pageSize: () => number = () => 20) {
  return createInfiniteQuery(() => ({
    queryKey: ['discussions', 'list', 'infinite', pageSize()] as const,
    initialPageParam: 1,
    queryFn: async ({ pageParam, signal }) => mapDiscussionListView((await discussionApi.listDiscussions({ page: Number(pageParam) || 1, pageSize: pageSize() }, { signal })) as DiscussionPageDTO),
    getNextPageParam: (lastPage) => lastPage.page.hasNext ? lastPage.page.page + 1 : undefined
  }))
}

export function combineDiscussionListPages(pages: DiscussionListViewModel[] = []): DiscussionListViewModel {
  const discussions = uniqueDiscussionSummaries(pages.flatMap((page) => page.discussions))
  const first = pages[0]?.page
  const last = pages[pages.length - 1]?.page
  return {
    discussions,
    page: {
      page: last?.page ?? first?.page ?? 1,
      pageSize: first?.pageSize ?? last?.pageSize ?? 20,
      total: last?.total ?? first?.total ?? discussions.length,
      hasPrevious: false,
      hasNext: last?.hasNext ?? false
    }
  }
}

function uniqueDiscussionSummaries(discussions: DiscussionListViewModel['discussions']) {
  const seen = new Set<string>()
  return discussions.filter((discussion) => {
    if (seen.has(discussion.id)) return false
    seen.add(discussion.id)
    return true
  })
}

export function useDiscussionDeleteMutation() {
  return createMutation(() => ({
    mutationFn: (discussionId: string) => discussionApi.deleteDiscussion(discussionId, { idempotencyKey: createIdempotencyKey() })
  }))
}

export function useDiscussionAbortMutation() {
  return createMutation(() => ({
    mutationFn: (discussionId: string) =>
      discussionApi.abortDiscussion(discussionId, { idempotencyKey: createIdempotencyKey() }) as Promise<DiscussionDTO>
  }))
}

export function useDiscussionAddMembersMutation() {
  return createMutation(() => ({
    mutationFn: async ({ discussionId, targetIds }: { discussionId: string; targetIds: string[] }) =>
      mapDiscussionView((await discussionApi.addMembers(
        discussionId,
        { target_ids: targetIds },
        { idempotencyKey: createIdempotencyKey() }
      )) as DiscussionDTO)
  }))
}

export function useDiscussionWorkspaceMutation() {
  return createMutation(() => ({
    mutationFn: async ({ discussionId, workspaceRootDir }: { discussionId: string; workspaceRootDir: string }) =>
      mapDiscussionView((await discussionApi.setWorkspace(
        discussionId,
        { workspace_root_dir: workspaceRootDir },
        { idempotencyKey: createIdempotencyKey() }
      )) as DiscussionDTO)
  }))
}

export function useDiscussionAttachmentUploadMutation() {
  return createMutation(() => ({
    mutationFn: async ({ discussionId, file, forceNewConversation, directTargetId, directTargetKind, directAgentId }: { discussionId?: string; file: File; forceNewConversation?: boolean; directTargetId?: string; directTargetKind?: string; directAgentId?: string }) => {
      if (discussionId) {
        const attachment = mapDiscussionAttachment((await discussionApi.uploadDiscussionAttachment(
          discussionId,
          file,
          { idempotencyKey: createIdempotencyKey() }
        )) as DiscussionAttachmentDTO)
        return { attachment }
      }
      const draft = (await discussionApi.startDiscussionAttachmentDraft(
        { file, forceNewConversation, directTargetId, directTargetKind, directAgentId },
        { idempotencyKey: createIdempotencyKey() }
      )) as DiscussionAttachmentDraftDTO
      return {
        discussion: mapDiscussionView(draft.discussion),
        attachment: mapDiscussionAttachment(draft.attachment)
      }
    }
  }))
}

export function subscribeDiscussionStream(
  discussionId: string,
  invalidate: () => void,
  onEvent?: (event: DiscussionStreamEventDTO) => void
) {
  if (!discussionId) return () => {}
  if (!hasSession()) return () => {}
  const client = createRealtimeClient()
  const unsubscribe = createDiscussionRealtimeSubscription({
    client,
    discussionId,
    onEvent: (envelope) => {
      const parsed = discussionStreamEventFromEnvelope(envelope)
      if (parsed && discussionStreamEventIsCorrelated(parsed, discussionId)) {
        onEvent?.(parsed)
        if (!discussionRealtimeEventNeedsRefetch(envelope.type)) return
      }
      invalidate()
    },
    onRecover: () => invalidate()
  })
  client.connect()
  return () => {
    unsubscribe()
    client.disconnect()
  }
}

function discussionRealtimeEventNeedsRefetch(type: string) {
  switch (type) {
    case 'discussion.run.started':
    case 'discussion.token.delta':
    case 'discussion.thinking.delta':
    case 'discussion.tool.call':
    case 'discussion.plan.updated':
    case 'discussion.hil_mode.updated':
      return false
    default:
      return true
  }
}

export function useDiscussionQuery(discussionId: () => string) {
  return createQuery(() => ({
    queryKey: discussionQueryKeys.detail(discussionId()),
    queryFn: async () => mapDiscussionView((await discussionApi.getDiscussion(discussionId(), { limit: 20 })) as DiscussionDTO),
    enabled: discussionId().length > 0,
    retry: retryDiscussionQuery
  }))
}

export async function fetchDiscussionMessagePage(discussionId: string, limit = 20, offset = 0) {
  return mapDiscussionMessagePageView((await discussionApi.listDiscussionMessages(discussionId, { limit, offset })) as DiscussionMessagePageDTO)
}

export function useDiscussionRunDebugQuery(discussionId: () => string, runId: () => string, enabled: () => boolean) {
  return createQuery(() => ({
    queryKey: discussionQueryKeys.debug(discussionId(), runId() || 'latest'),
    queryFn: async () => {
      const id = runId()
      const dto = id
        ? await discussionApi.getRunDebug(discussionId(), id)
        : await discussionApi.getLatestRunDebug(discussionId())
      return mapDiscussionRunDebugView(dto as DiscussionRunDebugDTO)
    },
    enabled: discussionId().length > 0 && enabled(),
    retry: retryDiscussionQuery
  }))
}

export function useDiscussionHILModeMutation() {
  return createMutation(() => ({
    mutationFn: async ({ discussionId, mode }: { discussionId: string; mode: DiscussionHILMode }) =>
      (await discussionApi.setHILMode(
        discussionId,
        { mode },
        { idempotencyKey: createIdempotencyKey() }
      )) as DiscussionHILModeUpdateDTO
  }))
}

function createDiscussionDeltaFrameBatcher(apply: (event: DiscussionStreamEventDTO) => void) {
  let queued: DiscussionStreamEventDTO | undefined
  let cancelFrame: (() => void) | undefined

  const flushQueued = () => {
    const event = queued
    queued = undefined
    if (event) apply(event)
  }
  const flush = () => {
    const cancel = cancelFrame
    cancelFrame = undefined
    cancel?.()
    flushQueued()
  }
  const schedule = () => {
    if (cancelFrame) return
    if (typeof requestAnimationFrame === 'function') {
      const frame = requestAnimationFrame(() => {
        cancelFrame = undefined
        flushQueued()
      })
      cancelFrame = () => {
        if (typeof cancelAnimationFrame === 'function') cancelAnimationFrame(frame)
      }
      return
    }
    const timeout = setTimeout(() => {
      cancelFrame = undefined
      flushQueued()
    }, 16)
    cancelFrame = () => clearTimeout(timeout)
  }
  const push = (event: DiscussionStreamEventDTO) => {
    if (!isDiscussionTextDelta(event)) {
      flush()
      apply(event)
      return
    }
    if (queued && discussionDeltaEventsCanMerge(queued, event)) {
      queued = { ...event, delta: `${queued.delta ?? ''}${event.delta ?? ''}` }
    } else {
      flush()
      queued = event
    }
    schedule()
  }
  return { push, flush }
}

function isDiscussionTextDelta(event: DiscussionStreamEventDTO) {
  return event.kind === 'token_delta' || event.kind === 'thinking_delta'
}

function discussionDeltaEventsCanMerge(left: DiscussionStreamEventDTO, right: DiscussionStreamEventDTO) {
  return isDiscussionTextDelta(left) &&
    left.kind === right.kind &&
    (left.run_id || '') === (right.run_id || '') &&
    (left.turn_id || '') === (right.turn_id || '') &&
    (left.discussion_id || '') === (right.discussion_id || '')
}

export async function sendDiscussionMessageStream(params: {
  discussionId?: string
  form: DiscussionMessageFormViewModel
  onDiscussion: (discussion: DiscussionViewModel) => void
  onDiscussionId?: (discussionId: string) => void
  onEvent?: (event: DiscussionStreamEventDTO) => void
  initialDiscussion?: DiscussionViewModel
  signal?: AbortSignal
}) {
  let current = appendPendingDiscussionMessage(params.initialDiscussion, params.form.content, params.form.attachments ?? [])
  let sawSnapshot = false
  params.onDiscussion(current)
  const applyEvent = (event: DiscussionStreamEventDTO) => {
    if (event.kind === 'snapshot') sawSnapshot = true
    const next = applyDiscussionStreamEvent(current, event)
    if (!next) return
    current = next
    if (current.id) {
      params.onDiscussionId?.(current.id)
    }
    params.onDiscussion(current)
  }
  const eventBatcher = createDiscussionDeltaFrameBatcher(applyEvent)
  try {
    await discussionApi.sendMessageStream(
      params.discussionId || undefined,
      mapDiscussionMessageRequest(params.form),
      { idempotencyKey: createIdempotencyKey(), signal: params.signal },
      (event: DiscussionStreamEventDTO) => {
        if (!discussionStreamEventIsCorrelated(event, current.id || params.discussionId || '')) return
        params.onEvent?.(event)
        eventBatcher.push(event)
      }
    )
    eventBatcher.flush()
    if (!sawSnapshot) throw new Error('discussion stream ended without an authoritative snapshot')
  } catch (error) {
    eventBatcher.flush()
    if (isAbortError(error)) throw error
    const runtimeError = preferredStreamRuntimeError(current.runtimeError, error)
    current = { ...current, status: 'failed', runtimeStatus: 'failed', runtimeError }
    params.onDiscussion(current)
    throw preferredStreamError(error, runtimeError)
  }
  return current
}

export async function retryDiscussionMessageStream(params: {
  discussion: DiscussionViewModel
  messageId: string
  approve?: boolean
  onDiscussion: (discussion: DiscussionViewModel) => void
  onEvent?: (event: DiscussionStreamEventDTO) => void
  signal?: AbortSignal
}) {
  let current = { ...params.discussion, status: 'running', runtimeStatus: 'running' }
  let sawSnapshot = false
  params.onDiscussion(current)
  const applyEvent = (event: DiscussionStreamEventDTO) => {
    if (event.kind === 'snapshot') sawSnapshot = true
    const next = applyDiscussionStreamEvent(current, event)
    if (!next) return
    current = next
    params.onDiscussion(current)
  }
  const eventBatcher = createDiscussionDeltaFrameBatcher(applyEvent)
  try {
    await discussionApi.retryMessageStream(
      params.discussion.id,
      params.messageId,
      { approve: Boolean(params.approve) },
      { idempotencyKey: createIdempotencyKey(), signal: params.signal },
      (event: DiscussionStreamEventDTO) => {
        if (!discussionStreamEventIsCorrelated(event, params.discussion.id)) return
        params.onEvent?.(event)
        eventBatcher.push(event)
      }
    )
    eventBatcher.flush()
    if (!sawSnapshot) throw new Error('discussion retry stream ended without an authoritative snapshot')
  } catch (error) {
    eventBatcher.flush()
    if (isAbortError(error)) throw error
    const runtimeError = preferredStreamRuntimeError(current.runtimeError, error)
    current = { ...current, status: 'failed', runtimeStatus: 'failed', runtimeError }
    params.onDiscussion(current)
    throw preferredStreamError(error, runtimeError)
  }
  return current
}

function isAbortError(error: unknown) {
  return typeof error === 'object' && error !== null && 'name' in error && error.name === 'AbortError'
}

function preferredStreamRuntimeError(current: string | undefined, error: unknown) {
  if (current?.trim()) return current
  return error instanceof Error ? error.message : undefined
}

function preferredStreamError(error: unknown, runtimeError: string | undefined) {
  const message = runtimeError?.trim()
  if (message && (!(error instanceof Error) || error.message !== message)) {
    return new Error(message)
  }
  return error
}
