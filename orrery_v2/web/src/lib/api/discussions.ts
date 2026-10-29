import { apiFetch, type ApiFetchOptions } from './client'
import { get } from 'svelte/store'
import { locale } from '#lib/i18n'
import { session } from '#lib/stores/session.svelte'
import { waitForApiBaseUrl } from '#lib/runtime/api-base'
import { createRequestId } from '#lib/runtime/ids'
import type { DiscussionStreamEventDTO } from '#lib/types/discussions'

interface DiscussionMessagePageQuery {
  limit?: number
  offset?: number
}

export const discussionApi = {
  listDiscussions: (query: { page?: number; pageSize?: number } = {}, options?: ApiFetchOptions) => {
    const params = new URLSearchParams()
    if (query.page) params.set('page', String(query.page))
    if (query.pageSize) params.set('page_size', String(query.pageSize))
    const suffix = params.toString()
    return apiFetch(`/api/discussions${suffix ? `?${suffix}` : ''}`, options)
  },
  getDiscussion: (discussionId: string, query: DiscussionMessagePageQuery = {}, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}${messagePageQuery(query)}`, options),
  listDiscussionMessages: (discussionId: string, query: DiscussionMessagePageQuery = {}, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/messages${messagePageQuery(query)}`, options),
  listDiscussionAttachments: (discussionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/attachments`, options),
  prewarmDiscussion: (discussionId: string | undefined, body: { title?: string; conversation_kind?: string } = {}, options?: ApiFetchOptions) => {
    const path = discussionId ? `/api/discussions/${discussionId}/prewarm` : '/api/discussions/prewarm'
    return apiFetch(path, jsonMutation('POST', body, options))
  },
  uploadDiscussionAttachment: (discussionId: string, file: File, options?: ApiFetchOptions) => {
    const form = new FormData()
    form.set('file', file)
    return apiFetch(`/api/discussions/${discussionId}/attachments`, { ...options, method: 'POST', body: form })
  },
  startDiscussionAttachmentDraft: (params: { file: File; forceNewConversation?: boolean; directTargetId?: string; directTargetKind?: string; directAgentId?: string }, options?: ApiFetchOptions) => {
    const form = new FormData()
    form.set('file', params.file)
    if (params.forceNewConversation) form.set('force_new_conversation', 'true')
    if (params.directTargetId) form.set('direct_target_id', params.directTargetId)
    if (params.directTargetKind) form.set('direct_target_kind', params.directTargetKind)
    if (params.directAgentId) form.set('direct_agent_id', params.directAgentId)
    return apiFetch('/api/discussions/attachments', { ...options, method: 'POST', body: form })
  },
  deleteDiscussion: (discussionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}`, { ...options, method: 'DELETE' }),
  getLatestRunDebug: (discussionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/debug/latest`, options),
  getRunDebug: (discussionId: string, runId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/runs/${runId}/debug`, options),
  abortDiscussion: (discussionId: string, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/abort`, { ...options, method: 'POST' }),
  interjectDiscussion: (discussionId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/interjections`, jsonMutation('POST', body, options)),
  addMembers: (discussionId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/members`, jsonMutation('POST', body, options)),
  setWorkspace: (discussionId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/workspace`, jsonMutation('PATCH', body, options)),
  setHILMode: (discussionId: string, body: unknown, options?: ApiFetchOptions) =>
    apiFetch(`/api/discussions/${discussionId}/hil-mode`, jsonMutation('PATCH', body, options)),
  sendMessage: (discussionId: string | undefined, body: unknown, options?: ApiFetchOptions) => {
    const path = discussionId ? `/api/discussions/${discussionId}/messages` : '/api/discussions/messages'
    return apiFetch(path, jsonMutation('POST', body, options))
  },
  sendMessageStream: (
    discussionId: string | undefined,
    body: unknown,
    options: ApiFetchOptions = {},
    onEvent: (event: DiscussionStreamEventDTO) => void
  ) => {
    const path = discussionId ? `/api/discussions/${discussionId}/messages` : '/api/discussions/messages'
    return fetchDiscussionEventStream(path, jsonMutation('POST', body, options), onEvent)
  },
  retryMessageStream: (
    discussionId: string,
    messageId: string,
    body: unknown,
    options: ApiFetchOptions = {},
    onEvent: (event: DiscussionStreamEventDTO) => void
  ) => {
    const path = `/api/discussions/${discussionId}/messages/${messageId}/retry`
    return fetchDiscussionEventStream(path, jsonMutation('POST', body, options), onEvent)
  }
}

function messagePageQuery(query: DiscussionMessagePageQuery) {
  const params = new URLSearchParams()
  if (query.limit) params.set('limit', String(query.limit))
  if (query.offset) params.set('offset', String(query.offset))
  const suffix = params.toString()
  return suffix ? `?${suffix}` : ''
}

function jsonMutation(method: string, body: unknown, options: ApiFetchOptions = {}): ApiFetchOptions {
  return { ...options, method, body: JSON.stringify(body) }
}

async function fetchDiscussionEventStream(
  path: string,
  options: ApiFetchOptions,
  onEvent: (event: DiscussionStreamEventDTO) => void
) {
  const baseUrl = waitForApiBaseUrl()
  const headers = new Headers(options.headers)
  headers.set('Accept', 'text/event-stream')
  setDefaultHeader(headers, 'Accept-Language', get(locale))
  headers.set('X-Request-ID', createRequestId())
  if (session.authenticated) {
    setDefaultHeader(headers, 'X-User-ID', session.actorId)
    setDefaultHeader(headers, 'X-User-Display-Name', encodeHeaderValue(session.displayName))
    setDefaultHeader(headers, 'X-Viewer-Kind', session.viewerKind)
    setDefaultHeader(headers, 'X-Agent-Profile-ID', session.user.agentProfileId)
    setDefaultHeader(headers, 'X-Permission-Scope', session.permissionScope.join(','))
  }

  if (options.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (options.idempotencyKey) {
    headers.set('Idempotency-Key', options.idempotencyKey)
  }

  const response = await fetch(`${baseUrl}${path}`, {
    ...options,
    credentials: options.credentials ?? 'include',
    headers
  })
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response))
  }
  const contentType = response.headers.get('content-type')?.toLowerCase() ?? ''
  if (!contentType.includes('text/event-stream')) {
    throw new Error(`discussion stream returned unexpected content type: ${contentType || 'missing'}`)
  }
  if (!response.body) throw new Error('discussion stream ended without a response body')

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let eventCount = 0
  let terminalEvent = false
  const emit = (event: DiscussionStreamEventDTO) => {
    eventCount += 1
    terminalEvent = terminalEvent || discussionStreamEventIsTerminal(event)
    onEvent(event)
  }
  while (true) {
    const { value, done } = await reader.read()
    buffer += decoder.decode(value, { stream: !done })
    buffer = emitCompleteSSEFrames(buffer, emit)
    if (done) break
  }
  emitCompleteSSEFrames(buffer + '\n\n', emit)
  if (eventCount === 0) throw new Error('discussion stream ended without events')
  if (!terminalEvent) throw new Error('discussion stream ended before a terminal event')
}

function discussionStreamEventIsTerminal(event: DiscussionStreamEventDTO) {
  if (event.kind === 'turn_completed' || event.kind === 'turn_failed' || event.kind === 'error') return true
  if (event.kind !== 'snapshot' || !event.discussion) return false
  return !['running', 'started', 'queued'].includes(event.discussion.status.trim().toLowerCase())
}

async function responseErrorMessage(response: Response) {
  try {
    const body = await response.clone().json()
    const setupGuidance = discussionRuntimeSetupGuidance(body)
    if (setupGuidance) return setupGuidance
    if (typeof body?.message === 'string' && body.message.trim()) return body.message.trim()
    if (typeof body?.error === 'string' && body.error.trim()) return body.error.trim()
  } catch {
    // Keep the transport fallback when the server did not return a JSON error body.
  }
  return response.statusText || `discussion stream failed with ${response.status}`
}

function discussionRuntimeSetupGuidance(body: unknown) {
  if (!body || typeof body !== 'object') return ''
  const values = body as Record<string, unknown>
  const code = String(values.code ?? values.error ?? '').trim()
  const message = String(values.message ?? '').trim()
  if (code !== 'runtime_unavailable' && !isRuntimeSetupMessage(message)) return ''
  const details = values.details && typeof values.details === 'object' ? values.details as Record<string, unknown> : {}
  const guidance = stringDetail(details.guidance)
  const envGuidance = stringDetail(details.env_guidance)
  return [guidance, envGuidance].filter(Boolean).join(' ')
}

function isRuntimeSetupMessage(message: string) {
  const normalized = message.trim().toLowerCase()
  return normalized === 'discussion runtime unavailable' ||
    normalized.includes('discussion runtime is not configured') ||
    normalized.includes('configure a discussion runtime')
}

function stringDetail(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function emitCompleteSSEFrames(buffer: string, onEvent: (event: DiscussionStreamEventDTO) => void) {
  let remaining = buffer
  let separator = remaining.search(/\r?\n\r?\n/)
  while (separator >= 0) {
    const frame = remaining.slice(0, separator)
    remaining = remaining.slice(frame.length).replace(/^\r?\n\r?\n/, '')
    const event = parseSSEFrame(frame)
    if (event) onEvent(event)
    separator = remaining.search(/\r?\n\r?\n/)
  }
  return remaining
}

function parseSSEFrame(frame: string): DiscussionStreamEventDTO | undefined {
  const lines = frame.split(/\r?\n/)
  const data = lines
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trimStart())
    .join('\n')
    .trim()
  if (!data) return undefined
  const event = JSON.parse(data) as DiscussionStreamEventDTO
  if (!event.event_id) {
    const id = lines.find((line) => line.startsWith('id:'))?.slice(3).trim()
    if (id) event.event_id = id
  }
  return event
}

function setDefaultHeader(headers: Headers, name: string, value: string) {
  if (!headers.has(name) && value.trim()) {
    headers.set(name, value)
  }
}

function encodeHeaderValue(value: string) {
  return encodeURIComponent(value.trim())
}
